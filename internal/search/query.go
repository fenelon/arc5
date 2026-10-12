package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxQueryTerms bounds a query, and a phrase, so that a pasted page of text
// cannot build an expression the index refuses.
const maxQueryTerms = 32

// parsed is a query in the two forms it is used: the expression the index is
// asked, and what a snippet should highlight.
type parsed struct {
	match    string          // FTS5 MATCH expression; empty when nothing is searchable
	exact    map[string]bool // exact forms of every query word
	stems    map[string]bool // stems of the unquoted words
	prefixes []string        // exact forms that match as a prefix
}

// Phones and word processors type these in place of a plain double quote.
var plainQuotes = strings.NewReplacer("“", `"`, "”", `"`, "„", `"`)

// parse turns what the owner typed into an FTS5 MATCH expression.
//
// Unquoted words are ANDed, each matching its exact form or any of its stems.
// Quoted text is a phrase on the exact column. Every term is emitted as a
// quoted string built from analyzer tokens, which contain only letters,
// numbers and marks, so nothing the owner types can act as FTS5 syntax.
func (a *Analyzer) parse(query string) parsed {
	p := parsed{exact: map[string]bool{}, stems: map[string]bool{}}
	query = plainQuotes.Replace(query)
	// A word still being typed matches as a prefix. Trailing space says it is finished.
	typing := strings.TrimRightFunc(query, unicode.IsSpace) == query

	var groups []string
	parts := strings.Split(query, `"`) // odd-numbered parts were inside quotes
	for i, part := range parts {
		tokens := a.Tokens(part)
		if len(tokens) == 0 {
			continue
		}
		if i%2 == 1 {
			groups = append(groups, p.phrase(tokens))
			continue
		}
		groups = append(groups, p.words(part, tokens, typing && i == len(parts)-1)...)
	}
	if len(groups) > maxQueryTerms {
		groups = groups[:maxQueryTerms]
	}
	p.match = strings.Join(groups, " AND ")
	return p
}

// phrase returns a phrase on the exact column.
func (p *parsed) phrase(tokens []Token) string {
	if len(tokens) > maxQueryTerms {
		tokens = tokens[:maxQueryTerms]
	}
	words := make([]string, len(tokens))
	for i, t := range tokens {
		words[i] = t.Exact
		p.exact[t.Exact] = true
	}
	return `exact:"` + strings.Join(words, " ") + `"`
}

// words returns one group per unquoted word of text.
func (p *parsed) words(text string, tokens []Token, lastIsPrefix bool) []string {
	var groups []string
	for i := 0; i < len(tokens); {
		t := tokens[i]
		if t.Gram {
			// The character pairs of one run overlap; together they are a phrase.
			j := i + 1
			for j < len(tokens) && tokens[j].Gram && tokens[j].Start < tokens[j-1].End {
				j++
			}
			if j == i+1 && utf8.RuneCountInString(text[t.Start:t.End]) == 1 {
				// One character is stored only as the start of a pair.
				p.exact[t.Exact] = true
				p.prefixes = append(p.prefixes, t.Exact)
				groups = append(groups, `exact:"`+t.Exact+`"*`)
			} else {
				groups = append(groups, p.phrase(tokens[i:j]))
			}
			i = j
			continue
		}

		p.exact[t.Exact] = true
		terms := make([]string, 0, 1+len(t.Stems))
		if lastIsPrefix && i == len(tokens)-1 {
			p.prefixes = append(p.prefixes, t.Exact)
			terms = append(terms, `exact:"`+t.Exact+`"*`)
		} else {
			terms = append(terms, `exact:"`+t.Exact+`"`)
		}
		for _, s := range t.Stems {
			p.stems[s] = true
			terms = append(terms, `stem:"`+s+`"`)
		}
		if len(terms) == 1 {
			groups = append(groups, terms[0])
		} else {
			groups = append(groups, "("+strings.Join(terms, " OR ")+")")
		}
		i++
	}
	return groups
}
