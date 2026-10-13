package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

// order returns the positions of each text in body, so tests can check
// which result comes first.
func order(t *testing.T, body string, texts ...string) []int {
	t.Helper()
	var at []int
	for _, text := range texts {
		i := strings.Index(body, text)
		if i < 0 {
			t.Fatalf("the page lacks %q", text)
		}
		at = append(at, i)
	}
	return at
}

func TestSearchFilters(t *testing.T) {
	t.Parallel()
	m := newMarinaSite(t)
	m.say(m.acc, "family", "2", "Marina joined the group", noon, func(msg *model.Message) { msg.Kind = model.KindService })
	path := func(query string) string { return fmt.Sprintf("/s/%d?q=marina%s", m.acc, query) }
	const flight, deposit, keys, joined = "s flight lands", "Sent the deposit", "Dile a", "joined the group"

	body := m.page(path(""))
	has(t, body, `<summary class="filters__summary">Filters</summary>`, `class="filters__sort filters__sort--on">Best match`,
		fmt.Sprintf(`href="/s/%d?q=marina&amp;sort=newest"`, m.acc))
	lacks(t, body, joined)

	newest := m.page(path("&sort=newest"))
	if at := order(t, newest, keys, deposit, flight); !(at[0] < at[1] && at[1] < at[2]) {
		t.Errorf("newest first gave the order %v", at)
	}
	has(t, newest, `class="filters__sort filters__sort--on">Newest first`)

	for name, c := range map[string]struct {
		query      string
		want, lack []string
	}{
		"with attachments":  {"&files=1", []string{deposit}, []string{flight, keys}},
		"deleted at source": {"&deleted=1", []string{flight}, []string{deposit, keys}},
		"service messages":  {"&service=1", []string{joined, flight}, nil},
		"one day":           {"&from=2026-09-27&to=2026-09-27", []string{deposit}, []string{flight, keys}},
		"from a day on":     {"&from=2026-09-27", []string{deposit, keys}, []string{flight}},
		"a bad date":        {"&from=yesterday", []string{deposit, keys, flight}, nil},
	} {
		body := m.page(path(c.query))
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: lacks %q", name, w)
			}
		}
		for _, l := range c.lack {
			if strings.Contains(body, l) {
				t.Errorf("%s: has %q", name, l)
			}
		}
	}

	filtered := m.page(path("&files=1&sort=newest"))
	has(t, filtered, `<summary class="filters__summary">Filters (1)</summary>`, `<details class="filters__more" open>`,
		`name="files" value="1" checked`, `<input type="hidden" name="sort" value="newest">`,
		fmt.Sprintf(`href="/s/%d/c/%d?at=%d&amp;q=marina&amp;sort=newest&amp;files=1"`, m.acc, m.dan, m.deposit),
		fmt.Sprintf(`href="/s/%d?q=marina&amp;sort=newest">Clear filters`, m.acc))
}
