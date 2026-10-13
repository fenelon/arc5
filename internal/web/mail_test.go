package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/connector/demo"
	"github.com/fenelon/arc5/internal/connector/email"
)

// mailSite is a site holding the demo archive, with its mailbox "Home mail".
type mailSite struct {
	*site
	box int64
}

func newMailSite(t *testing.T) *mailSite {
	t.Helper()
	s := newSite(t, "")
	s.signIn()
	demo.Register(s.reg)
	demo.RegisterMail(s.reg)
	if _, err := demo.Seed(s.store); err != nil {
		t.Fatal(err)
	}
	s.srv.deps.Now = func() time.Time { return time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC) }
	m := &mailSite{site: s}
	accounts, _ := s.store.Accounts()
	for _, a := range accounts {
		if a.Connector == demo.MailName {
			m.box = a.ID
		}
	}
	return m
}

func (m *mailSite) thread(title string) int64 {
	m.t.Helper()
	var id int64
	if err := m.store.Reader().QueryRow(`SELECT id FROM chats WHERE account_id = ? AND title = ?`, m.box, title).Scan(&id); err != nil {
		m.t.Fatalf("thread %q: %v", title, err)
	}
	return id
}

func (m *mailSite) folder(name string) int64 {
	m.t.Helper()
	folders, _ := m.store.AccountFolders(m.box)
	for _, f := range folders {
		if f.Name == name {
			return f.ID
		}
	}
	m.t.Fatalf("no folder %q", name)
	return 0
}

func TestMailInbox(t *testing.T) {
	m := newMailSite(t)
	body := m.page(fmt.Sprintf("/s/%d", m.box))
	has(t, body,
		`<nav class="folders" aria-label="Folders">`, `<div class="folders__name">Home mail</div>`, `me@example.org`,
		fmt.Sprintf(`<a class="folder folder--current" href="/s/%d?folder=inbox" aria-current="page">`, m.box), ">Inbox</a>",
		fmt.Sprintf(`href="/s/%d?folder=sent"`, m.box), ">Sent</a>", fmt.Sprintf(`href="/s/%d?folder=all"`, m.box), ">All mail</a>",
		fmt.Sprintf(`href="/s/%d?folder=f%d"`, m.box, m.folder("Receipts")), ">Receipts</a>",
		"Mail deleted from the mailbox stays here, marked as deleted at source.",
		`<h1 class="mail-head__title">Inbox</h1>`, "With attachments", "Deleted at source",
		// The rows of the mockup.
		`<span class="mrow__name">Pablo Ruiz</span>`, "pablo.ruiz@example.com, and you", "2 messages", "3 Oct",
		`<span class="mrow__subject">Cena el sábado</span>`, "¿A las nueve en casa? Yo llevo el postre y el vino.",
		"Harbour Yacht Club", `<span class="tag tag--warn">Deleted at source</span>`,
		"Dan Whitfield", "dan.whitfield@example.com, Priya Nair and you", "5 messages", `aria-label="Has attachment"`, "27 Sep",
		`<span class="avatar mrow__avatar" aria-hidden="true">DW</span>`, "Олег Ветров", "14 Jul",
		fmt.Sprintf(`href="/s/%d/c/%d"`, m.box, m.thread("Monday planning, room 4")),
		`placeholder="Search mail by word, sender, address or subject"`,
	)
	lacks(t, body, "Trash", "Choose a chat on the left", "Show more chats")
	// Only the club's reminder is wholly deleted: one tag in the list.
	if n := strings.Count(body, `<span class="tag tag--warn">Deleted at source</span>`); n != 1 {
		t.Errorf("%d deleted tags, want 1", n)
	}
	// Single messages show no count.
	if strings.Contains(body, "1 messages") || strings.Contains(body, "1 message<") {
		t.Error("a single message is counted")
	}
}

func TestMailFolders(t *testing.T) {
	m := newMailSite(t)
	sent := m.page(fmt.Sprintf("/s/%d?folder=sent", m.box))
	has(t, sent, `<h1 class="mail-head__title">Sent</h1>`, "Cena el sábado", "Deposit for Calle Sol 14")
	lacks(t, sent, "Membership renewal reminder", "Your tickets for 11 July")

	receipts := m.page(fmt.Sprintf("/s/%d?folder=f%d", m.box, m.folder("Receipts")))
	has(t, receipts, `<h1 class="mail-head__title">Receipts</h1>`, "Your tickets for 11 July", "Deposit for Calle Sol 14",
		fmt.Sprintf(`<a class="folder folder--current" href="/s/%d?folder=f%d" aria-current="page">`, m.box, m.folder("Receipts")))
	lacks(t, receipts, "Cena el sábado")

	all := m.page(fmt.Sprintf("/s/%d?folder=all", m.box))
	has(t, all, `<h1 class="mail-head__title">All mail</h1>`, "Cena el sábado", "Your tickets for 11 July")

	for _, bad := range []string{"f999999", "fnope", "trash", "f" + fmt.Sprint(m.folder("Trash"))} {
		if rec := m.get(fmt.Sprintf("/s/%d?folder=%s", m.box, bad)); rec.Code != http.StatusNotFound {
			t.Errorf("folder %q: %d, want 404", bad, rec.Code)
		}
	}
	// Another account's folder is not this mailbox's.
	other := m.account("Work", "telegram")
	if rec := m.get(fmt.Sprintf("/s/%d?folder=f%d", other, m.folder("Receipts"))); rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "Receipts") {
		t.Error("a messenger shows a mailbox's folder")
	}
}

func TestMailFilters(t *testing.T) {
	m := newMailSite(t)
	files := m.page(fmt.Sprintf("/s/%d?folder=inbox&files=1", m.box))
	has(t, files, "Monday planning, room 4", "Fotos de Valencia", "Your tickets for 11 July",
		fmt.Sprintf(`<a class="check check--on" role="checkbox" aria-checked="true" href="/s/%d?folder=inbox">`, m.box))
	lacks(t, files, "Cena el sábado", "Membership renewal reminder")

	deleted := m.page(fmt.Sprintf("/s/%d?folder=inbox&deleted=1", m.box))
	has(t, deleted, "Membership renewal reminder", "Monday planning, room 4")
	lacks(t, deleted, "Cena el sábado")

	inbox := m.page(fmt.Sprintf("/s/%d", m.box))
	has(t, inbox, fmt.Sprintf(`<a class="check" role="checkbox" aria-checked="false" href="/s/%d?folder=inbox&amp;files=1">`, m.box))
	none := m.page(fmt.Sprintf("/s/%d?folder=f%d&deleted=1", m.box, m.folder("Receipts")))
	has(t, none, "No conversations here.")
}

func TestMailPaging(t *testing.T) {
	m := newMailSite(t)
	sink := connector.NewSink(m.store, m.box, nil, func(connector.Progress) {})
	for i := range 60 {
		raw := fmt.Sprintf("From: Bulk <bulk@example.com>\r\nSubject: Bulk %02d\r\nMessage-ID: <bulk-%d@example.com>\r\nDate: %s\r\n\r\nNumber %d\r\n",
			i, i, time.Date(2026, time.October, 10, 0, i, 0, 0, time.UTC).Format(time.RFC1123Z), i)
		chat, id, err := email.Store(sink, []byte(raw), time.Now(), func(string) bool { return false })
		if err != nil {
			t.Fatal(err)
		}
		if err := sink.FileInFolder("INBOX", uint32(1000+i), chat, id); err != nil {
			t.Fatal(err)
		}
	}
	first := m.page(fmt.Sprintf("/s/%d", m.box))
	if n := strings.Count(first, `<a class="mrow"`); n != 50 {
		t.Errorf("first page: %d rows", n)
	}
	has(t, first, fmt.Sprintf(`href="/s/%d?folder=inbox&amp;page=2">Older conversations</a>`, m.box), "Bulk 59")
	second := m.page(fmt.Sprintf("/s/%d?folder=inbox&page=2", m.box))
	has(t, second, "Bulk 09", "Cena el sábado", "Отчёт о походе и расходы")
	lacks(t, second, "Bulk 59", "Older conversations")
}

func TestEverythingAtOnceMail(t *testing.T) {
	m := newMailSite(t)
	body := m.page("/s/mail")
	has(t, body, `<div class="folders__name">Everything at once</div>`, `href="/s/mail?folder=sent"`, "Cena el sábado",
		fmt.Sprintf(`href="/s/mail/c/%d"`, m.thread("Cena el sábado")))
	lacks(t, body, "Receipts")
	if rec := m.get("/s/mail?folder=f1"); rec.Code != http.StatusNotFound {
		t.Errorf("a folder of one mailbox under everything: %d", rec.Code)
	}
}

func TestMailShowsHostileTextAsText(t *testing.T) {
	m := newMailSite(t)
	sink := connector.NewSink(m.store, m.box, nil, func(connector.Progress) {})
	raw := "From: \"<img src=x onerror=alert(1)>\" <evil@example.com>\r\nSubject: <script>alert(2)</script> offer\r\nMessage-ID: <evil@example.com>\r\nDate: Mon, 12 Oct 2026 10:00:00 +0000\r\nContent-Type: text/html\r\n\r\n<p>Click <a href=\"javascript:alert(3)\">here</a><script>alert(4)</script></p>\r\n"
	chat, id, err := email.Store(sink, []byte(raw), time.Now(), func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.FileInFolder("INBOX", 999, chat, id); err != nil {
		t.Fatal(err)
	}
	body := m.page(fmt.Sprintf("/s/%d", m.box))
	lacks(t, body, "<script>alert", "<img src=x", "javascript:")
	has(t, body, "&lt;script&gt;alert(2)&lt;/script&gt; offer", "&lt;img src=x onerror=alert(1)&gt;", "Click here")
}
