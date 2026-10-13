package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

var noon = time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)

// account adds a source. Connector "telegram" is registered as a chats source
// the first time it is used; other names are left unregistered.
func (s *site) account(name, connectorName string) int64 {
	s.t.Helper()
	if _, known := s.reg.Info(connectorName); !known && connectorName == "telegram" {
		s.reg.Register(connector.Info{Name: "telegram", Title: "Telegram", Display: connector.DisplayChats}, nil)
	}
	id, err := s.store.CreateAccount(connectorName, name, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}

// say stores a message, creating the chat as a private one titled like its
// source ID if it is new, and returns the message's database ID.
func (s *site) say(acc int64, chat, id, text string, at time.Time, change ...func(*model.Message)) int64 {
	s.t.Helper()
	if archived, _ := s.store.ChatArchived(acc, chat); !archived {
		if err := s.store.UpsertChat(acc, model.Chat{SourceID: chat, Kind: model.ChatPrivate, Title: chat}); err != nil {
			s.t.Fatal(err)
		}
	}
	m := model.Message{ChatSourceID: chat, SourceID: id, SenderSourceID: "them", SentAt: at, Text: text}
	for _, c := range change {
		c(&m)
	}
	if _, err := s.store.UpsertMessage(acc, m); err != nil {
		s.t.Fatal(err)
	}
	stored, err := s.store.Message(acc, chat, id)
	if err != nil {
		s.t.Fatal(err)
	}
	return stored.ID
}

func (s *site) chatID(acc int64, chat string) int64 {
	s.t.Helper()
	list, err := s.store.ChatList([]int64{acc}, 1000, 0)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, c := range list {
		if c.Title == chat {
			return c.ID
		}
	}
	s.t.Fatalf("no chat titled %q", chat)
	return 0
}

func (s *site) page(path string) string {
	s.t.Helper()
	rec := s.get(path)
	if rec.Code != http.StatusOK {
		s.t.Fatalf("GET %s: status %d, body %s", path, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

func has(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

func lacks(t *testing.T, body string, bans ...string) {
	t.Helper()
	for _, ban := range bans {
		if strings.Contains(body, ban) {
			t.Errorf("the page has %q", ban)
		}
	}
}

func TestHomeOpensTheChats(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	s.account("Personal", "telegram")
	if rec := s.get("/"); !redirectsTo(rec, "/s/chats") {
		t.Fatalf("home with a source: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestChatListShowsLastMessages(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan Whitfield", "1", "Are we still on for Monday?", noon.Add(-48*time.Hour))
	s.say(acc, "Dan Whitfield", "2", "Got her. Traffic is bad.", noon, func(m *model.Message) { m.Outgoing = true })
	s.say(acc, "Marina Sokolova", "1", "Спасибо, что встретил!", noon.Add(-400*24*time.Hour))
	s.say(acc, "Photos only", "1", "", noon.Add(-time.Hour), func(m *model.Message) {
		m.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "a.jpg", Size: 1}, {Kind: model.AttachPhoto, FileName: "b.jpg", Size: 1}}
	})
	s.srv.deps.Now = func() time.Time { return noon.Add(time.Hour) }

	body := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, body, "Dan Whitfield", "You: Got her. Traffic is bad.", "Marina Sokolova", "Спасибо, что встретил!",
		"2 attachments", "Choose a chat on the left")
	// Today shows the time, this year the day, earlier years the year too.
	has(t, body, ">12:00<", ">24 Aug 2025<")
	if dan, marina := strings.Index(body, "Dan Whitfield"), strings.Index(body, "Marina Sokolova"); dan > marina {
		t.Error("the most recent chat is not first")
	}
	lacks(t, body, "Show more chats")
}

func TestChatListGrows(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	for i := range 101 {
		s.say(acc, fmt.Sprintf("chat-%03d", i), "1", "hello", noon.Add(time.Duration(i)*time.Minute))
	}
	first := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, first, "chat-100", "chat-001", fmt.Sprintf(`href="/s/%d?chats=200"`, acc))
	lacks(t, first, "chat-000")
	all := s.page(fmt.Sprintf("/s/%d?chats=200", acc))
	has(t, all, "chat-000")
	lacks(t, all, "Show more chats")
	if rec := s.get(fmt.Sprintf("/s/%d?chats=banana", acc)); rec.Code != http.StatusOK {
		t.Errorf("a bad chats value: status %d", rec.Code)
	}

	// Opening a chat from the longer list keeps the list that long, with the
	// chat in it, and so does moving about the conversation.
	oldest := s.chatID(acc, "chat-000")
	open := fmt.Sprintf("/s/%d/c/%d?chats=200", acc, oldest)
	has(t, all, fmt.Sprintf(`href="%s"`, open))
	chat := s.page(open)
	has(t, chat, `class="row row--current" href="`+strings.ReplaceAll(open, "&", "&amp;")+`"`, "chat-000", "chat-100",
		fmt.Sprintf(`href="/s/%d/c/%d?date=2026-09-01&amp;chats=200"`, acc, oldest))
	lacks(t, chat, "Show more chats")
	// The first page's rows need no count.
	has(t, first, fmt.Sprintf(`href="/s/%d/c/%d"`, acc, s.chatID(acc, "chat-100")))
}

func TestPickerListsSourcesByKind(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	personal := s.account("Personal", "telegram")
	work := s.account("Work", "telegram")
	s.reg.Register(connector.Info{Name: "imap", Title: "Email", Display: connector.DisplayMail}, nil)
	s.account("Home mail", "imap")
	s.say(personal, "Dan", "1", "in personal", noon)
	s.say(work, "Boss", "1", "in work", noon)
	s.runner.status = []connector.Status{
		{AccountID: personal, State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseLive}},
		{AccountID: work, State: connector.StateNeedsLogin, LastError: "session revoked"},
	}

	body := s.page(fmt.Sprintf("/s/%d", personal))
	has(t, body, `<h2 class="picker__heading">Messengers</h2>`, `<h2 class="picker__heading">Mail</h2>`,
		"Everything at once", "2 messengers", "1 mailbox", "Personal", "Work", "Home mail",
		`aria-label="Live"`, `aria-label="Needs login"`, `href="/sources"`,
		fmt.Sprintf(`class="tile tile--current" href="/s/%d"`, personal), "logo--telegram", "logo--email",
		"<strong>Work is not archiving.</strong>")
	has(t, body, "in personal")
	lacks(t, body, "in work")

	everything := s.page("/s/chats")
	has(t, everything, "in personal", "in work", `class="tile tile--current" href="/s/chats"`)
}

func TestSourceStates(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	ids := map[string]int64{}
	for _, name := range []string{"Live", "Backfill", "Connecting", "Backoff", "Paused", "Off", "Unknown"} {
		ids[name] = s.account(name, "telegram")
	}
	if err := s.store.SetAccountEnabled(ids["Off"], false); err != nil {
		t.Fatal(err)
	}
	s.runner.status = []connector.Status{
		{AccountID: ids["Live"], State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseLive}},
		{AccountID: ids["Backfill"], State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseBackfill, ChatsDone: 3, ChatsTotal: 9}},
		{AccountID: ids["Connecting"], State: connector.StateRunning},
		{AccountID: ids["Backoff"], State: connector.StateBackoff, LastError: "connection reset"},
		{AccountID: ids["Paused"], State: connector.StatePaused, LastError: "disk full"},
	}
	body := s.page("/sources")
	has(t, body, "Telegram, catching up on history, 3 of 9 chats", "Telegram, connecting", "Telegram, not connected: connection reset",
		"Telegram, paused, the archive could not be written: disk full", "Telegram, switched off", "Telegram, not running", "Telegram, live",
		`aria-label="Live"`, `aria-label="Catching up on history"`)
	if strings.Count(body, "tile--warn") != 2 {
		t.Errorf("want the two failing sources drawn with the attention border, got %d", strings.Count(body, "tile--warn"))
	}
}

func TestUnknownSourceIs404(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	s.account("Personal", "telegram")
	for _, path := range []string{"/s/999", "/s/banana", "/s/-1", "/s/mail"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestSourceOfAnUnregisteredConnectorStillOpens(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Old import", "retired")
	s.say(acc, "Someone", "1", "still readable", noon)
	body := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, body, "still readable", "Old import", "retired", "logo--generic")
}

func TestSourcePagesNeedASession(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	s.account("Personal", "telegram")
	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	for _, path := range []string{"/s/chats", "/sources"} {
		if rec := stranger.get(path); !redirectsTo(rec, "/login") {
			t.Errorf("GET %s without a session: %d", path, rec.Code)
		}
	}
}
