package domain

import "testing"

// DFLT-00138: the color `graph-engine create-label` picks when --color is
// omitted.
func TestPickLabelColor(t *testing.T) {
	all := append([]LabelColor(nil), LabelColors...)
	plus := func(base []LabelColor, extra ...LabelColor) []LabelColor {
		return append(append([]LabelColor(nil), base...), extra...)
	}
	cases := []struct {
		name string
		used []LabelColor
		want LabelColor
	}{
		{"no labels yet", nil, LabelColorGray},
		{"first unused in palette order", []LabelColor{LabelColorGray, LabelColorRed}, LabelColorOrange},
		{"gap in the middle", []LabelColor{LabelColorGray, LabelColorRed, LabelColorAmber, LabelColorPink}, LabelColorOrange},
		{"only the last color unused", all[:9], LabelColorPink},
		{"all used once -> palette order tie-break", all, LabelColorGray},
		{"all used, gray twice -> least used, earliest", plus(all, LabelColorGray), LabelColorRed},
		{"all used, least used is late in the palette", plus(plus(all, all[:8]...), LabelColorPink), LabelColorPurple},
		{"colors outside the palette are ignored", []LabelColor{"crimson", "Red", LabelColorGray}, LabelColorRed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PickLabelColor(tc.used); got != tc.want {
				t.Errorf("PickLabelColor(%v) = %q, want %q", tc.used, got, tc.want)
			}
		})
	}
}
