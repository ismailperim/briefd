package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopeOf(t *testing.T) {
	tests := []struct {
		path  string
		scope string
		ok    bool
	}{
		{"domain/glossary.md", "domain", true},
		{"domain/rules/refunds.md", "domain", true},
		{"conventions/go.md", "conventions", true},
		{"projects/ledger/README.md", "projects/ledger", true},
		{"projects/ledger/runbooks/oncall.md", "projects/ledger", true},
		{"projects/README.md", "", false},
		{"README.md", "", false},
		{"docs/x.md", "", false},
		{"domain", "", false},
	}
	for _, tt := range tests {
		scope, ok := ScopeOf(tt.path)
		if scope != tt.scope || ok != tt.ok {
			t.Errorf("ScopeOf(%q) = (%q, %v), want (%q, %v)", tt.path, scope, ok, tt.scope, tt.ok)
		}
	}
}

func TestWalk(t *testing.T) {
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
	write("README.md", "root readme is ignored")
	write("domain/b.md", "# B")
	write("domain/a.MD", "# A")
	write("domain/notes.txt", "not markdown")
	write("conventions/c.md", "# C")
	write("projects/p1/README.md", "# P1")
	write("projects/orphan.md", "# not in a project")
	write(".git/domain/x.md", "# hidden dir")
	write("domain/.draft.md", "# hidden file")

	files, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.RelPath)
	}
	want := []string{"conventions/c.md", "domain/a.MD", "domain/b.md", "projects/p1/README.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d = %q, want %q", i, got[i], want[i])
		}
	}

	doc, err := Load(files[3])
	if err != nil {
		t.Fatal(err)
	}
	if doc.Scope != "projects/p1" || doc.Title != "P1" || doc.ContentHash == "" {
		t.Errorf("unexpected document: %+v", doc)
	}
}

func TestParseOutsideScope(t *testing.T) {
	if _, err := Parse("README.md", []byte("# x")); err == nil {
		t.Error("expected error for out-of-scope path")
	}
}
