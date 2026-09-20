package search

import (
	"container/heap"
	"context"
	"sync"

	"github.com/ismailperim/briefd/internal/store"
)

// VectorIndex is an in-memory, brute-force cosine index over the vectors
// stored in SQLite (ADR-0002). Vectors are L2-normalised so cosine
// similarity is a dot product.
type VectorIndex struct {
	mu     sync.RWMutex
	model  string
	dim    int
	ids    []string
	scopes []string
	data   []float32 // row-major, len(ids)*dim
}

// NewVectorIndex returns an empty index for model.
func NewVectorIndex(model string) *VectorIndex {
	return &VectorIndex{model: model}
}

// Model returns the embedding model name the index was built for.
func (ix *VectorIndex) Model() string { return ix.model }

// Len returns the number of indexed vectors.
func (ix *VectorIndex) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.ids)
}

// Reload replaces the index contents from the store.
func (ix *VectorIndex) Reload(ctx context.Context, st *store.Store) error {
	rows, err := st.LoadVectors(ctx, ix.model)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(rows))
	scopes := make([]string, 0, len(rows))
	var data []float32
	dim := 0
	for _, r := range rows {
		if dim == 0 {
			dim = len(r.Embedding)
			data = make([]float32, 0, len(rows)*dim)
		}
		if len(r.Embedding) != dim {
			continue // skip vectors from a mismatched dimension
		}
		ids = append(ids, r.ChunkID)
		scopes = append(scopes, r.Scope)
		data = append(data, r.Embedding...)
	}
	ix.mu.Lock()
	ix.dim, ix.ids, ix.scopes, ix.data = dim, ids, scopes, data
	ix.mu.Unlock()
	return nil
}

// VecHit is a vector search result.
type VecHit struct {
	ChunkID string
	Score   float32
}

// Search returns the k most similar chunks to query within scopes.
func (ix *VectorIndex) Search(query []float32, scopes []string, k int) []VecHit {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if k <= 0 || ix.dim == 0 || len(query) != ix.dim {
		return nil
	}
	allowed := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		allowed[s] = true
	}
	h := &minHeap{}
	for i, id := range ix.ids {
		if !allowed[ix.scopes[i]] {
			continue
		}
		row := ix.data[i*ix.dim : (i+1)*ix.dim]
		var dot float32
		for j, q := range query {
			dot += q * row[j]
		}
		if h.Len() < k {
			heap.Push(h, VecHit{ChunkID: id, Score: dot})
		} else if dot > (*h)[0].Score {
			(*h)[0] = VecHit{ChunkID: id, Score: dot}
			heap.Fix(h, 0)
		}
	}
	out := make([]VecHit, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(VecHit)
	}
	return out
}

type minHeap []VecHit

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].Score != h[j].Score {
		return h[i].Score < h[j].Score
	}
	return h[i].ChunkID > h[j].ChunkID // deterministic ties
}
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push implements heap.Interface.
func (h *minHeap) Push(x any) { *h = append(*h, x.(VecHit)) }

// Pop implements heap.Interface.
func (h *minHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
