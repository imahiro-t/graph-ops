package displayname

import (
	"strings"
	"testing"
)

func TestID(t *testing.T) {
	for in, want := range map[string]string{
		"0a1b2c3d-0000-4000-8000-000000000000": "0a1b2c3d-0000-4000-8000-000000000000",
		"run-20260929-120000-abcd1234":         "run-20260929-120000-abcd1234",
		"":                                     InvalidID,
		"sess\nforged":                         InvalidID,
		"sess\x1b[2J":                          InvalidID,
		"sess‮":                                InvalidID,
		"sess id":                              InvalidID,
		strings.Repeat("a", 192):               InvalidID,
	} {
		if got := ID(in); got != want {
			t.Errorf("ID(%q) = %q, want %q", in, got, want)
		}
	}
	if ID(ID("x\n")) != InvalidID {
		t.Error("ID is not idempotent on its replacement")
	}
}

func TestTimestamp(t *testing.T) {
	for in, want := range map[string]string{
		"2026-09-29T12:00:00.000000000Z": "2026-09-29T12:00:00.000000000Z",
		"2026-09-29T12:00:00Z":           "2026-09-29T12:00:00Z",
		"2026\x1b[31m":                   UnknownTime,
		"":                               UnknownTime,
		"2026-09-29T12:00:00Z\n":         UnknownTime,
	} {
		if got := Timestamp(in); got != want {
			t.Errorf("Timestamp(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMemberLabel(t *testing.T) {
	for _, c := range []struct {
		name     string
		fallback bool
		want     string
	}{
		{"Alice", false, "Alice"},
		{"bob@host", true, "bob@host (name not set)"},
		{"", false, "an unnamed member"},
		{"\x1b‮", true, "an unnamed member"},
		{"Mallory\x1b[2J\nforged", false, "Mallory[2J forged"},
	} {
		if got := MemberLabel(c.name, c.fallback); got != c.want {
			t.Errorf("MemberLabel(%q, %v) = %q, want %q", c.name, c.fallback, got, c.want)
		}
	}
}
