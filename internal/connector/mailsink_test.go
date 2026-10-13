package connector

import (
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

func TestMailSink(t *testing.T) {
	store := openStore(t)
	acc := newAccount(t, store)
	var sink MailSink = NewSink(store, acc, nil, func(Progress) {})

	if err := sink.SetAccountAddress("me@example.org"); err != nil {
		t.Fatal(err)
	}
	if a, _ := store.Account(acc); a.Address != "me@example.org" {
		t.Errorf("address = %q", a.Address)
	}
	if err := sink.UpsertFolder(model.Folder{SourceID: "INBOX", Name: "Inbox", Role: model.FolderInbox, ArchiveByDefault: true}); err != nil {
		t.Fatal(err)
	}
	if on, err := sink.FolderArchived("INBOX"); err != nil || !on {
		t.Fatalf("archived = %v, %v", on, err)
	}
	if err := sink.UpsertChat(model.Chat{SourceID: "thread:a@x", Kind: model.ChatThread, Title: "Hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.UpsertMessage(model.Message{ChatSourceID: "thread:a@x", SourceID: "a@x", SentAt: time.Unix(5, 0), Text: "hi", Original: []byte("raw")}); err != nil {
		t.Fatal(err)
	}
	if err := sink.FileInFolder("INBOX", 7, "thread:a@x", "a@x"); err != nil {
		t.Fatal(err)
	}
	if uids, err := sink.FolderUIDs("INBOX"); err != nil || !slices.Equal(uids, []uint32{7}) {
		t.Fatalf("uids = %v, %v", uids, err)
	}
	r, err := sink.Original("thread:a@x", "a@x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r)
	r.Close()
	if string(body) != "raw" {
		t.Errorf("original = %q", body)
	}
	if err := sink.ResetFolder("INBOX"); err != nil {
		t.Fatal(err)
	}
	if err := sink.LeaveFolder("INBOX", []uint32{7}); err != nil {
		t.Fatal(err)
	}

	// A connector's mistakes are not storage failures.
	if err := sink.FileInFolder("Nowhere", 1, "thread:a@x", "a@x"); !errors.Is(err, archive.ErrNotFound) || errors.Is(err, ErrStorage) {
		t.Errorf("unknown folder: %v", err)
	}
	if _, err := sink.Original("thread:a@x", "zzz"); !errors.Is(err, archive.ErrNotFound) || errors.Is(err, ErrStorage) {
		t.Errorf("unknown original: %v", err)
	}
	if err := sink.UpsertFolder(model.Folder{SourceID: "x", Role: "bogus"}); !errors.Is(err, archive.ErrInvalid) {
		t.Errorf("bogus role: %v", err)
	}
	store.Close()
	if err := sink.LeaveFolder("INBOX", []uint32{1}); !errors.Is(err, ErrStorage) {
		t.Errorf("after the store closed: %v", err)
	}
}
