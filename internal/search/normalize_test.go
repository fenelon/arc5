package search

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestNormalizeWord(t *testing.T) {
	for raw, want := range map[string]string{
		"Hello":  "hello",
		"ＡＢＣ":    "abc",  // fullwidth forms
		"ﬁne":    "fine", // ligature
		"Ёлка":   "елка",
		"ёж":     "еж",
		"½":      "12", // the fraction slash is not a letter
		"Straße": "straße",
		"x²":     "x2",
	} {
		if got := normalizeWord(raw); got != want {
			t.Errorf("normalizeWord(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestFoldRemovesAccentsFromLatinAndGreekOnly(t *testing.T) {
	for in, want := range map[string]string{
		"crème":    "creme",
		"brûlée":   "brulee",
		"canción":  "cancion",
		"ñandú":    "nandu",
		"x̂":       "x", // a combining mark that has no precomposed form
		"ελληνικά": "ελληνικα",
		"мой":      "мой", // й is a letter of its own, not an accented и
		"한국어":      "한국어",
		"नमस्ते":   norm.NFC.String("नमस्ते"),
		"plain":    "plain",
		"":         "",
	} {
		if got := fold(in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}
