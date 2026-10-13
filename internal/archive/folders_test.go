package archive

import (
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

func mustFolder(t *testing.T, s *Store, acc int64, f model.Folder) {
	t.Helper()
	if err := s.UpsertFolder(acc, f); err != nil {
		t.Fatal(err)
	}
}

func folderNames(t *testing.T, s *Store, acc int64) []string {
	t.Helper()
	folders, err := s.AccountFolders(acc)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range folders {
		mark := "-"
		if f.Archived {
			mark = "+"
		}
		out = append(out, mark+f.Name)
	}
	return out
}

func TestFolders(t *testing.T) {
	s := openTemp(t)
	acc := seedThread(t, s)
	for _, f := range []model.Folder{
		{SourceID: "[Gmail]/Trash", Name: "Trash", Role: model.FolderTrash},
		{SourceID: "Work", Name: "Work", Role: model.FolderOther},
		{SourceID: "[Gmail]/Sent Mail", Name: "Sent Mail", Role: model.FolderSent, ArchiveByDefault: true},
		{SourceID: "INBOX", Name: "Inbox", Role: model.FolderInbox, ArchiveByDefault: true},
		{SourceID: "Archive", Name: "Archive", Role: model.FolderOther, ArchiveByDefault: true},
		{SourceID: "[Gmail]/All Mail", Name: "All Mail", Role: model.FolderAll, ArchiveByDefault: true},
		{SourceID: "[Gmail]/Drafts", Name: "Drafts", Role: model.FolderDrafts},
	} {
		mustFolder(t, s, acc, f)
	}
	want := []string{"+Inbox", "+Sent Mail", "+All Mail", "+Archive", "-Work", "-Drafts", "-Trash"}
	if got := folderNames(t, s, acc); !slices.Equal(got, want) {
		t.Fatalf("folders = %v, want %v", got, want)
	}
	if err := s.UpsertFolder(acc, model.Folder{SourceID: "x", Name: "x", Role: "bogus"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a bogus role: %v", err)
	}

	// The owner's choice stands when the connector sees the folder again.
	folders, _ := s.AccountFolders(acc)
	byName := map[string]int64{}
	for _, f := range folders {
		byName[f.Name] = f.ID
	}
	if err := s.SetFoldersArchived(acc, map[int64]bool{byName["Work"]: true, byName["Archive"]: false}); err != nil {
		t.Fatal(err)
	}
	mustFolder(t, s, acc, model.Folder{SourceID: "Work", Name: "Work (renamed)", Role: model.FolderOther})
	mustFolder(t, s, acc, model.Folder{SourceID: "Archive", Name: "Archive", Role: model.FolderOther, ArchiveByDefault: true})
	if on, err := s.FolderArchived(acc, "Work"); err != nil || !on {
		t.Errorf("Work archived = %v, %v", on, err)
	}
	if on, _ := s.FolderArchived(acc, "Archive"); on {
		t.Error("the connector switched Archive back on")
	}
	if on, err := s.FolderArchived(acc, "Nowhere"); err != nil || on {
		t.Errorf("an unknown folder: %v, %v", on, err)
	}
	if got := folderNames(t, s, acc); !slices.Contains(got, "+Work (renamed)") {
		t.Errorf("the new name was not kept: %v", got)
	}
	// Another account's folders cannot be switched.
	other := newAccount(t, s)
	if err := s.SetFoldersArchived(other, map[int64]bool{byName["Trash"]: true}); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.FolderArchived(acc, "[Gmail]/Trash"); on {
		t.Error("another account switched a folder")
	}
}

func stored(t *testing.T, s *Store, acc int64, id string) StoredMessage {
	t.Helper()
	m, err := s.Message(acc, "t1", id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMembershipAndDeletion(t *testing.T) {
	s := openTemp(t)
	acc := seedThread(t, s)
	mustFolder(t, s, acc, model.Folder{SourceID: "INBOX", Name: "Inbox", Role: model.FolderInbox, ArchiveByDefault: true})
	mustFolder(t, s, acc, model.Folder{SourceID: "All", Name: "All Mail", Role: model.FolderAll, ArchiveByDefault: true})
	mustUpsert(t, s, acc, mailMsg("a@x", "One", "one", nil))
	mustUpsert(t, s, acc, mailMsg("b@x", "Two", "two", nil))
	file := func(folder string, uid uint32, id string) {
		t.Helper()
		if err := s.FileInFolder(acc, folder, uid, "t1", id); err != nil {
			t.Fatal(err)
		}
	}
	file("INBOX", 10, "a@x")
	file("All", 100, "a@x")
	file("All", 101, "b@x")
	file("All", 101, "b@x") // again: idempotent

	if err := s.FileInFolder(acc, "Nowhere", 1, "t1", "a@x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown folder: %v", err)
	}
	if err := s.FileInFolder(acc, "INBOX", 1, "t1", "zzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown message: %v", err)
	}
	if uids, err := s.FolderUIDs(acc, "All"); err != nil || !slices.Equal(uids, []uint32{100, 101}) {
		t.Fatalf("All UIDs = %v, %v", uids, err)
	}

	// Gone from the Inbox only (archived in Gmail): not deleted.
	if err := s.LeaveFolder(acc, "INBOX", []uint32{10}); err != nil {
		t.Fatal(err)
	}
	if !stored(t, s, acc, "a@x").DeletedAt.IsZero() {
		t.Fatal("leaving one folder of two marked the message deleted")
	}
	if uids, _ := s.FolderUIDs(acc, "INBOX"); len(uids) != 0 {
		t.Errorf("Inbox still lists %v", uids)
	}
	// Gone from every folder: deleted, once.
	if err := s.LeaveFolder(acc, "All", []uint32{100, 999}); err != nil {
		t.Fatal(err)
	}
	first := stored(t, s, acc, "a@x").DeletedAt
	if first.IsZero() {
		t.Fatal("a message gone from every folder is not deleted")
	}
	if !stored(t, s, acc, "b@x").DeletedAt.IsZero() {
		t.Error("another message was marked")
	}
	if err := s.LeaveFolder(acc, "All", []uint32{100}); err != nil {
		t.Fatal(err)
	}
	if again := stored(t, s, acc, "a@x").DeletedAt; !again.Equal(first) {
		t.Errorf("deletion time moved from %v to %v", first, again)
	}
	// Seen again (moved back): no longer deleted.
	file("INBOX", 11, "a@x")
	if !stored(t, s, acc, "a@x").DeletedAt.IsZero() {
		t.Error("a message filed again is still marked deleted")
	}
	// The thread is still listed under the folder it left.
	var memberships int
	s.r.QueryRow(`SELECT COUNT(*) FROM message_folders`).Scan(&memberships)
	if memberships != 3 {
		t.Errorf("memberships = %d, want 3 (rows are kept when mail leaves)", memberships)
	}
}

func TestResetFolder(t *testing.T) {
	s := openTemp(t)
	acc := seedThread(t, s)
	mustFolder(t, s, acc, model.Folder{SourceID: "INBOX", Name: "Inbox", Role: model.FolderInbox, ArchiveByDefault: true})
	mustUpsert(t, s, acc, mailMsg("a@x", "One", "one", nil))
	if err := s.FileInFolder(acc, "INBOX", 10, "t1", "a@x"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetFolder(acc, "INBOX"); err != nil {
		t.Fatal(err)
	}
	if uids, _ := s.FolderUIDs(acc, "INBOX"); len(uids) != 0 {
		t.Fatalf("UIDs after a reset: %v", uids)
	}
	// Old UIDs mean nothing now: leaving UID 10 does not touch the message.
	if err := s.LeaveFolder(acc, "INBOX", []uint32{10}); err != nil {
		t.Fatal(err)
	}
	if !stored(t, s, acc, "a@x").DeletedAt.IsZero() {
		t.Error("a forgotten UID deleted the message")
	}
	if err := s.FileInFolder(acc, "INBOX", 3, "t1", "a@x"); err != nil {
		t.Fatal(err)
	}
	if uids, _ := s.FolderUIDs(acc, "INBOX"); !slices.Equal(uids, []uint32{3}) {
		t.Errorf("UIDs after filing again: %v", uids)
	}
}

func TestOriginal(t *testing.T) {
	s := openTemp(t)
	acc := seedThread(t, s)
	mustUpsert(t, s, acc, mailMsg("a@x", "One", "one", []byte("the original")))
	mustUpsert(t, s, acc, mailMsg("b@x", "Two", "two", nil))
	r, err := s.Original(acc, "t1", "a@x")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != "the original" {
		t.Errorf("original = %q", got)
	}
	if _, err := s.Original(acc, "t1", "b@x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a message without one: %v", err)
	}
	if _, err := s.Original(acc, "t1", "zzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown message: %v", err)
	}
}

func TestAccountAddress(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	if err := s.SetAccountAddress(acc, "me@example.org"); err != nil {
		t.Fatal(err)
	}
	a, err := s.Account(acc)
	if err != nil || a.Address != "me@example.org" {
		t.Fatalf("address = %q, %v", a.Address, err)
	}
}
