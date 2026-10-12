package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ChatSummary is one row of a conversation list: a chat and its last message.
type ChatSummary struct {
	ID           int64
	AccountID    int64
	Kind         model.ChatKind
	Title        string
	LastAt       time.Time
	LastText     string
	LastSender   string
	LastOutgoing bool
	LastKind     model.MessageKind
	LastFiles    int // attachments on the last message
}

// ChatList returns the chats of the given accounts that hold messages, most
// recently active first.
func (s *Store) ChatList(accountIDs []int64, limit, offset int) ([]ChatSummary, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(accountIDs)+2)
	for _, id := range accountIDs {
		args = append(args, id)
	}
	args = append(args, limit, offset)
	rows, err := s.r.Query(`
		SELECT c.id, c.account_id, c.kind, c.title, m.sent_at, m.text, m.kind, m.outgoing,
		       COALESCE(sn.display_name, ''),
		       (SELECT COUNT(*) FROM attachments a WHERE a.message_id = m.id)
		FROM chats c
		JOIN messages m ON m.id = (
			SELECT m2.id FROM messages m2 WHERE m2.chat_id = c.id
			ORDER BY m2.sent_at DESC, m2.id DESC LIMIT 1)
		LEFT JOIN senders sn ON sn.id = m.sender_id
		WHERE c.account_id IN (`+placeholders(len(accountIDs))+`)
		ORDER BY m.sent_at DESC, m.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var (
			c              ChatSummary
			kind, lastKind string
			sent           int64
			outgoing       int
		)
		if err := rows.Scan(&c.ID, &c.AccountID, &kind, &c.Title, &sent, &c.LastText, &lastKind, &outgoing, &c.LastSender, &c.LastFiles); err != nil {
			return nil, err
		}
		c.Kind, c.LastKind = model.ChatKind(kind), model.MessageKind(lastKind)
		c.LastAt, c.LastOutgoing = time.Unix(sent, 0).UTC(), outgoing != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChatInfo describes one chat.
type ChatInfo struct {
	ID        int64
	AccountID int64
	SourceID  string
	Kind      model.ChatKind
	Title     string
	Archived  bool
	Messages  int
	FirstAt   time.Time // zero when the chat is empty
}

func (s *Store) Chat(id int64) (ChatInfo, error) {
	var (
		c        ChatInfo
		kind     string
		archived int
		first    sql.NullInt64
	)
	err := s.r.QueryRow(`
		SELECT c.id, c.account_id, c.source_id, c.kind, c.title, c.archived,
		       (SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id),
		       (SELECT MIN(m.sent_at) FROM messages m WHERE m.chat_id = c.id)
		FROM chats c WHERE c.id = ?`, id).
		Scan(&c.ID, &c.AccountID, &c.SourceID, &kind, &c.Title, &archived, &c.Messages, &first)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatInfo{}, fmt.Errorf("chat %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return ChatInfo{}, err
	}
	c.Kind, c.Archived, c.FirstAt = model.ChatKind(kind), archived != 0, fromUnix(first)
	return c, nil
}

// MessageView is a message as a conversation page shows it.
type MessageView struct {
	ID              int64
	SourceID        string
	Kind            model.MessageKind
	Outgoing        bool
	SenderName      string
	SentAt          time.Time
	Text            string
	EditedAt        time.Time
	DeletedAt       time.Time
	ForwardedFrom   string
	ReplyToSourceID string
	Versions        int // earlier texts kept for this message
	Attachments     []StoredAttachment
}

// Anchor says which part of a conversation to read. At most one field is set;
// with none set, the newest messages are read.
type Anchor struct {
	Before int64     // messages just older than this message ID
	After  int64     // messages just newer than this message ID
	Around int64     // this message with its neighbours
	From   time.Time // messages from this moment on
}

// Window is a run of consecutive messages, oldest first.
type Window struct {
	Messages []MessageView
	HasOlder bool
	HasNewer bool
}

const messageViewSelect = `
	SELECT m.id, m.source_id, m.kind, m.outgoing, COALESCE(sn.display_name, ''), m.sent_at, m.text,
	       m.edited_at, m.deleted_at, m.forwarded_from, m.reply_to_source_id,
	       (SELECT COUNT(*) FROM message_versions v WHERE v.message_id = m.id)
	FROM messages m LEFT JOIN senders sn ON sn.id = m.sender_id
	WHERE m.chat_id = ?`

// pivot is the position of an anchor message, if it belongs to the chat being
// read. A message of another chat, or none, compares as NULL and matches nothing.
const pivot = `(SELECT p.sent_at, p.id FROM messages p WHERE p.id = ? AND p.chat_id = m.chat_id)`

const (
	newestFirst = ` ORDER BY m.sent_at DESC, m.id DESC LIMIT ?`
	oldestFirst = ` ORDER BY m.sent_at ASC, m.id ASC LIMIT ?`
)

func (s *Store) views(query string, args ...any) ([]MessageView, error) {
	rows, err := s.r.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageView
	for rows.Next() {
		var (
			v               MessageView
			kind            string
			outgoing        int
			sent            int64
			edited, deleted sql.NullInt64
		)
		if err := rows.Scan(&v.ID, &v.SourceID, &kind, &outgoing, &v.SenderName, &sent, &v.Text,
			&edited, &deleted, &v.ForwardedFrom, &v.ReplyToSourceID, &v.Versions); err != nil {
			return nil, err
		}
		v.Kind, v.Outgoing = model.MessageKind(kind), outgoing != 0
		v.SentAt, v.EditedAt, v.DeletedAt = time.Unix(sent, 0).UTC(), fromUnix(edited), fromUnix(deleted)
		out = append(out, v)
	}
	return out, rows.Err()
}

// trim cuts v to n messages and reports whether there were more.
func trim(v *[]MessageView, n int) bool {
	if len(*v) > n {
		*v = (*v)[:n]
		return true
	}
	return false
}

// ChatMessages reads up to limit consecutive messages of a chat.
func (s *Store) ChatMessages(chatID int64, a Anchor, limit int) (Window, error) {
	limit = max(limit, 1)
	var (
		w   Window
		err error
	)
	switch {
	case a.Before != 0:
		w.Messages, err = s.views(messageViewSelect+` AND (m.sent_at, m.id) < `+pivot+newestFirst, chatID, a.Before, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&w.Messages, limit)
		slices.Reverse(w.Messages)
		w.HasNewer = len(w.Messages) > 0 // the anchor itself is newer

	case a.After != 0:
		w.Messages, err = s.views(messageViewSelect+` AND (m.sent_at, m.id) > `+pivot+oldestFirst, chatID, a.After, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasNewer = trim(&w.Messages, limit)
		w.HasOlder = len(w.Messages) > 0 // the anchor itself is older

	case a.Around != 0:
		older := limit / 2 // messages shown before the anchor
		before, err := s.views(messageViewSelect+` AND (m.sent_at, m.id) <= `+pivot+newestFirst, chatID, a.Around, older+2)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&before, older+1)
		slices.Reverse(before)
		newer := max(limit-older-1, 0)
		after, err := s.views(messageViewSelect+` AND (m.sent_at, m.id) > `+pivot+oldestFirst, chatID, a.Around, newer+1)
		if err != nil {
			return Window{}, err
		}
		w.HasNewer = trim(&after, newer)
		w.Messages = append(before, after...)

	case !a.From.IsZero():
		w.Messages, err = s.views(messageViewSelect+` AND m.sent_at >= ?`+oldestFirst, chatID, a.From.Unix(), limit+1)
		if err != nil {
			return Window{}, err
		}
		if len(w.Messages) == 0 {
			return s.ChatMessages(chatID, Anchor{}, limit) // nothing that late: show the end
		}
		w.HasNewer = trim(&w.Messages, limit)
		if err := s.r.QueryRow(`SELECT EXISTS (SELECT 1 FROM messages WHERE chat_id = ? AND sent_at < ?)`,
			chatID, a.From.Unix()).Scan(&w.HasOlder); err != nil {
			return Window{}, err
		}

	default:
		w.Messages, err = s.views(messageViewSelect+newestFirst, chatID, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&w.Messages, limit)
		slices.Reverse(w.Messages)
	}
	return w, s.attach(w.Messages)
}

// attach fills in the attachments of each message.
func (s *Store) attach(messages []MessageView) error {
	if len(messages) == 0 {
		return nil
	}
	at := make(map[int64]int, len(messages))
	ids := make([]any, len(messages))
	for i, m := range messages {
		at[m.ID], ids[i] = i, m.ID
	}
	rows, err := s.r.Query(`
		SELECT message_id, id, position, kind, file_name, mime_type, size, sha256, status, reason, ephemeral, requested
		FROM attachments WHERE message_id IN (`+placeholders(len(ids))+`) ORDER BY message_id, position`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			messageID            int64
			a                    StoredAttachment
			kind, status         string
			ephemeral, requested int
		)
		if err := rows.Scan(&messageID, &a.ID, &a.Position, &kind, &a.FileName, &a.MimeType, &a.Size, &a.SHA256,
			&status, &a.Reason, &ephemeral, &requested); err != nil {
			return err
		}
		a.Kind, a.Status = model.AttachmentKind(kind), model.AttachmentStatus(status)
		a.Ephemeral, a.Requested = ephemeral != 0, requested != 0
		i := at[messageID]
		messages[i].Attachments = append(messages[i].Attachments, a)
	}
	return rows.Err()
}

// Quarter is how many messages a chat holds in one quarter of a year.
type Quarter struct {
	Year     int
	Quarter  int // 1 to 4
	Messages int
}

// ChatQuarters counts a chat's messages by quarter (UTC), newest first.
// Quarters with no messages are not returned.
func (s *Store) ChatQuarters(chatID int64) ([]Quarter, error) {
	rows, err := s.r.Query(`
		SELECT CAST(strftime('%Y', sent_at, 'unixepoch') AS INTEGER) AS y,
		       (CAST(strftime('%m', sent_at, 'unixepoch') AS INTEGER) + 2) / 3 AS q,
		       COUNT(*)
		FROM messages WHERE chat_id = ?
		GROUP BY y, q ORDER BY y DESC, q DESC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Quarter
	for rows.Next() {
		var q Quarter
		if err := rows.Scan(&q.Year, &q.Quarter, &q.Messages); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// AttachmentFile is an attachment and, once stored, where its file is.
type AttachmentFile struct {
	StoredAttachment
	MessageID int64
	Path      string
}

func (s *Store) AttachmentFile(id int64) (AttachmentFile, error) {
	var (
		f                    AttachmentFile
		kind, status         string
		ephemeral, requested int
	)
	err := s.r.QueryRow(`
		SELECT id, message_id, position, kind, file_name, mime_type, size, sha256, status, reason, ephemeral, requested
		FROM attachments WHERE id = ?`, id).
		Scan(&f.ID, &f.MessageID, &f.Position, &kind, &f.FileName, &f.MimeType, &f.Size, &f.SHA256,
			&status, &f.Reason, &ephemeral, &requested)
	if errors.Is(err, sql.ErrNoRows) {
		return AttachmentFile{}, fmt.Errorf("attachment %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return AttachmentFile{}, err
	}
	f.Kind, f.Status = model.AttachmentKind(kind), model.AttachmentStatus(status)
	f.Ephemeral, f.Requested = ephemeral != 0, requested != 0
	if f.Status == model.StatusStored && len(f.SHA256) == 64 {
		f.Path = s.MediaPath(f.SHA256)
	}
	return f, nil
}
