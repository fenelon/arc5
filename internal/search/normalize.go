// Package search turns text into index terms, parses queries, and runs them
// against the archive's full-text index.
package search

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// normalizeWord prepares a word for stemming. Compatibility forms are unified
// and case is dropped. ё becomes е: Russian treats them as one letter, and so
// does its stemmer. Anything that is not a letter, number or mark, which
// normalization can introduce, is removed.
func normalizeWord(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(norm.NFKC.String(raw)) {
		switch {
		case r == 'ё':
			b.WriteRune('е')
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fold removes accents from Latin and Greek letters, so "creme" finds
// "crème". Other scripts are left alone: their marks carry meaning, and й is
// not an accented и.
func fold(s string) string {
	var b strings.Builder
	foldable := false
	for _, r := range norm.NFD.String(s) {
		if foldable && r >= 0x0300 && r <= 0x036F {
			continue
		}
		foldable = unicode.In(r, unicode.Latin, unicode.Greek)
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
}
