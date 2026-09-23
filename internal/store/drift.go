package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DocRefs is a document with the code paths it claims to govern.
type DocRefs struct {
	Path      string
	Refs      []string
	UpdatedAt time.Time
}

// DocumentsWithRefs lists documents that declare `refs` globs.
func (s *Store) DocumentsWithRefs(ctx context.Context) ([]DocRefs, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, refs, updated_at FROM documents WHERE refs != '[]' ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("listing documents with refs: %w", err)
	}
	defer rows.Close()
	var out []DocRefs
	for rows.Next() {
		var d DocRefs
		var refs, at string
		if err := rows.Scan(&d.Path, &refs, &at); err != nil {
			return nil, fmt.Errorf("listing documents with refs: %w", err)
		}
		if err := json.Unmarshal([]byte(refs), &d.Refs); err != nil {
			return nil, fmt.Errorf("decoding refs of %s: %w", d.Path, err)
		}
		d.UpdatedAt = parseTime(at)
		if len(d.Refs) > 0 {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// Drift records that code governed by a document changed after the document did.
type Drift struct {
	DocPath string `json:"doc_path"`
	Repo    string `json:"repo"`
	// Commits is how many commits touched a governed path after the
	// document's last change.
	Commits int `json:"commits"`
	// LastChangeAt and LastPath describe the most recent such commit.
	LastChangeAt time.Time `json:"last_change_at"`
	LastPath     string    `json:"last_path"`
	CheckedAt    time.Time `json:"checked_at"`
}

// ReplaceDrift overwrites the drift table with the given rows and reports
// whether anything observable (paths, commit counts, last changes) differs
// from what was stored before, so callers know when cached bundles that
// render drift are out of date.
func (s *Store) ReplaceDrift(ctx context.Context, drifts []Drift) (changed bool, err error) {
	before, err := s.DriftByPath(ctx)
	if err != nil {
		return false, err
	}
	changed = len(before) != len(drifts)
	for _, d := range drifts {
		if b, ok := before[d.DocPath]; !ok || b.Commits != d.Commits || !b.LastChangeAt.Equal(d.LastChangeAt.Truncate(time.Second)) {
			changed = true
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("replacing drift: %w", err)
	}
	defer tx.Rollback() // no-op after commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM code_drift`); err != nil {
		return false, fmt.Errorf("replacing drift: %w", err)
	}
	for _, d := range drifts {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO code_drift (doc_path, repo, commits, last_change_at, last_path, checked_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			d.DocPath, d.Repo, d.Commits, d.LastChangeAt.UTC().Format(time.RFC3339), d.LastPath, d.CheckedAt.UTC().Format(time.RFC3339))
		if err != nil {
			return false, fmt.Errorf("storing drift of %s: %w", d.DocPath, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("replacing drift: %w", err)
	}
	return changed, nil
}

// DriftByPath returns every drift row keyed by document path.
func (s *Store) DriftByPath(ctx context.Context) (map[string]Drift, error) {
	list, err := s.ListDrift(ctx, 1<<30)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Drift, len(list))
	for _, d := range list {
		out[d.DocPath] = d
	}
	return out, nil
}

// ListDrift returns drift rows, most commits first.
func (s *Store) ListDrift(ctx context.Context, limit int) ([]Drift, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT doc_path, repo, commits, last_change_at, last_path, checked_at
		FROM code_drift ORDER BY commits DESC, last_change_at DESC, doc_path LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing drift: %w", err)
	}
	defer rows.Close()
	var out []Drift
	for rows.Next() {
		var d Drift
		var last, checked string
		if err := rows.Scan(&d.DocPath, &d.Repo, &d.Commits, &last, &d.LastPath, &checked); err != nil {
			return nil, fmt.Errorf("listing drift: %w", err)
		}
		d.LastChangeAt, d.CheckedAt = parseTime(last), parseTime(checked)
		out = append(out, d)
	}
	return out, rows.Err()
}

// AttachDrift fills CodeChanges / CodeChangedAt on hits whose document has
// a drift row. It is a no-op when the table is empty.
func (s *Store) AttachDrift(ctx context.Context, hits []ChunkHit) error {
	if len(hits) == 0 {
		return nil
	}
	drift, err := s.DriftByPath(ctx)
	if err != nil || len(drift) == 0 {
		return err
	}
	for i := range hits {
		if d, ok := drift[hits[i].DocPath]; ok {
			hits[i].CodeChanges, hits[i].CodeChangedAt = d.Commits, d.LastChangeAt
		}
	}
	return nil
}

// DeleteAllBundles empties the bundle cache (used when rendered content
// depends on state outside the index fingerprint, such as drift).
func (s *Store) DeleteAllBundles(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM bundles`); err != nil {
		return fmt.Errorf("clearing bundles: %w", err)
	}
	return nil
}

// ChunksByDoc returns every chunk of the given documents that lies in one
// of the scopes, ordered by document path and position.
func (s *Store) ChunksByDoc(ctx context.Context, docPaths, scopes []string) ([]ChunkHit, error) {
	if len(docPaths) == 0 || len(scopes) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(docPaths)+len(scopes))
	dp := make([]string, len(docPaths))
	for i, p := range docPaths {
		dp[i] = "?"
		args = append(args, p)
	}
	sp := make([]string, len(scopes))
	for i, sc := range scopes {
		sp[i] = "?"
		args = append(args, sc)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT c.id, d.path, d.scope, c.title, c.heading_path, c.content, c.tokens, d.updated_at
		FROM chunks c JOIN documents d ON d.id = c.doc_id
		WHERE d.path IN (%s) AND d.scope IN (%s)
		ORDER BY d.path, c.position`, strings.Join(dp, ","), strings.Join(sp, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("loading document chunks: %w", err)
	}
	defer rows.Close()
	var out []ChunkHit
	for rows.Next() {
		var h ChunkHit
		var updated string
		if err := rows.Scan(&h.ChunkID, &h.DocPath, &h.Scope, &h.Title, &h.HeadingPath, &h.Content, &h.Tokens, &updated); err != nil {
			return nil, fmt.Errorf("loading document chunks: %w", err)
		}
		h.UpdatedAt = parseTime(updated)
		out = append(out, h)
	}
	return out, rows.Err()
}
