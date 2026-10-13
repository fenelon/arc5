package email

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// archived is a real archive with one mailbox account, and its sink.
type archived struct {
	store *archive.Store
	acc   int64
	sink  connector.MailSink
}

func newArchived(t *testing.T) *archived {
	t.Helper()
	store, err := archive.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	acc, err := store.CreateAccount(Name, "Home mail", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &archived{store: store, acc: acc, sink: connector.NewSink(store, acc, nil, func(connector.Progress) {})}
}

func crlf(s string) []byte {
	return []byte(strings.ReplaceAll(strings.TrimPrefix(s, "\n"), "\n", "\r\n"))
}

func (a *archived) message(t *testing.T, chat, id string) archive.StoredMessage {
	t.Helper()
	m, err := a.store.Message(a.acc, chat, id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var received = time.Date(2026, 9, 27, 17, 0, 0, 0, time.UTC)

func own(address string) bool { return address == "me@example.org" }

func TestStoreMessage(t *testing.T) {
	a := newArchived(t)
	first := crlf(`
From: Dan Whitfield <dan@example.com>
To: me@example.org, Priya Nair <priya@example.com>
Subject: Monday planning
Date: Thu, 25 Sep 2026 09:12:00 +0000
Message-ID: <one@example.com>
Content-Type: multipart/mixed; boundary=b

--b
Content-Type: text/plain

Can we move it to Monday?
--b
Content-Type: application/pdf
Content-Disposition: attachment; filename=agenda.pdf

%PDF agenda
--b--
`)
	chat, id, err := Store(a.sink, first, received, own)
	if err != nil {
		t.Fatal(err)
	}
	if chat != "thread:one@example.com" || id != "one@example.com" {
		t.Fatalf("stored as %q %q", chat, id)
	}
	m := a.message(t, chat, id)
	if m.Text != "Can we move it to Monday?" || m.SenderSourceID != "dan@example.com" || m.Outgoing {
		t.Errorf("message = %+v", m)
	}
	atts, _ := a.store.Attachments(m.ID)
	if len(atts) != 1 || atts[0].Status != model.StatusStored || atts[0].FileName != "agenda.pdf" {
		t.Fatalf("attachments = %+v", atts)
	}
	if body, _ := os.ReadFile(a.store.MediaPath(atts[0].SHA256)); string(body) != "%PDF agenda" {
		t.Errorf("attachment file = %q", body)
	}
	info, _ := a.store.Chat(mustChatID(t, a, chat))
	if info.Kind != model.ChatThread || info.Title != "Monday planning" {
		t.Errorf("chat = %+v", info)
	}
	r, err := a.sink.Original(chat, id)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()

	// The reply joins the thread; it is the owner's.
	reply := crlf(`
From: Me <me@example.org>
To: Dan Whitfield <dan@example.com>
Subject: Re: Monday planning
Date: Thu, 25 Sep 2026 09:40:00 +0000
Message-ID: <two@example.org>
In-Reply-To: <one@example.com>
References: <one@example.com>

Monday works.
`)
	chat2, id2, err := Store(a.sink, reply, received, own)
	if err != nil || chat2 != chat || id2 != "two@example.org" {
		t.Fatalf("reply stored as %q %q, %v", chat2, id2, err)
	}
	if !a.message(t, chat, id2).Outgoing {
		t.Error("the owner's reply is not outgoing")
	}
	// A reply naming only its parent joins the parent's thread.
	late := crlf("From: priya@example.com\nSubject: Re: Monday planning\nMessage-ID: <three@example.com>\nIn-Reply-To: <two@example.org>\n\nFine.\n")
	if chat3, _, err := Store(a.sink, late, received, own); err != nil || chat3 != chat {
		t.Errorf("an In-Reply-To reply went to %q, %v", chat3, err)
	}
	// Storing again is a no-op that names the same message.
	if c, i, err := Store(a.sink, first, received, own); err != nil || c != chat || i != id {
		t.Errorf("again: %q %q %v", c, i, err)
	}
	// Names are kept even when a later message gives none.
	sender, err := a.store.Sender(senderID(t, a, "dan@example.com"))
	if err != nil || sender.DisplayName != "Dan Whitfield" || sender.Username != "dan@example.com" {
		t.Errorf("sender = %+v, %v", sender, err)
	}
	priya, err := a.store.Sender(senderID(t, a, "priya@example.com"))
	if err != nil || priya.DisplayName != "Priya Nair" {
		t.Errorf("a recipient's name was lost: %+v, %v", priya, err)
	}
}

func TestStoreUnreadableMessage(t *testing.T) {
	a := newArchived(t)
	chat, id, err := Store(a.sink, []byte("\x00\x01 not mail"), received, own)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "sha256:") || a.message(t, chat, id).SentAt.IsZero() {
		t.Errorf("stored as %q %q", chat, id)
	}
	if _, err := a.sink.Original(chat, id); err != nil {
		t.Errorf("the original of an unreadable message was not kept: %v", err)
	}
}

func TestStoreOverTheCapSkipsTheFile(t *testing.T) {
	a := newArchived(t)
	if err := a.store.SetMediaCap(4); err != nil {
		t.Fatal(err)
	}
	raw := crlf("From: a@example.com\nSubject: Big\nMessage-ID: <big@x>\nContent-Type: multipart/mixed; boundary=b\n\n--b\nContent-Type: text/plain\n\nhi\n--b\nContent-Type: application/zip\nContent-Disposition: attachment; filename=big.zip\n\nmore than four bytes\n--b--\n")
	chat, id, err := Store(a.sink, raw, received, own)
	if err != nil {
		t.Fatal(err)
	}
	atts, _ := a.store.Attachments(a.message(t, chat, id).ID)
	if len(atts) != 1 || atts[0].Status != model.StatusSkipped {
		t.Fatalf("attachments = %+v", atts)
	}
	// Fetch now takes it out of the original.
	if err := FetchFromOriginal(a.sink, model.AttachmentRef{ChatSourceID: chat, MessageSourceID: id, Position: 0}); err != nil {
		t.Fatal(err)
	}
	atts, _ = a.store.Attachments(a.message(t, chat, id).ID)
	if atts[0].Status != model.StatusStored {
		t.Fatalf("after fetching: %+v", atts[0])
	}
	if err := FetchFromOriginal(a.sink, model.AttachmentRef{ChatSourceID: chat, MessageSourceID: id, Position: 3}); !errors.Is(err, archive.ErrUnknownAttachment) && err == nil {
		t.Errorf("a position the message does not have: %v", err)
	}
}

func mustChatID(t *testing.T, a *archived, source string) int64 {
	t.Helper()
	var id int64
	if err := a.store.Reader().QueryRow(`SELECT id FROM chats WHERE account_id = ? AND source_id = ?`, a.acc, source).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func senderID(t *testing.T, a *archived, source string) int64 {
	t.Helper()
	var id int64
	if err := a.store.Reader().QueryRow(`SELECT id FROM senders WHERE account_id = ? AND source_id = ?`, a.acc, source).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
