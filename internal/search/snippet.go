package search

import (
	"strings"
	"unicode/utf8"
)

// Fragment is a piece of a snippet. Match fragments are the words the query found.
type Fragment struct {
	Text  string
	Match bool
}

const (
	// snippetRunes is the most text a snippet shows, in characters.
	snippetRunes = 160
	// snippetLead is how much text is shown before the first match.
	snippetLead = 40
)

var flatten = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")

// matches reports whether a token of the text is one the query was looking for.
func (p parsed) matches(t Token) bool {
	if p.exact[t.Exact] {
		return true
	}
	for _, s := range t.Stems {
		if p.stems[s] {
			return true
		}
	}
	for _, prefix := range p.prefixes {
		if strings.HasPrefix(t.Exact, prefix) {
			return true
		}
	}
	return false
}

// matchSpans returns the byte ranges of text the query matched, in order,
// with touching and overlapping ranges merged.
func (a *Analyzer) matchSpans(text string, p parsed) [][2]int {
	var spans [][2]int
	for _, t := range a.Tokens(text) {
		if !p.matches(t) {
			continue
		}
		if n := len(spans); n > 0 && t.Start <= spans[n-1][1] {
			spans[n-1][1] = max(spans[n-1][1], t.End)
			continue
		}
		spans = append(spans, [2]int{t.Start, t.End})
	}
	return spans
}

// back and forward move a byte offset by up to n characters, staying on character boundaries.
func back(text string, pos, n int) int {
	for ; n > 0 && pos > 0; n-- {
		_, size := utf8.DecodeLastRuneInString(text[:pos])
		pos -= size
	}
	return pos
}

func forward(text string, pos, n int) int {
	for ; n > 0 && pos < len(text); n-- {
		_, size := utf8.DecodeRuneInString(text[pos:])
		pos += size
	}
	return pos
}

// snippet returns up to snippetRunes characters of text around the first
// match, or the start of the text if nothing matches, with "…" fragments
// where text was cut.
func (a *Analyzer) snippet(text string, p parsed) []Fragment {
	text = prepare(text)
	if text == "" {
		return nil
	}
	spans := a.matchSpans(text, p)
	start := 0
	if len(spans) > 0 {
		start = back(text, spans[0][0], snippetLead)
	}
	end := forward(text, start, snippetRunes)

	var out []Fragment
	add := func(from, to int, match bool) {
		if from < to {
			out = append(out, Fragment{Text: flatten.Replace(text[from:to]), Match: match})
		}
	}
	if start > 0 {
		out = append(out, Fragment{Text: "…"})
	}
	pos := start
	for _, span := range spans {
		if span[0] >= end {
			break
		}
		add(pos, span[0], false)
		add(span[0], min(span[1], end), true)
		pos = min(span[1], end)
	}
	add(pos, end, false)
	if end < len(text) {
		out = append(out, Fragment{Text: "…"})
	}
	return out
}
