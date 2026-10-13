package web

import (
	"fmt"
	"slices"

	"github.com/fenelon/arc5/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThreadView(t *testing.T) {
	t.Parallel()
	m := newMailSite(t)
	planning := m.thread("Monday planning, room 4")
	body := m.page(fmt.Sprintf("/s/%d/c/%d", m.box, planning))
	has(t, body,
		fmt.Sprintf(`<a class="back-link" href="/s/%d?folder=inbox"><svg`, m.box), `</svg>Back to Inbox</a>`,
		`<h1 class="thread__title">Monday planning, room 4</h1>`,
		"5 messages, 25 to 27 September 2026. The three earliest are folded; select one to open it.",
		// Folded: one line with the start of the text.
		`<span class="mcard__name">Dan Whitfield</span>`, "dan.whitfield@example.com", "25 Sep, 09:12", "1 of 5",
		"Can we move the planning session to Monday?",
		`<span class="mcard__name">You</span>`, "2 of 5",
		// Priya's message was deleted at source.
		`<span class="tag tag--warn">Deleted at source</span>`, "Fine by me, though I have to leave by noon.",
		// Open: recipients, full date, the text and the agenda's card.
		"to me@example.org, priya.nair@example.com", "27 Sep 2026, 18:40", "4 of 5",
		"Room 4 is booked from ten.", "agenda-monday.pdf", "PDF, 82.0 KB", `>Open</a>`,
		"Confirmed. Budget first, then the room plan.", "5 of 5",
		"Download original (.eml)", `class="mcard mcard--own"`,
	)
	if n := strings.Count(body, `<details class="mcard`); n != 5 {
		t.Errorf("%d cards, want 5", n)
	}
	if n := strings.Count(body, " open>"); n != 2 {
		t.Errorf("%d cards open, want the last 2", n)
	}
	// From another folder, the way back leads there.
	sent := m.page(fmt.Sprintf("/s/%d/c/%d?folder=sent", m.box, planning))
	has(t, sent, fmt.Sprintf(`<a class="back-link" href="/s/%d?folder=sent"><svg`, m.box), `</svg>Back to Sent</a>`)

	// A conversation of another source, or a chat, is not found here.
	other := m.account("Personal 2", "telegram")
	chat := m.say(other, "Dan", "1", "hi", noon)
	_ = chat
	for _, path := range []string{
		fmt.Sprintf("/s/%d/c/%d", m.box, m.chatID(other, "Dan")),
		fmt.Sprintf("/s/%d/c/999999", m.box),
		fmt.Sprintf("/s/%d/c/nope", m.box),
	} {
		if rec := m.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
}

// A file still waiting to be taken from its message can be asked for.
func TestWaitingFileCanBeFetched(t *testing.T) {
	t.Parallel()
	m := newMailSite(t)
	if err := m.store.UpsertChat(m.box, model.Chat{SourceID: "thread:wait@x", Kind: model.ChatThread, Title: "Waiting"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpsertMessage(m.box, model.Message{ChatSourceID: "thread:wait@x", SourceID: "wait@x", SentAt: noon, Text: "see file",
		Attachments: []model.Attachment{{Kind: model.AttachDocument, FileName: "wait.zip", Size: 9, SourceID: "part:2"}}}); err != nil {
		t.Fatal(err)
	}
	body := m.page(fmt.Sprintf("/s/%d/c/%d?folder=all", m.box, m.thread("Waiting")))
	has(t, body, "wait.zip", "Waiting to be downloaded", ">Fetch now</button>")
}

func TestThreadFoldsQuotes(t *testing.T) {
	t.Parallel()
	m := newMailSite(t)
	body := m.page(fmt.Sprintf("/s/%d/c/%d", m.box, m.thread("Hiking trip report and expenses")))
	has(t, body, "It all adds up, thanks!",
		`<details class="quote"><summary>Show quoted text</summary>`, "&gt; Put the costs in a spreadsheet")
	planning := m.page(fmt.Sprintf("/s/%d/c/%d", m.box, m.thread("Monday planning, room 4")))
	// The owner's reply quotes Dan: the lead line goes with the quote.
	has(t, planning, "Monday works. I can bring the projector if room 4 is free.")
}

func TestThreadOpenedFromSearch(t *testing.T) {
	t.Parallel()
	m := newMailSite(t)
	planning := m.thread("Monday planning, room 4")
	var first int64
	m.store.Reader().QueryRow(`SELECT MIN(id) FROM messages WHERE chat_id = ?`, planning).Scan(&first)
	body := m.page(fmt.Sprintf("/s/%d/c/%d?at=%d&q=audit", m.box, planning, first))
	has(t, body, fmt.Sprintf(`id="m%d" open>`, first), "mcard--target", `<mark class="hit">audit</mark>`,
		`value="audit"`, fmt.Sprintf(`<a class="back-link" href="/s/%d?q=audit"><svg`, m.box), `</svg>Back to results</a>`)
	if n := strings.Count(body, " open>"); n != 3 {
		t.Errorf("%d cards open, want the target and the last two", n)
	}
	// The first is open, so the folded ones are not "the earliest".
	has(t, body, "5 messages, 25 to 27 September 2026. Two are folded; select one to open it.")
}

func TestOriginalDownload(t *testing.T) {
	t.Parallel()
	m := newMailSite(t)
	var id int64
	m.store.Reader().QueryRow(`SELECT id FROM messages WHERE source_id = 'mp-4@example.com'`).Scan(&id)
	rec := m.get(fmt.Sprintf("/original/%d", id))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "message/rfc822" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != mediaPolicy || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(h.Get("Content-Disposition"), ".eml") {
		t.Errorf("headers = %v", h)
	}
	if !strings.Contains(rec.Body.String(), "Message-ID: <mp-4@example.com>") {
		t.Errorf("body = %.200q", rec.Body.String())
	}
	var chatMessage int64
	m.store.Reader().QueryRow(`SELECT MIN(id) FROM messages WHERE original_sha256 = ''`).Scan(&chatMessage)
	for _, path := range []string{fmt.Sprintf("/original/%d", chatMessage), "/original/999999", "/original/x"} {
		if rec := m.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
	// Signed out: no original.
	anon := httptest.NewRecorder()
	m.srv.ServeHTTP(anon, httptest.NewRequest("GET", fmt.Sprintf("/original/%d", id), nil))
	if anon.Code == http.StatusOK {
		t.Error("the original is served without a session")
	}
}

// Links in mail text are shown short and can be followed; nothing else in
// the text becomes markup.
func TestMailTextLinks(t *testing.T) {
	long := "https://c.gle/AAuDWvlVmVSUgPq6YFsEIUvln08VZuotNwm1-S8rYiNyauzCvzObBK2hG2kPZ9OqdboAeBEaM2f2XTirYFNcmdJIWOXdXfDTquhGttINfpzC"
	got := linkParts("View report snapshot <" + long + ">. Or www.example.com, or https://example.com/a).")
	want := []TextPart{
		{Text: "View report snapshot "},
		{Href: long, Label: "c.gle/AAuDWvlVmVSUgPq6YFsEIUvln08VZuo…"},
		{Text: ". Or www.example.com, or "},
		{Href: "https://example.com/a", Label: "example.com/a"},
		{Text: ")."},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parts =\n%+v\nwant\n%+v", got, want)
	}
	if got := linkParts("javascript:alert(1) and <script>"); len(got) != 1 || got[0].Href != "" {
		t.Errorf("not a link: %+v", got)
	}
}
