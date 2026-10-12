package search

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Token is one searchable unit of a text.
type Token struct {
	Start, End int      // byte offsets of the token in the original text
	Exact      string   // normalized form, as stored in the index's exact column
	Stems      []string // stemmed forms; nil when no configured language applies
	Gram       bool     // a character pair from a script written without spaces

	// tail marks the last character of an unspaced run, indexed on its own
	// because it starts no pair. It belongs to the index, not to queries.
	tail bool
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

// joiner reports whether r is an invisible character that sits inside a word
// without separating it: soft hyphen, zero-width joiners, word joiner.
func joiner(r rune) bool {
	return r == 0x00AD || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF
}

func halfwidthKana(r rune) bool { return r >= 0xFF61 && r <= 0xFF9F }

// prepare puts text into the form Tokens expects, so that text which reads
// the same is indexed the same. It is canonically composed (file names from
// macOS arrive decomposed) and half-width kana are widened so they pair up
// like full-width ones. The result reads the same as the original, so
// snippets are cut from it.
func prepare(text string) string {
	text = norm.NFC.String(text)
	if !strings.ContainsFunc(text, halfwidthKana) {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !halfwidthKana(r) {
			b.WriteString(text[i : i+size])
			i += size
			continue
		}
		end := i
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if !halfwidthKana(r) {
				break
			}
			end += size
		}
		b.WriteString(norm.NFKC.String(text[i:end]))
		i = end
	}
	return b.String()
}

// Tokens splits text into index terms. Offsets refer to the text given, which
// should have been through prepare.
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
				if unspaced(r) || !(wordStart(r) || unicode.IsMark(r) || joiner(r)) {
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

// appendPairs adds the overlapping character pairs of text[start:end], then
// its last character on its own. The last character starts no pair, and with
// it indexed every character of the run begins a token, so a one-character
// query can find any of them as a prefix. A run of one character is that
// character alone.
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
	if len(chars) == 0 {
		return out
	}
	for k := 0; k+1 < len(chars); k++ {
		out = append(out, Token{Start: chars[k].start, End: chars[k+1].end, Exact: chars[k].form + chars[k+1].form, Gram: true})
	}
	last := chars[len(chars)-1]
	return append(out, Token{Start: last.start, End: last.end, Exact: last.form, Gram: true, tail: len(chars) > 1})
}

// Index returns the text of the index's two columns for a document: tokens
// separated by single spaces. With Signature it implements archive.Analyzer.
func (a *Analyzer) Index(text string) (exact, stem string) {
	var e, s strings.Builder
	for _, t := range a.Tokens(prepare(text)) {
		e.WriteString(t.Exact)
		e.WriteByte(' ')
		for _, form := range t.Stems {
			s.WriteString(form)
			s.WriteByte(' ')
		}
	}
	return e.String(), s.String()
}
