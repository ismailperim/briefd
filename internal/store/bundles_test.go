package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBundlesAndUsage(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	fp, err := s.IndexFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doc := mustParse(t, "domain/a.md", "# A\n\n## One\n\nalpha\n")
	if err := s.UpsertDocument(ctx, doc, ""); err != nil {
		t.Fatal(err)
	}
	fp2, _ := s.IndexFingerprint(ctx)
	if fp == fp2 || len(fp2) != 16 {
		t.Errorf("fingerprint did not change with content: %s vs %s", fp, fp2)
	}
	if err := s.SetIndexFingerprint(ctx, fp2); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetIndexFingerprint(ctx); got != fp2 {
		t.Errorf("stored fingerprint = %s, want %s", got, fp2)
	}

	b := &Bundle{ID: "b1", CacheKey: "k1", Task: "t", Scopes: []string{"domain"}, MaxTokens: 100, IndexFingerprint: fp2,
		Model: "m", Content: "hello", Tokens: 1, Sections: []BundleSection{{ChunkID: doc.Chunks[0].ID, DocPath: "domain/a.md", Scope: "domain", Heading: "A > One", Tokens: 1}},
		CreatedAt: time.Now()}
	if err := s.PutBundle(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBundle(ctx, b); err != nil {
		t.Errorf("duplicate put should be ignored: %v", err)
	}
	got, err := s.GetBundleByKey(ctx, "k1")
	if err != nil || got.ID != "b1" || got.Hits != 1 || len(got.Sections) != 1 || got.Scopes[0] != "domain" {
		t.Fatalf("GetBundleByKey = %+v, %v", got, err)
	}
	got, _ = s.GetBundleByKey(ctx, "k1")
	if got.Hits != 2 {
		t.Errorf("hits = %d, want 2", got.Hits)
	}
	if _, err := s.GetBundleByKey(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key err = %v", err)
	}
	if byID, err := s.GetBundle(ctx, "b1"); err != nil || byID.Content != "hello" {
		t.Errorf("GetBundle = %+v, %v", byID, err)
	}
	st, _ := s.GetBundleStats(ctx)
	if st.Bundles != 1 || st.Hits != 2 {
		t.Errorf("stats = %+v", st)
	}

	if err := s.PutUsage(ctx, []UsageEvent{{BundleID: "b1", ChunkID: "c", Useful: true, Client: "test"}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountUsage(ctx); n != 1 {
		t.Errorf("usage count = %d", n)
	}

	n, err := s.PruneBundles(ctx, "other-fingerprint")
	if err != nil || n != 1 {
		t.Errorf("pruned %d, %v", n, err)
	}
}
