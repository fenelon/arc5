package connector

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

func openStore(t *testing.T) *archive.Store {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func newAccount(t *testing.T, store *archive.Store) int64 {
	t.Helper()
	id, err := store.CreateAccount("stub", "Test", map[string]string{"k": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSinkWritesAreBoundToItsAccount(t *testing.T) {
	store := openStore(t)
	acc, other := newAccount(t, store), newAccount(t, store)
	var reported Progress
	sink := newSink(store, acc, nil, func(p Progress) { reported = p })

	if err := sink.UpsertChat(model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.UpsertSender(model.Sender{SourceID: "u1", DisplayName: "Alice"}); err != nil {
		t.Fatal(err)
	}
	msg := model.Message{
		ChatSourceID: "c1", SourceID: "1", SenderSourceID: "u1", SentAt: time.Unix(1_700_000_000, 0), Text: "hi",
		Attachments: []model.Attachment{{Kind: model.AttachDocument, FileName: "a.txt", Size: 5}},
	}
	refs, err := sink.UpsertMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("pending refs = %+v, want one", refs)
	}
	if err := sink.PutMedia(refs[0], strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if archived, err := sink.ChatArchived("c1"); err != nil || !archived {
		t.Fatalf("ChatArchived = %v, %v", archived, err)
	}
	chats, err := sink.LookupChats("1", []model.ChatKind{model.ChatPrivate})
	if err != nil || len(chats) != 1 || chats[0] != "c1" {
		t.Fatalf("LookupChats = %v, %v", chats, err)
	}
	if err := sink.MarkDeleted("c1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	sink.ReportProgress(Progress{Phase: PhaseLive})

	stored, err := store.Message(acc, "c1", "1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DeletedAt.IsZero() {
		t.Error("message not marked deleted")
	}
	if _, err := store.Message(other, "c1", "1"); !errors.Is(err, archive.ErrNotFound) {
		t.Errorf("message visible in another account: %v", err)
	}
	if reported.Phase != PhaseLive {
		t.Errorf("reported progress = %+v", reported)
	}
}

func TestSinkErrorClassification(t *testing.T) {
	store := openStore(t)
	acc := newAccount(t, store)
	sink := newSink(store, acc, nil, func(Progress) {})

	// A connector mistake is not a storage failure.
	_, err := sink.UpsertMessage(model.Message{ChatSourceID: "nope", SourceID: "1", SentAt: time.Unix(1, 0)})
	if !errors.Is(err, archive.ErrUnknownChat) || errors.Is(err, ErrStorage) {
		t.Errorf("unknown chat error = %v; want ErrUnknownChat and not ErrStorage", err)
	}

	// A broken database is.
	store.Close()
	err = sink.UpsertChat(model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "x"})
	if !errors.Is(err, ErrStorage) {
		t.Errorf("error after the store closed = %v; want ErrStorage", err)
	}
}

func TestStateIsBoundToItsAccount(t *testing.T) {
	store := openStore(t)
	a := accountState{store: store, accountID: newAccount(t, store)}
	b := accountState{store: store, accountID: newAccount(t, store)}

	if err := a.Set("session", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if v, err := a.Get("session"); err != nil || !bytes.Equal(v, []byte("secret")) {
		t.Fatalf("a.Get = %q, %v", v, err)
	}
	if v, err := b.Get("session"); err != nil || v != nil {
		t.Fatalf("b.Get = %q, %v; want nil", v, err)
	}
}
