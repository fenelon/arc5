package archive

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Account is one configured instance of a connector.
type Account struct {
	ID        int64
	Connector string
	Label     string
	Settings  map[string]string
	Enabled   bool
	CreatedAt time.Time
}

const accountColumns = `id, connector, label, settings, enabled, created_at`

func encodeSettings(settings map[string]string) (string, error) {
	if settings == nil {
		settings = map[string]string{}
	}
	body, err := json.Marshal(settings)
	return string(body), err
}

func scanAccount(row interface{ Scan(dest ...any) error }) (Account, error) {
	var (
		a        Account
		settings string
		enabled  int
		created  int64
	)
	if err := row.Scan(&a.ID, &a.Connector, &a.Label, &settings, &enabled, &created); err != nil {
		return Account{}, err
	}
	a.Settings = map[string]string{}
	if err := json.Unmarshal([]byte(settings), &a.Settings); err != nil {
		return Account{}, fmt.Errorf("account %d settings: %w", a.ID, err)
	}
	a.Enabled = enabled != 0
	a.CreatedAt = time.Unix(created, 0).UTC()
	return a, nil
}

func (s *Store) CreateAccount(connector, label string, settings map[string]string) (int64, error) {
	if connector == "" {
		return 0, fmt.Errorf("%w: account needs a connector name", ErrInvalid)
	}
	body, err := encodeSettings(settings)
	if err != nil {
		return 0, err
	}
	res, err := s.w.Exec(`INSERT INTO accounts (connector, label, settings, enabled, created_at) VALUES (?, ?, ?, 1, ?)`,
		connector, clean(label), body, s.now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Account(id int64) (Account, error) {
	a, err := scanAccount(s.r.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, fmt.Errorf("account %d: %w", id, ErrNotFound)
	}
	return a, err
}

func (s *Store) Accounts() ([]Account, error) {
	rows, err := s.r.Query(`SELECT ` + accountColumns + ` FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAccountSettings(id int64, settings map[string]string) error {
	body, err := encodeSettings(settings)
	if err != nil {
		return err
	}
	return s.updateAccount(id, `UPDATE accounts SET settings = ? WHERE id = ?`, body, id)
}

func (s *Store) SetAccountEnabled(id int64, enabled bool) error {
	return s.updateAccount(id, `UPDATE accounts SET enabled = ? WHERE id = ?`, b2i(enabled), id)
}

func (s *Store) updateAccount(id int64, query string, args ...any) error {
	res, err := s.w.Exec(query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("account %d: %w", id, ErrNotFound)
	}
	return nil
}
