package search

import (
	"slices"
	"strings"
	"testing"
)

func TestParseBuildsMatchExpressions(t *testing.T) {
	a := mustAnalyzer(t, "en", "ru")
	for query, want := range map[string]string{
		// The word being typed matches as a prefix; a trailing space says it is finished.
		`cats`:  `(exact:"cats"* OR stem:"cat")`,
		`cats `: `(exact:"cats" OR stem:"cat")`,
		// Terms are ANDed; only the last one is a prefix.
		`running cats`: `(exact:"running" OR stem:"run") AND (exact:"cats"* OR stem:"cat")`,
		// A phrase matches the exact column only.
		`"running cats"`:     `exact:"running cats"`,
		`"running cats" dog`: `exact:"running cats" AND (exact:"dog"* OR stem:"dog")`,
		`dog "running cats"`: `(exact:"dog" OR stem:"dog") AND exact:"running cats"`,
		`"running ca`:        `exact:"running ca"`, // an unclosed quote runs to the end
		`“smart quotes”`:     `exact:"smart quotes"`,
		`""`:                 ``,
		// No stemmer applies to digits, so there is nothing to OR.
		`2024`: `exact:"2024"*`,
		// Russian.
		`собаки `: `(exact:"собаки" OR stem:"собак")`,
		// Scripts without spaces: pairs of one run are adjacent, so they form a phrase.
		`東京都`:     `exact:"東京 京都"`,
		`東京`:      `exact:"東京"`,
		`東`:       `exact:"東"*`,
		`東京 cat `: `exact:"東京" AND (exact:"cat" OR stem:"cat")`,
		// Nothing searchable.
		``:        ``,
		`   `:     ``,
		`!!! ...`: ``,
	} {
		if got := a.parse(query).match; got != want {
			t.Errorf("parse(%q)\n  got  %s\n  want %s", query, got, want)
		}
	}
}

func TestParseTreatsFTSSyntaxAsWords(t *testing.T) {
	a := mustAnalyzer(t)
	for query, want := range map[string]string{
		`cats AND NOT dogs `: `exact:"cats" AND exact:"and" AND exact:"not" AND exact:"dogs"`,
		`exact:foo `:         `exact:"exact" AND exact:"foo"`,
		`NEAR(a b, 5) `:      `exact:"near" AND exact:"a" AND exact:"b" AND exact:"5"`,
		`a* (b) ^c `:         `exact:"a" AND exact:"b" AND exact:"c"`,
		// Accents and case are folded the same way as in the index.
		`CRÈME Brûlée `: `exact:"creme" AND exact:"brulee"`,
	} {
		if got := a.parse(query).match; got != want {
			t.Errorf("parse(%q)\n  got  %s\n  want %s", query, got, want)
		}
	}
}

func TestParseRecordsWhatToHighlight(t *testing.T) {
	p := mustAnalyzer(t, "en").parse(`"black cats" running sle`)
	for _, exact := range []string{"black", "cats", "running", "sle"} {
		if !p.exact[exact] {
			t.Errorf("exact forms %v lack %q", p.exact, exact)
		}
	}
	if !p.stems["run"] {
		t.Errorf("stems %v lack run", p.stems)
	}
	if p.stems["cat"] {
		t.Error("a phrase word must not highlight by stem")
	}
	if !slices.Equal(p.prefixes, []string{"sle"}) {
		t.Errorf("prefixes = %v", p.prefixes)
	}

	if p := mustAnalyzer(t).parse("東"); !slices.Equal(p.prefixes, []string{"東"}) {
		t.Errorf("single-character prefixes = %v", p.prefixes)
	}
}

func TestParseCapsTheNumberOfTerms(t *testing.T) {
	a := mustAnalyzer(t)
	many := strings.Repeat("word ", 5000)
	if got := strings.Count(a.parse(many).match, " AND "); got != maxQueryTerms-1 {
		t.Errorf("%d terms joined, want %d", got+1, maxQueryTerms)
	}
	phrase := a.parse(`"` + many + `"`).match
	if got := strings.Count(phrase, "word"); got != maxQueryTerms {
		t.Errorf("phrase holds %d words, want %d", got, maxQueryTerms)
	}
}
