package search

import (
	"context"
	"database/sql"
	"slices"
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
	AccountIDs     []int64 // the accounts to search; nil means every account
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
	ChatKind   model.ChatKind
	SenderName string
	Outgoing   bool // sent by the account owner
	Kind       model.MessageKind
	SentAt     time.Time
	Edited     bool
	Deleted    bool
	Snippet    []Fragment
	// Files holds the attachment file names the query matched, highlighted.
	// A message found only by a file name has nothing to highlight in Snippet.
	Files [][]Fragment
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
	if len(q.AccountIDs) > 0 {
		add(`c.account_id IN (`+placeholders(len(q.AccountIDs))+`)`, ids(q.AccountIDs)...)
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
		SELECT m.id, c.account_id, c.id, c.title, c.kind, COALESCE(sn.display_name, ''), m.outgoing, m.kind, m.sent_at,
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
			h        Hit
			chatKind string
			kind     string
			sent     int64
			text     string
		)
		if err := rows.Scan(&h.MessageID, &h.AccountID, &h.ChatID, &h.ChatTitle, &chatKind, &h.SenderName, &h.Outgoing, &kind, &sent, &h.Edited, &h.Deleted, &text); err != nil {
			return Page{}, err
		}
		if len(page.Hits) == limit {
			page.More = true
			break
		}
		h.Kind, h.ChatKind = model.MessageKind(kind), model.ChatKind(chatKind)
		h.SentAt = time.Unix(sent, 0).UTC()
		h.Snippet = s.analyzer.snippet(text, p)
		page.Hits = append(page.Hits, h)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if err := rows.Close(); err != nil {
		return Page{}, err
	}
	if p.match != "" {
		if err := s.addFiles(ctx, page.Hits, p); err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

// addFiles fills in, for each hit, the attachment file names the query
// matched. The index covers file names, so without this a message found by
// one would come back with nothing to show for it.
func (s *Searcher) addFiles(ctx context.Context, hits []Hit, p parsed) error {
	if len(hits) == 0 {
		return nil
	}
	byID := make(map[int64]*Hit, len(hits))
	ids := make([]any, len(hits))
	for i := range hits {
		byID[hits[i].MessageID] = &hits[i]
		ids[i] = hits[i].MessageID
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, file_name FROM attachments
		WHERE file_name != '' AND message_id IN (`+placeholders(len(ids))+`)
		ORDER BY message_id, position`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id   int64
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		fragments := s.analyzer.snippet(name, p)
		if slices.ContainsFunc(fragments, func(f Fragment) bool { return f.Match }) {
			byID[id].Files = append(byID[id].Files, fragments)
		}
	}
	return rows.Err()
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// ids turns account IDs into query arguments.
func ids(accountIDs []int64) []any {
	out := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		out[i] = id
	}
	return out
}
