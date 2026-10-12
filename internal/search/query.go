package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxQueryTerms bounds the work one query can ask for. Words past the
	// limit, in the query or in a phrase, are ignored.
	maxQueryTerms = 32
	// minPrefixRunes is the shortest word that matches as a prefix while it
	// is being typed. One or two letters begin a large share of any archive,
	// which is slow to rank and no use to see; until the word is longer it is
	// searched as it stands.
	minPrefixRunes = 3
)

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
	query = prepare(plainQuotes.Replace(query))
	// A word still being typed matches as a prefix. Trailing space says it is finished.
	typing := strings.TrimRightFunc(query, unicode.IsSpace) == query

	var groups []string
	parts := strings.Split(query, `"`) // odd-numbered parts were inside quotes
	for i, part := range parts {
		tokens := withoutTails(a.Tokens(part))
		if len(tokens) == 0 {
			continue
		}
		if i%2 == 1 {
			groups = append(groups, p.phrase(tokens))
			continue
		}
		groups = append(groups, p.words(tokens, typing && i == len(parts)-1)...)
	}
	if len(groups) > maxQueryTerms {
		groups = groups[:maxQueryTerms]
	}
	p.match = strings.Join(groups, " AND ")
	return p
}

// withoutTails drops the lone last characters of unspaced runs. They are
// indexed so that single characters can be found; a query for the run itself
// is made of its pairs.
func withoutTails(tokens []Token) []Token {
	kept := tokens[:0]
	for _, t := range tokens {
		if !t.tail {
			kept = append(kept, t)
		}
	}
	return kept
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
	expr := `exact:"` + strings.Join(words, " ") + `"`
	// A single character of an unspaced script is indexed as the start of a
	// token (a pair, or the last character of its run), never as a word of
	// its own inside a run, so it has to match as a prefix.
	if last := tokens[len(tokens)-1]; last.Gram && utf8.RuneCountInString(last.Exact) == 1 {
		p.prefixes = append(p.prefixes, last.Exact)
		expr += "*"
	}
	return expr
}

// words returns one group per unquoted word.
func (p *parsed) words(tokens []Token, lastIsPrefix bool) []string {
	var groups []string
	for i := 0; i < len(tokens); {
		t := tokens[i]
		if t.Gram {
			// The character pairs of one run overlap; together they are a phrase.
			j := i + 1
			for j < len(tokens) && tokens[j].Gram && tokens[j].Start < tokens[j-1].End {
				j++
			}
			groups = append(groups, p.phrase(tokens[i:j]))
			i = j
			continue
		}

		p.exact[t.Exact] = true
		terms := make([]string, 0, 1+len(t.Stems))
		if lastIsPrefix && i == len(tokens)-1 && utf8.RuneCountInString(t.Exact) >= minPrefixRunes {
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
