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
	LastFiles    int    // attachments on the last message
	AvatarSHA    string // the chat's picture in the media store, or ""
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
		       (SELECT COUNT(*) FROM attachments a WHERE a.message_id = m.id AND a.position >= 0), c.avatar_sha256
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
		if err := rows.Scan(&c.ID, &c.AccountID, &kind, &c.Title, &sent, &c.LastText, &lastKind, &outgoing, &c.LastSender, &c.LastFiles, &c.AvatarSHA); err != nil {
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
	AvatarSHA string    // the chat's picture in the media store, or ""
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
		       (SELECT MIN(m.sent_at) FROM messages m WHERE m.chat_id = c.id), c.avatar_sha256
		FROM chats c WHERE c.id = ?`, id).
		Scan(&c.ID, &c.AccountID, &c.SourceID, &kind, &c.Title, &archived, &c.Messages, &first, &c.AvatarSHA)
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
	Replaced        []StoredAttachment // attachments edits replaced, oldest first
	SenderID        int64              // zero when the source named no sender
	SenderAvatarSHA string             // the sender's picture in the media store, or ""
	Reply           *Quote             // what it replies to, when that is archived in the same chat
}

// Quote is the message a reply answers, as the reply shows it.
type Quote struct {
	ID            int64
	SenderName    string
	ForwardedFrom string // who wrote it, when it was itself a forward
	Outgoing      bool
	Text          string
	File          string               // its first attachment's name, or ""
	FileKind      model.AttachmentKind // and kind
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
	       (SELECT COUNT(*) FROM message_versions v WHERE v.message_id = m.id),
	       COALESCE(sn.id, 0), COALESCE(sn.avatar_sha256, '')
	FROM messages m LEFT JOIN senders sn ON sn.id = m.sender_id
	WHERE m.chat_id = ?`

// A conversation is ordered by when each message was sent, then, within a
// second, by its source ID (numbers compare as numbers: shorter first), then
// by when it was stored. A batch of forwards shares a second, and the
// catch-up stores newest first, so the database ID alone would reverse it.
const position = `(m.sent_at, length(m.source_id), m.source_id, m.id)`

// pivot is the position of an anchor message, if it belongs to the chat being
// read. A message of another chat, or none, compares as NULL and matches
// nothing. Each use also bounds sent_at, so the chat's index narrows the scan.
func pivot(op string) string {
	bound := op[:1] + "=" // "<" and "<=" bound sent_at by "<="; ">" by ">="
	return ` AND m.sent_at ` + bound + ` (SELECT p.sent_at FROM messages p WHERE p.id = ? AND p.chat_id = m.chat_id)` +
		` AND ` + position + ` ` + op + ` (SELECT p.sent_at, length(p.source_id), p.source_id, p.id FROM messages p WHERE p.id = ? AND p.chat_id = m.chat_id)`
}

const (
	newestFirst = ` ORDER BY m.sent_at DESC, length(m.source_id) DESC, m.source_id DESC, m.id DESC LIMIT ?`
	oldestFirst = ` ORDER BY m.sent_at ASC, length(m.source_id) ASC, m.source_id ASC, m.id ASC LIMIT ?`
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
			&edited, &deleted, &v.ForwardedFrom, &v.ReplyToSourceID, &v.Versions, &v.SenderID, &v.SenderAvatarSHA); err != nil {
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
		w.Messages, err = s.views(messageViewSelect+pivot("<")+newestFirst, chatID, a.Before, a.Before, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&w.Messages, limit)
		slices.Reverse(w.Messages)
		w.HasNewer = len(w.Messages) > 0 // the anchor itself is newer

	case a.After != 0:
		w.Messages, err = s.views(messageViewSelect+pivot(">")+oldestFirst, chatID, a.After, a.After, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasNewer = trim(&w.Messages, limit)
		w.HasOlder = len(w.Messages) > 0 // the anchor itself is older

	case a.Around != 0:
		older := limit / 2 // messages shown before the anchor
		before, err := s.views(messageViewSelect+pivot("<=")+newestFirst, chatID, a.Around, a.Around, older+2)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&before, older+1)
		slices.Reverse(before)
		newer := max(limit-older-1, 0)
		after, err := s.views(messageViewSelect+pivot(">")+oldestFirst, chatID, a.Around, a.Around, newer+1)
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
	if err := s.attach(w.Messages); err != nil {
		return Window{}, err
	}
	return w, s.quote(chatID, w.Messages)
}

// quote fills in what each reply answers, from the same chat.
func (s *Store) quote(chatID int64, messages []MessageView) error {
	var wanted []any
	for _, m := range messages {
		if m.ReplyToSourceID != "" {
			wanted = append(wanted, m.ReplyToSourceID)
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	rows, err := s.r.Query(`
		SELECT m.id, m.source_id, m.outgoing, COALESCE(sn.display_name, ''), m.forwarded_from, m.text,
		       COALESCE(a.file_name, ''), COALESCE(a.kind, '')
		FROM messages m LEFT JOIN senders sn ON sn.id = m.sender_id
		LEFT JOIN attachments a ON a.id = (SELECT f.id FROM attachments f WHERE f.message_id = m.id AND f.position >= 0 ORDER BY f.position LIMIT 1)
		WHERE m.chat_id = ? AND m.source_id IN (`+placeholders(len(wanted))+`)`, append([]any{chatID}, wanted...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := map[string]*Quote{}
	for rows.Next() {
		var (
			q        Quote
			source   string
			outgoing int
			kind     string
		)
		if err := rows.Scan(&q.ID, &source, &outgoing, &q.SenderName, &q.ForwardedFrom, &q.Text, &q.File, &kind); err != nil {
			return err
		}
		q.Outgoing, q.FileKind = outgoing != 0, model.AttachmentKind(kind)
		found[source] = &q
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range messages {
		messages[i].Reply = found[messages[i].ReplyToSourceID]
	}
	return nil
}

// attach fills in the attachments of each message, current and replaced.
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
		SELECT message_id, `+attachmentColumns+`
		FROM attachments WHERE message_id IN (`+placeholders(len(ids))+`) ORDER BY message_id, replaced_at IS NOT NULL, replaced_at, position`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var messageID int64
		a, err := scanAttachment(rows, &messageID)
		if err != nil {
			return err
		}
		m := &messages[at[messageID]]
		if a.Replaced.IsZero() {
			m.Attachments = append(m.Attachments, a)
		} else {
			m.Replaced = append(m.Replaced, a)
		}
	}
	return rows.Err()
}

// Month is how many messages a chat holds in one month.
type Month struct {
	Year     int
	Month    time.Month
	Messages int
}

// ChatMonths counts a chat's messages by month in loc, the time zone the
// reader sees dates in, newest first. Months with no messages are left out.
func (s *Store) ChatMonths(chatID int64, loc *time.Location) ([]Month, error) {
	var first, last sql.NullInt64
	if err := s.r.QueryRow(`SELECT MIN(sent_at), MAX(sent_at) FROM messages WHERE chat_id = ?`, chatID).
		Scan(&first, &last); err != nil || !first.Valid {
		return nil, err
	}
	monthOf := func(unix int64) time.Time {
		t := time.Unix(unix, 0).In(loc)
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	}
	oldest := monthOf(first.Int64)
	var out []Month
	for start := monthOf(last.Int64); !start.Before(oldest); start = start.AddDate(0, -1, 0) {
		counts, err := s.ChatCounts(chatID, []time.Time{start, start.AddDate(0, 1, 0)})
		if err != nil {
			return nil, err
		}
		if counts[0] > 0 {
			out = append(out, Month{Year: start.Year(), Month: start.Month(), Messages: counts[0]})
		}
	}
	return out, nil
}

// ChatCounts counts a chat's messages between consecutive moments: from
// bounds[i], inclusive, to bounds[i+1].
func (s *Store) ChatCounts(chatID int64, bounds []time.Time) ([]int, error) {
	var out []int
	for i := 0; i+1 < len(bounds); i++ {
		var n int
		if err := s.r.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND sent_at >= ? AND sent_at < ?`,
			chatID, bounds[i].Unix(), bounds[i+1].Unix()).Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// AttachmentFile is an attachment and, once stored, where its file is.
type AttachmentFile struct {
	StoredAttachment
	MessageID int64
	Path      string
}

func (s *Store) AttachmentFile(id int64) (AttachmentFile, error) {
	var f AttachmentFile
	a, err := scanAttachment(s.r.QueryRow(`SELECT message_id, `+attachmentColumns+` FROM attachments WHERE id = ?`, id), &f.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return AttachmentFile{}, fmt.Errorf("attachment %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return AttachmentFile{}, err
	}
	f.StoredAttachment = a
	if f.Status == model.StatusStored && len(f.SHA256) == 64 {
		f.Path = s.MediaPath(f.SHA256)
	}
	return f, nil
}
