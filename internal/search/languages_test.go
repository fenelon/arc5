package search

import (
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/blevesearch/snowballstem"
)

func mustAnalyzer(t *testing.T, codes ...string) *Analyzer {
	t.Helper()
	a, err := NewAnalyzer(codes)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLanguagesAreSortedAndComplete(t *testing.T) {
	langs := Languages()
	if len(langs) != 18 {
		t.Fatalf("%d languages, want 18", len(langs))
	}
	if !sort.SliceIsSorted(langs, func(i, j int) bool { return langs[i].Code < langs[j].Code }) {
		t.Error("languages are not sorted by code")
	}
	for _, l := range langs {
		if l.Code == "" || l.Name == "" || l.script == nil || l.stem == nil {
			t.Errorf("incomplete language %+v", l)
		}
	}
}

// Every stemmer must run on a word in its own script.
func TestEveryLanguageStems(t *testing.T) {
	samples := map[string]string{
		"ar": "الكتاب", "da": "bøgerne", "de": "häuser", "en": "houses", "es": "casas", "fi": "taloissa",
		"fr": "maisons", "ga": "tithe", "hu": "házak", "it": "case", "nl": "huizen", "no": "husene",
		"pt": "casas", "ro": "casele", "ru": "дома", "sv": "husen", "ta": "வீடுகள்", "tr": "evler",
	}
	for _, l := range Languages() {
		word, ok := samples[l.Code]
		if !ok {
			t.Errorf("no sample word for %s", l.Code)
			continue
		}
		got := mustAnalyzer(t, l.Code).stems(normalizeWord(word))
		if len(got) != 1 || got[0] == "" {
			t.Errorf("%s: stems(%q) = %q, want one non-empty stem", l.Code, word, got)
		}
	}
}

func TestNewAnalyzer(t *testing.T) {
	a := mustAnalyzer(t, "ru", "en", "ru")
	if got := a.Signature(); got != "v1:en,ru" {
		t.Errorf("signature = %q, want v1:en,ru", got)
	}
	if got := a.Languages(); !slices.Equal(got, []string{"en", "ru"}) {
		t.Errorf("languages = %v", got)
	}
	if got := mustAnalyzer(t).Signature(); got != "v1:" {
		t.Errorf("signature with no languages = %q", got)
	}
	if _, err := NewAnalyzer([]string{"en", "xx"}); err == nil || !strings.Contains(err.Error(), `"xx"`) {
		t.Errorf("unknown language error = %v", err)
	}
}

func TestParseLanguages(t *testing.T) {
	if got := ParseLanguages(" EN, ru ,,es "); !slices.Equal(got, []string{"en", "ru", "es"}) {
		t.Errorf("ParseLanguages = %v", got)
	}
	if got := ParseLanguages(""); len(got) != 0 {
		t.Errorf("ParseLanguages of empty = %v", got)
	}
}

func TestStems(t *testing.T) {
	en, ru, es := mustAnalyzer(t, "en"), mustAnalyzer(t, "ru"), mustAnalyzer(t, "es")
	for _, c := range []struct {
		a    *Analyzer
		word string
		want []string
	}{
		{en, "cats", []string{"cat"}},
		{en, "cat", []string{"cat"}},
		{en, "running", []string{"run"}},
		{ru, "собаки", []string{"собак"}},
		{ru, "собака", []string{"собак"}},
		{ru, "собаку", []string{"собак"}},
		{es, "canciones", []string{"cancion"}},
		{es, "canción", []string{"cancion"}}, // stemmed with its accent, folded afterwards
		{en, "собаки", nil},                  // no configured language uses this script
		{en, "2024", nil},
		{en, strings.Repeat("a", 65), nil}, // too long to be a word
	} {
		if got := c.a.stems(c.word); !slices.Equal(got, c.want) {
			t.Errorf("%s stems(%q) = %q, want %q", c.a.Signature(), c.word, got, c.want)
		}
	}
}

func TestSeveralLanguagesOfOneScriptEachContributeAStem(t *testing.T) {
	got := mustAnalyzer(t, "en", "es").stems("running")
	if !slices.Contains(got, "run") || len(got) == 0 || len(got) > 2 {
		t.Fatalf("stems = %q; want the English stem among at most two", got)
	}
	if unique := slices.Compact(slices.Sorted(slices.Values(got))); len(unique) != len(got) {
		t.Fatalf("stems = %q contain a duplicate", got)
	}
}

// A stemmer is generated code run on arbitrary text; one bad word must not stop ingestion.
func TestPanickingStemmerYieldsNoStem(t *testing.T) {
	a := &Analyzer{langs: []Language{{
		Code: "xx", Name: "Broken", script: unicode.Latin,
		stem: func(*snowballstem.Env) bool { panic("boom") },
	}}}
	if got := a.stems("word"); got != nil {
		t.Fatalf("stems = %q, want none", got)
	}
}
