// Package displayname makes a person's display name safe to show on
// another member's terminal or screen (DFLT-00336). Since DFLT-00326 the
// name of whoever started an autopilot run travels through the shared data
// source, so a member who can write to it could otherwise plant line
// breaks, terminal escape sequences or bidirectional overrides in a name
// that everybody else's CLI, error messages and Web UI then print.
//
// Since DFLT-00339 the same rules also cover the other strings of another
// member's autopilot run record that reach a display -- mode, state, stop
// reason, the current session's role and awaiting-human note (Text) -- and
// the IDs among them (tokens.go).
//
// The package depends on the standard library only, and must stay that
// way: domain, identity, autopilot and autopilot/runner all use it, and
// identity already depends on autopilot (through runtimeconfig), so a
// dependency on any of them here would be an import cycle.
package displayname

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxRunes is the longest a sanitized name can be, in runes. It fits the
// data sources' started_by_name column (VARCHAR(255), counted in
// characters under utf8mb4) with room to spare.
const MaxRunes = 128

// MaxTextRunes is the longest a sanitized free-form text (Text) can be, in
// runes: the same bound as a run summary's (autopilot.SummaryMaxChars).
const MaxTextRunes = 500

// Sanitize returns s made safe to display:
//
//   - invalid UTF-8 becomes U+FFFD;
//   - tabs and line breaks (\t \n \v \f \r, U+0085, U+2028, U+2029) become
//     a space, so words stay apart;
//   - every other control character (C0 including ESC, DEL, C1) and every
//     invisible format character (category Cf: bidirectional controls,
//     zero-width characters, the BOM, ...) is dropped -- which also splits
//     emoji joined with a zero-width joiner;
//   - a run of white space becomes its first character (so a full-width
//     space in a Japanese name stays as it is), and the ends are trimmed;
//   - the result is cut to MaxRunes runes (and trimmed again).
//
// The result may be "" (a name made only of such characters); callers treat
// that as an unknown name. Ordinary names -- "山田 太郎", "taro@mac01" --
// come back unchanged. Sanitize is idempotent:
// Sanitize(Sanitize(s)) == Sanitize(s).
func Sanitize(s string) string { return sanitize(s, MaxRunes) }

// Text is Sanitize for a free-form text that may be longer than a name --
// a run's awaiting-human note, say (DFLT-00339): the same rules, with the
// result cut to MaxTextRunes runes instead of MaxRunes. Ordinary sentences,
// Japanese punctuation and full-width spaces included, come back unchanged.
// Text is idempotent too.
func Text(s string) string { return sanitize(s, MaxTextRunes) }

// sanitize is Sanitize with the result cut to max runes.
func sanitize(s string, max int) string {
	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	var b strings.Builder
	b.Grow(len(s))
	var space rune // the white space waiting to be written, 0 when none
	n := 0
	for _, r := range s {
		switch {
		case isBreak(r):
			if space == 0 {
				space = ' '
			}
			continue
		case unicode.IsSpace(r):
			if space == 0 {
				space = r
			}
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			continue
		}
		if space != 0 && n > 0 {
			if n+1 >= max {
				break
			}
			b.WriteRune(space)
			n++
		}
		space = 0
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// isBreak reports whether r separates words or lines without being a
// printable character: these become a space rather than being dropped.
func isBreak(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}
