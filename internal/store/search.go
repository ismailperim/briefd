package store

import (
	"context"
	"fmt"
	"strings"
)

// ChunkHit is one ranked chunk returned by a search.
type ChunkHit struct {
	ChunkID     string  `json:"chunk_id"`
	DocPath     string  `json:"doc_path"`
	Scope       string  `json:"scope"`
	Title       string  `json:"title"`
	HeadingPath string  `json:"heading"`
	Content     string  `json:"content"`
	Tokens      int     `json:"tokens"`
	Score       float64 `json:"score"`
}

// BM25 column weights for (title, heading_path, content). A heading match is
// a strong relevance signal; the title is shared by every chunk of a
// document so it counts less. Tune with `briefd eval`.
const bm25Weights = "1.5, 3.0, 1.0"

// SearchFTS runs an FTS5 MATCH query restricted to the given scopes and
// returns up to limit hits ordered by BM25 relevance (higher Score is
// better). match must already be a valid FTS5 query expression; see
// package search for building one from user input.
func (s *Store) SearchFTS(ctx context.Context, match string, scopes []string, limit int) ([]ChunkHit, error) {
	if match == "" || len(scopes) == 0 || limit <= 0 {
		return nil, nil
	}
	args := []any{match}
	placeholders := make([]string, len(scopes))
	for i, sc := range scopes {
		placeholders[i] = "?"
		args = append(args, sc)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT c.id, d.path, d.scope, c.title, c.heading_path, c.content, c.tokens,
		       -bm25(chunks_fts, %s) AS score
		FROM chunks_fts
		JOIN chunks c ON c.rowid = chunks_fts.rowid
		JOIN documents d ON d.id = c.doc_id
		WHERE chunks_fts MATCH ? AND d.scope IN (%s)
		ORDER BY score DESC, c.id
		LIMIT ?`, bm25Weights, strings.Join(placeholders, ", ")), args...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()
	var hits []ChunkHit
	for rows.Next() {
		var h ChunkHit
		if err := rows.Scan(&h.ChunkID, &h.DocPath, &h.Scope, &h.Title, &h.HeadingPath, &h.Content, &h.Tokens, &h.Score); err != nil {
			return nil, fmt.Errorf("fts search: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// AllChunks returns every chunk with its document metadata, ordered by
// document path and position. Used by evaluation tooling.
func (s *Store) AllChunks(ctx context.Context) ([]ChunkHit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, d.path, d.scope, c.title, c.heading_path, c.content, c.tokens
		FROM chunks c JOIN documents d ON d.id = c.doc_id
		ORDER BY d.path, c.position`)
	if err != nil {
		return nil, fmt.Errorf("listing chunks: %w", err)
	}
	defer rows.Close()
	var out []ChunkHit
	for rows.Next() {
		var h ChunkHit
		if err := rows.Scan(&h.ChunkID, &h.DocPath, &h.Scope, &h.Title, &h.HeadingPath, &h.Content, &h.Tokens); err != nil {
			return nil, fmt.Errorf("listing chunks: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
