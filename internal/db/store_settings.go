package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// GetSetting returns the value of a runtime setting, or ErrNotFound if it has
// never been set (callers treat that as the default).
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	switch err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return v, nil
}

// SetSetting upserts a runtime setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings(key, value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}
