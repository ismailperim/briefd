package search

import "testing"

func TestVectorIndexSearch(t *testing.T) {
	ix := NewVectorIndex("m")
	ix.dim = 2
	ix.ids = []string{"a", "b", "c", "d"}
	ix.scopes = []string{"domain", "domain", "conventions", "domain"}
	ix.data = []float32{1, 0, 0.7, 0.7, 0, 1, -1, 0}

	hits := ix.Search([]float32{1, 0}, []string{"domain"}, 2)
	if len(hits) != 2 || hits[0].ChunkID != "a" || hits[1].ChunkID != "b" {
		t.Errorf("hits = %+v", hits)
	}
	hits = ix.Search([]float32{0, 1}, []string{"domain", "conventions"}, 10)
	if len(hits) != 4 || hits[0].ChunkID != "c" || hits[3].ChunkID != "d" {
		t.Errorf("hits = %+v", hits)
	}
	if got := ix.Search([]float32{1, 0, 0}, []string{"domain"}, 3); got != nil {
		t.Errorf("dimension mismatch should return nil, got %+v", got)
	}
	if got := ix.Search([]float32{1, 0}, nil, 3); len(got) != 0 {
		t.Errorf("no scopes should return nothing, got %+v", got)
	}
}
