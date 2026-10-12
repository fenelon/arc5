package search

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
	"golang.org/x/text/unicode/norm"
)

var day = time.Unix(1_700_000_000, 0).UTC()

type fixture struct {
	t      *testing.T
	store  *archive.Store
	search *Searcher
	acc    int64
	n      int
}

// newFixture returns an archive with one account, a private chat "c1" titled
// Alice, and a sender "u1" named Alice.
func newFixture(t *testing.T, langs ...string) *fixture {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a := mustAnalyzer(t, langs...)
	store.SetAnalyzer(a)
	acc, err := store.CreateAccount("test", "Test", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, store: store, search: New(store.Reader(), a), acc: acc}
	f.chat("c1", model.ChatPrivate, "Alice")
	if err := store.UpsertSender(acc, model.Sender{SourceID: "u1", DisplayName: "Alice"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) chat(sourceID string, kind model.ChatKind, title string) {
	f.t.Helper()
	if err := f.store.UpsertChat(f.acc, model.Chat{SourceID: sourceID, Kind: kind, Title: title}); err != nil {
		f.t.Fatal(err)
	}
}

// say stores a message in chat c1 from u1, one minute after the previous one,
// and returns its source ID. change adjusts the message before it is stored.
func (f *fixture) say(text string, change ...func(*model.Message)) string {
	f.t.Helper()
	f.n++
	m := model.Message{
		ChatSourceID: "c1", SourceID: strconv.Itoa(f.n), SenderSourceID: "u1",
		SentAt: day.Add(time.Duration(f.n) * time.Minute), Text: text,
	}
	for _, c := range change {
		c(&m)
	}
	if _, err := f.store.UpsertMessage(f.acc, m); err != nil {
		f.t.Fatal(err)
	}
	return m.SourceID
}

func (f *fixture) page(q Query) Page {
	f.t.Helper()
	page, err := f.search.Search(context.Background(), q)
	if err != nil {
		f.t.Fatalf("Search(%+v): %v", q, err)
	}
	return page
}

// find returns the source IDs of a query's hits, in order.
func (f *fixture) find(q Query) []string {
	f.t.Helper()
	var ids []string
	for _, h := range f.page(q).Hits {
		var id string
		if err := f.store.Reader().QueryRow(`SELECT source_id FROM messages WHERE id = ?`, h.MessageID).Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (f *fixture) text(query string) []string { return f.find(Query{Text: query}) }

func (f *fixture) id(table, sourceID string) int64 {
	f.t.Helper()
	var id int64
	if err := f.store.Reader().QueryRow(`SELECT id FROM `+table+` WHERE source_id = ?`, sourceID).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func same(got []string, want ...string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

func TestFindsWordFormsInSeveralLanguages(t *testing.T) {
	f := newFixture(t, "en", "ru", "es")
	cats := f.say("The cats are sleeping")
	dogs := f.say("Собаки лают")
	songs := f.say("Las canciones de amor")
	cream := f.say("Crème brûlée")
	f.say("Nothing relevant here")

	for query, want := range map[string]string{
		"cat ": cats, "CATS ": cats, "собака ": dogs, "собаку ": dogs,
		"cancion ": songs, "canción ": songs, "creme ": cream, "BRULEE ": cream,
	} {
		if got := f.text(query); !same(got, want) {
			t.Errorf("%q found %v, want [%s]", query, got, want)
		}
	}
}

func TestPhrasePrefixAndConjunction(t *testing.T) {
	f := newFixture(t, "en")
	both := f.say("cats and dogs are sleeping")
	cats := f.say("only cats here")

	if got := f.text(`"cats and dogs"`); !same(got, both) {
		t.Errorf("phrase found %v", got)
	}
	if got := f.text(`"dogs and cats"`); len(got) != 0 {
		t.Errorf("phrase in the wrong order found %v", got)
	}
	if got := f.text(`slee`); !same(got, both) {
		t.Errorf("prefix found %v", got)
	}
	if got := f.text(`slee `); len(got) != 0 {
		t.Errorf("a finished word must not match as a prefix: %v", got)
	}
	if got := f.text(`cats dogs `); !same(got, both) {
		t.Errorf("two terms found %v", got)
	}
	if got := f.text(`cats `); !same(got, both, cats) {
		t.Errorf("one term found %v", got)
	}
}

func TestUnspacedScripts(t *testing.T) {
	f := newFixture(t, "en")
	tokyo := f.say("東京都に行く")
	f.say("大阪に行く")
	for _, query := range []string{"京都", "東京都", "東", "都"} {
		if got := f.text(query); !same(got, tokyo) {
			t.Errorf("%q found %v, want [%s]", query, got, tokyo)
		}
	}
	if got := f.text("行く"); len(got) != 2 {
		t.Errorf("shared pair found %v, want both", got)
	}
}

func TestExactWordRanksAboveWordForm(t *testing.T) {
	f := newFixture(t, "en")
	for _, filler := range []string{"nothing to see", "move along now", "quite unrelated text"} {
		f.say(filler)
	}
	exact := f.say("black cat here")
	form := f.say("black cats here") // newer, so only its score can put it second
	if got := f.text("cat "); !slices.Equal(got, []string{exact, form}) {
		t.Errorf("by relevance = %v, want [%s %s]", got, exact, form)
	}
	if got := f.find(Query{Text: "cat ", Sort: ByNewest}); !slices.Equal(got, []string{form, exact}) {
		t.Errorf("by newest = %v, want [%s %s]", got, form, exact)
	}
}

func TestFilters(t *testing.T) {
	f := newFixture(t, "en")
	f.chat("g1", model.ChatGroup, "Group")
	plain := f.say("cats one")
	inGroup := f.say("cats two", func(m *model.Message) { m.ChatSourceID = "g1" })
	fromBob := f.say("cats three", func(m *model.Message) { m.SenderSourceID = "u2" })
	withFile := f.say("cats four", func(m *model.Message) {
		m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "a.pdf", Size: 1}}
	})
	deleted := f.say("cats five")
	service := f.say("cats six", func(m *model.Message) { m.Kind = model.KindService })
	if err := f.store.MarkDeleted(f.acc, "c1", []string{deleted}); err != nil {
		t.Fatal(err)
	}

	other, err := f.store.CreateAccount("test", "Other", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertChat(other, model.Chat{SourceID: "x1", Kind: model.ChatPrivate, Title: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpsertMessage(other, model.Message{ChatSourceID: "x1", SourceID: "other", SentAt: day, Text: "cats elsewhere"}); err != nil {
		t.Fatal(err)
	}

	q := func(change func(*Query)) []string {
		query := Query{Text: "cats "}
		change(&query)
		return f.find(query)
	}
	// By default: every account, deleted messages included, service messages not.
	if got := f.text("cats "); !same(got, plain, inGroup, fromBob, withFile, deleted, "other") {
		t.Errorf("no filter = %v", got)
	}
	if got := q(func(q *Query) { q.IncludeService = true }); !slices.Contains(got, service) {
		t.Errorf("with service messages = %v", got)
	}
	if got := q(func(q *Query) { q.AccountID = other }); !same(got, "other") {
		t.Errorf("by account = %v", got)
	}
	if got := q(func(q *Query) { q.ChatID = f.id("chats", "g1") }); !same(got, inGroup) {
		t.Errorf("by chat = %v", got)
	}
	if got := q(func(q *Query) { q.SenderID = f.id("senders", "u2") }); !same(got, fromBob) {
		t.Errorf("by sender = %v", got)
	}
	if got := q(func(q *Query) { q.HasAttachment = true }); !same(got, withFile) {
		t.Errorf("with attachment = %v", got)
	}
	if got := q(func(q *Query) { q.DeletedOnly = true }); !same(got, deleted) {
		t.Errorf("deleted only = %v", got)
	}
	// plain was said at minute 1, inGroup at 2, fromBob at 3: From is inclusive, To exclusive.
	if got := q(func(q *Query) { q.From, q.To = day.Add(2*time.Minute), day.Add(4*time.Minute) }); !same(got, inGroup, fromBob) {
		t.Errorf("by date = %v", got)
	}
	if got := q(func(q *Query) { q.AccountID, q.HasAttachment, q.DeletedOnly = f.acc, true, true }); len(got) != 0 {
		t.Errorf("filters must combine with AND: %v", got)
	}
}

func TestPaging(t *testing.T) {
	f := newFixture(t, "en")
	for range 5 {
		f.say("cats again")
	}
	first := f.page(Query{Text: "cats ", Sort: ByNewest, Limit: 2})
	if len(first.Hits) != 2 || !first.More {
		t.Fatalf("first page: %d hits, more = %v", len(first.Hits), first.More)
	}
	last := f.page(Query{Text: "cats ", Sort: ByNewest, Limit: 2, Offset: 4})
	if len(last.Hits) != 1 || last.More {
		t.Fatalf("last page: %d hits, more = %v", len(last.Hits), last.More)
	}
	if got := len(f.page(Query{Text: "cats "}).Hits); got != 5 {
		t.Errorf("default limit returned %d hits", got)
	}
	if got := len(f.page(Query{Text: "cats ", Limit: 100000}).Hits); got != 5 {
		t.Errorf("huge limit returned %d hits", got)
	}
	if got := len(f.page(Query{Text: "cats ", Offset: -3}).Hits); got != 5 {
		t.Errorf("negative offset returned %d hits", got)
	}
}

func TestEmptyTextBrowsesNewestFirst(t *testing.T) {
	f := newFixture(t, "en")
	first := f.say("one")
	second := f.say("two", func(m *model.Message) {
		m.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "p.jpg", Size: 1}}
	})
	if got := f.find(Query{}); !slices.Equal(got, []string{second, first}) {
		t.Errorf("browse = %v", got)
	}
	if got := f.find(Query{HasAttachment: true}); !same(got, second) {
		t.Errorf("browse with a filter = %v", got)
	}
}

func TestFindsByAttachmentFileName(t *testing.T) {
	f := newFixture(t, "en")
	id := f.say("see attached", func(m *model.Message) {
		m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "Quarterly_Report.pdf", Size: 1}}
	})
	if got := f.text("quarterly "); !same(got, id) {
		t.Fatalf("file name found %v", got)
	}
}

// Review Focus 5: edits and deletions.
func TestEditedAndDeletedMessages(t *testing.T) {
	f := newFixture(t, "en")
	id := f.say("the old wording")
	f.n-- // say the same message again, edited
	f.say("the new wording", func(m *model.Message) { m.EditedAt = day.Add(time.Hour) })
	if got := f.text("old "); len(got) != 0 {
		t.Errorf("old text still found: %v", got)
	}
	if err := f.store.MarkDeleted(f.acc, "c1", []string{id}); err != nil {
		t.Fatal(err)
	}
	hits := f.page(Query{Text: "new "}).Hits
	if len(hits) != 1 || !hits[0].Edited || !hits[0].Deleted {
		t.Fatalf("hits = %+v; want one, marked edited and deleted", hits)
	}
}

func TestHitCarriesWhatAResultRowShows(t *testing.T) {
	f := newFixture(t, "en")
	f.say("The cats are sleeping")
	hits := f.page(Query{Text: "cat "}).Hits
	if len(hits) != 1 {
		t.Fatalf("%d hits", len(hits))
	}
	h := hits[0]
	if h.ChatTitle != "Alice" || h.SenderName != "Alice" || h.AccountID != f.acc || h.ChatID != f.id("chats", "c1") ||
		h.Kind != model.KindMessage || !h.SentAt.Equal(day.Add(time.Minute)) || h.Edited || h.Deleted {
		t.Errorf("hit = %+v", h)
	}
	if got := render(h.Snippet); got != "The [cats] are sleeping" {
		t.Errorf("snippet = %q", got)
	}
}

// Review Focus 1: nothing the owner types may produce an error.
func TestOddQueriesNeverFail(t *testing.T) {
	f := newFixture(t, "en", "ru")
	f.say("The cats are sleeping")
	for _, query := range []string{
		`"`, `""`, `"""`, `"unclosed`, `a"b"c"d`, `*`, `cat*`, `*cat`, `(`, `)`, `((cat)`, `cat)`,
		`AND`, `OR`, `NOT`, `cat AND`, `AND cat`, `cat OR OR dog`, `NOT cat`, `cat NOT`,
		`NEAR(cat dog, 3)`, `NEAR(`, `exact:cat`, `stem:`, `{exact stem}: cat`, `nosuchcolumn:cat`,
		`^cat`, `cat + dog`, `cat - dog`, `-cat`, `'; DROP TABLE messages; --`, `%`, `_`, `\`, `\"`,
		`!!!`, `...`, `   `, "\x00", "cat\x00dog", "\xff\xfe", "👍🏽", "👨‍👩‍👧", "é́́",
		strings.Repeat("cat ", 5000), `"` + strings.Repeat("cat ", 5000) + `"`, strings.Repeat(`"`, 999),
		strings.Repeat("a", 100000), strings.Repeat("東", 5000),
	} {
		if _, err := f.search.Search(context.Background(), Query{Text: query}); err != nil {
			shown := query
			if len(shown) > 40 {
				shown = shown[:40] + "…"
			}
			t.Errorf("Search(%q): %v", shown, err)
		}
	}
	if got := f.text("!!!"); len(got) != 0 {
		t.Errorf("punctuation found %v", got)
	}
	// The table the injection attempt names is still there.
	if got := f.text("cat "); len(got) != 1 {
		t.Errorf("after the odd queries, cat found %v", got)
	}
}

func TestSearchStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, "en")
	f.say("cats")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.search.Search(ctx, Query{Text: "cats "}); err == nil {
		t.Fatal("Search with a cancelled context succeeded")
	}
}

func TestAnyCharacterOfAnUnspacedRunCanBeFound(t *testing.T) {
	f := newFixture(t, "en")
	cat := f.say("我有一只猫")
	thanks := f.say("谢谢你。再见")
	tokyo := f.say("東京都に行く")
	cats := f.say("I love 猫猫")
	for query, want := range map[string][]string{
		"猫":          {cat, cats}, // last character of its run
		"你":          {thanks},
		"く":          {tokyo},
		"我":          {cat}, // first character
		"只":          {cat}, // in the middle
		`"猫"`:        {cat, cats},
		`"I love 猫"`: {cats},
		"再见":         {thanks},
	} {
		if got := f.text(query); !same(got, want...) {
			t.Errorf("%q found %v, want %v", query, got, want)
		}
	}
}

func TestFindsTextHoweverItWasSpelled(t *testing.T) {
	f := newFixture(t, "en", "ru")
	hyphen := f.say("an exam\u00adple here")
	stress := f.say("большо\u0301й за\u0301мок")
	halfwidth := f.say("ｶﾞｲﾄﾞ")
	decomposed := f.say(norm.NFD.String("ドキュメント"))
	for query, want := range map[string]string{
		"example ": hyphen, "замок ": stress, "большой ": stress,
		"ガイド": halfwidth, "ｶﾞｲﾄﾞ": halfwidth, "ドキュメント": decomposed,
	} {
		if got := f.text(query); !same(got, want) {
			t.Errorf("%q found %v, want [%s]", query, got, want)
		}
	}
}

// A hit found by a file name must show which file, or the result row is blank.
func TestHitShowsMatchingFileNames(t *testing.T) {
	f := newFixture(t, "en")
	f.say("", func(m *model.Message) {
		m.Attachments = []model.Attachment{
			{Kind: model.AttachDocument, FileName: "Quarterly_Report.pdf", Size: 1},
			{Kind: model.AttachPhoto, FileName: "holiday.jpg", Size: 1},
		}
	})
	f.say("the quarterly numbers, no file")
	hits := f.page(Query{Text: "quarterly ", Sort: ByNewest}).Hits
	if len(hits) != 2 {
		t.Fatalf("%d hits, want 2", len(hits))
	}
	if len(hits[0].Files) != 0 || render(hits[0].Snippet) != "the [quarterly] numbers, no file" {
		t.Errorf("text hit: snippet %q, files %v", render(hits[0].Snippet), hits[0].Files)
	}
	if len(hits[1].Files) != 1 || render(hits[1].Files[0]) != "[Quarterly]_Report.pdf" {
		t.Errorf("file hit: files = %v; want only the matching name, highlighted", hits[1].Files)
	}
	// Browsing without search text has nothing to highlight, so it lists no files.
	for _, h := range f.page(Query{}).Hits {
		if len(h.Files) != 0 {
			t.Errorf("browse hit lists files %v", h.Files)
		}
	}
}
