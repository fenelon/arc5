package search

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

type Sort int

const (
	ByRelevance Sort = iota // best match first; exact words above word forms
	ByNewest
)

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Query is a search request. IDs are database IDs; zero means any.
type Query struct {
	Text           string
	AccountID      int64
	ChatID         int64
	SenderID       int64
	From           time.Time // inclusive; zero means unbounded
	To             time.Time // exclusive; zero means unbounded
	HasAttachment  bool
	DeletedOnly    bool // only messages deleted at the source
	IncludeService bool // also joins, calls and other service messages
	Sort           Sort
	Limit          int // zero means DefaultLimit; capped at MaxLimit
	Offset         int
}

// Hit is one found message, with what a result row shows.
type Hit struct {
	MessageID  int64
	AccountID  int64
	ChatID     int64
	ChatTitle  string
	SenderName string
	Kind       model.MessageKind
	SentAt     time.Time
	Edited     bool
	Deleted    bool
	Snippet    []Fragment
}

type Page struct {
	Hits []Hit
	More bool // further hits exist past this page
}

// Searcher runs queries against the archive's index.
type Searcher struct {
	db       *sql.DB
	analyzer *Analyzer
}

// New returns a Searcher that reads through db, normally archive.Store.Reader.
// The analyzer must be the one the index was built with.
func New(db *sql.DB, a *Analyzer) *Searcher {
	return &Searcher{db: db, analyzer: a}
}

// Search finds messages. With empty Text it lists messages by the filters
// alone, newest first. Text that holds nothing searchable finds nothing.
func (s *Searcher) Search(ctx context.Context, q Query) (Page, error) {
	p := s.analyzer.parse(q.Text)
	if p.match == "" && strings.TrimSpace(q.Text) != "" {
		return Page{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	offset := max(q.Offset, 0)

	from := `messages m`
	order := `m.sent_at DESC, m.id DESC`
	var (
		where []string
		args  []any
	)
	add := func(condition string, values ...any) {
		where = append(where, condition)
		args = append(args, values...)
	}
	if p.match != "" {
		from = `messages_fts JOIN messages m ON m.id = messages_fts.rowid`
		add(`messages_fts MATCH ?`, p.match)
		if q.Sort == ByRelevance {
			order = `bm25(messages_fts, 2.0, 1.0), m.sent_at DESC, m.id DESC`
		}
	}
	if q.AccountID != 0 {
		add(`c.account_id = ?`, q.AccountID)
	}
	if q.ChatID != 0 {
		add(`m.chat_id = ?`, q.ChatID)
	}
	if q.SenderID != 0 {
		add(`m.sender_id = ?`, q.SenderID)
	}
	if !q.From.IsZero() {
		add(`m.sent_at >= ?`, q.From.Unix())
	}
	if !q.To.IsZero() {
		add(`m.sent_at < ?`, q.To.Unix())
	}
	if q.HasAttachment {
		add(`EXISTS (SELECT 1 FROM attachments a WHERE a.message_id = m.id)`)
	}
	if q.DeletedOnly {
		add(`m.deleted_at IS NOT NULL`)
	}
	if !q.IncludeService {
		add(`m.kind = 'message'`)
	}

	query := `
		SELECT m.id, c.account_id, c.id, c.title, COALESCE(sn.display_name, ''), m.kind, m.sent_at,
		       m.edited_at IS NOT NULL, m.deleted_at IS NOT NULL, m.text
		FROM ` + from + `
		JOIN chats c ON c.id = m.chat_id
		LEFT JOIN senders sn ON sn.id = m.sender_id`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset) // one extra row tells us whether more exist

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var page Page
	for rows.Next() {
		var (
			h    Hit
			kind string
			sent int64
			text string
		)
		if err := rows.Scan(&h.MessageID, &h.AccountID, &h.ChatID, &h.ChatTitle, &h.SenderName, &kind, &sent, &h.Edited, &h.Deleted, &text); err != nil {
			return Page{}, err
		}
		if len(page.Hits) == limit {
			page.More = true
			break
		}
		h.Kind = model.MessageKind(kind)
		h.SentAt = time.Unix(sent, 0).UTC()
		h.Snippet = s.analyzer.snippet(text, p)
		page.Hits = append(page.Hits, h)
	}
	return page, rows.Err()
}
