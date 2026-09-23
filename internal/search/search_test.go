package search

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/ingest"
	"github.com/ismailperim/briefd/internal/store"
)

func TestBuildMatch(t *testing.T) {
	tests := []struct {
		in, wantAll, wantAny string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{"retry policy", `"retry" "policy"`, `"retry" OR "policy"`},
		{`"quoted" AND (injection) NOT x*`, `"quoted" "injection" "x"`, `"quoted" OR "injection" OR "x"`},
		{"Ödeme iadesi", `"ödeme" "iadesi"`, `"ödeme" OR "iadesi"`},
		{"payment-service v2", `"payment" "service" "v2"`, `"payment" OR "service" OR "v2"`},
		{"what is the refund window", `"refund" "window"`, `"refund" OR "window"`},
		{"the who", `"the" "who"`, `"the" OR "who"`},
	}
	for _, tt := range tests {
		all, anyTerm := BuildMatch(tt.in)
		if all != tt.wantAll || anyTerm != tt.wantAny {
			t.Errorf("BuildMatch(%q) = (%q, %q), want (%q, %q)", tt.in, all, anyTerm, tt.wantAll, tt.wantAny)
		}
	}
}

func seedStore(t *testing.T, sections int) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var sb strings.Builder
	sb.WriteString("# Retry guide\n\n")
	for i := range sections {
		fmt.Fprintf(&sb, "## Section %d\n\n%s\n\n", i, strings.Repeat("retry backoff jitter policy. ", 5+i*3))
	}
	doc, err := ingest.Parse("conventions/retry.md", []byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDocument(context.Background(), doc, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSearchRespectsBudgetAndTopK(t *testing.T) {
	ctx := context.Background()
	st := seedStore(t, 12)
	s := New(st, Options{})

	res, err := s.Search(ctx, Query{Text: "retry policy", TopK: 3, MaxTokens: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Chunks) != 3 || res.Omitted != 0 {
		t.Fatalf("topK not honored: %d chunks, %d omitted", len(res.Chunks), res.Omitted)
	}

	res, err = s.Search(ctx, Query{Text: "retry policy", TopK: 50, MaxTokens: 120})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalTokens > Budget(120) || res.TotalTokens == 0 {
		t.Errorf("total tokens %d outside (0, %d]", res.TotalTokens, Budget(120))
	}
	if res.Omitted == 0 {
		t.Error("expected some chunks to be omitted under a tight budget")
	}
	if res.Scopes[0] != "domain" || res.Scopes[1] != "conventions" {
		t.Errorf("default scopes = %v", res.Scopes)
	}

	res, _ = s.Search(ctx, Query{Text: "", MaxTokens: 100})
	if len(res.Chunks) != 0 {
		t.Error("empty query should return no chunks")
	}
}

// Property: for random budgets, the total never exceeds the budget.
func TestSearchBudgetProperty(t *testing.T) {
	ctx := context.Background()
	st := seedStore(t, 15)
	s := New(st, Options{MaxTopK: 100})
	rng := rand.New(rand.NewSource(7))
	for range 200 {
		max := 1 + rng.Intn(3000)
		res, err := s.Search(ctx, Query{Text: "retry jitter", TopK: 1 + rng.Intn(40), MaxTokens: max})
		if err != nil {
			t.Fatal(err)
		}
		if res.TotalTokens > Budget(max) {
			t.Fatalf("max_tokens=%d: total %d exceeds budget %d", max, res.TotalTokens, Budget(max))
		}
		sum := 0
		for _, c := range res.Chunks {
			sum += c.Tokens
		}
		if sum != res.TotalTokens {
			t.Fatalf("TotalTokens %d != sum %d", res.TotalTokens, sum)
		}
	}
}

// Paths pull the documents whose refs cover them to the top, even for a
// task whose wording does not retrieve them first.
func TestSearchPathsGovern(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := indexer.Run(ctx, st, indexer.Options{Root: "../../testdata/knowledge"}); err != nil {
		t.Fatal(err)
	}
	s := New(st, Options{})
	plain, err := s.Search(ctx, Query{Text: "settlement batch is late", TopK: 5, MaxTokens: 100000})
	if err != nil || len(plain.Chunks) == 0 {
		t.Fatalf("plain search: %v", err)
	}
	if plain.Chunks[0].DocPath == "domain/rules/refunds.md" {
		t.Skip("refunds already ranks first without paths; pick another query")
	}
	governed, err := s.Search(ctx, Query{Text: "settlement batch is late", TopK: 10, MaxTokens: 100000,
		Paths: []string{"services/ledger/refund/partial.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if governed.Chunks[0].DocPath != "domain/rules/refunds.md" {
		t.Errorf("first result with paths = %s, want domain/rules/refunds.md", governed.Chunks[0].DocPath)
	}
	// The original top hit is still there, just not first; and unclaimed
	// paths change nothing.
	found := false
	for _, c := range governed.Chunks {
		if c.ChunkID == plain.Chunks[0].ChunkID {
			found = true
		}
	}
	if !found {
		t.Error("the plain top hit disappeared")
	}
	same, _ := s.Search(ctx, Query{Text: "settlement batch is late", TopK: 5, MaxTokens: 100000, Paths: []string{"nothing/claims/this.go"}})
	if same.Chunks[0].ChunkID != plain.Chunks[0].ChunkID {
		t.Error("unclaimed paths should not change the ranking")
	}
}
