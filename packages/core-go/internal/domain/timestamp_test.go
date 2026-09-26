package domain

import "testing"

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

func TestCompareTimestamps(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"string order disagrees with time order", "2026-09-26T10:00:43.1091Z", "2026-09-26T10:00:43.10910002Z", -1},
		{"no fraction before a fraction", "2026-09-26T10:00:43Z", "2026-09-26T10:00:43.5Z", -1},
		{"same instant, different precision", "2026-09-26T10:00:43.1Z", "2026-09-26T10:00:43.100Z", 0},
		{"same instant, different offset", "2026-09-26T10:00:43Z", "2026-09-26T19:00:43+09:00", 0},
		{"different offset, later instant", "2026-09-26T19:00:42+09:00", "2026-09-26T10:00:43Z", -1},
		{"identical", "2026-09-26T10:00:43.1091Z", "2026-09-26T10:00:43.1091Z", 0},
		{"unparseable before parseable", "not a time", "2000-01-01T00:00:00Z", -1},
		{"empty before parseable", "", "2000-01-01T00:00:00Z", -1},
		{"unparseable values compare as strings", "abc", "abd", -1},
		{"identical unparseable values", "abc", "abc", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sign(CompareTimestamps(c.a, c.b)); got != c.want {
				t.Errorf("CompareTimestamps(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
			}
			// Antisymmetry.
			if got := sign(CompareTimestamps(c.b, c.a)); got != -c.want {
				t.Errorf("CompareTimestamps(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
			}
		})
	}
}
