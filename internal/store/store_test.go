package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ismailperim/briefd/internal/ingest"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustParse(t *testing.T, path, src string) *ingest.Document {
	t.Helper()
	d, err := ingest.Parse(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestUpsertSearchDelete(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	retry := mustParse(t, "conventions/retries.md", "---\ntags: [resilience]\n---\n# Retry policy\n\n## Backoff\n\nUse exponential backoff with jitter for transient failures.\n\n## Limits\n\nAt most five attempts.\n")
	refunds := mustParse(t, "domain/refunds.md", "# Refunds\n\n## Window\n\nRefunds are allowed within 30 days of capture.\n")
	proj := mustParse(t, "projects/ledger/README.md", "# Ledger\n\n## Retries\n\nThe ledger retries failed postings with backoff.\n")
	for _, d := range []*ingest.Document{retry, refunds, proj} {
		if err := s.UpsertDocument(ctx, d, "abc123"); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := s.SearchFTS(ctx, `"backoff"`, []string{"domain", "conventions"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].DocPath != "conventions/retries.md" || hits[0].HeadingPath != "Retry policy > Backoff" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	if hits[0].Score <= 0 || hits[0].Tokens <= 0 || hits[0].Scope != "conventions" {
		t.Errorf("hit fields not populated: %+v", hits[0])
	}

	// Scope filter: the project chunk becomes visible only when requested.
	hits, err = s.SearchFTS(ctx, `"backoff"`, []string{"conventions", "projects/ledger"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits across scopes, got %d", len(hits))
	}

	// Stemming: "retrying" matches "retries"/"retry".
	hits, err = s.SearchFTS(ctx, `"retrying"`, []string{"conventions"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("expected stemmed match for 'retrying'")
	}

	// Re-upsert with changed content replaces chunks and the FTS index.
	retry2 := mustParse(t, "conventions/retries.md", "# Retry policy\n\n## Backoff\n\nUse linear delays.\n")
	if err := s.UpsertDocument(ctx, retry2, "def456"); err != nil {
		t.Fatal(err)
	}
	hits, _ = s.SearchFTS(ctx, `"exponential"`, []string{"conventions"}, 10)
	if len(hits) != 0 {
		t.Errorf("stale FTS rows survived re-upsert: %+v", hits)
	}
	hits, _ = s.SearchFTS(ctx, `"linear"`, []string{"conventions"}, 10)
	if len(hits) != 1 {
		t.Errorf("new content not searchable: %+v", hits)
	}

	doc, content, err := s.GetDocument(ctx, "conventions/retries.md")
	if err != nil {
		t.Fatal(err)
	}
	if doc.UpdatedCommit != "def456" || doc.Title != "Retry policy" || content != "## Backoff\n\nUse linear delays." {
		t.Errorf("unexpected document: %+v content=%q", doc, content)
	}

	scopes, err := s.ListScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 3 || scopes[0].Scope != "conventions" || scopes[0].Documents != 1 || scopes[0].Chunks != 1 {
		t.Errorf("unexpected scopes: %+v", scopes)
	}

	hashes, err := s.ContentHashes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 3 || hashes["domain/refunds.md"] != refunds.ContentHash {
		t.Errorf("unexpected hashes: %v", hashes)
	}

	if err := s.DeleteDocument(ctx, "domain/refunds.md"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetDocument(ctx, "domain/refunds.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
	hits, _ = s.SearchFTS(ctx, `"refunds"`, []string{"domain"}, 10)
	if len(hits) != 0 {
		t.Errorf("chunks of deleted document still searchable: %+v", hits)
	}
}

func TestSyncState(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	st, err := s.GetSyncState(ctx)
	if err != nil || st.Source != "" || !st.LastSyncAt.IsZero() {
		t.Fatalf("unexpected initial state %+v, err %v", st, err)
	}
	if err := s.SetSyncState(ctx, SyncState{Source: "/tmp/k", LastCommit: "abc"}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.GetSyncState(ctx)
	if st.Source != "/tmp/k" || st.LastCommit != "abc" {
		t.Errorf("unexpected state %+v", st)
	}
}
