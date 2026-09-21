package store

import (
	"context"
	"fmt"
	"time"
)

// DocumentAge is a document with the time its content last changed.
type DocumentAge struct {
	Path      string    `json:"path"`
	Scope     string    `json:"scope"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SetDocumentUpdatedAt records when each document's content last changed.
// Unknown paths are ignored.
func (s *Store) SetDocumentUpdatedAt(ctx context.Context, times map[string]time.Time) error {
	if len(times) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("setting document ages: %w", err)
	}
	defer tx.Rollback() // no-op after commit
	stmt, err := tx.PrepareContext(ctx, `UPDATE documents SET updated_at = ? WHERE path = ?`)
	if err != nil {
		return fmt.Errorf("setting document ages: %w", err)
	}
	defer stmt.Close()
	for path, at := range times {
		if _, err := stmt.ExecContext(ctx, at.UTC().Format(time.RFC3339), path); err != nil {
			return fmt.Errorf("setting age of %s: %w", path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("setting document ages: %w", err)
	}
	return nil
}

// DocumentsWithoutAge lists documents whose updated_at is not yet known.
func (s *Store) DocumentsWithoutAge(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM documents WHERE updated_at = '' ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("listing documents without age: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("listing documents without age: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StalestDocuments returns the documents whose content changed longest ago,
// oldest first. Documents with unknown age are excluded.
func (s *Store) StalestDocuments(ctx context.Context, limit int) ([]DocumentAge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, scope, title, updated_at FROM documents
		WHERE updated_at != '' ORDER BY updated_at ASC, path LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing stalest documents: %w", err)
	}
	defer rows.Close()
	var out []DocumentAge
	for rows.Next() {
		var d DocumentAge
		var at string
		if err := rows.Scan(&d.Path, &d.Scope, &d.Title, &at); err != nil {
			return nil, fmt.Errorf("listing stalest documents: %w", err)
		}
		d.UpdatedAt = parseTime(at)
		out = append(out, d)
	}
	return out, rows.Err()
}

// parseTime reads an RFC 3339 column; "" and garbage become the zero time.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
