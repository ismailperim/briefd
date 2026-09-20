package store

import (
	"context"
	"testing"
)

func TestVectorsLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	doc := mustParse(t, "domain/a.md", "# A\n\n## One\n\nalpha\n\n## Two\n\nbeta\n")
	if err := s.UpsertDocument(ctx, doc, ""); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingVectors(ctx, "m1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].Text != "A > One\n## One\n\nalpha" {
		t.Fatalf("pending = %+v", pending)
	}
	vecs := [][]float32{{1, 0}, {0, 1}}
	if err := s.PutVectors(ctx, "m1", pending, vecs); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountPendingVectors(ctx, "m1"); n != 0 {
		t.Errorf("pending after put = %d", n)
	}
	// A different model needs everything again.
	if n, _ := s.CountPendingVectors(ctx, "m2"); n != 2 {
		t.Errorf("pending for other model = %d", n)
	}

	// Editing one section re-queues only that chunk; the other keeps its vector.
	doc2 := mustParse(t, "domain/a.md", "# A\n\n## One\n\nalpha changed\n\n## Two\n\nbeta\n")
	if err := s.UpsertDocument(ctx, doc2, ""); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.PendingVectors(ctx, "m1", 100)
	if len(pending) != 1 || pending[0].ID != doc2.Chunks[0].ID {
		t.Errorf("pending after edit = %+v", pending)
	}
	loaded, err := s.LoadVectors(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ChunkID != doc2.Chunks[1].ID || loaded[0].Embedding[1] != 1 || loaded[0].Scope != "domain" {
		t.Errorf("loaded = %+v", loaded)
	}

	// Deleting the document leaves orphans that prune removes.
	if err := s.DeleteDocument(ctx, "domain/a.md"); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneVectors(ctx)
	if err != nil || n != 2 {
		t.Errorf("pruned %d, err %v", n, err)
	}

	hits, err := s.GetChunks(ctx, []string{doc2.Chunks[0].ID})
	if err != nil || len(hits) != 0 {
		t.Errorf("GetChunks after delete = %v, %v", hits, err)
	}
}

func TestVectorCodec(t *testing.T) {
	v := []float32{0.5, -1, 3.25}
	got, err := DecodeVector(EncodeVector(v), 3)
	if err != nil || got[0] != 0.5 || got[1] != -1 || got[2] != 3.25 {
		t.Errorf("roundtrip = %v, %v", got, err)
	}
	if _, err := DecodeVector([]byte{1, 2}, 3); err == nil {
		t.Error("expected size error")
	}
}
