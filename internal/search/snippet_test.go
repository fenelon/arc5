package search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// render shows a snippet with matches in square brackets.
func render(fragments []Fragment) string {
	var b strings.Builder
	for _, f := range fragments {
		if f.Match {
			b.WriteString("[" + f.Text + "]")
		} else {
			b.WriteString(f.Text)
		}
	}
	return b.String()
}

func TestSnippetHighlightsMatches(t *testing.T) {
	a := mustAnalyzer(t, "en", "ru")
	for _, c := range []struct{ query, text, want string }{
		{`cat `, "The cats sleep", "The [cats] sleep"},            // by stem
		{`cats `, "One cat here", "One [cat] here"},               // by stem, the other way
		{`sle`, "The cats sleep", "The cats [sleep]"},             // by prefix
		{`"cats sleep"`, "The cats sleep", "The [cats] [sleep]"},  // phrase words
		{`собака `, "Мои собаки лают", "Мои [собаки] лают"},       // Russian word form
		{`creme `, "Crème brûlée", "[Crème] brûlée"},              // accent-insensitive
		{`京都`, "東京都に行く", "東[京都]に行く"},                              // character pairs
		{`東京都`, "東京都に行く", "[東京都]に行く"},                             // overlapping pairs merge
		{`cats `, "line one\ncats\there", "line one [cats] here"}, // whitespace flattened
		{`dog `, "The cats sleep", "The cats sleep"},              // no match: plain text
		{`cat `, "", ""},
	} {
		if got := render(a.snippet(c.text, a.parse(c.query))); got != c.want {
			t.Errorf("query %q on %q\n  got  %s\n  want %s", c.query, c.text, got, c.want)
		}
	}
}

func TestSnippetOfLongTextIsAWindowAroundTheFirstMatch(t *testing.T) {
	a := mustAnalyzer(t, "ru")
	text := strings.Repeat("слово ", 100) + "кошки" + strings.Repeat(" слово", 100)
	fragments := a.snippet(text, a.parse("кошки "))

	got := render(fragments)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("a window cut from the middle must be marked at both ends: %q", got)
	}
	if !strings.Contains(got, "[кошки]") {
		t.Errorf("the match is not in the window: %q", got)
	}
	runes := 0
	for _, f := range fragments {
		if !utf8.ValidString(f.Text) {
			t.Fatalf("fragment splits a character: %q", f.Text)
		}
		if f.Text != "…" {
			runes += utf8.RuneCountInString(f.Text)
		}
	}
	if runes > snippetRunes {
		t.Errorf("snippet holds %d characters, limit %d", runes, snippetRunes)
	}
}

func TestSnippetWithoutMatchIsTheStartOfTheText(t *testing.T) {
	a := mustAnalyzer(t)
	text := strings.Repeat("я", 500)
	fragments := a.snippet(text, a.parse("nothing "))
	if len(fragments) != 2 || fragments[0].Match || fragments[1].Text != "…" {
		t.Fatalf("fragments = %+v", fragments)
	}
	if got := utf8.RuneCountInString(fragments[0].Text); got != snippetRunes {
		t.Errorf("snippet holds %d characters, want %d", got, snippetRunes)
	}
}

// Review Focus 2: odd text must not break a snippet.
func TestSnippetSurvivesOddText(t *testing.T) {
	a := mustAnalyzer(t, "en")
	for _, text := range []string{
		"👨‍👩‍👧 family 👍🏽 cats",
		"catś̂̃ with marks",
		strings.Repeat("👍🏽", 300) + " cats",
		"é́́ cats " + strings.Repeat("x̂", 300),
	} {
		for _, f := range a.snippet(text, a.parse("cats ")) {
			if !utf8.ValidString(f.Text) {
				t.Errorf("fragment of %q splits a character: %q", text, f.Text)
			}
		}
	}
}
