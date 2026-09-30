package displayname

import (
	"strings"
	"testing"
	"unicode/utf8"
)

var cases = []struct {
	name, in, want string
}{
	{"ascii name", "Alice", "Alice"},
	{"japanese name", "山田 太郎", "山田 太郎"},
	{"full-width space kept", "山田\u3000太郎", "山田\u3000太郎"},
	{"user@host", "taro@mac01", "taro@mac01"},
	{"trimmed", "  Alice  ", "Alice"},
	{"escape sequence", "\x1b[31mevil\x1b[0m", "[31mevil[0m"},
	{"line breaks and tab", "a\nb\tc\r\nd", "a b c d"},
	{"vertical tab and form feed", "a\vb\fc", "a b c"},
	{"line and paragraph separators", "a\u2028b\u2029c", "a b c"},
	{"NEL", "a\u0085b", "a b"},
	{"DEL and C1", "a\x7fb\u009bc\u0080d", "abcd"},
	{"NUL and BEL", "a\x00b\x07c", "abc"},
	{"bidi override", "evil\u202etxt.exe", "eviltxt.exe"},
	{"bidi isolates and marks", "\u2066a\u2067b\u2068c\u2069\u200ed\u200f", "abcd"},
	{"zero-width characters and BOM", "\ufeffa\u200bb\u200cc\u200dd", "abcd"},
	{"ZWJ emoji is split", "👨\u200d👩\u200d👧", "👨👩👧"},
	{"invalid UTF-8", "a\xffb", "a\ufffdb"},
	{"runs of space collapse", "a \t\n  b", "a b"},
	{"control characters only", "\x1b\n\t\u202e\u200b", ""},
	{"empty", "", ""},
	{"forged line", "Alice\n[graph-engine] error: run stopped", "Alice [graph-engine] error: run stopped"},
}

func TestSanitize(t *testing.T) {
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("%s: Sanitize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestSanitizeCapsInRunes(t *testing.T) {
	for _, unit := range []string{"a", "山"} {
		got := Sanitize(strings.Repeat(unit, MaxRunes+50))
		if n := utf8.RuneCountInString(got); n != MaxRunes {
			t.Fatalf("%q x %d: %d runes, want %d", unit, MaxRunes+50, n, MaxRunes)
		}
		if got != strings.Repeat(unit, MaxRunes) {
			t.Fatalf("%q: not the first %d runes", unit, MaxRunes)
		}
	}
	// A name of exactly MaxRunes runes is kept whole.
	exact := strings.Repeat("山", MaxRunes)
	if got := Sanitize(exact); got != exact {
		t.Fatalf("a name of MaxRunes runes changed")
	}
	// Dropped characters do not count towards the cap.
	padded := strings.Repeat("\x1b", 500) + exact
	if got := Sanitize(padded); got != exact {
		t.Fatalf("dropped characters counted towards the cap")
	}
}

func TestSanitizeDropsSpaceAtTheCut(t *testing.T) {
	// A space would fall on the last rune: it is dropped, not kept at the end.
	in := strings.Repeat("a", MaxRunes-1) + " bcd"
	got := Sanitize(in)
	if strings.HasSuffix(got, " ") || got != strings.Repeat("a", MaxRunes-1) {
		t.Fatalf("Sanitize = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	// A space inside the cap is kept.
	in = strings.Repeat("a", MaxRunes-2) + " bcd"
	if got := Sanitize(in); got != strings.Repeat("a", MaxRunes-2)+" b" {
		t.Fatalf("Sanitize = %q", got)
	}
}

func TestSanitizeIsIdempotent(t *testing.T) {
	inputs := []string{
		strings.Repeat("a", MaxRunes-1) + " bcd",
		strings.Repeat("山 ", MaxRunes),
		strings.Repeat("\u3000x", MaxRunes),
		" \x1b a \u2028 b ",
	}
	for _, c := range cases {
		inputs = append(inputs, c.in)
	}
	for _, in := range inputs {
		once := Sanitize(in)
		if twice := Sanitize(once); twice != once {
			t.Errorf("Sanitize(%q): once %q, twice %q", in, once, twice)
		}
		if n := utf8.RuneCountInString(once); n > MaxRunes {
			t.Errorf("Sanitize(%q) has %d runes", in, n)
		}
	}
}

func TestText(t *testing.T) {
	// The same rules as Sanitize for everything shorter than MaxRunes.
	for _, c := range cases {
		if got := Text(c.in); got != c.want {
			t.Errorf("%s: Text(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
	// An ordinary Japanese sentence, punctuation and full-width space
	// included, and longer than a name may be, comes back unchanged.
	sentence := strings.Repeat("レビューの判定を確認してください。　「承認」か「差し戻し」を選びます、", 5)
	if utf8.RuneCountInString(sentence) <= MaxRunes || utf8.RuneCountInString(sentence) > MaxTextRunes {
		t.Fatalf("test sentence has %d runes", utf8.RuneCountInString(sentence))
	}
	if got := Text(sentence); got != sentence {
		t.Fatalf("Text changed an ordinary sentence: %q", got)
	}
	// Cut to MaxTextRunes runes.
	for _, unit := range []string{"a", "山"} {
		got := Text(strings.Repeat(unit, MaxTextRunes+50))
		if got != strings.Repeat(unit, MaxTextRunes) {
			t.Fatalf("%q: Text kept %d runes, want %d", unit, utf8.RuneCountInString(got), MaxTextRunes)
		}
	}
	// Idempotent.
	for _, in := range []string{sentence, strings.Repeat("a \x1b\n", MaxTextRunes), strings.Repeat("　x", MaxTextRunes)} {
		once := Text(in)
		if twice := Text(once); twice != once {
			t.Fatalf("Text is not idempotent on %q: %q then %q", in, once, twice)
		}
		if n := utf8.RuneCountInString(once); n > MaxTextRunes {
			t.Fatalf("Text kept %d runes", n)
		}
	}
}
