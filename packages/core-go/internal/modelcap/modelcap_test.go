package modelcap

import "testing"

// TestResolve checks all 64 combinations of assignment x explicit cap x
// session family against a hand-written table (not one derived from the
// rule), so a mistake in the rule cannot also produce the expectation.
func TestResolve(t *testing.T) {
	const (
		h = Haiku
		s = Sonnet
		o = Opus
		x = Model("") // no cap / unknown session / "do not pass a model"
	)
	cases := []struct {
		assigned string
		cap      Model
		session  Model
		want     Model
	}{
		// No explicit cap: today's behavior.
		{"inherit", x, x, x}, {"haiku", x, x, h}, {"sonnet", x, x, s}, {"opus", x, x, o},
		{"inherit", x, h, x}, {"haiku", x, h, x}, {"sonnet", x, h, x}, {"opus", x, h, x},
		{"inherit", x, s, x}, {"haiku", x, s, h}, {"sonnet", x, s, x}, {"opus", x, s, x},
		{"inherit", x, o, x}, {"haiku", x, o, h}, {"sonnet", x, o, s}, {"opus", x, o, x},
		// Cap haiku.
		{"inherit", h, x, h}, {"haiku", h, x, h}, {"sonnet", h, x, h}, {"opus", h, x, h},
		{"inherit", h, h, x}, {"haiku", h, h, x}, {"sonnet", h, h, x}, {"opus", h, h, x},
		{"inherit", h, s, h}, {"haiku", h, s, h}, {"sonnet", h, s, h}, {"opus", h, s, h},
		{"inherit", h, o, h}, {"haiku", h, o, h}, {"sonnet", h, o, h}, {"opus", h, o, h},
		// Cap sonnet.
		{"inherit", s, x, s}, {"haiku", s, x, h}, {"sonnet", s, x, s}, {"opus", s, x, s},
		{"inherit", s, h, s}, {"haiku", s, h, x}, {"sonnet", s, h, s}, {"opus", s, h, s},
		{"inherit", s, s, x}, {"haiku", s, s, h}, {"sonnet", s, s, x}, {"opus", s, s, x},
		{"inherit", s, o, s}, {"haiku", s, o, h}, {"sonnet", s, o, s}, {"opus", s, o, s},
		// Cap opus.
		{"inherit", o, x, o}, {"haiku", o, x, h}, {"sonnet", o, x, s}, {"opus", o, x, o},
		{"inherit", o, h, o}, {"haiku", o, h, x}, {"sonnet", o, h, s}, {"opus", o, h, o},
		{"inherit", o, s, o}, {"haiku", o, s, h}, {"sonnet", o, s, x}, {"opus", o, s, o},
		{"inherit", o, o, x}, {"haiku", o, o, h}, {"sonnet", o, o, s}, {"opus", o, o, x},
	}
	if len(cases) != 64 {
		t.Fatalf("table has %d cases, want 64", len(cases))
	}
	for _, c := range cases {
		if got := Resolve(c.assigned, c.cap, c.session); got != c.want {
			t.Errorf("Resolve(%q, cap=%q, session=%q) = %q, want %q", c.assigned, c.cap, c.session, got, c.want)
		}
	}
}

func TestResolve_UnknownAssignmentIsInherit(t *testing.T) {
	if got := Resolve("", Sonnet, Opus); got != Sonnet {
		t.Errorf("empty assignment = %q, want sonnet", got)
	}
	if got := Resolve("custom-thing", "", Opus); got != "" {
		t.Errorf("unknown assignment, no cap = %q, want empty", got)
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Model{"haiku": Haiku, "Sonnet": Sonnet, "OPUS": Opus, " opus ": Opus} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"fable", "gpt-4", "inherit", "", "opus; rm -rf /", "claude-opus-4-5"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %q, want an error", in, got)
		}
	}
}

func TestValidAssignment(t *testing.T) {
	for _, in := range []string{"haiku", "sonnet", "opus", "inherit"} {
		if !ValidAssignment(in) {
			t.Errorf("ValidAssignment(%q) = false", in)
		}
	}
	for _, in := range []string{"", "fable", "gpt-4", "Opus", "INHERIT"} {
		if ValidAssignment(in) {
			t.Errorf("ValidAssignment(%q) = true", in)
		}
	}
}

func TestFamily(t *testing.T) {
	for in, want := range map[string]Model{
		"claude-opus-4-5":                            Opus,
		"claude-sonnet-4-5-20250929":                 Sonnet,
		"claude-haiku-4-5":                           Haiku,
		"claude-3-5-sonnet-20241022":                 Sonnet,
		"us.anthropic.claude-opus-4-1-20250805-v1:0": Opus,
		"opus[1m]":              Opus,
		"claude-sonnet-4-5[1m]": Sonnet,
		"sonnet":                Sonnet,
		"Haiku":                 Haiku,
		"fable":                 "",
		"something-unknown":     "",
		"":                      "",
	} {
		if got := Family(in); got != want {
			t.Errorf("Family(%q) = %q, want %q", in, got, want)
		}
	}
}
