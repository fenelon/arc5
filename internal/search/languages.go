package search

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/arabic"
	"github.com/blevesearch/snowballstem/danish"
	"github.com/blevesearch/snowballstem/dutch"
	"github.com/blevesearch/snowballstem/english"
	"github.com/blevesearch/snowballstem/finnish"
	"github.com/blevesearch/snowballstem/french"
	"github.com/blevesearch/snowballstem/german"
	"github.com/blevesearch/snowballstem/hungarian"
	"github.com/blevesearch/snowballstem/irish"
	"github.com/blevesearch/snowballstem/italian"
	"github.com/blevesearch/snowballstem/norwegian"
	"github.com/blevesearch/snowballstem/portuguese"
	"github.com/blevesearch/snowballstem/romanian"
	"github.com/blevesearch/snowballstem/russian"
	"github.com/blevesearch/snowballstem/spanish"
	"github.com/blevesearch/snowballstem/swedish"
	"github.com/blevesearch/snowballstem/tamil"
	"github.com/blevesearch/snowballstem/turkish"
)

// Language is a language whose word forms the analyzer can reduce to a stem.
type Language struct {
	Code string // ISO 639-1
	Name string

	script *unicode.RangeTable
	stem   func(*snowballstem.Env) bool
}

// languages is sorted by code.
var languages = []Language{
	{"ar", "Arabic", unicode.Arabic, arabic.Stem},
	{"da", "Danish", unicode.Latin, danish.Stem},
	{"de", "German", unicode.Latin, german.Stem},
	{"en", "English", unicode.Latin, english.Stem},
	{"es", "Spanish", unicode.Latin, spanish.Stem},
	{"fi", "Finnish", unicode.Latin, finnish.Stem},
	{"fr", "French", unicode.Latin, french.Stem},
	{"ga", "Irish", unicode.Latin, irish.Stem},
	{"hu", "Hungarian", unicode.Latin, hungarian.Stem},
	{"it", "Italian", unicode.Latin, italian.Stem},
	{"nl", "Dutch", unicode.Latin, dutch.Stem},
	{"no", "Norwegian", unicode.Latin, norwegian.Stem},
	{"pt", "Portuguese", unicode.Latin, portuguese.Stem},
	{"ro", "Romanian", unicode.Latin, romanian.Stem},
	{"ru", "Russian", unicode.Cyrillic, russian.Stem},
	{"sv", "Swedish", unicode.Latin, swedish.Stem},
	{"ta", "Tamil", unicode.Tamil, tamil.Stem},
	{"tr", "Turkish", unicode.Latin, turkish.Stem},
}

// Languages lists every language stemming is available for, sorted by code.
func Languages() []Language { return slices.Clone(languages) }

// DefaultLanguages is the language list used until the owner chooses one.
const DefaultLanguages = "en"

// ParseLanguages splits a comma-separated setting such as "en, ru,es".
func ParseLanguages(setting string) []string {
	var codes []string
	for _, c := range strings.Split(setting, ",") {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			codes = append(codes, c)
		}
	}
	return codes
}

const (
	// analyzerVersion changes whenever analysis changes in a way that makes
	// an existing index wrong, which forces a rebuild.
	analyzerVersion = "v1"
	// maxStemBytes keeps stemmers away from tokens that are not words, such
	// as pasted keys and hashes.
	maxStemBytes = 64
)

// Analyzer turns text into index terms for a chosen set of languages.
type Analyzer struct {
	langs []Language // sorted by code, no duplicates
}

// NewAnalyzer returns an analyzer that stems the given languages. With no
// languages it still finds exact words, prefixes and phrases.
func NewAnalyzer(codes []string) (*Analyzer, error) {
	a := &Analyzer{}
	for _, code := range codes {
		if slices.ContainsFunc(a.langs, func(l Language) bool { return l.Code == code }) {
			continue
		}
		i := slices.IndexFunc(languages, func(l Language) bool { return l.Code == code })
		if i < 0 {
			known := make([]string, len(languages))
			for k, l := range languages {
				known[k] = l.Code
			}
			return nil, fmt.Errorf("unknown search language %q; available: %s", code, strings.Join(known, ", "))
		}
		a.langs = append(a.langs, languages[i])
	}
	sort.Slice(a.langs, func(i, j int) bool { return a.langs[i].Code < a.langs[j].Code })
	return a, nil
}

// Languages returns the configured language codes, sorted.
func (a *Analyzer) Languages() []string {
	codes := make([]string, len(a.langs))
	for i, l := range a.langs {
		codes[i] = l.Code
	}
	return codes
}

// Signature identifies this analysis. An index built under another signature is stale.
func (a *Analyzer) Signature() string {
	return analyzerVersion + ":" + strings.Join(a.Languages(), ",")
}

// stems returns the distinct stems of a normalized word under every
// configured language written in the word's script, accents folded.
func (a *Analyzer) stems(word string) []string {
	if len(word) > maxStemBytes {
		return nil
	}
	first, _ := utf8.DecodeRuneInString(word)
	var out []string
	for _, lang := range a.langs {
		if !unicode.Is(lang.script, first) {
			continue
		}
		if s := fold(runStemmer(lang, word)); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// runStemmer returns "" if the stemmer panics: it is generated code run on
// arbitrary text, and one bad word must not stop a message being stored.
func runStemmer(lang Language, word string) (stem string) {
	defer func() {
		if recover() != nil {
			stem = ""
		}
	}()
	env := snowballstem.NewEnv(word)
	lang.stem(env)
	return env.Current()
}
