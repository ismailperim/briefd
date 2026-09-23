package metrics

import "testing"

func TestRecentlyServed(t *testing.T) {
	r := New("test")
	r.RecordServed("compile_bundle", []string{"domain/a.md", "domain/b.md", "domain/a.md"})
	r.RecordServed("search_context", []string{"domain/a.md"})
	got := r.RecentlyServed()
	if len(got) != 2 || got[0].Path != "domain/a.md" || got[0].Count != 2 || got[0].Via != "search_context" {
		t.Fatalf("served = %+v", got)
	}
	var nilReg *Registry
	nilReg.RecordServed("x", []string{"a"})
	if len(nilReg.RecentlyServed()) != 0 {
		t.Error("nil registry should serve nothing")
	}
}
