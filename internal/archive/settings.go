package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// DefaultMediaCap is the largest attachment downloaded automatically.
const DefaultMediaCap = 50 << 20

const mediaCapKey = "media_cap_bytes"

func (s *Store) Setting(key string) (string, bool, error) {
	var v string
	err := s.r.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.w.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func mediaCap(q queryer) (int64, error) {
	var v string
	err := q.QueryRow(`SELECT value FROM settings WHERE key = ?`, mediaCapKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultMediaCap, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s setting: %w", mediaCapKey, err)
	}
	return n, nil
}

func (s *Store) MediaCap() (int64, error) { return mediaCap(s.r) }

func (s *Store) SetMediaCap(n int64) error {
	if n < 0 {
		return fmt.Errorf("%w: media cap cannot be negative", ErrInvalid)
	}
	return s.SetSetting(mediaCapKey, strconv.FormatInt(n, 10))
}

// StateGet returns a connector's stored value, or nil if the key is unset.
func (s *Store) StateGet(accountID int64, key string) ([]byte, error) {
	var v []byte
	err := s.r.QueryRow(`SELECT value FROM connector_state WHERE account_id = ? AND key = ?`, accountID, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func (s *Store) StateSet(accountID int64, key string, value []byte) error {
	if value == nil {
		value = []byte{}
	}
	_, err := s.w.Exec(`
		INSERT INTO connector_state (account_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT (account_id, key) DO UPDATE SET value = excluded.value`, accountID, key, value)
	return err
}
