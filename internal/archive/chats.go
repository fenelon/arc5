package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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

// ChatEntry is one chat of an account, as the owner sees it when choosing
// which chats are archived.
type ChatEntry struct {
	ID       int64
	SourceID string
	Kind     model.ChatKind
	Title    string
	Archived bool
	Messages int
	LastAt   time.Time // zero when nothing from it is archived
}

// AccountChats lists every chat of an account, archived or not: the most
// recently active first, then the ones with nothing archived, by title.
func (s *Store) AccountChats(accountID int64) ([]ChatEntry, error) {
	rows, err := s.r.Query(`
		SELECT c.id, c.source_id, c.kind, c.title, c.archived,
		       (SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id),
		       (SELECT MAX(m.sent_at) FROM messages m WHERE m.chat_id = c.id) AS last
		FROM chats c WHERE c.account_id = ?
		ORDER BY last IS NULL, last DESC, c.title, c.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatEntry
	for rows.Next() {
		var (
			c        ChatEntry
			kind     string
			archived int
			last     sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &c.SourceID, &kind, &c.Title, &archived, &c.Messages, &last); err != nil {
			return nil, err
		}
		c.Kind, c.Archived, c.LastAt = model.ChatKind(kind), archived != 0, fromUnix(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetChatsArchived switches archiving on or off for chats of one account, by
// chat ID. IDs of other accounts' chats are ignored.
func (s *Store) SetChatsArchived(accountID int64, archived map[int64]bool) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		for id, on := range archived {
			if _, err := tx.Exec(`UPDATE chats SET archived = ? WHERE id = ? AND account_id = ?`, b2i(on), id, accountID); err != nil {
				return err
			}
		}
		return nil
	})
}

// LastMessageTimes returns, per account, when its newest archived message
// was sent. Accounts with no messages are left out.
func (s *Store) LastMessageTimes() (map[int64]time.Time, error) {
	rows, err := s.r.Query(`
		SELECT account_id, MAX(last) FROM (
			SELECT c.account_id, (SELECT MAX(m.sent_at) FROM messages m WHERE m.chat_id = c.id) AS last
			FROM chats c)
		WHERE last IS NOT NULL GROUP BY account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var (
			id   int64
			last int64
		)
		if err := rows.Scan(&id, &last); err != nil {
			return nil, err
		}
		out[id] = time.Unix(last, 0).UTC()
	}
	return out, rows.Err()
}

// SenderInfo is one person who wrote in an account's chats.
type SenderInfo struct {
	ID          int64
	AccountID   int64
	DisplayName string
	Username    string
}

func (s *Store) Sender(id int64) (SenderInfo, error) {
	var sn SenderInfo
	err := s.r.QueryRow(`SELECT id, account_id, display_name, username FROM senders WHERE id = ?`, id).
		Scan(&sn.ID, &sn.AccountID, &sn.DisplayName, &sn.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return SenderInfo{}, fmt.Errorf("sender %d: %w", id, ErrNotFound)
	}
	return sn, err
}
