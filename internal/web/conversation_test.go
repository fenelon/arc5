package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// talk opens a site with one source and one chat, "Dan Whitfield", holding n
// messages a minute apart ending at noon, and returns the chat's page address.
func talk(t *testing.T, n int) (*site, int64, string, []int64) {
	t.Helper()
	s := newSite(t, "")
	s.signIn()
	s.srv.deps.Now = func() time.Time { return noon.Add(time.Hour) }
	acc := s.account("Personal", "telegram")
	ids := make([]int64, n)
	for i := range n {
		at := noon.Add(-time.Duration(n-1-i) * time.Minute)
		ids[i] = s.say(acc, "Dan Whitfield", fmt.Sprint(i+1), fmt.Sprintf("message number %d", i+1), at)
	}
	if n == 0 {
		return s, acc, "", nil
	}
	return s, acc, fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Dan Whitfield")), ids
}

func TestConversationShowsTheNewestMessages(t *testing.T) {
	s, acc, path, ids := talk(t, 70)
	body := s.page(path)
	has(t, body, "<h1>Dan Whitfield</h1>", "Private chat on Telegram, 70 messages since September 2026",
		"message number 70", "message number 11", `<h2 class="day" data-day="2026-09-28"><span>Monday, 28 September 2026</span></h2>`,
		fmt.Sprintf(`href="%s?before=%d"`, path, ids[10]), "Earlier messages",
		`class="row row--current"`, `<div class="shell shell--open">`, fmt.Sprintf(`href="/s/%d"`, acc))
	lacks(t, body, "message number 10<", "Later messages", "Choose a chat on the left")
}

func TestConversationPaging(t *testing.T) {
	s, _, path, ids := talk(t, 70)

	older := s.page(fmt.Sprintf("%s?before=%d", path, ids[10]))
	has(t, older, "message number 10", "message number 1<", fmt.Sprintf(`href="%s?after=%d"`, path, ids[9]), "Later messages")
	lacks(t, older, "Earlier messages", "message number 11<")

	around := s.page(fmt.Sprintf("%s?at=%d", path, ids[34]))
	has(t, around, "message number 35", fmt.Sprintf(`id="m%d"`, ids[34]), "msg--target", "Earlier messages", "Later messages")
	if strings.Count(around, "msg--target") != 1 {
		t.Error("more than one message is marked as the target")
	}

	byDate := s.page(path + "?date=2026-09-28")
	has(t, byDate, "message number 1<")
}

// Review Focus 4: paging edges.
func TestConversationEdges(t *testing.T) {
	s, acc, path, _ := talk(t, 1)
	other := s.say(acc, "Someone else", "1", "in another chat", noon)

	for name, query := range map[string]string{
		"another chat's message": fmt.Sprintf("?at=%d", other),
		"a missing message":      "?before=987654",
		"a later missing one":    "?after=987654",
		"nonsense":               "?at=banana&before=-3",
		"a bad date":             "?date=yesterday",
		"a date far ahead":       "?date=2999-01-01",
	} {
		body := s.page(path + query)
		if !strings.Contains(body, "message number 1") || strings.Count(body, `class="msg__text"`) != 1 {
			t.Errorf("%s: want the chat's one message and nothing else", name)
		}
	}

	empty := s.account("Second", "telegram")
	if err := s.store.UpsertChat(empty, model.Chat{SourceID: "quiet", Kind: model.ChatPrivate, Title: "Quiet"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/s/chats/c/999999", "/s/chats/c/banana", fmt.Sprintf("/s/%d/c/%d", empty, s.chatID(acc, "Dan Whitfield"))} {
		if rec := s.get(bad); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", bad, rec.Code)
		}
	}
}

// Review Focus 1: hostile content appears as text.
func TestHostileContentIsShownAsText(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account(`<b>Personal</b>`, "telegram")
	const attack = `<script>alert(1)</script><img src=x onerror=alert(2)>"'`
	if err := s.store.UpsertChat(acc, model.Chat{SourceID: "c", Kind: model.ChatGroup, Title: attack}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertSender(acc, model.Sender{SourceID: "evil", DisplayName: attack}); err != nil {
		t.Fatal(err)
	}
	s.say(acc, "c", "1", attack, noon, func(m *model.Message) {
		m.SenderSourceID, m.ForwardedFrom = "evil", attack
		m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: attack, Size: 99999999, NoFetch: true, NoFetchReason: attack}}
	})
	s.say(acc, "c", "1", attack+" edited", noon, func(m *model.Message) {
		m.SenderSourceID, m.EditedAt = "evil", noon.Add(time.Minute)
	})

	list, err := s.store.ChatList([]int64{acc}, 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	for _, path := range []string{fmt.Sprintf("/s/%d", acc), fmt.Sprintf("/s/%d/c/%d", acc, list[0].ID), "/sources"} {
		body := s.page(path)
		lacks(t, body, "<script>alert", "<img src=x", `onerror=alert(2)>"`, "<b>Personal</b>")
		has(t, body, "&lt;b&gt;Personal&lt;/b&gt;")
	}
	has(t, s.page(fmt.Sprintf("/s/%d/c/%d", acc, list[0].ID)), "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestMessageDetails(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	if err := s.store.UpsertChat(acc, model.Chat{SourceID: "g", Kind: model.ChatGroup, Title: "Flat"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertSender(acc, model.Sender{SourceID: "dan", DisplayName: "Dan"}); err != nil {
		t.Fatal(err)
	}
	from := func(m *model.Message) { m.SenderSourceID = "dan" }

	s.say(acc, "g", "1", "Room 2 from ten.", noon, from)
	s.say(acc, "g", "1", "Room 4 from ten.", noon, from, func(m *model.Message) { m.EditedAt = noon.Add(4 * time.Minute) })
	s.say(acc, "g", "2", "Deleted later", noon.Add(time.Minute), from, func(m *model.Message) { m.ForwardedFrom = "Priya" })
	if err := s.store.MarkDeleted(acc, "g", []string{"2"}); err != nil {
		t.Fatal(err)
	}
	s.say(acc, "g", "3", "On my way", noon.Add(2*time.Minute), func(m *model.Message) { m.Outgoing, m.SenderSourceID = true, "" })
	s.say(acc, "g", "4", "Dan pinned a message", noon.Add(3*time.Minute), func(m *model.Message) { m.Kind, m.SenderSourceID = model.KindService, "" })
	s.say(acc, "g", "5", "line one\nline two", noon.Add(24*time.Hour), from)

	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Flat")))
	has(t, body, "Group on Telegram, 5 messages since September 2026",
		`<div class="msg__sender">Dan</div>`, "Room 4 from ten.", "Edited at 12:04, show earlier", "Room 2 from ten.",
		`Forwarded from <span class="fwd__name">Priya</span>`, "Deleted at source on", `class="msg msg--out"`, "On my way",
		`<p class="service"`, "Dan pinned a message", "line one\nline two",
		"Monday, 28 September 2026", "Tuesday, 29 September 2026")
	if strings.Count(body, `<div class="msg__sender">`) != 3 {
		t.Errorf("sender shown %d times; want it on Dan's three messages and not on the owner's", strings.Count(body, `<div class="msg__sender">`))
	}
}

func TestAttachmentModes(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	if err := s.store.SetMediaCap(1000); err != nil {
		t.Fatal(err)
	}
	s.say(acc, "Files", "1", "", noon, func(m *model.Message) {
		m.Attachments = []model.Attachment{
			{Kind: model.AttachPhoto, FileName: "plan.jpg", MimeType: "image/jpeg", Size: 5},
			{Kind: model.AttachVoice, FileName: "note.ogg", MimeType: "audio/ogg", Size: 5},
			{Kind: model.AttachVideo, FileName: "clip.mp4", MimeType: "video/mp4", Size: 5},
			{Kind: model.AttachDocument, FileName: "a.pdf", MimeType: "application/pdf", Size: 5},
			{Kind: model.AttachDocument, FileName: "page.html", MimeType: "text/html", Size: 5},
			{Kind: model.AttachVideo, FileName: "big.mp4", MimeType: "video/mp4", Size: 3 * 1024 * 1024},
			{Kind: model.AttachPhoto, FileName: "once.jpg", MimeType: "image/jpeg", Size: 5, Ephemeral: true, NoFetch: true, NoFetchReason: "self-destructing media is not archived"},
			{Kind: model.AttachDocument, FileName: "waiting.bin", Size: 5},
			{Kind: model.AttachDocument, FileName: "broken.bin", Size: 5},
			{Kind: model.AttachDocument, FileName: "gone.bin", Size: 5},
		}
	})
	ref := func(i int) model.AttachmentRef {
		return model.AttachmentRef{ChatSourceID: "Files", MessageSourceID: "1", Position: i}
	}
	for i := range 5 {
		if err := s.store.PutMedia(acc, ref(i), strings.NewReader("hello")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.SetAttachmentStatus(acc, ref(8), model.StatusFailed, "timed out"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetAttachmentStatus(acc, ref(9), model.StatusUnavailable, ""); err != nil {
		t.Fatal(err)
	}

	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Files")))
	has(t, body,
		`<img class="media media--image"`, `alt="plan.jpg"`,
		`<audio class="media" controls preload="none"`, `<video class="media" controls preload="none"`,
		"a.pdf", "5 B", ">Download<", "page.html",
		"big.mp4", "3.0 MB, not downloaded: over size cap", ">Fetch now<",
		"once.jpg", "Sent as view-once", "self-destructing media is not archived",
		"waiting.bin", "Waiting to be downloaded",
		"broken.bin", "The download failed: timed out", ">Try again<",
		"gone.bin", "No longer available at the source")
	// HTML is never offered inline, only as a download.
	if strings.Count(body, "<img ") != 1 {
		t.Error("something other than the one photo is drawn as an image")
	}
	if got := strings.Count(body, `name="csrf"`); got != 3 {
		t.Errorf("%d CSRF fields; want the page's token plus two fetch forms", got)
	}
	// View-once media the source chose not to keep is not offered for fetching.
	once := body[strings.Index(body, "once.jpg"):strings.Index(body, "Sent as view-once")]
	if strings.Contains(once, "Fetch now") {
		t.Error("withheld view-once media offers Fetch now")
	}
}

func TestYearRail(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	n := 0
	at := func(year int, month time.Month, count int) {
		for range count {
			n++
			s.say(acc, "Long", fmt.Sprint(n), "x", time.Date(year, month, 10, 12, 0, n%60, 0, time.UTC))
		}
	}
	at(2024, time.February, 1)
	at(2025, time.May, 4)
	at(2026, time.August, 8)
	path := fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Long"))

	body := s.page(path)
	has(t, body, `<nav class="rail" aria-label="Jump to a quarter" data-controller="rail">`,
		`<span class="rail__label">2026</span>`, `<span class="rail__label">2025</span>`, `<span class="rail__label">2024</span>`,
		fmt.Sprintf(`href="%s?date=2026-07-01"`, path), fmt.Sprintf(`href="%s?date=2024-01-01"`, path),
		`aria-label="July to September 2026, 8 messages"`, `class="band band--4 band--now"`,
		`aria-label="April to June 2025, 4 messages"`, `class="band band--2"`,
		`aria-label="January to March 2024, 1 message"`, `class="band band--1"`,
		`<option value="2026-01-01" selected>2026</option>`, `<option value="2024-01-01">2024</option>`)
	if got := strings.Count(body, `class="band`); got != 12 {
		t.Errorf("%d bands, want four for each of three years", got)
	}

	old := s.page(path + "?date=2024-01-01")
	has(t, old, `aria-label="January to March 2024, 1 message"`)
	if !strings.Contains(old, `<div class="rail__year rail__year--now"><span class="rail__label">2024</span>`) {
		t.Error("the rail does not mark 2024 as the year being read")
	}
}

// The page opens where the reader is going: at the newest message, at the
// top of a page of later messages or of a date, or at the message asked for.
func TestConversationSaysWhereToScroll(t *testing.T) {
	s, _, path, ids := talk(t, 70)
	for query, want := range map[string]string{
		"":                                 "end",
		fmt.Sprintf("?before=%d", ids[10]): "end",
		fmt.Sprintf("?after=%d", ids[9]):   "start",
		"?date=2026-09-28":                 "start",
		fmt.Sprintf("?at=%d", ids[34]):     fmt.Sprintf("m%d", ids[34]),
		"?at=987654":                       "end",
	} {
		body := s.page(path + query)
		has(t, body, fmt.Sprintf(`<div class="messages" data-controller="scroll pager" data-scroll-to-value="%s">`, want))
	}
}

// On a phone the open chat is a screen of its own, as in the approved mockup:
// the page says so, and the year picker jumps as soon as a year is chosen.
func TestConversationPageIsItsOwnScreenOnPhones(t *testing.T) {
	s, acc, path, _ := talk(t, 3)
	has(t, s.page(path), `<body class="app app--panes app--open">`,
		`<form class="jump only-phone" method="get" data-controller="jump">`,
		`data-action="jump#go"`, `data-jump-target="button"`)
	list := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, list, `<body class="app app--panes">`)
	if rec := s.get(s.srv.asset("js/scroll_controller.js")); rec.Code != http.StatusOK {
		t.Errorf("scroll_controller.js: %d", rec.Code)
	}
	if rec := s.get(s.srv.asset("js/jump_controller.js")); rec.Code != http.StatusOK {
		t.Errorf("jump_controller.js: %d", rec.Code)
	}
}

// The year rail counts and jumps in the time zone dates are shown in.
func TestRailFollowsTheTimeZone(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	s.srv.deps.Location = la
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan Whitfield", "1", "happy new year", time.Date(2025, time.December, 31, 20, 0, 0, 0, la))
	s.say(acc, "Dan Whitfield", "2", "much later", time.Date(2026, time.September, 1, 12, 0, 0, 0, la))
	path := fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Dan Whitfield"))

	has(t, s.page(path), fmt.Sprintf(`href="%s?date=2025-10-01" aria-label="October to December 2025, 1 message"`, path))
	has(t, s.page(path+"?date=2025-10-01"), "happy new year")
}

// An edit made on a later day says which day.
func TestEditedOnAnotherDayShowsTheDay(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan", "1", "first", noon)
	s.say(acc, "Dan", "1", "second", noon, func(m *model.Message) { m.EditedAt = noon.Add(26 * time.Hour) })
	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Dan")))
	has(t, body, "Edited on 29 Sep at 14:00, show earlier")
}

// The page tells the scroll controller to keep the newest message in view
// while pictures above it load.
func TestScrollControllerFollowsLoadingPictures(t *testing.T) {
	s := newSite(t, "")
	body := s.get(s.srv.asset("js/scroll_controller.js")).Body.String()
	if !strings.Contains(body, `addEventListener("load"`) {
		t.Error("the scroll controller does not scroll again when a picture loads")
	}
}

// The rail reads like the conversation: oldest at the top, newest at the
// bottom, so it can be read as a scroll bar.
func TestRailRunsOldestToNewest(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan", "1", "long ago", time.Date(2024, time.February, 1, 12, 0, 0, 0, time.UTC))
	s.say(acc, "Dan", "2", "recently", noon)
	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Dan")))
	in := func(want string) int {
		t.Helper()
		i := strings.Index(body, want)
		if i < 0 {
			t.Fatalf("the page lacks %q", want)
		}
		return i
	}
	if in(`rail__label">2024<`) > in(`rail__label">2025<`) || in(`rail__label">2025<`) > in(`rail__label">2026<`) {
		t.Error("the rail's years do not run oldest first")
	}
	if in(`"January to March 2026`) > in(`"July to September 2026`) {
		t.Error("a year's quarters do not run oldest first")
	}
	has(t, body, `<nav class="rail" aria-label="Jump to a quarter" data-controller="rail">`)
}

// Scrolling toward either end loads the next page in place: the page's
// messages alone, with its own links onward, and forms that return to the
// conversation rather than to the fragment.
func TestAPageOfMessagesCanBeLoadedOnItsOwn(t *testing.T) {
	s, _, path, ids := talk(t, 70)
	rec := s.get(fmt.Sprintf("%s?before=%d&part=messages", path, ids[10]))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("fragment: %d", rec.Code)
	}
	body := rec.Body.String()
	has(t, body, "message number 10", "message number 1<", `data-day="2026-09-28"`,
		fmt.Sprintf(`<a class="more" href="%s?after=%d" data-pager-target="newer">Later messages</a>`, path, ids[9]))
	lacks(t, body, "<html", "<header", "conv-head", "rail", "Earlier messages", "part=messages")

	whole := s.page(path)
	has(t, whole, fmt.Sprintf(`<a class="more" href="%s?before=%d" data-pager-target="older">Earlier messages</a>`, path, ids[10]),
		`<h2 class="day" data-day="2026-09-28">`)
	if rec := s.get(s.srv.asset("js/pager_controller.js")); rec.Code != http.StatusOK {
		t.Errorf("pager_controller.js: %d", rec.Code)
	}
	if rec := s.get(s.srv.asset("js/rail_controller.js")); rec.Code != http.StatusOK {
		t.Errorf("rail_controller.js: %d", rec.Code)
	}
}

// A fetch form inside a loaded page returns to the conversation, not to the
// fragment.
func TestAFragmentsFormsReturnToTheConversation(t *testing.T) {
	s, acc, path, ids := talk(t, 70)
	s.say(acc, "Dan Whitfield", "0", "an early file", noon.Add(-2*time.Hour), func(m *model.Message) {
		m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "big.pdf", MimeType: "application/pdf", Size: 10, NoFetch: true, NoFetchReason: "too large"}}
	})
	body := s.get(fmt.Sprintf("%s?before=%d&part=messages", path, ids[10])).Body.String()
	has(t, body, "big.pdf", fmt.Sprintf(`name="back" value="%s?before=%d"`, path, ids[10]))
}

// Opening a chat keeps the chat list where it was, and saving a setting
// keeps the page where it was: the pages say so to their controllers.
func TestPagesKeepTheirPlace(t *testing.T) {
	s, acc, path, _ := talk(t, 3)
	has(t, s.page(path), `<aside class="list" id="list" aria-label="Chats" data-controller="list" data-action="click->list#remember">`)
	has(t, s.page(fmt.Sprintf("/s/%d", acc)), `data-controller="list"`)
	has(t, s.page("/settings"), `<main class="plain plain--settings" data-controller="place" data-action="submit->place#remember">`)
	has(t, s.page(fmt.Sprintf("/sources/%d", acc)), `<main class="plain plain--wide" data-controller="place" data-action="submit->place#remember">`)
	for _, js := range []string{"js/list_controller.js", "js/place_controller.js"} {
		if rec := s.get(s.srv.asset(js)); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", js, rec.Code)
		}
	}
}

// A forward sets its content apart under who wrote it, and a reply quotes
// what it answers, linking to it.
func TestForwardsAndRepliesAreMarked(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	if err := s.store.UpsertSender(acc, model.Sender{SourceID: "anton", DisplayName: "Anton U"}); err != nil {
		t.Fatal(err)
	}
	question := s.say(acc, "Infra", "1", "Where are the copies kept?", noon, func(m *model.Message) { m.SenderSourceID = "anton" })
	s.say(acc, "Infra", "2", "Everything is geo-replicated", noon.Add(time.Minute), func(m *model.Message) {
		m.ForwardedFrom, m.ReplyToSourceID = "Mikhail", "1"
	})
	s.say(acc, "Infra", "3", "a photo answer", noon.Add(2*time.Minute), func(m *model.Message) {
		m.Outgoing, m.ReplyToSourceID = true, "2"
	})
	s.say(acc, "Infra", "4", "about something older", noon.Add(3*time.Minute), func(m *model.Message) { m.ReplyToSourceID = "999" })
	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Infra")))
	has(t, body,
		`<div class="fwd"><div class="fwd__head">Forwarded from <span class="fwd__name">Mikhail</span></div>`,
		fmt.Sprintf(`<a class="quote" href="#m%d"><span class="quote__name">Anton U</span><span class="quote__text">Where are the copies kept?</span></a>`, question),
		`<span class="quote__name">Mikhail</span><span class="quote__text">Everything is geo-replicated</span>`,
		`<p class="quote quote--missing">Reply to a message not in the archive</p>`)
}
