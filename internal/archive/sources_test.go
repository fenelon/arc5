package archive

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

func TestRenameAccount(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	if err := s.RenameAccount(acc, "  Work\u0000 phone "); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Account(acc); got.Label != "Work phone" {
		t.Errorf("label = %q, want %q", got.Label, "Work phone")
	}
	for _, bad := range []string{"", "   "} {
		if err := s.RenameAccount(acc, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("RenameAccount(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if err := s.RenameAccount(9999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: %v, want ErrNotFound", err)
	}
}

func TestRemoveAccountKeepsItsArchive(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "kept"))
	if err := s.StateSet(acc, "session", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0 }
	if err := s.RemoveAccount(acc); err != nil {
		t.Fatal(err)
	}
	got, err := s.Account(acc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || !got.RemovedAt.Equal(t0) {
		t.Errorf("removed account = %+v; want it switched off and marked removed", got)
	}
	if v, _ := s.StateGet(acc, "session"); v != nil {
		t.Error("the connector's session survived removal")
	}
	if mustMessage(t, s, acc, "1").Text != "kept" {
		t.Error("the archive was not kept")
	}
	if err := s.RemoveAccount(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: %v, want ErrNotFound", err)
	}
	if err := s.SetAccountEnabled(acc, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("switching a removed account back on: %v, want ErrInvalid", err)
	}
}

func TestAccountChatsAndArchiving(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s) // c1, private, "Alice"
	other := newAccount(t, s)
	for _, c := range []model.Chat{
		{SourceID: "news", Kind: model.ChatChannel, Title: "News"},
		{SourceID: "g1", Kind: model.ChatGroup, Title: "Group"},
	} {
		if err := s.UpsertChat(acc, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	later := textMsg("2", "in the group")
	later.ChatSourceID, later.SentAt = "g1", t0.Add(time.Hour)
	mustUpsert(t, s, acc, later)

	chats, err := s.AccountChats(acc)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, c := range chats {
		titles = append(titles, c.Title)
	}
	// Most recently active first; chats with nothing archived after, by title.
	if want := []string{"Group", "Alice", "News"}; !slices.Equal(titles, want) {
		t.Fatalf("chats = %v, want %v", titles, want)
	}
	if g := chats[0]; g.Kind != model.ChatGroup || !g.Archived || g.Messages != 1 || !g.LastAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("group = %+v", g)
	}
	if n := chats[2]; n.Archived || n.Messages != 0 || !n.LastAt.IsZero() {
		t.Errorf("channel = %+v", n)
	}

	elsewhere, err := s.AccountChats(other)
	if err != nil || len(elsewhere) != 1 {
		t.Fatalf("other account's chats = %+v, %v", elsewhere, err)
	}
	changes := map[int64]bool{chats[0].ID: false, chats[2].ID: true, elsewhere[0].ID: false}
	if err := s.SetChatsArchived(acc, changes); err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string]bool{"g1": false, "news": true, "c1": true} {
		if got, _ := s.ChatArchived(acc, source); got != want {
			t.Errorf("chat %s archived = %v, want %v", source, got, want)
		}
	}
	if got, _ := s.ChatArchived(other, "x"); !got {
		t.Error("a chat of another account was changed")
	}
}

func TestLastMessageTimes(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	quiet := newAccount(t, s)
	mustUpsert(t, s, acc, textMsg("1", "first"))
	m := textMsg("2", "second")
	m.SentAt = t0.Add(time.Hour)
	mustUpsert(t, s, acc, m)

	got, err := s.LastMessageTimes()
	if err != nil {
		t.Fatal(err)
	}
	if !got[acc].Equal(t0.Add(time.Hour)) {
		t.Errorf("last message of the account = %v", got[acc])
	}
	if _, ok := got[quiet]; ok {
		t.Error("an account with no messages has a last message time")
	}
}

func TestSender(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s) // sender u1, "Alice"
	var id int64
	if err := s.r.QueryRow(`SELECT id FROM senders WHERE source_id = 'u1'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sender(id)
	if err != nil || got.ID != id || got.AccountID != acc || got.DisplayName != "Alice" {
		t.Fatalf("Sender = %+v, %v", got, err)
	}
	if _, err := s.Sender(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown sender: %v, want ErrNotFound", err)
	}
}
