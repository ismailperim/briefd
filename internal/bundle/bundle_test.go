package bundle

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

func chunk(id, scope, heading, content string) store.ChunkHit {
	return store.ChunkHit{ChunkID: id, DocPath: scope + "/" + id + ".md", Scope: scope, HeadingPath: heading, Content: content, Tokens: tokenizer.Count(content)}
}

func TestPackDedupesOrdersAndTruncates(t *testing.T) {
	ranked := []store.ChunkHit{
		chunk("p1", "projects/x", "X > Retries", "Callers must retry posting submissions with the standard retry policy."),
		chunk("d1", "domain", "Refunds > Window", "A refund may be requested up to 180 days after capture."),
		chunk("d1dup", "domain", "Refunds > Window copy", "A refund may be requested up to 180 days after capture."),
		chunk("c1", "conventions", "Retries > Policy", "Use exponential backoff starting at 200 ms with full jitter, at most five attempts."),
		chunk("d2", "domain", "Refunds > Near copy", "A refund may be requested up to 180 days after the capture."),
	}
	selected, truncated := Pack(ranked, 10000, 10)
	if truncated {
		t.Error("unexpected truncation")
	}
	var ids []string
	for _, c := range Order(selected) {
		ids = append(ids, c.ChunkID)
	}
	if got := strings.Join(ids, ","); got != "d1,c1,p1" {
		t.Errorf("selected order = %s, want d1,c1,p1 (dupes dropped, scope-ordered)", got)
	}

	// Tight budget: only the small chunks fit; a large top chunk is skipped.
	big := chunk("big", "domain", "Big", strings.Repeat("settlement batch reconciliation ", 200))
	selected, truncated = Pack(append([]store.ChunkHit{big}, ranked...), 120, 10)
	for _, c := range selected {
		if c.ChunkID == "big" {
			t.Error("oversized chunk should be skipped when smaller ones fit")
		}
	}
	if len(selected) == 0 || truncated {
		t.Errorf("expected small chunks to fit, got %d (truncated=%v)", len(selected), truncated)
	}

	// Nothing fits: the top chunk is truncated with a marker.
	selected, truncated = Pack([]store.ChunkHit{big}, 120, 10)
	if !truncated || len(selected) != 1 || !strings.HasSuffix(selected[0].Content, truncationMarker) || selected[0].Tokens > 120 {
		t.Errorf("expected truncated head, got truncated=%v n=%d tokens=%d", truncated, len(selected), selected[0].Tokens)
	}
	// Budget too small even for a truncated head: empty.
	if selected, _ := Pack([]store.ChunkHit{big}, 20, 10); len(selected) != 0 {
		t.Errorf("expected nothing for a tiny budget, got %d", len(selected))
	}
}

func TestBody(t *testing.T) {
	if got := Body("## Heading\n\ntext\nmore"); got != "text\nmore" {
		t.Errorf("Body = %q", got)
	}
	if got := Body("no heading"); got != "no heading" {
		t.Errorf("Body = %q", got)
	}
	if got := Body("### only"); got != "" {
		t.Errorf("Body = %q", got)
	}
}

func TestJaccard(t *testing.T) {
	if j := jaccard([]string{"a", "b", "c"}, []string{"a", "b", "d"}); j < 0.49 || j > 0.51 {
		t.Errorf("jaccard = %v", j)
	}
	if jaccard(nil, nil) != 1 || jaccard([]string{"a"}, nil) != 0 {
		t.Error("edge cases")
	}
}

func newCompiler(t *testing.T) (*Compiler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := indexer.Run(context.Background(), st, indexer.Options{Root: "../../testdata/knowledge"}); err != nil {
		t.Fatal(err)
	}
	s := search.New(st, search.Options{MaxTopK: 100})
	return New(st, s, Options{}), st
}

func TestCompileIsDeterministicAndCached(t *testing.T) {
	ctx := context.Background()
	c, st := newCompiler(t)
	req := Request{Task: "implement refunds for disputed payments", Scopes: []string{"conventions", "domain"}, MaxTokens: 900}

	first, err := c.Compile(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || first.Tokens == 0 || first.Tokens > first.Budget || len(first.Sections) == 0 {
		t.Fatalf("first = cached:%v tokens:%d budget:%d sections:%d", first.Cached, first.Tokens, first.Budget, len(first.Sections))
	}
	if !strings.HasPrefix(first.Content, "<!-- briefd bundle "+first.ID) || !strings.Contains(first.Content, "## domain/rules/refunds.md") {
		t.Errorf("unexpected content:\n%s", first.Content[:200])
	}
	// Scope ordering: every domain section precedes every conventions section.
	seenConventions := false
	for _, s := range first.Sections {
		if s.Scope == "conventions" {
			seenConventions = true
		} else if s.Scope == "domain" && seenConventions {
			t.Error("domain section after conventions section")
		}
	}

	// Same request (scopes in another order) hits the in-memory cache.
	second, err := c.Compile(ctx, Request{Task: req.Task, Scopes: []string{"domain", "conventions"}, MaxTokens: 900})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached || second.Content != first.Content || second.ID != first.ID {
		t.Error("expected identical cached bundle")
	}
	// A fresh compiler (empty LRU) hits the SQLite cache.
	c2 := New(st, search.New(st, search.Options{MaxTopK: 100}), Options{})
	third, _ := c2.Compile(ctx, req)
	if !third.Cached || third.Content != first.Content {
		t.Error("expected SQLite cache hit")
	}
	// Different budget => different bundle id.
	other, _ := c.Compile(ctx, Request{Task: req.Task, Scopes: req.Scopes, MaxTokens: 400})
	if other.ID == first.ID || other.Cached {
		t.Error("budget must be part of the cache key")
	}
	// Recompiling from scratch is byte-identical.
	if _, err := st.PruneBundles(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	fresh, _ := New(st, search.New(st, search.Options{MaxTopK: 100}), Options{}).Compile(ctx, req)
	if fresh.Cached || fresh.Content != first.Content {
		t.Error("recompiled bundle differs from the original")
	}
	if _, err := c.Compile(ctx, Request{Task: "   "}); err == nil {
		t.Error("empty task should error")
	}
}

// Property: for random tasks and budgets, the rendered bundle never exceeds
// the budget and is reproducible.
func TestCompileBudgetProperty(t *testing.T) {
	ctx := context.Background()
	c, _ := newCompiler(t)
	rng := rand.New(rand.NewSource(42))
	words := strings.Fields("refund payout settlement chargeback retry backoff idempotency webhook kubernetes rollout secrets fee currency rounding dispute evidence kyc tier velocity risk migration flag incident postmortem")
	for i := range 60 {
		n := 1 + rng.Intn(5)
		var q []string
		for range n {
			q = append(q, words[rng.Intn(len(words))])
		}
		max := 50 + rng.Intn(3000)
		req := Request{Task: fmt.Sprintf("%s #%d", strings.Join(q, " "), i), MaxTokens: max}
		res, err := c.Compile(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if got := tokenizer.Count(res.Content); got > res.Budget || got != res.Tokens {
			t.Fatalf("max_tokens=%d: content %d tokens, budget %d, reported %d", max, got, res.Budget, res.Tokens)
		}
		again, _ := c.Compile(ctx, req)
		if again.Content != res.Content {
			t.Fatal("non-deterministic bundle")
		}
	}
}
