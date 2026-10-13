package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// mailbox is an account with Inbox, Sent and a Work folder, and a helper to
// store a message in a thread and file it.
type mailbox struct {
	t   *testing.T
	s   *Store
	acc int64
	n   int
}

func newMailbox(t *testing.T, s *Store) *mailbox {
	t.Helper()
	acc := newAccount(t, s)
	for _, f := range []model.Folder{
		{SourceID: "INBOX", Name: "Inbox", Role: model.FolderInbox, ArchiveByDefault: true},
		{SourceID: "Sent", Name: "Sent", Role: model.FolderSent, ArchiveByDefault: true},
		{SourceID: "Work", Name: "Work", Role: model.FolderOther, ArchiveByDefault: true},
	} {
		mustFolder(t, s, acc, f)
	}
	return &mailbox{t: t, s: s, acc: acc}
}

func addr(role model.AddressRole, name, address string) model.Address {
	return model.Address{Role: role, Name: name, Address: address}
}

// mail stores one message from `from` in thread `thread`, filed in folder.
func (b *mailbox) mail(thread, folder string, at time.Time, from model.Address, outgoing bool, text string, more ...model.Address) model.Message {
	b.t.Helper()
	b.n++
	if err := b.s.UpsertChat(b.acc, model.Chat{SourceID: thread, Kind: model.ChatThread, Title: strings.TrimPrefix(thread, "thread:")}); err != nil {
		b.t.Fatal(err)
	}
	m := model.Message{
		ChatSourceID: thread, SourceID: fmt.Sprintf("m%d@x", b.n), SenderSourceID: from.Address, SentAt: at,
		Subject: strings.TrimPrefix(thread, "thread:"), Text: text, Outgoing: outgoing,
		Addresses: append([]model.Address{from}, more...),
	}
	mustUpsert(b.t, b.s, b.acc, m)
	if err := b.s.FileInFolder(b.acc, folder, uint32(b.n), thread, m.SourceID); err != nil {
		b.t.Fatal(err)
	}
	return m
}

var (
	me    = addr(model.AddrFrom, "", "me@example.org")
	dan   = addr(model.AddrFrom, "Dan Whitfield", "dan@example.com")
	priya = addr(model.AddrFrom, "Priya Nair", "priya@example.com")
	pablo = addr(model.AddrFrom, "Pablo Ruiz", "pablo@example.com")
)

func titles(t *testing.T, s *Store, ids []int64, f ThreadFilter) []string {
	t.Helper()
	list, err := s.Threads(ids, f, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, th := range list {
		out = append(out, th.Title)
	}
	return out
}

func folderIDOf(t *testing.T, s *Store, acc int64, source string) int64 {
	t.Helper()
	id, err := folderID(s.r, acc, source)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestThreadsByFolder(t *testing.T) {
	s := openTemp(t)
	b := newMailbox(t, s)
	b.mail("thread:Monday planning", "INBOX", t0, dan, false, "Can we move it?")
	b.mail("thread:Monday planning", "Sent", t0.Add(time.Hour), me, true, "Monday works.")
	b.mail("thread:Invoice", "Work", t0.Add(2*time.Hour), pablo, false, "Attached.")
	b.mail("thread:Only sent", "Sent", t0.Add(3*time.Hour), me, true, "Hello", addr(model.AddrTo, "Marina", "marina@example.com"))
	if err := s.UpsertChat(b.acc, model.Chat{SourceID: "empty", Kind: model.ChatThread, Title: "No messages"}); err != nil {
		t.Fatal(err)
	}
	ids := []int64{b.acc}

	if got := titles(t, s, ids, ThreadFilter{}); !slices.Equal(got, []string{"Only sent", "Invoice", "Monday planning"}) {
		t.Errorf("all mail = %v", got)
	}
	if got := titles(t, s, ids, ThreadFilter{Role: model.FolderInbox}); !slices.Equal(got, []string{"Monday planning"}) {
		t.Errorf("inbox by role = %v", got)
	}
	if got := titles(t, s, ids, ThreadFilter{Role: model.FolderSent}); !slices.Equal(got, []string{"Only sent", "Monday planning"}) {
		t.Errorf("sent = %v", got)
	}
	if got := titles(t, s, ids, ThreadFilter{FolderID: folderIDOf(t, s, b.acc, "Work")}); !slices.Equal(got, []string{"Invoice"}) {
		t.Errorf("work = %v", got)
	}
	// Mail that left the Inbox at the source is still listed there.
	if err := s.LeaveFolder(b.acc, "INBOX", []uint32{1}); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, s, ids, ThreadFilter{Role: model.FolderInbox}); !slices.Equal(got, []string{"Monday planning"}) {
		t.Errorf("inbox after the mail left = %v", got)
	}
	// Chats of a messenger account are not threads.
	other := seedChat(t, s)
	mustUpsert(t, s, other, textMsg("1", "hi"))
	if got := titles(t, s, []int64{b.acc, other}, ThreadFilter{}); len(got) != 3 {
		t.Errorf("a chat was listed as a thread: %v", got)
	}

	list, err := s.Threads(ids, ThreadFilter{}, 1, 1)
	if err != nil || len(list) != 1 || list[0].Title != "Invoice" {
		t.Errorf("paging: %+v, %v", list, err)
	}
}

func TestThreadFilters(t *testing.T) {
	s := openTemp(t)
	b := newMailbox(t, s)
	plain := b.mail("thread:Plain", "INBOX", t0, dan, false, "hello")
	files := b.mail("thread:Files", "INBOX", t0.Add(time.Hour), dan, false, "see attached")
	files.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "a.pdf", MimeType: "application/pdf", Size: 3}}
	mustUpsert(t, s, b.acc, files)
	b.mail("thread:Half gone", "INBOX", t0.Add(2*time.Hour), dan, false, "one")
	b.mail("thread:Half gone", "INBOX", t0.Add(3*time.Hour), priya, false, "two")
	if err := s.LeaveFolder(b.acc, "INBOX", []uint32{3}); err != nil {
		t.Fatal(err)
	}
	_ = plain
	ids := []int64{b.acc}
	if got := titles(t, s, ids, ThreadFilter{Files: true}); !slices.Equal(got, []string{"Files"}) {
		t.Errorf("with attachments = %v", got)
	}
	if got := titles(t, s, ids, ThreadFilter{Deleted: true}); !slices.Equal(got, []string{"Half gone"}) {
		t.Errorf("deleted = %v", got)
	}
	list, _ := s.Threads(ids, ThreadFilter{}, 10, 0)
	for _, th := range list {
		switch th.Title {
		case "Files":
			if !th.HasFiles || th.AllDeleted {
				t.Errorf("Files: %+v", th)
			}
		case "Half gone":
			if th.AllDeleted || th.Messages != 2 {
				t.Errorf("Half gone: %+v", th)
			}
		}
	}
	if err := s.LeaveFolder(b.acc, "INBOX", []uint32{4}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Threads(ids, ThreadFilter{Deleted: true}, 10, 0)
	if len(list) != 1 || !list[0].AllDeleted {
		t.Errorf("every message gone: %+v", list)
	}
}

func TestThreadPeople(t *testing.T) {
	s := openTemp(t)
	b := newMailbox(t, s)
	b.mail("thread:Planning", "INBOX", t0, dan, false, "Can we move it?")
	b.mail("thread:Planning", "Sent", t0.Add(time.Hour), me, true, "Monday works.")
	b.mail("thread:Planning", "INBOX", t0.Add(2*time.Hour), priya, false, "Fine by me.")
	b.mail("thread:Planning", "INBOX", t0.Add(3*time.Hour), dan, false, "Agenda attached.\n\n> Fine by me.")
	b.mail("thread:Planning", "Sent", t0.Add(4*time.Hour), me, true, "Confirmed.")
	b.mail("thread:Only sent", "Sent", t0.Add(5*time.Hour), me, true, "Hello", addr(model.AddrTo, "Marina Sokolova", "marina@example.com"))

	list, err := s.Threads([]int64{b.acc}, ThreadFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	only, planning := list[0], list[1]
	if planning.Person != "Dan Whitfield" || planning.PersonAddress != "dan@example.com" || !slices.Equal(planning.Others, []string{"Priya Nair"}) ||
		!planning.WithOwner || !planning.LastOutgoing || planning.Messages != 5 || planning.Snippet != "Confirmed." ||
		!planning.FirstAt.Equal(t0) || !planning.LastAt.Equal(t0.Add(4*time.Hour)) {
		t.Errorf("planning = %+v", planning)
	}
	if only.Person != "Marina Sokolova" || only.PersonAddress != "marina@example.com" || !only.WithOwner || len(only.Others) != 0 {
		t.Errorf("a thread only from the owner = %+v", only)
	}
}

func TestThreadMessages(t *testing.T) {
	s := openTemp(t)
	b := newMailbox(t, s)
	first := b.mail("thread:Planning", "INBOX", t0, dan, false, "Can we move it?",
		addr(model.AddrTo, "", "me@example.org"), addr(model.AddrTo, "Priya Nair", "priya@example.com"), addr(model.AddrCc, "Ops", "ops@example.com"))
	first.Original = []byte("raw")
	first.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "agenda.pdf", MimeType: "application/pdf", Size: 3}}
	mustUpsert(t, s, b.acc, first)
	b.mail("thread:Planning", "Sent", t0.Add(time.Hour), me, true, "Monday works.")
	b.mail("thread:Planning", "INBOX", t0.Add(2*time.Hour), priya, false, "Fine.")
	chat := chatDBID(t, s, b.acc, "thread:Planning")

	msgs, more, err := s.ThreadMessages(chat, 10)
	if err != nil || more || len(msgs) != 3 {
		t.Fatalf("%d messages, more %v, %v", len(msgs), more, err)
	}
	m := msgs[0]
	if m.Subject != "Planning" || m.Text != "Can we move it?" || m.From.Name != "Dan Whitfield" || m.From.Address != "dan@example.com" ||
		len(m.To) != 2 || m.To[1].Name != "Priya Nair" || len(m.Cc) != 1 || m.Cc[0].Address != "ops@example.com" ||
		len(m.Attachments) != 1 || !m.HasOriginal || m.Outgoing {
		t.Errorf("first = %+v", m)
	}
	if !msgs[1].Outgoing || msgs[1].HasOriginal || msgs[2].From.Name != "Priya Nair" {
		t.Errorf("others = %+v, %+v", msgs[1], msgs[2])
	}
	newest, more, _ := s.ThreadMessages(chat, 2)
	if !more || len(newest) != 2 || newest[0].Text != "Monday works." {
		t.Errorf("newest two: %v, %+v", more, newest)
	}

	path, subject, err := s.OriginalFile(m.ID)
	if err != nil || subject != "Planning" || path != s.MediaPath(shaOf("raw")) {
		t.Errorf("original file = %q %q %v", path, subject, err)
	}
	if _, _, err := s.OriginalFile(msgs[1].ID); err == nil {
		t.Error("a message without an original has a file")
	}
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
