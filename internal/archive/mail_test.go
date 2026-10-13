package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

// seedThread returns an account with a mail thread "t1".
func seedThread(t *testing.T, s *Store) int64 {
	t.Helper()
	acc := newAccount(t, s)
	if err := s.UpsertChat(acc, model.Chat{SourceID: "t1", Kind: model.ChatThread, Title: "Monday planning"}); err != nil {
		t.Fatal(err)
	}
	return acc
}

func mailMsg(id, subject, text string, original []byte) model.Message {
	return model.Message{
		ChatSourceID: "t1", SourceID: id, SenderSourceID: "dan@example.com", SentAt: t0, Subject: subject, Text: text,
		Addresses: []model.Address{
			{Role: model.AddrFrom, Name: "Dan Whitfield", Address: "dan@example.com"},
			{Role: model.AddrTo, Address: "me@example.org"},
			{Role: model.AddrTo, Name: "Priya Nair", Address: "priya@example.com"},
			{Role: model.AddrCc, Name: "Ops", Address: "ops@example.com"},
		},
		Original: original,
	}
}

func TestMailMessageStored(t *testing.T) {
	s := openTemp(t)
	acc := seedThread(t, s)
	original := []byte("From: Dan <dan@example.com>\r\nSubject: Monday planning\r\n\r\nRoom 4.\r\n")
	mustUpsert(t, s, acc, mailMsg("a@example.com", "Monday planning", "Room 4.", original))

	var (
		id                int64
		subject, sha, raw = "", "", []byte(nil)
	)
	if err := s.r.QueryRow(`SELECT id, subject, original_sha256, raw FROM messages WHERE source_id = 'a@example.com'`).Scan(&id, &subject, &sha, &raw); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	if subject != "Monday planning" || sha != hex.EncodeToString(sum[:]) || raw != nil {
		t.Fatalf("subject %q, original %q, raw %d bytes", subject, sha, len(raw))
	}
	if got, err := os.ReadFile(s.MediaPath(sha)); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("original on disk: %q, %v", got, err)
	}

	rows, err := s.r.Query(`SELECT role, position, address, name FROM message_addresses WHERE message_id = ? ORDER BY role, position`, id)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var role, address, name string
		var position int
		if err := rows.Scan(&role, &position, &address, &name); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{role, string(rune('0' + position)), address, name}, " "))
	}
	rows.Close()
	want := []string{"cc 0 ops@example.com Ops", "from 0 dan@example.com Dan Whitfield", "to 0 me@example.org ", "to 1 priya@example.com Priya Nair"}
	if !slices.Equal(got, want) {
		t.Fatalf("addresses = %q, want %q", got, want)
	}

	// A second copy changes nothing; a copy without an original keeps it.
	again := mailMsg("a@example.com", "Monday planning", "Room 4.", nil)
	again.Addresses = again.Addresses[:1]
	mustUpsert(t, s, acc, again)
	var addresses int
	s.r.QueryRow(`SELECT COUNT(*) FROM message_addresses WHERE message_id = ?`, id).Scan(&addresses)
	var sha2 string
	s.r.QueryRow(`SELECT original_sha256 FROM messages WHERE id = ?`, id).Scan(&sha2)
	if addresses != 4 || sha2 != sha {
		t.Errorf("after a second copy: %d addresses, original %q", addresses, sha2)
	}

	// A message stored without its original gets it from a later copy.
	mustUpsert(t, s, acc, mailMsg("b@example.com", "Re: Monday planning", "Fine.", nil))
	mustUpsert(t, s, acc, mailMsg("b@example.com", "Re: Monday planning", "Fine.", []byte("second original")))
	var shaB string
	s.r.QueryRow(`SELECT original_sha256 FROM messages WHERE source_id = 'b@example.com'`).Scan(&shaB)
	if shaB == "" {
		t.Error("a later copy did not fill in the missing original")
	}
}

func TestSubjectIsSearchable(t *testing.T) {
	s := openTemp(t)
	s.SetAnalyzer(wordAnalyzer{sig: "w"})
	acc := seedThread(t, s)
	mustUpsert(t, s, acc, mailMsg("a@example.com", "Quarterly zeppelin budget", "Numbers attached.", nil))
	if got := matches(t, s, "exact:zeppelin"); !slices.Equal(got, []string{"a@example.com"}) {
		t.Fatalf("the subject is not in the index: %v", got)
	}
}

func TestMigrationRebuildsChats(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	migrationLimit = 5
	s, err := Open(dir)
	migrationLimit = 0
	if err != nil {
		t.Fatal(err)
	}
	acc, err := s.CreateAccount("telegram", "Personal", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`INSERT INTO chats (id, account_id, source_id, kind, title, archived, created_at, avatar_id, avatar_sha256)
		VALUES (7, ?, 'user:1', 'private', 'Alice', 1, 1, 'photo:9', 'abc'), (9, ?, 'channel:2', 'channel', 'News', 0, 2, '', '')`, acc, acc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`INSERT INTO messages (id, chat_id, source_id, kind, sent_at, text, captured_at) VALUES (100, 7, '1', 'message', 5, 'hi', 5)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`INSERT INTO chats (account_id, source_id, kind, title, archived, created_at) VALUES (?, 'x', 'thread', 't', 1, 1)`, acc); err == nil {
		t.Fatal("version 5 already accepts threads; the test proves nothing")
	}
	s.Close()

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var chats, messages, applied int
	var title, avatar string
	s.r.QueryRow(`SELECT COUNT(*) FROM chats`).Scan(&chats)
	s.r.QueryRow(`SELECT title, avatar_sha256 FROM chats WHERE id = 7`).Scan(&title, &avatar)
	s.r.QueryRow(`SELECT COUNT(*) FROM messages m JOIN chats c ON c.id = m.chat_id WHERE m.id = 100`).Scan(&messages)
	s.r.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied)
	if chats != 2 || title != "Alice" || avatar != "abc" || messages != 1 || applied != 6 {
		t.Fatalf("after the rebuild: %d chats, %q %q, %d joined messages, %d migrations", chats, title, avatar, messages, applied)
	}
	var index int
	s.r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'avatars_by_chat_file'`).Scan(&index)
	if index != 1 {
		t.Error("the avatar index was lost")
	}
	if err := s.UpsertChat(acc, model.Chat{SourceID: "thread:a@example.com", Kind: model.ChatThread, Title: "Hello"}); err != nil {
		t.Fatalf("a thread is refused after the migration: %v", err)
	}
	// Foreign keys are on again: a message of a missing chat is refused.
	if _, err := s.w.Exec(`INSERT INTO messages (chat_id, source_id, kind, sent_at, text, captured_at) VALUES (999, '2', 'message', 5, 'x', 5)`); err == nil {
		t.Error("foreign keys were left off")
	}
}
