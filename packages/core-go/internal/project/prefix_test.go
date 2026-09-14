package project

import (
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

func TestResolvePrefix_ExplicitValid(t *testing.T) {
	got, err := ResolvePrefix("Sample Project", "SMPL", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "SMPL" {
		t.Fatalf("expected SMPL, got %q", got)
	}
}

func TestResolvePrefix_ExplicitInvalidFormat(t *testing.T) {
	_, err := ResolvePrefix("Sample", "ABCDEF", nil) // 6 chars, too long
	assertAPIErrCode(t, err, domain.ErrCodeInvalidPrefix)

	_, err = ResolvePrefix("Sample", "AB-CD", nil) // non-alnum
	assertAPIErrCode(t, err, domain.ErrCodeInvalidPrefix)
}

func TestResolvePrefix_ExplicitTakenCaseInsensitive(t *testing.T) {
	_, err := ResolvePrefix("Other", "abcde", []string{"ABCDE"})
	assertAPIErrCode(t, err, domain.ErrCodePrefixTaken)
}

// TestResolvePrefix_AutoFromAlnumName covers the "英数字名" pattern: the
// prefix is derived from the name's ASCII letters/digits, upper-cased and
// truncated to 5 characters.
func TestResolvePrefix_AutoFromAlnumName(t *testing.T) {
	got, err := ResolvePrefix("My Project", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "MYPRO" {
		t.Fatalf("expected MYPRO, got %q", got)
	}
}

// TestResolvePrefix_AutoFromJapaneseName covers the "日本語名" pattern: a
// name with no ASCII alphanumeric characters falls back to the "PRJ" base.
func TestResolvePrefix_AutoFromJapaneseName(t *testing.T) {
	got, err := ResolvePrefix("日本語プロジェクト", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "PRJ" {
		t.Fatalf("expected PRJ, got %q", got)
	}
	if len(got) < 1 || len(got) > MaxPrefixLen {
		t.Fatalf("prefix %q not within 1..%d chars", got, MaxPrefixLen)
	}
}

// TestResolvePrefix_AutoCorrectsOnCollision covers the "重複時の補正"
// pattern: when the derived base collides with an existing prefix, a
// numbered variant is produced instead.
func TestResolvePrefix_AutoCorrectsOnCollision(t *testing.T) {
	got, err := ResolvePrefix("MyProject2", "", []string{"MYPRO"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "MYPRO" {
		t.Fatalf("expected a corrected prefix distinct from MYPRO, got %q", got)
	}
	if len(got) > MaxPrefixLen {
		t.Fatalf("prefix %q exceeds %d chars", got, MaxPrefixLen)
	}
}

// TestResolvePrefix_AutoRandomFallbackWhenAllNumberedTaken covers the "全滅
// 時のランダムフォールバック" pattern: every base+n candidate up to 5 chars
// is already taken, so a random alphanumeric string must be produced.
func TestResolvePrefix_AutoRandomFallbackWhenAllNumberedTaken(t *testing.T) {
	base := "MYPRO"
	existing := []string{base}
	// MaxPrefixLen=5 leaves room for suffixes "2".."9" (single digit) only
	// (head[:4]+suffix); block every one of those too.
	for n := 2; n <= 9; n++ {
		existing = append(existing, base[:4]+string(rune('0'+n)))
	}
	got, err := ResolvePrefix("MyProject", "", existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range existing {
		if strings.EqualFold(got, e) {
			t.Fatalf("expected a prefix not in %v, got %q", existing, got)
		}
	}
	if len(got) != MaxPrefixLen {
		t.Fatalf("expected a %d-char random prefix, got %q", MaxPrefixLen, got)
	}
}

func assertAPIErrCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", code)
	}
	apiErr, ok := err.(*domain.APIError)
	if !ok {
		t.Fatalf("expected *domain.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != code {
		t.Fatalf("expected code %s, got %s", code, apiErr.Code)
	}
}
