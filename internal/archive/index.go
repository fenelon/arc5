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

// Three settings describe the index. The signature is the analyzer the whole
// index was built with, empty while a rebuild is under way. The target is the
// analyzer the current or last rebuild used. Dirty is set when a writer using
// any other analyzer, or none, stores searchable content: that row is not
// indexed the way the rest of the index is, so the index needs rebuilding.
const (
	indexSignatureKey = "search_index_signature"
	indexTargetKey    = "search_index_target"
	indexDirtyKey     = "search_index_dirty"
)

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

// IndexCurrent reports whether the whole index was built by the analyzer now
// set and nothing has been written to it any other way since, by this process
// or another one sharing the data folder.
func (s *Store) IndexCurrent() (bool, error) {
	a := s.currentAnalyzer()
	if a == nil {
		return false, nil
	}
	built, _, err := s.Setting(indexSignatureKey)
	if err != nil || built != a.Signature() {
		return false, err
	}
	dirty, _, err := s.Setting(indexDirtyKey)
	return dirty == "", err
}

// noteIndexWrite is called in the transaction of a write that changed
// searchable content. If the writer is not using the analyzer the index is
// being, or was, built with, the index no longer matches the messages; that is
// recorded so IndexCurrent reports it and the index gets rebuilt.
func noteIndexWrite(tx *sql.Tx, a Analyzer) error {
	rows, err := tx.Query(`SELECT key, value FROM settings WHERE key IN (?, ?)`, indexTargetKey, indexDirtyKey)
	if err != nil {
		return err
	}
	var target, dirty string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		if key == indexTargetKey {
			target = value
		} else {
			dirty = value
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if dirty != "" || (a != nil && a.Signature() == target) {
		return nil
	}
	return setInTx(tx, indexDirtyKey, "1")
}

func setInTx(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// indexMessage writes one message's index row from its current text and
// attachment file names, replacing any earlier row.
func indexMessage(tx *sql.Tx, a Analyzer, messageID int64) error {
	var doc strings.Builder
	var subject, text string
	if err := tx.QueryRow(`SELECT subject, text FROM messages WHERE id = ?`, messageID).Scan(&subject, &text); err != nil {
		return err
	}
	if subject != "" {
		doc.WriteString(subject)
		doc.WriteByte('\n')
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
// is detected by IndexCurrent and can simply be run again. A writer in another
// process that indexes differently during or after the rebuild marks the
// index dirty, which IndexCurrent also reports.
func (s *Store) Reindex(ctx context.Context, progress func(done, total int)) error {
	a := s.currentAnalyzer()
	if a == nil {
		return ErrNoAnalyzer
	}
	if progress == nil {
		progress = func(int, int) {}
	}

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for key, value := range map[string]string{indexSignatureKey: "", indexTargetKey: a.Signature(), indexDirtyKey: ""} {
			if err := setInTx(tx, key, value); err != nil {
				return err
			}
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
