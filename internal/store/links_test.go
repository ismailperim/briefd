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
	write("domain/snapshot-server.md", "# SnapshotServer\n\n## Role\n\nKeeps the latest quote.\n")
	write("domain/outage.md", "# Outage handling\n\n## Steps\n\nRestart the SnapshotServer first, then check the SnapshotServer log.\n")
	write("domain/linked.md", "# Linked\n\n## Body\n\nThe snapshotserver is covered in [[snapshot-server]].\n")
	write("domain/heading-only.md", "# About\n\n## SnapshotServer notes\n\nNothing else.\n")
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
		t.Fatalf("suggestions = %+v, want one (outage → snapshot-server)", got)
	}
	if s := got[0]; s.From != "domain/outage.md" || s.To != "domain/snapshot-server.md" || s.Mention != "snapshotserver" || s.Count != 2 {
		t.Errorf("suggestion = %+v", s)
	}
}
