package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThreadView(t *testing.T) {
	m := newMailSite(t)
	planning := m.thread("Monday planning, room 4")
	body := m.page(fmt.Sprintf("/s/%d/c/%d", m.box, planning))
	has(t, body,
		fmt.Sprintf(`<a class="thread__back" href="/s/%d?folder=inbox">Back to Inbox</a>`, m.box),
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
	has(t, sent, fmt.Sprintf(`<a class="thread__back" href="/s/%d?folder=sent">Back to Sent</a>`, m.box))

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

func TestThreadFoldsQuotes(t *testing.T) {
	m := newMailSite(t)
	body := m.page(fmt.Sprintf("/s/%d/c/%d", m.box, m.thread("Отчёт о походе и расходы")))
	has(t, body, "Всё сходится, спасибо!",
		`<details class="quote"><summary>Show quoted text</summary>`, "&gt; Свёл расходы в таблицу")
	planning := m.page(fmt.Sprintf("/s/%d/c/%d", m.box, m.thread("Monday planning, room 4")))
	// The owner's reply quotes Dan: the lead line goes with the quote.
	has(t, planning, "Monday works. I can bring the projector if room 4 is free.")
}

func TestThreadOpenedFromSearch(t *testing.T) {
	m := newMailSite(t)
	planning := m.thread("Monday planning, room 4")
	var first int64
	m.store.Reader().QueryRow(`SELECT MIN(id) FROM messages WHERE chat_id = ?`, planning).Scan(&first)
	body := m.page(fmt.Sprintf("/s/%d/c/%d?at=%d&q=audit", m.box, planning, first))
	has(t, body, fmt.Sprintf(`id="m%d" open>`, first), "mcard--target", `<mark class="hit">audit</mark>`,
		`value="audit"`, fmt.Sprintf(`<a class="thread__back" href="/s/%d?q=audit">Back to results</a>`, m.box))
	if n := strings.Count(body, " open>"); n != 3 {
		t.Errorf("%d cards open, want the target and the last two", n)
	}
}

func TestOriginalDownload(t *testing.T) {
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
