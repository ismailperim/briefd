package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"

	"github.com/ismailperim/briefd/internal/store"
)

// ADR-0013: get_document must hand out the file byte for byte, including
// the front matter and title that chunks leave out.
func TestSourceIsStored(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	const file = "---\ntitle: Refunds\ntags: [payments]\nrelated:\n  - \"[[chargebacks]]\"\n---\n# Refunds\n\nIntro line.\r\n\n## Window\n\nRefunds within 30 days.\n"
	p := filepath.Join(root, "domain", "refunds.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "i.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := Run(ctx, st, Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(file))
	check := func(when string) {
		t.Helper()
		doc, content, err := st.GetDocument(ctx, "domain/refunds.md")
		if err != nil {
			t.Fatal(err)
		}
		if doc.Source != file {
			t.Errorf("%s: source = %q, want the file byte for byte", when, doc.Source)
		}
		if doc.ContentHash != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: content_hash is not the SHA-256 of the file", when)
		}
		if doc.FrontMatter == "" || content == "" || content == file {
			t.Errorf("%s: front_matter/content: %q / %q", when, doc.FrontMatter, content)
		}
	}
	check("first run")

	// A document indexed before sources were stored is re-parsed once,
	// although its content is unchanged.
	raw, err := driver.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE documents SET source = NULL`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	stats, err := Run(ctx, st, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 1 {
		t.Errorf("missing source not refilled: %+v", stats)
	}
	check("after refill")
	if stats, _ := Run(ctx, st, Options{Root: root}); stats.Skipped != 1 {
		t.Errorf("unchanged document re-parsed again: %+v", stats)
	}
}
