package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// PendingChunk is a chunk that has no up-to-date vector for a model.
type PendingChunk struct {
	ID          string
	ContentHash string
	Text        string // heading breadcrumb + content, what gets embedded
}

// PendingVectors lists chunks lacking a vector for model, or whose content
// changed since it was embedded, oldest document first, up to limit.
func (s *Store) PendingVectors(ctx context.Context, model string, limit int) ([]PendingChunk, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.content_hash, c.heading_path, c.content
		FROM chunks c
		LEFT JOIN chunk_vectors v ON v.chunk_id = c.id
		WHERE v.chunk_id IS NULL OR v.model != ? OR v.content_hash != c.content_hash
		ORDER BY c.doc_id, c.position
		LIMIT ?`, model, limit)
	if err != nil {
		return nil, fmt.Errorf("listing pending vectors: %w", err)
	}
	defer rows.Close()
	var out []PendingChunk
	for rows.Next() {
		var p PendingChunk
		var heading, content string
		if err := rows.Scan(&p.ID, &p.ContentHash, &heading, &content); err != nil {
			return nil, fmt.Errorf("listing pending vectors: %w", err)
		}
		p.Text = EmbeddingText(heading, content)
		out = append(out, p)
	}
	return out, rows.Err()
}

// EmbeddingText is the text embedded for a chunk: the breadcrumb gives the
// model context that the section body alone may lack.
func EmbeddingText(headingPath, content string) string {
	return headingPath + "\n" + content
}

// CountPendingVectors returns how many chunks still need a vector for model.
func (s *Store) CountPendingVectors(ctx context.Context, model string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM chunks c LEFT JOIN chunk_vectors v ON v.chunk_id = c.id
		WHERE v.chunk_id IS NULL OR v.model != ? OR v.content_hash != c.content_hash`, model).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting pending vectors: %w", err)
	}
	return n, nil
}

// PutVectors stores embeddings for chunks in one transaction.
func (s *Store) PutVectors(ctx context.Context, model string, chunks []PendingChunk, vectors [][]float32) error {
	if len(chunks) != len(vectors) {
		return fmt.Errorf("storing vectors: %d chunks but %d vectors", len(chunks), len(vectors))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storing vectors: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunk_vectors (chunk_id, model, dim, content_hash, embedding) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id) DO UPDATE SET model = excluded.model, dim = excluded.dim,
			content_hash = excluded.content_hash, embedding = excluded.embedding`)
	if err != nil {
		return fmt.Errorf("storing vectors: %w", err)
	}
	defer stmt.Close()
	for i, c := range chunks {
		if _, err := stmt.ExecContext(ctx, c.ID, model, len(vectors[i]), c.ContentHash, EncodeVector(vectors[i])); err != nil {
			return fmt.Errorf("storing vector for chunk %s: %w", c.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storing vectors: %w", err)
	}
	return nil
}

// PruneVectors deletes vectors whose chunk no longer exists.
func (s *Store) PruneVectors(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM chunk_vectors WHERE chunk_id NOT IN (SELECT id FROM chunks)`)
	if err != nil {
		return 0, fmt.Errorf("pruning vectors: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// StoredVector is one row of chunk_vectors joined with its scope.
type StoredVector struct {
	ChunkID   string
	Scope     string
	Embedding []float32
}

// LoadVectors returns every vector for model (with the owning document's
// scope) so the caller can build an in-memory index.
func (s *Store) LoadVectors(ctx context.Context, model string) ([]StoredVector, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.chunk_id, d.scope, v.dim, v.embedding
		FROM chunk_vectors v
		JOIN chunks c ON c.id = v.chunk_id
		JOIN documents d ON d.id = c.doc_id
		WHERE v.model = ? AND v.content_hash = c.content_hash`, model)
	if err != nil {
		return nil, fmt.Errorf("loading vectors: %w", err)
	}
	defer rows.Close()
	var out []StoredVector
	for rows.Next() {
		var sv StoredVector
		var dim int
		var blob []byte
		if err := rows.Scan(&sv.ChunkID, &sv.Scope, &dim, &blob); err != nil {
			return nil, fmt.Errorf("loading vectors: %w", err)
		}
		vec, err := DecodeVector(blob, dim)
		if err != nil {
			return nil, fmt.Errorf("loading vector %s: %w", sv.ChunkID, err)
		}
		sv.Embedding = vec
		out = append(out, sv)
	}
	return out, rows.Err()
}

// GetChunks returns hits for the given chunk ids (order not guaranteed),
// with Score left zero.
func (s *Store) GetChunks(ctx context.Context, ids []string) ([]ChunkHit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	ph := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		ph[i] = "?"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT c.id, d.path, d.scope, c.title, c.heading_path, c.content, c.tokens
		FROM chunks c JOIN documents d ON d.id = c.doc_id
		WHERE c.id IN (%s)`, strings.Join(ph, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("loading chunks: %w", err)
	}
	defer rows.Close()
	var out []ChunkHit
	for rows.Next() {
		var h ChunkHit
		if err := rows.Scan(&h.ChunkID, &h.DocPath, &h.Scope, &h.Title, &h.HeadingPath, &h.Content, &h.Tokens); err != nil {
			return nil, fmt.Errorf("loading chunks: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// EncodeVector serializes a float32 slice as little-endian bytes.
func EncodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}

// DecodeVector parses EncodeVector output.
func DecodeVector(b []byte, dim int) ([]float32, error) {
	if len(b) != dim*4 {
		return nil, fmt.Errorf("vector blob has %d bytes, want %d", len(b), dim*4)
	}
	v := make([]float32, dim)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v, nil
}
