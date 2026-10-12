package archive

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Analyzer turns text into the two columns of the search index. Package
// search implements it; the archive only stores what it returns, so the
// archive stays the only writer.
type Analyzer interface {
	// Index returns space-separated terms for the exact and stem columns.
	Index(text string) (exact, stem string)
	// Signature identifies the analysis. An index built under another signature is stale.
	Signature() string
}

// ErrNoAnalyzer is returned by Reindex when SetAnalyzer has not been called.
var ErrNoAnalyzer = errors.New("no search analyzer is set")

const indexSignatureKey = "search_index_signature"

// reindexBatch is how many messages Reindex indexes per transaction, so that
// ingestion is never blocked for long. It is a variable so tests can shrink it.
var reindexBatch = 500

// SetAnalyzer sets how messages are indexed from now on. Until it is called,
// messages are stored without being indexed.
func (s *Store) SetAnalyzer(a Analyzer) { s.analyzer.Store(&a) }

func (s *Store) currentAnalyzer() Analyzer {
	if p := s.analyzer.Load(); p != nil {
		return *p
	}
	return nil
}

// Reader returns the read-only connection pool, for packages that query the
// archive. Writes through it fail.
func (s *Store) Reader() *sql.DB { return s.r }

// IndexCurrent reports whether the whole index was built by the analyzer now set.
func (s *Store) IndexCurrent() (bool, error) {
	a := s.currentAnalyzer()
	if a == nil {
		return false, nil
	}
	built, _, err := s.Setting(indexSignatureKey)
	return built == a.Signature(), err
}

// indexMessage writes one message's index row from its current text and
// attachment file names, replacing any earlier row.
func indexMessage(tx *sql.Tx, a Analyzer, messageID int64) error {
	var doc strings.Builder
	var text string
	if err := tx.QueryRow(`SELECT text FROM messages WHERE id = ?`, messageID).Scan(&text); err != nil {
		return err
	}
	doc.WriteString(text)

	rows, err := tx.Query(`SELECT file_name FROM attachments WHERE message_id = ? AND file_name != '' ORDER BY position`, messageID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		doc.WriteByte('\n')
		doc.WriteString(name)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	exact, stem := a.Index(doc.String())
	_, err = tx.Exec(`INSERT OR REPLACE INTO messages_fts (rowid, exact, stem) VALUES (?, ?, ?)`, messageID, exact, stem)
	return err
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Reindex rebuilds the search index from the archive with the analyzer now
// set. It works in small transactions, so ingestion continues meanwhile and
// messages that arrive during the rebuild are indexed as usual. The index is
// marked stale first and current only at the end, so an interrupted rebuild
// is detected by IndexCurrent and can simply be run again.
func (s *Store) Reindex(ctx context.Context, progress func(done, total int)) error {
	a := s.currentAnalyzer()
	if a == nil {
		return ErrNoAnalyzer
	}
	if progress == nil {
		progress = func(int, int) {}
	}

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, '') ON CONFLICT (key) DO UPDATE SET value = ''`, indexSignatureKey); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO messages_fts (messages_fts) VALUES ('delete-all')`)
		return err
	})
	if err != nil {
		return err
	}

	var total int
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&total); err != nil {
		return err
	}
	done, last := 0, int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var ids []int64
		err := s.inTx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.Query(`SELECT id FROM messages WHERE id > ? ORDER BY id LIMIT ?`, last, reindexBatch)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
			for _, id := range ids {
				if err := indexMessage(tx, a, id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if len(ids) == 0 {
			break
		}
		last = ids[len(ids)-1]
		done += len(ids)
		progress(done, total)
	}
	return s.SetSetting(indexSignatureKey, a.Signature())
}
