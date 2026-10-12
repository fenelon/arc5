package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is one signed-in browser.
type Session struct {
	CSRF      string
	ExpiresAt time.Time
}

func (s *Store) CreateSession(tokenHash, csrf string, expiresAt time.Time) error {
	_, err := s.w.Exec(`INSERT INTO web_sessions (token_hash, csrf, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, csrf, s.now().Unix(), expiresAt.Unix())
	return err
}

// Session returns the session for a token hash, or ErrNotFound if there is
// none or it has expired.
func (s *Store) Session(tokenHash string) (Session, error) {
	var (
		sess    Session
		expires int64
	)
	err := s.r.QueryRow(`SELECT csrf, expires_at FROM web_sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, s.now().Unix()).Scan(&sess.CSRF, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session: %w", ErrNotFound)
	}
	if err != nil {
		return Session{}, err
	}
	sess.ExpiresAt = time.Unix(expires, 0).UTC()
	return sess, nil
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.w.Exec(`DELETE FROM web_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteAllSessions signs every browser out. It also drops expired rows.
func (s *Store) DeleteAllSessions() error {
	_, err := s.w.Exec(`DELETE FROM web_sessions`)
	return err
}
