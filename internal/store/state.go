package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetState returns a saved value, or nil when none is stored.
func (s *Store) GetState(ctx context.Context, key string) ([]byte, error) {
	var v []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state %s: %w", key, err)
	}
	return v, nil
}

// SetState saves a value under key, replacing any previous one.
func (s *Store) SetState(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_state (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("saving state %s: %w", key, err)
	}
	return nil
}

// DeleteState removes a saved value; a missing key is not an error.
func (s *Store) DeleteState(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM app_state WHERE key = ?`, key); err != nil {
		return fmt.Errorf("deleting state %s: %w", key, err)
	}
	return nil
}
