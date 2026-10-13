package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden files were written by hand from what a reader sees when the
// HTML is shown in a browser. There is no flag to rewrite them from the
// converter's output: a change of output is a change of the rules.
func corpus(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob("testdata/html/*.html")
	if err != nil || len(files) < 10 {
		t.Fatalf("corpus: %d files, %v", len(files), err)
	}
	out := map[string][]byte{}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".html")] = body
	}
	return out
}

func TestHTMLText(t *testing.T) {
	for name, body := range corpus(t) {
		want, err := os.ReadFile(filepath.Join("testdata/html", name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := HTMLText(body, ""); got != strings.TrimSuffix(string(want), "\n") {
			t.Errorf("%s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}

// TestHTMLHasNoResidue checks the corpus leaves nothing of the markup, the
// styles, the hidden text or the tracking in the text the archive shows and
// indexes.
func TestHTMLHasNoResidue(t *testing.T) {
	for name, body := range corpus(t) {
		got := HTMLText(body, "")
		for _, residue := range []string{
			"{", "}", "@media", "@font-face", "<o:p", "<p", "<div", "</", "display:", "mso-", "MsoNormal",
			"open.harbour-yc.example", "track/open", "unsubscribe?", "Regatta results", "Never shown", "Hidden",
			"Turn on scripts", "svg text", "alert(", "\u00a0", "\u200b", "\u200c", "\ufeff", "\u00ad", "\u034f", "\r", "\n\n\n",
		} {
			if strings.Contains(got, residue) {
				t.Errorf("%s: the text contains %q", name, residue)
			}
		}
		for _, line := range strings.Split(got, "\n") {
			if line != strings.TrimRight(line, " \t") {
				t.Errorf("%s: trailing space on %q", name, line)
			}
		}
	}
}

func TestHTMLTextCharsets(t *testing.T) {
	// A part whose MIME header named a charset arrives as UTF-8 already: a
	// <meta> saying otherwise must not convert it twice.
	if got := HTMLText([]byte(`<meta charset="windows-1251"><p>Привет</p>`), "utf-8"); got != "Привет" {
		t.Errorf("declared UTF-8: %q", got)
	}
	// No declaration at all: UTF-8 when it is valid.
	if got := HTMLText([]byte(`<p>Café</p>`), ""); got != "Café" {
		t.Errorf("undeclared UTF-8: %q", got)
	}
	// Invalid bytes are replaced, never passed on.
	if got := HTMLText([]byte("<p>caf\xe9</p>"), "utf-8"); got != "caf\uFFFD" {
		t.Errorf("invalid UTF-8: %q", got)
	}
}

func TestHTMLTextEdgeCases(t *testing.T) {
	for in, want := range map[string]string{
		"":                              "",
		"plain words, no tags":          "plain words, no tags",
		"<p>unclosed <b>bold<p>next":    "unclosed bold\n\nnext",
		"<table><tr><td>a<td>b</table>": "a\tb",
		"<blockquote><blockquote>deep</blockquote></blockquote>": "> > deep",
		"<ul><li></li><li>only</li></ul>":                        "• only",
		"<p>a</p><!-- comment --><p>b</p>":                       "a\n\nb",
	} {
		if got := HTMLText([]byte(in), "utf-8"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
