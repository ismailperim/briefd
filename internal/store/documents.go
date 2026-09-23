package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ismailperim/briefd/internal/ingest"
)

// ErrNotFound is returned when a document or chunk does not exist.
var ErrNotFound = errors.New("not found")

// Document is a stored knowledge document without its chunks.
type Document struct {
	ID            int64
	Path          string
	Scope         string
	Title         string
	Tags          []string
	Refs          []string
	FrontMatter   string
	ContentHash   string
	UpdatedCommit string
	IndexedAt     time.Time
	// UpdatedAt is when the content last changed (git commit time, or file
	// mtime for plain directories); zero when not yet known.
	UpdatedAt time.Time
}

// UpsertDocument stores a parsed document and replaces all of its chunks in
// one transaction. commit records which repository commit the content came
// from ("" for a local directory).
func (s *Store) UpsertDocument(ctx context.Context, doc *ingest.Document, commit string) error {
	tags, err := json.Marshal(nonNil(doc.Tags))
	if err != nil {
		return fmt.Errorf("encoding tags for %s: %w", doc.Path, err)
	}
	refs, err := json.Marshal(nonNil(doc.Refs))
	if err != nil {
		return fmt.Errorf("encoding refs for %s: %w", doc.Path, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("upserting %s: %w", doc.Path, err)
	}
	defer tx.Rollback() // no-op after commit

	var docID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO documents (path, scope, title, tags, refs, front_matter, content_hash, updated_commit, indexed_at, links_scanned)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(path) DO UPDATE SET
			scope = excluded.scope, title = excluded.title, tags = excluded.tags,
			refs = excluded.refs, front_matter = excluded.front_matter,
			content_hash = excluded.content_hash, updated_commit = excluded.updated_commit,
			indexed_at = excluded.indexed_at, links_scanned = 1
		RETURNING id`,
		doc.Path, doc.Scope, doc.Title, string(tags), string(refs), doc.FrontMatter,
		doc.ContentHash, commit, time.Now().UTC().Format(time.RFC3339),
	).Scan(&docID)
	if err != nil {
		return fmt.Errorf("upserting %s: %w", doc.Path, err)
	}

	// Chunks are replaced wholesale; the FTS index follows via triggers.
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE doc_id = ?`, docID); err != nil {
		return fmt.Errorf("clearing chunks of %s: %w", doc.Path, err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (id, doc_id, title, heading_path, content, tokens, content_hash, position)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("upserting %s: %w", doc.Path, err)
	}
	defer stmt.Close()
	for _, c := range doc.Chunks {
		if _, err := stmt.ExecContext(ctx, c.ID, docID, doc.Title, c.HeadingPath, c.Content, c.Tokens, c.ContentHash, c.Position); err != nil {
			if isConstraint(err) {
				return fmt.Errorf("inserting chunk %s of %s: duplicate chunk id: %w", c.ID, doc.Path, err)
			}
			return fmt.Errorf("inserting chunk %s of %s: %w", c.ID, doc.Path, err)
		}
	}
	if err := replaceLinks(ctx, tx, doc.Path, doc.Links); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("upserting %s: %w", doc.Path, err)
	}
	return nil
}

// DeleteDocument removes a document and its chunks. Deleting a missing
// document is not an error.
func (s *Store) DeleteDocument(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE path = ?`, path); err != nil {
		return fmt.Errorf("deleting %s: %w", path, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM doc_links WHERE from_path = ?`, path); err != nil {
		return fmt.Errorf("deleting links of %s: %w", path, err)
	}
	return nil
}

// ContentHashes returns path -> content_hash for every stored document, so
// an indexer can skip unchanged files and detect deletions.
func (s *Store) ContentHashes(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, content_hash FROM documents`)
	if err != nil {
		return nil, fmt.Errorf("listing document hashes: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			return nil, fmt.Errorf("listing document hashes: %w", err)
		}
		out[p] = h
	}
	return out, rows.Err()
}

// GetDocument returns a document's metadata and full Markdown content
// (reassembled from its chunks in order). Returns ErrNotFound if absent.
func (s *Store) GetDocument(ctx context.Context, path string) (*Document, string, error) {
	var d Document
	var tags, refs, indexedAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, path, scope, title, tags, refs, front_matter, content_hash, updated_commit, indexed_at, updated_at
		FROM documents WHERE path = ?`, path).Scan(
		&d.ID, &d.Path, &d.Scope, &d.Title, &tags, &refs, &d.FrontMatter, &d.ContentHash, &d.UpdatedCommit, &indexedAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("loading %s: %w", path, err)
	}
	if err := json.Unmarshal([]byte(tags), &d.Tags); err != nil {
		return nil, "", fmt.Errorf("decoding tags of %s: %w", path, err)
	}
	if err := json.Unmarshal([]byte(refs), &d.Refs); err != nil {
		return nil, "", fmt.Errorf("decoding refs of %s: %w", path, err)
	}
	d.IndexedAt, _ = time.Parse(time.RFC3339, indexedAt)
	d.UpdatedAt = parseTime(updatedAt)

	rows, err := s.db.QueryContext(ctx, `SELECT content FROM chunks WHERE doc_id = ? ORDER BY position`, d.ID)
	if err != nil {
		return nil, "", fmt.Errorf("loading chunks of %s: %w", path, err)
	}
	defer rows.Close()
	var content string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, "", fmt.Errorf("loading chunks of %s: %w", path, err)
		}
		if content != "" {
			content += "\n\n"
		}
		content += c
	}
	return &d, content, rows.Err()
}

// ScopeInfo summarizes one scope for list_scopes.
type ScopeInfo struct {
	Scope     string `json:"scope"`
	Documents int    `json:"documents"`
	Chunks    int    `json:"chunks"`
	Tokens    int    `json:"tokens"`
}

// ListScopes returns every scope present in the index with its counts.
func (s *Store) ListScopes(ctx context.Context) ([]ScopeInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.scope, COUNT(DISTINCT d.id), COUNT(c.rowid), COALESCE(SUM(c.tokens), 0)
		FROM documents d LEFT JOIN chunks c ON c.doc_id = d.id
		GROUP BY d.scope ORDER BY d.scope`)
	if err != nil {
		return nil, fmt.Errorf("listing scopes: %w", err)
	}
	defer rows.Close()
	var out []ScopeInfo
	for rows.Next() {
		var si ScopeInfo
		if err := rows.Scan(&si.Scope, &si.Documents, &si.Chunks, &si.Tokens); err != nil {
			return nil, fmt.Errorf("listing scopes: %w", err)
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// SyncState records where the index last synced from.
type SyncState struct {
	Source     string
	LastCommit string
	LastSyncAt time.Time
	LastError  string
}

// SetSyncState overwrites the singleton sync_state row.
func (s *Store) SetSyncState(ctx context.Context, st SyncState) error {
	var at *string
	if !st.LastSyncAt.IsZero() {
		v := st.LastSyncAt.UTC().Format(time.RFC3339)
		at = &v
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE sync_state SET source = ?, last_commit = ?, last_sync_at = ?, last_error = ? WHERE id = 1`,
		st.Source, st.LastCommit, at, st.LastError)
	if err != nil {
		return fmt.Errorf("updating sync state: %w", err)
	}
	return nil
}

// GetSyncState reads the singleton sync_state row.
func (s *Store) GetSyncState(ctx context.Context) (SyncState, error) {
	var st SyncState
	var at sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT source, last_commit, last_sync_at, last_error FROM sync_state WHERE id = 1`).
		Scan(&st.Source, &st.LastCommit, &at, &st.LastError)
	if err != nil {
		return st, fmt.Errorf("reading sync state: %w", err)
	}
	if at.Valid {
		st.LastSyncAt, _ = time.Parse(time.RFC3339, at.String)
	}
	return st, nil
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
