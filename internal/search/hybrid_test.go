package search

import (
	"context"
	"hash/fnv"
	"math"
	"path/filepath"
	"testing"

	"github.com/ismailperim/briefd/internal/ingest"
	"github.com/ismailperim/briefd/internal/store"
)

// fakeEmbedder is a deterministic bag-of-words embedder with a tiny synonym
// table, enough to exercise vector retrieval and fusion without a model.
type fakeEmbedder struct{}

var synonyms = map[string]string{
	"retries": "retry", "retrying": "retry", "reattempt": "retry", "reattempts": "retry",
	"dispute": "chargeback", "disputes": "chargeback",
}

func (fakeEmbedder) Name() string { return "fake/bow" }
func (f fakeEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	out, err := f.Embed(ctx, []string{text})
	return out[0], err
}
func (fakeEmbedder) Dim() int { return 64 }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 64)
		for _, term := range Terms(t) {
			if s, ok := synonyms[term]; ok {
				term = s
			}
			h := fnv.New32a()
			_, _ = h.Write([]byte(term))
			v[h.Sum32()%64] += 1
		}
		var n float64
		for _, x := range v {
			n += float64(x * x)
		}
		if n > 0 {
			for j := range v {
				v[j] /= float32(math.Sqrt(n))
			}
		}
		out[i] = v
	}
	return out, nil
}

func hybridStore(t *testing.T) (*store.Store, *Searcher) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	docs := map[string]string{
		"conventions/retries.md": "# Retry policy\n\n## Backoff\n\nRetries use exponential backoff with jitter.\n\n## Limits\n\nAt most five retries per call.\n",
		"domain/chargebacks.md":  "# Chargebacks\n\n## Evidence\n\nMerchants may submit evidence when a chargeback is raised.\n",
		"domain/glossary.md":     "# Glossary\n\n## Payout\n\nA transfer to the merchant bank account.\n",
	}
	ctx := context.Background()
	for p, body := range docs {
		d, err := ingest.Parse(p, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertDocument(ctx, d, ""); err != nil {
			t.Fatal(err)
		}
	}
	s := New(st, Options{}).WithEmbedder(fakeEmbedder{})
	pending, _ := st.PendingVectors(ctx, s.embedder.Name(), 100)
	texts := make([]string, len(pending))
	for i, p := range pending {
		texts[i] = p.Text
	}
	vecs, _ := s.embedder.Embed(ctx, texts)
	if err := st.PutVectors(ctx, s.embedder.Name(), pending, vecs); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Vectors().Len() != 4 {
		t.Fatalf("vector index has %d entries, want 4", s.Vectors().Len())
	}
	return st, s
}

func TestHybridFindsSynonymsAndFuses(t *testing.T) {
	ctx := context.Background()
	_, s := hybridStore(t)
	// Ensure the FTS tokenizer would not stem "reattempt" to "retry".
	bm, err := s.Search(ctx, Query{Text: "reattempt strategy", Mode: ModeBM25})
	if err != nil {
		t.Fatal(err)
	}
	if len(bm.Chunks) != 0 {
		t.Fatalf("BM25 unexpectedly matched: %+v", bm.Chunks)
	}
	vec, err := s.Search(ctx, Query{Text: "reattempt strategy", Mode: ModeVector})
	if err != nil {
		t.Fatal(err)
	}
	if len(vec.Chunks) == 0 || vec.Chunks[0].DocPath != "conventions/retries.md" {
		t.Fatalf("vector mode should find the retry doc, got %+v", vec.Chunks)
	}
	hy, err := s.Search(ctx, Query{Text: "reattempt strategy"})
	if err != nil {
		t.Fatal(err)
	}
	if hy.Mode != ModeHybrid || len(hy.Chunks) == 0 || hy.Chunks[0].DocPath != "conventions/retries.md" {
		t.Fatalf("hybrid = %+v", hy)
	}

	// Both retrievers agree on "chargeback evidence"; fused score > single-list score.
	hy, _ = s.Search(ctx, Query{Text: "chargeback evidence"})
	if hy.Chunks[0].DocPath != "domain/chargebacks.md" || hy.Chunks[0].Score < 2.0/(RRFK+1)-1e-9 {
		t.Errorf("expected agreement bonus, got %+v", hy.Chunks[0])
	}

	// Scope filtering applies to the vector side as well.
	hy, _ = s.Search(ctx, Query{Text: "chargeback dispute", Scopes: []string{"conventions"}})
	for _, c := range hy.Chunks {
		if c.Scope != "conventions" {
			t.Errorf("scope leak: %+v", c)
		}
	}
	if _, err := New(s.store, Options{}).Search(ctx, Query{Text: "x", Mode: ModeVector}); err == nil {
		t.Error("vector mode without embedder should error")
	}
}
