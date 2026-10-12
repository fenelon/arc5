package search

import (
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func exacts(tokens []Token) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = t.Exact
	}
	return out
}

func TestTokensOfSpacedText(t *testing.T) {
	a := mustAnalyzer(t)
	for text, want := range map[string][]string{
		"Hello, World!":         {"hello", "world"},
		"don't stop":            {"don", "t", "stop"},
		"Crème brûlée":          {"creme", "brulee"},
		"Ёлка и ёж":             {"елка", "и", "еж"},
		"мой дом":               {"мой", "дом"},
		"ＡＢＣ ﬁne":               {"abc", "fine"},
		"report_2024-final.pdf": {"report", "2024", "final", "pdf"},
		"a\xffb":                {"a", "b"}, // invalid UTF-8 separates
		"👍🏽 ok 👨‍👩‍👧 fine":      {"ok", "fine"},
		"":                      nil,
		" \n\t ":                nil,
		"!!! ... ???":           nil,
		"नमस्ते दुनिया":         {norm.NFKC.String("नमस्ते"), norm.NFKC.String("दुनिया")},
	} {
		if got := exacts(a.Tokens(text)); !slices.Equal(got, want) {
			t.Errorf("Tokens(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestTokensOfUnspacedScriptsAreCharacterPairs(t *testing.T) {
	a := mustAnalyzer(t, "en")
	for text, want := range map[string][]string{
		"東京都":          {"東京", "京都", "都"},
		"東":            {"東"},
		"コーヒー":         {"コー", "ーヒ", "ヒー", "ー"},
		"iPhone15を買った": {"iphone15", "を買", "買っ", "った", "た"},
		"東京 タワー":       {"東京", "京", "タワ", "ワー", "ー"},
	} {
		tokens := a.Tokens(text)
		if got := exacts(tokens); !slices.Equal(got, want) {
			t.Errorf("Tokens(%q) = %q, want %q", text, got, want)
		}
		for _, tok := range tokens {
			if unicode.Is(unicode.Latin, []rune(tok.Exact)[0]) == tok.Gram {
				t.Errorf("Tokens(%q): %q has Gram = %v", text, tok.Exact, tok.Gram)
			}
			if tok.Gram && tok.Stems != nil {
				t.Errorf("Tokens(%q): pair %q has stems %q", text, tok.Exact, tok.Stems)
			}
		}
	}

	thai := a.Tokens("สวัสดี") // six characters: five overlapping pairs, then the last one alone
	if len(thai) != 6 {
		t.Fatalf("Thai tokens = %q, want 5 pairs and the last character", exacts(thai))
	}
}

func TestTokenOffsetsPointIntoTheOriginalText(t *testing.T) {
	a := mustAnalyzer(t)
	text := "Hi, Bób — 東京都!"
	for _, tok := range a.Tokens(text) {
		if tok.Start < 0 || tok.End > len(text) || tok.Start >= tok.End {
			t.Fatalf("token %+v is out of range", tok)
		}
		if !utf8.ValidString(text[tok.Start:tok.End]) {
			t.Errorf("token %+v splits a character: %q", tok, text[tok.Start:tok.End])
		}
	}
	tokens := a.Tokens(text)
	if got := text[tokens[1].Start:tokens[1].End]; got != "Bób" {
		t.Errorf("second token covers %q, want Bób", got)
	}
	pairs := tokens[2:]
	if len(pairs) != 3 || text[pairs[0].Start:pairs[0].End] != "東京" || text[pairs[1].Start:pairs[1].End] != "京都" ||
		text[pairs[2].Start:pairs[2].End] != "都" || !pairs[2].tail || pairs[0].tail || pairs[1].tail {
		t.Errorf("pairs = %+v", pairs)
	}
	if pairs[1].Start >= pairs[0].End {
		t.Error("pairs of one run must overlap")
	}
}

func TestTokensCarryStems(t *testing.T) {
	tokens := mustAnalyzer(t, "en", "ru").Tokens("The cats were running, собаки тоже")
	byExact := map[string][]string{}
	for _, tok := range tokens {
		byExact[tok.Exact] = tok.Stems
	}
	for exact, want := range map[string][]string{"cats": {"cat"}, "running": {"run"}, "собаки": {"собак"}} {
		if got := byExact[exact]; !slices.Equal(got, want) {
			t.Errorf("stems of %q = %q, want %q", exact, got, want)
		}
	}
}

func TestIndexColumns(t *testing.T) {
	exact, stem := mustAnalyzer(t, "en").Index("The cats, running!")
	if exact != "the cats running " {
		t.Errorf("exact = %q", exact)
	}
	if stem != "the cat run " {
		t.Errorf("stem = %q", stem)
	}
	exact, stem = mustAnalyzer(t).Index("東京都 2024")
	if exact != "東京 京都 都 2024 " || stem != "" {
		t.Errorf("exact = %q, stem = %q", exact, stem)
	}
}

// The ascii tokenizer splits on every ASCII character that is not a letter or
// digit, so no token may contain one.
func TestTokensNeverContainAsciiSeparators(t *testing.T) {
	a := mustAnalyzer(t, "en", "ru", "es")
	text := "It's a \"test\": foo_bar-baz (x) [y] {z} a.b,c;d 'q' 50% #tag @me \\ / | ½ ²  ㎏ ＡＢ＿Ｃ "
	for _, tok := range a.Tokens(text) {
		for _, form := range append([]string{tok.Exact}, tok.Stems...) {
			if form == "" || strings.ContainsFunc(form, func(r rune) bool {
				return r < utf8.RuneSelf && !unicode.IsLetter(r) && !unicode.IsDigit(r)
			}) {
				t.Errorf("token form %q (from %q) would be split by the index", form, text[tok.Start:tok.End])
			}
		}
	}
}

// Text that reads the same must be indexed the same, however it was typed or stored.
func TestEquivalentSpellingsIndexAlike(t *testing.T) {
	a := mustAnalyzer(t, "en", "ru")
	for _, c := range []struct{ name, odd, plain string }{
		{"soft hyphen inside a word", "an exam\u00adple here", "an example here"},
		{"zero-width non-joiner inside a word", "ex\u200cample", "example"},
		{"stress marks on Russian vowels", "большо\u0301й за\u0301мок", "большой замок"},
		{"decomposed kana, as in macOS file names", norm.NFD.String("ドキュメント"), "ドキュメント"},
		{"half-width kana", "ｶﾞｲﾄﾞ", "ガイド"},
		{"decomposed accents", norm.NFD.String("crème brûlée"), "crème brûlée"},
		{"keycap digit", "1\ufe0f\u20e3 one", "1 one"},
	} {
		oddExact, oddStem := a.Index(c.odd)
		exact, stem := a.Index(c.plain)
		if oddExact != exact || oddStem != stem {
			t.Errorf("%s:\n  %q indexes as %q / %q\n  %q indexes as %q / %q", c.name, c.odd, oddExact, oddStem, c.plain, exact, stem)
		}
	}
	// Letters that merely look accented keep their identity.
	for _, word := range []string{"мой", "ѓорѓи", "ёж"} {
		if exact, _ := a.Index(word); exact != fold(normalizeWord(word))+" " {
			t.Errorf("Index(%q) = %q", word, exact)
		}
	}
	if exact, _ := a.Index("ѓ г"); exact != "ѓ г " {
		t.Errorf("Macedonian ѓ was folded into г: %q", exact)
	}
}
