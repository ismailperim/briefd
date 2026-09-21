package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// IndexFingerprint hashes every document's path and content hash so it
// changes whenever the indexed knowledge changes.
func (s *Store) IndexFingerprint(ctx context.Context) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, content_hash FROM documents ORDER BY path`)
	if err != nil {
		return "", fmt.Errorf("computing index fingerprint: %w", err)
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var p, c string
		if err := rows.Scan(&p, &c); err != nil {
			return "", fmt.Errorf("computing index fingerprint: %w", err)
		}
		fmt.Fprintf(h, "%s\x00%s\n", p, c)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// SetIndexFingerprint stores the fingerprint in sync_state.
func (s *Store) SetIndexFingerprint(ctx context.Context, fp string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE sync_state SET index_fingerprint = ? WHERE id = 1`, fp); err != nil {
		return fmt.Errorf("storing index fingerprint: %w", err)
	}
	return nil
}

// GetIndexFingerprint reads the stored fingerprint ("" before any index run).
func (s *Store) GetIndexFingerprint(ctx context.Context) (string, error) {
	var fp string
	if err := s.db.QueryRowContext(ctx, `SELECT index_fingerprint FROM sync_state WHERE id = 1`).Scan(&fp); err != nil {
		return "", fmt.Errorf("reading index fingerprint: %w", err)
	}
	return fp, nil
}

// Bundle is a stored compiled bundle.
type Bundle struct {
	ID               string          `json:"bundle_id"`
	CacheKey         string          `json:"-"`
	Task             string          `json:"task"`
	Scopes           []string        `json:"scopes"`
	MaxTokens        int             `json:"max_tokens"`
	IndexFingerprint string          `json:"index_fingerprint"`
	Model            string          `json:"model"`
	Content          string          `json:"content"`
	Tokens           int             `json:"tokens"`
	Sections         []BundleSection `json:"sections"`
	Truncated        bool            `json:"truncated"`
	CreatedAt        time.Time       `json:"created_at"`
	Hits             int             `json:"hits"`
	// TopScore and Margin are the retrieval confidence at compile time
	// (see search.Result).
	TopScore float64 `json:"top_score"`
	Margin   float64 `json:"margin"`
}

// BundleSection attributes one part of the bundle to a chunk.
type BundleSection struct {
	ChunkID string `json:"chunk_id"`
	DocPath string `json:"doc_path"`
	Scope   string `json:"scope"`
	Heading string `json:"heading"`
	Tokens  int    `json:"tokens"`
}

// GetBundleByKey returns a cached bundle and bumps its hit counter.
func (s *Store) GetBundleByKey(ctx context.Context, cacheKey string) (*Bundle, error) {
	b, err := s.scanBundle(s.db.QueryRowContext(ctx, `
		SELECT id, cache_key, task, scopes, max_tokens, index_fingerprint, model, content, tokens, sections, truncated, created_at, hits, top_score, margin
		FROM bundles WHERE cache_key = ?`, cacheKey))
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE bundles SET hits = hits + 1 WHERE cache_key = ?`, cacheKey); err != nil {
		return nil, fmt.Errorf("counting bundle hit: %w", err)
	}
	b.Hits++
	return b, nil
}

// GetBundle returns a bundle by id.
func (s *Store) GetBundle(ctx context.Context, id string) (*Bundle, error) {
	return s.scanBundle(s.db.QueryRowContext(ctx, `
		SELECT id, cache_key, task, scopes, max_tokens, index_fingerprint, model, content, tokens, sections, truncated, created_at, hits, top_score, margin
		FROM bundles WHERE id = ?`, id))
}

func (s *Store) scanBundle(row *sql.Row) (*Bundle, error) {
	var b Bundle
	var scopes, sections, createdAt string
	var truncated int
	err := row.Scan(&b.ID, &b.CacheKey, &b.Task, &scopes, &b.MaxTokens, &b.IndexFingerprint, &b.Model, &b.Content, &b.Tokens, &sections, &truncated, &createdAt, &b.Hits, &b.TopScore, &b.Margin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading bundle: %w", err)
	}
	if err := json.Unmarshal([]byte(scopes), &b.Scopes); err != nil {
		return nil, fmt.Errorf("decoding bundle scopes: %w", err)
	}
	if err := json.Unmarshal([]byte(sections), &b.Sections); err != nil {
		return nil, fmt.Errorf("decoding bundle sections: %w", err)
	}
	b.Truncated = truncated != 0
	b.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &b, nil
}

// PutBundle stores a compiled bundle. A concurrent insert of the same key
// is not an error; the existing row wins.
func (s *Store) PutBundle(ctx context.Context, b *Bundle) error {
	scopes, err := json.Marshal(nonNil(b.Scopes))
	if err != nil {
		return fmt.Errorf("encoding bundle scopes: %w", err)
	}
	sections, err := json.Marshal(b.Sections)
	if err != nil {
		return fmt.Errorf("encoding bundle sections: %w", err)
	}
	if b.Sections == nil {
		sections = []byte("[]")
	}
	truncated := 0
	if b.Truncated {
		truncated = 1
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO bundles (id, cache_key, task, scopes, max_tokens, index_fingerprint, model, content, tokens, sections, truncated, created_at, hits, top_score, margin)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(cache_key) DO NOTHING`,
		b.ID, b.CacheKey, b.Task, string(scopes), b.MaxTokens, b.IndexFingerprint, b.Model, b.Content, b.Tokens, string(sections), truncated,
		b.CreatedAt.UTC().Format(time.RFC3339), b.TopScore, b.Margin)
	if err != nil {
		return fmt.Errorf("storing bundle: %w", err)
	}
	return nil
}

// PruneBundles deletes cached bundles compiled against a different index
// state than fingerprint (they can never be served again) and returns how
// many were removed.
func (s *Store) PruneBundles(ctx context.Context, fingerprint string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bundles WHERE index_fingerprint != ?`, fingerprint)
	if err != nil {
		return 0, fmt.Errorf("pruning bundles: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// BundleStats summarizes the bundle cache.
type BundleStats struct {
	Bundles int `json:"bundles"`
	Hits    int `json:"hits"`
}

// GetBundleStats returns cache size and cumulative hits.
func (s *Store) GetBundleStats(ctx context.Context) (BundleStats, error) {
	var bs BundleStats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(hits), 0) FROM bundles`).Scan(&bs.Bundles, &bs.Hits)
	if err != nil {
		return bs, fmt.Errorf("bundle stats: %w", err)
	}
	return bs, nil
}

// UsageEvent is one piece of feedback about a bundle's chunk.
type UsageEvent struct {
	BundleID string
	ChunkID  string
	Useful   bool
	Client   string
}

// PutUsage stores feedback events (SPEC §3.1.6; v0.1 only records them).
func (s *Store) PutUsage(ctx context.Context, events []UsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storing usage: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO usage_events (bundle_id, chunk_id, useful, client, created_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("storing usage: %w", err)
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, e := range events {
		useful := 0
		if e.Useful {
			useful = 1
		}
		if _, err := stmt.ExecContext(ctx, e.BundleID, e.ChunkID, useful, e.Client, now); err != nil {
			return fmt.Errorf("storing usage: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storing usage: %w", err)
	}
	return nil
}

// CountUsage returns the number of stored usage events.
func (s *Store) CountUsage(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting usage: %w", err)
	}
	return n, nil
}
