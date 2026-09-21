package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ismailperim/briefd/internal/store"
)

func TestRunIncremental(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("domain/a.md", "# A\n\n## One\n\nalpha\n")
	write("domain/b.md", "# B\n\n## Two\n\nbeta\n")
	write("conventions/c.md", "# C\n\n## Three\n\ngamma\n")

	st, err := store.Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	stats, err := Run(ctx, st, Options{Root: root, Commit: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Scanned != 3 || stats.Indexed != 3 || stats.Skipped != 0 || stats.Deleted != 0 || stats.Chunks != 3 {
		t.Fatalf("first run stats = %+v", stats)
	}

	// Without a git resolver, document ages come from file mtimes.
	ages, err := st.StalestDocuments(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ages) != 3 || ages[0].UpdatedAt.IsZero() {
		t.Fatalf("ages after first run = %+v", ages)
	}
	// A resolver overrides mtimes; paths it does not know keep the fallback.
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := Run(ctx, st, Options{Root: root, Commit: "c1", Force: true,
		LastModified: func(_ context.Context, _ []string) (map[string]time.Time, error) {
			return map[string]time.Time{"conventions/c.md": old}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	if ages, _ = st.StalestDocuments(ctx, 1); len(ages) != 1 || ages[0].Path != "conventions/c.md" || !ages[0].UpdatedAt.Equal(old) {
		t.Fatalf("stalest = %+v, want conventions/c.md @ %v", ages, old)
	}

	// Nothing changed: everything is skipped.
	stats, err = Run(ctx, st, Options{Root: root, Commit: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 0 || stats.Skipped != 3 {
		t.Fatalf("second run stats = %+v", stats)
	}

	// One edit, one delete.
	write("domain/a.md", "# A\n\n## One\n\nalpha changed\n")
	if err := os.Remove(filepath.Join(root, "domain", "b.md")); err != nil {
		t.Fatal(err)
	}
	stats, err = Run(ctx, st, Options{Root: root, Commit: "c2"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 1 || stats.Skipped != 1 || stats.Deleted != 1 {
		t.Fatalf("third run stats = %+v", stats)
	}
	hits, _ := st.SearchFTS(ctx, `"changed"`, []string{"domain"}, 5)
	if len(hits) != 1 {
		t.Errorf("edited content not searchable: %+v", hits)
	}
	hits, _ = st.SearchFTS(ctx, `"beta"`, []string{"domain"}, 5)
	if len(hits) != 0 {
		t.Errorf("deleted document still searchable: %+v", hits)
	}
	sync, _ := st.GetSyncState(ctx)
	if sync.LastCommit != "c2" || sync.LastSyncAt.IsZero() {
		t.Errorf("sync state not updated: %+v", sync)
	}

	// Force re-indexes everything.
	stats, err = Run(ctx, st, Options{Root: root, Commit: "c2", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 2 || stats.Skipped != 0 {
		t.Fatalf("forced run stats = %+v", stats)
	}
}
