package metrics

import (
	"testing"
	"time"
)

func TestExportImportReset(t *testing.T) {
	a := New("test")
	a.Record(Request{Surface: SurfaceMCP, Name: "compile_bundle", Tokens: 1500, Chunks: 4, Duration: 20 * time.Millisecond})
	a.Record(Request{Surface: SurfaceREST, Name: "api_search", Tokens: 300, Chunks: 2, Duration: 5 * time.Millisecond, Error: true})
	a.RecordCache(true)
	a.RecordServed("compile_bundle", []string{"domain/a.md"})
	data, err := a.Export()
	if err != nil {
		t.Fatal(err)
	}

	b := New("test")
	if err := b.Import(data); err != nil {
		t.Fatal(err)
	}
	snap := b.Snapshot()
	if snap.Totals.Requests != 2 || snap.Totals.TokensServed != 1800 || snap.Totals.Errors != 1 || snap.Totals.CacheHits != 1 {
		t.Errorf("restored totals = %+v", snap.Totals)
	}
	if len(snap.Recent) != 2 || snap.Recent[0].Name != "api_search" {
		t.Errorf("restored recent = %+v", snap.Recent)
	}
	if len(b.RecentlyServed()) != 1 || !b.Since().Equal(a.Since()) {
		t.Errorf("served=%v since=%v/%v", b.RecentlyServed(), b.Since(), a.Since())
	}
	b.Record(Request{Surface: SurfaceMCP, Name: "compile_bundle", Tokens: 100})
	if got := b.Snapshot().Totals.TokensServed; got != 1900 {
		t.Errorf("tokens after restore + record = %d", got)
	}

	b.Reset()
	if snap := b.Snapshot(); snap.Totals.Requests != 0 || len(snap.Recent) != 0 || len(b.RecentlyServed()) != 0 {
		t.Errorf("after reset: %+v served=%v", snap.Totals, b.RecentlyServed())
	}
	if err := b.Import([]byte(`{"version":99}`)); err == nil {
		t.Error("unknown version should be refused")
	}
}
