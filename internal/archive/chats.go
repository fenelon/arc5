package archive

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/fenelon/arc5/internal/model"
)

// UpsertChat records a chat. A chat seen for the first time is archived or not
// according to its kind; later upserts refresh the title and never change that choice.
func (s *Store) UpsertChat(accountID int64, c model.Chat) error {
	if c.SourceID == "" {
		return fmt.Errorf("%w: chat needs a source ID", ErrInvalid)
	}
	if !c.Kind.Valid() {
		return fmt.Errorf("%w: chat kind %q", ErrInvalid, c.Kind)
	}
	_, err := s.w.Exec(`
		INSERT INTO chats (account_id, source_id, kind, title, archived, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, source_id) DO UPDATE SET title = excluded.title, kind = excluded.kind`,
		accountID, c.SourceID, string(c.Kind), clean(c.Title), b2i(c.Kind.ArchivedByDefault()), s.now().Unix())
	return err
}

func (s *Store) UpsertSender(accountID int64, sn model.Sender) error {
	if sn.SourceID == "" {
		return fmt.Errorf("%w: sender needs a source ID", ErrInvalid)
	}
	_, err := s.w.Exec(`
		INSERT INTO senders (account_id, source_id, display_name, username)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (account_id, source_id) DO UPDATE SET display_name = excluded.display_name, username = excluded.username`,
		accountID, sn.SourceID, clean(sn.DisplayName), clean(sn.Username))
	return err
}

// ChatArchived reports whether the chat is being archived. An unknown chat is not.
func (s *Store) ChatArchived(accountID int64, chatSourceID string) (bool, error) {
	var archived int
	err := s.r.QueryRow(`SELECT archived FROM chats WHERE account_id = ? AND source_id = ?`, accountID, chatSourceID).Scan(&archived)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return archived != 0, err
}
