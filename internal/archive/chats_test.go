package archive

import (
	"errors"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

func newAccount(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.CreateAccount("test", "Test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestChatArchivedDefaultsFollowKind(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	for kind, want := range map[model.ChatKind]bool{
		model.ChatPrivate: true, model.ChatGroup: true, model.ChatChannel: false, model.ChatBot: false,
	} {
		id := "chat-" + string(kind)
		if err := s.UpsertChat(acc, model.Chat{SourceID: id, Kind: kind, Title: "t"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.ChatArchived(acc, id)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s archived = %v, want %v", kind, got, want)
		}
	}
}

func TestUpsertChatUpdatesTitleButKeepsArchivedChoice(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	if err := s.UpsertChat(acc, model.Chat{SourceID: "c1", Kind: model.ChatChannel, Title: "Old"}); err != nil {
		t.Fatal(err)
	}
	// The owner switches the channel on; a later upsert must not undo that.
	if _, err := s.w.Exec(`UPDATE chats SET archived = 1 WHERE source_id = 'c1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertChat(acc, model.Chat{SourceID: "c1", Kind: model.ChatChannel, Title: "New"}); err != nil {
		t.Fatal(err)
	}
	archived, err := s.ChatArchived(acc, "c1")
	if err != nil {
		t.Fatal(err)
	}
	var title string
	if err := s.r.QueryRow(`SELECT title FROM chats WHERE source_id = 'c1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if !archived || title != "New" {
		t.Fatalf("archived = %v, title = %q; want true and New", archived, title)
	}
}

func TestChatsAreScopedByAccount(t *testing.T) {
	s := openTemp(t)
	a, b := newAccount(t, s), newAccount(t, s)
	if err := s.UpsertChat(a, model.Chat{SourceID: "same", Kind: model.ChatPrivate, Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertChat(b, model.Chat{SourceID: "same", Kind: model.ChatChannel, Title: "B"}); err != nil {
		t.Fatal(err)
	}
	inA, _ := s.ChatArchived(a, "same")
	inB, _ := s.ChatArchived(b, "same")
	if !inA || inB {
		t.Fatalf("archived in A = %v, in B = %v; want true and false", inA, inB)
	}
}

func TestChatArchivedIsFalseForUnknownChat(t *testing.T) {
	s := openTemp(t)
	got, err := s.ChatArchived(newAccount(t, s), "nope")
	if err != nil || got {
		t.Fatalf("ChatArchived = %v, %v; want false, nil", got, err)
	}
}

func TestUpsertChatRejectsBadInput(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	if err := s.UpsertChat(acc, model.Chat{SourceID: "c", Kind: "dm", Title: "t"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad kind error = %v, want ErrInvalid", err)
	}
	if err := s.UpsertChat(acc, model.Chat{Kind: model.ChatPrivate, Title: "t"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty source ID error = %v, want ErrInvalid", err)
	}
}

func TestUpsertSenderUpdatesName(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	if err := s.UpsertSender(acc, model.Sender{SourceID: "u1", DisplayName: "Al"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSender(acc, model.Sender{SourceID: "u1", DisplayName: "Alice", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	var name, username string
	var n int
	if err := s.r.QueryRow(`SELECT COUNT(*), MAX(display_name), MAX(username) FROM senders`).Scan(&n, &name, &username); err != nil {
		t.Fatal(err)
	}
	if n != 1 || name != "Alice" || username != "alice" {
		t.Fatalf("senders = %d rows, %q, %q", n, name, username)
	}
}

func TestUpsertSenderKeepsANameItIsNotGiven(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	for _, sn := range []model.Sender{
		{SourceID: "dan@example.com", DisplayName: "Dan Whitfield", Username: "dan@example.com"},
		{SourceID: "dan@example.com", Username: "Dan@Example.com"},
	} {
		if err := s.UpsertSender(acc, sn); err != nil {
			t.Fatal(err)
		}
	}
	var name, username string
	if err := s.r.QueryRow(`SELECT display_name, username FROM senders`).Scan(&name, &username); err != nil {
		t.Fatal(err)
	}
	if name != "Dan Whitfield" || username != "Dan@Example.com" {
		t.Fatalf("sender = %q, %q", name, username)
	}
}

func TestInvalidUTF8InNamesIsReplaced(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	if err := s.UpsertChat(acc, model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "bad\xffname"}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := s.r.QueryRow(`SELECT title FROM chats`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "bad�name" {
		t.Fatalf("title = %q", title)
	}
}
