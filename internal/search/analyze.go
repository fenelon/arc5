package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token is one searchable unit of a text.
type Token struct {
	Start, End int      // byte offsets of the token in the original text
	Exact      string   // normalized form, as stored in the index's exact column
	Stems      []string // stemmed forms; nil when no configured language applies
	Gram       bool     // a character pair from a script written without spaces
}

// unspaced reports whether r belongs to a script written without spaces
// between words. Such text is indexed as overlapping character pairs, because
// there are no word boundaries to find. ー is the Japanese long-vowel mark,
// which Unicode files under no script.
func unspaced(r rune) bool {
	return r == 'ー' || unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana,
		unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}

func wordStart(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// Tokens splits text into index terms.
func (a *Analyzer) Tokens(text string) []Token {
	var out []Token
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case unspaced(r):
			end := i + size
			for end < len(text) {
				r, size := utf8.DecodeRuneInString(text[end:])
				if !unspaced(r) {
					break
				}
				end += size
			}
			out = appendPairs(out, text, i, end)
			i = end
		case wordStart(r):
			end := i + size
			for end < len(text) {
				r, size := utf8.DecodeRuneInString(text[end:])
				if unspaced(r) || !(wordStart(r) || unicode.IsMark(r)) {
					break
				}
				end += size
			}
			if word := normalizeWord(text[i:end]); word != "" {
				if exact := fold(word); exact != "" {
					out = append(out, Token{Start: i, End: end, Exact: exact, Stems: a.stems(word)})
				}
			}
			i = end
		default:
			i += size
		}
	}
	return out
}

// appendPairs adds the overlapping character pairs of text[start:end], or the
// single character if there is only one.
func appendPairs(out []Token, text string, start, end int) []Token {
	type char struct {
		start, end int
		form       string
	}
	var chars []char
	for i := start; i < end; {
		_, size := utf8.DecodeRuneInString(text[i:])
		if form := normalizeWord(text[i : i+size]); form != "" {
			chars = append(chars, char{i, i + size, form})
		}
		i += size
	}
	if len(chars) == 1 {
		return append(out, Token{Start: chars[0].start, End: chars[0].end, Exact: chars[0].form, Gram: true})
	}
	for k := 0; k+1 < len(chars); k++ {
		out = append(out, Token{Start: chars[k].start, End: chars[k+1].end, Exact: chars[k].form + chars[k+1].form, Gram: true})
	}
	return out
}

// Index returns the text of the index's two columns for a document: tokens
// separated by single spaces. With Signature it implements archive.Analyzer.
func (a *Analyzer) Index(text string) (exact, stem string) {
	var e, s strings.Builder
	for _, t := range a.Tokens(text) {
		e.WriteString(t.Exact)
		e.WriteByte(' ')
		for _, form := range t.Stems {
			s.WriteString(form)
			s.WriteByte(' ')
		}
	}
	return e.String(), s.String()
}
