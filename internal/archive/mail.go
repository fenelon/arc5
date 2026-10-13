package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// ThreadFilter chooses which mail conversations to list. With no folder and
// no role, every conversation is listed.
type ThreadFilter struct {
	FolderID int64            // one folder
	Role     model.FolderRole // the folders with this role, in every account listed
	Files    bool             // only conversations with attachments
	Deleted  bool             // only conversations with mail deleted at source
}

// ThreadSummary is one row of a conversation list.
type ThreadSummary struct {
	ID        int64
	AccountID int64
	Title     string
	Messages  int
	FirstAt   time.Time
	LastAt    time.Time
	Snippet   string // the text of the latest message
	// Person is who the conversation is with: the latest sender who is not
	// the owner, or, when the owner sent everything, the first recipient of
	// the latest message. Others are the other senders, not the owner.
	Person        string
	PersonAddress string
	Others        []string
	WithOwner     bool // the owner sent one of the messages
	LastOutgoing  bool
	HasFiles      bool
	AllDeleted    bool
}

// Threads lists mail conversations of the given accounts, the most recently
// active first. A conversation is in a folder when any of its messages was
// filed there, even if it has since left: deleted mail stays listed.
func (s *Store) Threads(accountIDs []int64, f ThreadFilter, limit, offset int) ([]ThreadSummary, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	args := ids(accountIDs)
	where := ``
	switch {
	case f.FolderID != 0:
		where += ` AND EXISTS (SELECT 1 FROM messages fm JOIN message_folders mf ON mf.message_id = fm.id
			WHERE fm.chat_id = c.id AND mf.folder_id = ?)`
		args = append(args, f.FolderID)
	case f.Role != "":
		where += ` AND EXISTS (SELECT 1 FROM messages fm JOIN message_folders mf ON mf.message_id = fm.id
			JOIN folders fo ON fo.id = mf.folder_id WHERE fm.chat_id = c.id AND fo.role = ?)`
		args = append(args, string(f.Role))
	}
	having := ``
	if f.Files {
		having += ` AND files`
	}
	if f.Deleted {
		having += ` AND SUM(m.deleted_at IS NOT NULL) > 0`
	}
	args = append(args, limit, offset)
	rows, err := s.r.Query(`
		SELECT c.id, c.account_id, c.title, COUNT(*), MIN(m.sent_at), MAX(m.sent_at), SUM(m.deleted_at IS NULL) = 0,
		       EXISTS (SELECT 1 FROM attachments a JOIN messages am ON am.id = a.message_id
		               WHERE am.chat_id = c.id AND a.position >= 0) AS files
		FROM chats c JOIN messages m ON m.chat_id = c.id
		WHERE c.kind = 'thread' AND c.account_id IN (`+placeholders(len(accountIDs))+`)`+where+`
		GROUP BY c.id
		HAVING 1`+having+`
		ORDER BY MAX(m.sent_at) DESC, c.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	var out []ThreadSummary
	for rows.Next() {
		var (
			t           ThreadSummary
			first, last int64
		)
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Title, &t.Messages, &first, &last, &t.AllDeleted, &t.HasFiles); err != nil {
			rows.Close()
			return nil, err
		}
		t.FirstAt, t.LastAt = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out = append(out, t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.threadPeople(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func ids(accountIDs []int64) []any {
	out := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		out[i] = id
	}
	return out
}

// threadPeople fills in who a conversation is with, and its snippet.
func (s *Store) threadPeople(t *ThreadSummary) error {
	rows, err := s.r.Query(`
		SELECT m.id, m.outgoing, m.text, COALESCE(a.name, ''), COALESCE(a.address, '')
		FROM messages m LEFT JOIN message_addresses a ON a.message_id = m.id AND a.role = 'from' AND a.position = 0
		WHERE m.chat_id = ? ORDER BY m.sent_at, m.id`, t.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type sender struct{ name, address string }
	var (
		senders []sender // not the owner, oldest first
		lastID  int64
	)
	for rows.Next() {
		var (
			outgoing      bool
			name, address string
		)
		if err := rows.Scan(&lastID, &outgoing, &t.Snippet, &name, &address); err != nil {
			return err
		}
		t.LastOutgoing = outgoing
		if outgoing {
			t.WithOwner = true
			continue
		}
		if name == "" {
			name = address
		}
		senders = append(senders, sender{name, address})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(senders) == 0 {
		// The owner sent everything: the conversation is with the recipient.
		err := s.r.QueryRow(`SELECT name, address FROM message_addresses WHERE message_id = ? AND role = 'to' ORDER BY position LIMIT 1`, lastID).
			Scan(&t.Person, &t.PersonAddress)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if t.Person == "" {
			t.Person = t.PersonAddress
		}
		return err
	}
	latest := senders[len(senders)-1]
	t.Person, t.PersonAddress = latest.name, latest.address
	for _, sn := range senders {
		if sn.address != latest.address && !slices.Contains(t.Others, sn.name) {
			t.Others = append(t.Others, sn.name)
		}
	}
	return nil
}

// MailMessage is one message of a mail conversation.
type MailMessage struct {
	ID          int64
	Subject     string
	Text        string
	SentAt      time.Time
	DeletedAt   time.Time
	Outgoing    bool
	From        model.Address
	To, Cc      []model.Address
	Attachments []StoredAttachment
	HasOriginal bool
}

// ThreadMessages reads the newest limit messages of a mail conversation,
// oldest first, and reports whether there are older ones.
func (s *Store) ThreadMessages(chatID int64, limit int) ([]MailMessage, bool, error) {
	limit = max(limit, 1)
	rows, err := s.r.Query(`
		SELECT m.id, m.subject, m.text, m.sent_at, m.deleted_at, m.outgoing, m.original_sha256 != ''
		FROM messages m WHERE m.chat_id = ?
		ORDER BY m.sent_at DESC, m.id DESC LIMIT ?`, chatID, limit+1)
	if err != nil {
		return nil, false, err
	}
	var out []MailMessage
	for rows.Next() {
		var (
			m       MailMessage
			sent    int64
			deleted sql.NullInt64
		)
		if err := rows.Scan(&m.ID, &m.Subject, &m.Text, &sent, &deleted, &m.Outgoing, &m.HasOriginal); err != nil {
			rows.Close()
			return nil, false, err
		}
		m.SentAt, m.DeletedAt = time.Unix(sent, 0).UTC(), fromUnix(deleted)
		out = append(out, m)
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	slices.Reverse(out)
	for i := range out {
		m := &out[i]
		if err := s.messageAddresses(m); err != nil {
			return nil, false, err
		}
		if m.Attachments, err = s.Attachments(m.ID); err != nil {
			return nil, false, err
		}
	}
	return out, more, nil
}

func (s *Store) messageAddresses(m *MailMessage) error {
	rows, err := s.r.Query(`SELECT role, name, address FROM message_addresses WHERE message_id = ? ORDER BY role, position`, m.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a model.Address
		var role string
		if err := rows.Scan(&role, &a.Name, &a.Address); err != nil {
			return err
		}
		a.Role = model.AddressRole(role)
		switch a.Role {
		case model.AddrFrom:
			if m.From.Address == "" {
				m.From = a
			}
		case model.AddrTo:
			m.To = append(m.To, a)
		case model.AddrCc:
			m.Cc = append(m.Cc, a)
		}
	}
	return rows.Err()
}

// OriginalFile is where a message's original is kept, and its subject, for a
// download's file name.
func (s *Store) OriginalFile(messageID int64) (path, subject string, err error) {
	var sha string
	err = s.r.QueryRow(`SELECT original_sha256, subject FROM messages WHERE id = ?`, messageID).Scan(&sha, &subject)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(sha) != 64) {
		return "", "", fmt.Errorf("original of message %d: %w", messageID, ErrNotFound)
	}
	if err != nil {
		return "", "", err
	}
	return s.MediaPath(sha), subject, nil
}
