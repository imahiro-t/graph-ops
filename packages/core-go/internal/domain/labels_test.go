package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestParseLabelColor(t *testing.T) {
	if len(LabelColors) != 10 {
		t.Fatalf("the fixed palette must have 10 colors, got %d", len(LabelColors))
	}
	for _, c := range LabelColors {
		got, err := ParseLabelColor(string(c))
		if err != nil || got != c {
			t.Errorf("ParseLabelColor(%q) = %q, %v", c, got, err)
		}
	}
	for _, bad := range []string{"", "magenta", "Red", " red", "rainbow"} {
		_, err := ParseLabelColor(bad)
		var apiErr *APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.Code != ErrCodeInvalidLabelColor {
			t.Errorf("ParseLabelColor(%q): expected INVALID_LABEL_COLOR, got %v", bad, err)
		}
	}
}

func TestNormalizeLabelName(t *testing.T) {
	fifty := strings.Repeat("a", 50)
	fiftyKanji := strings.Repeat("字", 50)
	ok := map[string]string{
		"バグ":                "バグ",
		"  機能追加  ":          "機能追加",
		"\t Bug \n":         "Bug",
		"　全角　":              "全角",
		fifty:               fifty,
		fiftyKanji:          fiftyKanji,
		"  " + fifty + "  ": fifty,
	}
	for in, want := range ok {
		got, err := NormalizeLabelName(in)
		if err != nil || got != want {
			t.Errorf("NormalizeLabelName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "　", fifty + "a", fiftyKanji + "字"} {
		_, err := NormalizeLabelName(bad)
		var apiErr *APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.Code != ErrCodeInvalidLabelName {
			t.Errorf("NormalizeLabelName(%q): expected INVALID_LABEL_NAME, got %v", bad, err)
		}
	}
}
