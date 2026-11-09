package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/store"
)

func TestSuggestLinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("domain/fx-rate-cache.md", "# FxRateCache\n\n## Role\n\nKeeps the latest FX rates.\n")
	write("domain/incident.md", "# Incident runbook\n\n## Steps\n\nRestart the FxRateCache first, then check the FxRateCache log.\n")
	write("domain/linked.md", "# Linked\n\n## Body\n\nThe fxratecache is covered in [[fx-rate-cache]].\n")
	write("domain/heading-only.md", "# About\n\n## FxRateCache notes\n\nNothing else.\n")
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := indexer.Run(ctx, st, indexer.Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	got, err := st.SuggestLinks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("suggestions = %+v, want one (incident → fx-rate-cache)", got)
	}
	if s := got[0]; s.From != "domain/incident.md" || s.To != "domain/fx-rate-cache.md" || s.Mention != "fxratecache" || s.Count != 2 {
		t.Errorf("suggestion = %+v", s)
	}
	if err := st.IgnoreLinkSuggestion(ctx, "domain/incident.md", "domain/fx-rate-cache.md"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.SuggestLinks(ctx, 10); len(got) != 0 {
		t.Errorf("ignored suggestion still listed: %+v", got)
	}
	if n, err := st.ClearLinkIgnores(ctx); err != nil || n != 1 {
		t.Errorf("clear = %d %v", n, err)
	}
	if got, _ := st.SuggestLinks(ctx, 10); len(got) != 1 {
		t.Errorf("after clear: %+v", got)
	}
}
