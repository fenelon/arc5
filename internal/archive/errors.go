package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrInvalid           = errors.New("invalid input")
	ErrUnknownChat       = errors.New("unknown chat")
	ErrUnknownAttachment = errors.New("unknown attachment")
	// ErrMediaRead marks a PutMedia failure caused by the reader, not by storage.
	ErrMediaRead = errors.New("media read failed")
)

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// clean makes source text safe to store and render.
func clean(s string) string { return strings.ToValidUTF8(s, "�") }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func nullUnix(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func fromUnix(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.Unix(v.Int64, 0).UTC()
}

func chatID(q queryer, accountID int64, sourceID string) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM chats WHERE account_id = ? AND source_id = ?`, accountID, sourceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %q", ErrUnknownChat, sourceID)
	}
	return id, err
}
