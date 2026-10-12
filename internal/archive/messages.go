package archive

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

const reasonOverCap = "over size cap"

// UpsertMessage stores a new message or applies an edit, and returns the
// attachments of that message still waiting to be downloaded.
//
// It is idempotent. An upsert whose edit time is older than the stored one is
// ignored, so replaying an original never reverts a later edit. Attachments
// are recorded when their position is first seen and are not changed by edits.
func (s *Store) UpsertMessage(accountID int64, m model.Message) ([]model.AttachmentRef, error) {
	if m.SourceID == "" {
		return nil, fmt.Errorf("%w: message needs a source ID", ErrInvalid)
	}
	if m.SentAt.IsZero() {
		return nil, fmt.Errorf("%w: message %q needs a sent time", ErrInvalid, m.SourceID)
	}
	if m.Kind == "" {
		m.Kind = model.KindMessage
	}
	if !m.Kind.Valid() {
		return nil, fmt.Errorf("%w: message kind %q", ErrInvalid, m.Kind)
	}
	for _, a := range m.Attachments {
		if !a.Kind.Valid() {
			return nil, fmt.Errorf("%w: attachment kind %q", ErrInvalid, a.Kind)
		}
	}
	raw, err := compress(m.Raw)
	if err != nil {
		return nil, err
	}
	text := clean(m.Text)
	now := s.now().Unix()
	incomingEdit := unixOrZero(m.EditedAt)

	tx, err := s.w.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	chat, err := chatID(tx, accountID, m.ChatSourceID)
	if err != nil {
		return nil, err
	}
	sender, err := senderID(tx, accountID, m.SenderSourceID)
	if err != nil {
		return nil, err
	}

	var (
		id         int64
		storedText string
		storedEdit sql.NullInt64
	)
	err = tx.QueryRow(`SELECT id, text, edited_at FROM messages WHERE chat_id = ? AND source_id = ?`, chat, m.SourceID).
		Scan(&id, &storedText, &storedEdit)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.Exec(`
			INSERT INTO messages (chat_id, source_id, sender_id, kind, sent_at, text,
			                      reply_to_source_id, forwarded_from, edited_at, captured_at, raw)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			chat, m.SourceID, sender, string(m.Kind), m.SentAt.Unix(), text,
			m.ReplyToSourceID, clean(m.ForwardedFrom), nullUnix(incomingEdit), now, raw)
		if err != nil {
			return nil, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case incomingEdit >= storedEdit.Int64 && text != storedText:
		if _, err := tx.Exec(`INSERT INTO message_versions (message_id, text, replaced_at) VALUES (?, ?, ?)`, id, storedText, now); err != nil {
			return nil, err
		}
		editedAt := incomingEdit
		if editedAt == 0 {
			editedAt = now
		}
		if _, err := tx.Exec(`UPDATE messages SET text = ?, edited_at = ?, raw = COALESCE(?, raw) WHERE id = ?`, text, editedAt, raw, id); err != nil {
			return nil, err
		}
	case incomingEdit > storedEdit.Int64:
		if _, err := tx.Exec(`UPDATE messages SET edited_at = ? WHERE id = ?`, incomingEdit, id); err != nil {
			return nil, err
		}
	}

	limit, err := mediaCap(tx)
	if err != nil {
		return nil, err
	}
	for position, a := range m.Attachments {
		status, reason := model.StatusPending, ""
		switch {
		case a.NoFetch:
			status, reason = model.StatusSkipped, a.NoFetchReason
		case a.Size > limit:
			status, reason = model.StatusSkipped, reasonOverCap
		}
		if _, err := tx.Exec(`
			INSERT INTO attachments (message_id, position, kind, file_name, mime_type, size, status, reason, ephemeral)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (message_id, position) DO NOTHING`,
			id, position, string(a.Kind), clean(a.FileName), clean(a.MimeType), a.Size, string(status), clean(reason), b2i(a.Ephemeral)); err != nil {
			return nil, err
		}
	}

	rows, err := tx.Query(`SELECT position FROM attachments WHERE message_id = ? AND status = 'pending' ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	var pending []model.AttachmentRef
	for rows.Next() {
		ref := model.AttachmentRef{ChatSourceID: m.ChatSourceID, MessageSourceID: m.SourceID}
		if err := rows.Scan(&ref.Position); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, ref)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pending, tx.Commit()
}

// senderID returns the sender's row ID, creating a nameless sender if the
// connector has not described it yet, so a message is never lost over ordering.
func senderID(tx *sql.Tx, accountID int64, sourceID string) (sql.NullInt64, error) {
	if sourceID == "" {
		return sql.NullInt64{}, nil
	}
	if _, err := tx.Exec(`INSERT INTO senders (account_id, source_id) VALUES (?, ?) ON CONFLICT (account_id, source_id) DO NOTHING`, accountID, sourceID); err != nil {
		return sql.NullInt64{}, err
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM senders WHERE account_id = ? AND source_id = ?`, accountID, sourceID).Scan(&id)
	return sql.NullInt64{Int64: id, Valid: err == nil}, err
}

// MarkDeleted records that the source deleted these messages. Nothing is
// removed. Unknown chats and messages are ignored, and a message already
// marked keeps its first deletion time.
func (s *Store) MarkDeleted(accountID int64, chatSourceID string, messageSourceIDs []string) error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	for _, id := range messageSourceIDs {
		if _, err := tx.Exec(`
			UPDATE messages SET deleted_at = ?
			WHERE deleted_at IS NULL AND source_id = ?
			  AND chat_id = (SELECT id FROM chats WHERE account_id = ? AND source_id = ?)`,
			now, id, accountID, chatSourceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LookupChats returns the source IDs of this account's chats of the given
// kinds that hold a message with this source ID. Connectors use it when the
// source reports a deletion without naming the chat.
func (s *Store) LookupChats(accountID int64, messageSourceID string, kinds []model.ChatKind) ([]string, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	args := []any{accountID, messageSourceID}
	for _, k := range kinds {
		args = append(args, string(k))
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	rows, err := s.r.Query(`
		SELECT c.source_id FROM messages m JOIN chats c ON c.id = m.chat_id
		WHERE c.account_id = ? AND m.source_id = ? AND c.kind IN (`+placeholders+`)
		ORDER BY c.source_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// StoredMessage is a message as held in the archive. Zero times mean unset.
type StoredMessage struct {
	ID              int64
	SourceID        string
	SenderSourceID  string
	Kind            model.MessageKind
	SentAt          time.Time
	Text            string
	ReplyToSourceID string
	ForwardedFrom   string
	EditedAt        time.Time
	DeletedAt       time.Time
}

func (s *Store) Message(accountID int64, chatSourceID, sourceID string) (StoredMessage, error) {
	var (
		m               StoredMessage
		kind            string
		sent            int64
		edited, deleted sql.NullInt64
	)
	err := s.r.QueryRow(`
		SELECT m.id, m.source_id, COALESCE(sn.source_id, ''), m.kind, m.sent_at, m.text,
		       m.reply_to_source_id, m.forwarded_from, m.edited_at, m.deleted_at
		FROM messages m
		JOIN chats c ON c.id = m.chat_id
		LEFT JOIN senders sn ON sn.id = m.sender_id
		WHERE c.account_id = ? AND c.source_id = ? AND m.source_id = ?`,
		accountID, chatSourceID, sourceID).
		Scan(&m.ID, &m.SourceID, &m.SenderSourceID, &kind, &sent, &m.Text, &m.ReplyToSourceID, &m.ForwardedFrom, &edited, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredMessage{}, fmt.Errorf("message %q in chat %q: %w", sourceID, chatSourceID, ErrNotFound)
	}
	if err != nil {
		return StoredMessage{}, err
	}
	m.Kind = model.MessageKind(kind)
	m.SentAt = time.Unix(sent, 0).UTC()
	m.EditedAt, m.DeletedAt = fromUnix(edited), fromUnix(deleted)
	return m, nil
}

// Version is the text a message had before an edit.
type Version struct {
	Text       string
	ReplacedAt time.Time
}

// Versions returns a message's earlier texts, oldest first.
func (s *Store) Versions(messageID int64) ([]Version, error) {
	rows, err := s.r.Query(`SELECT text, replaced_at FROM message_versions WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		var (
			v  Version
			at int64
		)
		if err := rows.Scan(&v.Text, &at); err != nil {
			return nil, err
		}
		v.ReplacedAt = time.Unix(at, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

type StoredAttachment struct {
	ID        int64
	Position  int
	Kind      model.AttachmentKind
	FileName  string
	MimeType  string
	Size      int64
	SHA256    string
	Status    model.AttachmentStatus
	Reason    string
	Ephemeral bool
	Requested bool
}

func (s *Store) Attachments(messageID int64) ([]StoredAttachment, error) {
	rows, err := s.r.Query(`
		SELECT id, position, kind, file_name, mime_type, size, sha256, status, reason, ephemeral, requested
		FROM attachments WHERE message_id = ? ORDER BY position`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredAttachment
	for rows.Next() {
		var (
			a                    StoredAttachment
			kind, status         string
			ephemeral, requested int
		)
		if err := rows.Scan(&a.ID, &a.Position, &kind, &a.FileName, &a.MimeType, &a.Size, &a.SHA256, &status, &a.Reason, &ephemeral, &requested); err != nil {
			return nil, err
		}
		a.Kind, a.Status = model.AttachmentKind(kind), model.AttachmentStatus(status)
		a.Ephemeral, a.Requested = ephemeral != 0, requested != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// Raw returns the source's original payload for a message, or nil if none was stored.
func (s *Store) Raw(messageID int64) ([]byte, error) {
	var packed []byte
	err := s.r.QueryRow(`SELECT raw FROM messages WHERE id = ?`, messageID).Scan(&packed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("message %d: %w", messageID, ErrNotFound)
	}
	if err != nil || packed == nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func (s *Store) MessageCount(accountID int64, chatSourceID string) (int, error) {
	var n int
	err := s.r.QueryRow(`
		SELECT COUNT(*) FROM messages m JOIN chats c ON c.id = m.chat_id
		WHERE c.account_id = ? AND c.source_id = ?`, accountID, chatSourceID).Scan(&n)
	return n, err
}

// compress gzips a payload. An empty payload yields an untyped nil, which the
// driver stores as NULL (a nil []byte is not guaranteed to be).
func compress(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
