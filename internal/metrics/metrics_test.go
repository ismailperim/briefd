package metrics

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRecordAndSnapshot(t *testing.T) {
	r := New("1.2.3")
	for i := range 10 {
		r.Record(Request{
			Surface: SurfaceMCP, Name: "search_context", Query: "retry",
			Duration: time.Duration(i+1) * time.Millisecond, Tokens: 100, Chunks: 2, Omitted: 1,
			Error: i == 9,
		})
	}
	r.Record(Request{Surface: SurfaceREST, Name: "api_scopes", Duration: 500 * time.Microsecond})
	r.SetIndex([]ScopeCount{{Scope: "domain", Documents: 3, Chunks: 20, Tokens: 4000}, {Scope: "conventions", Documents: 2, Chunks: 10, Tokens: 2000}})
	r.RecordSync(nil)
	r.RecordSync(errors.New("boom"))

	s := r.Snapshot()
	if s.Version != "1.2.3" || s.Totals.Requests != 11 || s.Totals.Errors != 1 || s.Totals.TokensServed != 1000 || s.Totals.Omitted != 10 {
		t.Errorf("totals = %+v", s.Totals)
	}
	if s.Totals.Documents != 5 || s.Totals.Chunks != 30 || s.Totals.IndexTokens != 6000 {
		t.Errorf("index totals = %+v", s.Totals)
	}
	if len(s.ByName) != 2 || s.ByName[0].Name != "search_context" || s.ByName[1].Name != "api_scopes" {
		t.Fatalf("by_name = %+v", s.ByName)
	}
	sc := s.ByName[0]
	if sc.Requests != 10 || sc.Errors != 1 || sc.P50Ms < 4 || sc.P50Ms > 7 || sc.P95Ms < 9 || sc.P95Ms > 10.5 || sc.AvgMs < 5 || sc.AvgMs > 6 {
		t.Errorf("search_context stats = %+v", sc)
	}
	if s.Sync.Runs != 2 || s.Sync.Failures != 1 || s.Sync.LastError != "boom" || s.Sync.LastOK == nil {
		t.Errorf("sync = %+v", s.Sync)
	}
	if len(s.Recent) != 11 || s.Recent[0].Name != "api_scopes" || s.Recent[1].Error != true {
		t.Errorf("recent order wrong: %+v", s.Recent[:2])
	}
	if s.Index[0].Scope != "conventions" || s.Index[1].Scope != "domain" {
		t.Errorf("index not sorted: %+v", s.Index)
	}
}

func TestRecentRingWraps(t *testing.T) {
	r := New("v")
	for i := range recentSize + 5 {
		r.Record(Request{Surface: SurfaceMCP, Name: "x", Tokens: i})
	}
	s := r.Snapshot()
	if len(s.Recent) != recentSize || s.Recent[0].Tokens != recentSize+4 || s.Recent[recentSize-1].Tokens != 5 {
		t.Errorf("ring buffer wrong: len=%d first=%d last=%d", len(s.Recent), s.Recent[0].Tokens, s.Recent[len(s.Recent)-1].Tokens)
	}
}

func TestPrometheusOutput(t *testing.T) {
	r := New("v9")
	r.Record(Request{Surface: SurfaceMCP, Name: "search_context", Duration: 3 * time.Millisecond, Tokens: 42})
	r.SetIndex([]ScopeCount{{Scope: "domain", Documents: 1, Chunks: 2, Tokens: 3}})
	r.RecordSync(nil)
	var buf bytes.Buffer
	r.WritePrometheus(&buf)
	out := buf.String()
	for _, want := range []string{
		`briefd_build_info{version="v9"} 1`,
		`briefd_requests_total{surface="mcp",name="search_context",status="ok"} 1`,
		`briefd_tokens_served_total{surface="mcp",name="search_context"} 42`,
		`briefd_request_duration_seconds_bucket{surface="mcp",name="search_context",le="0.005"} 1`,
		`briefd_request_duration_seconds_bucket{surface="mcp",name="search_context",le="0.001"} 0`,
		`briefd_request_duration_seconds_bucket{surface="mcp",name="search_context",le="+Inf"} 1`,
		`briefd_index_chunks{scope="domain"} 2`,
		`briefd_sync_runs_total{status="ok"} 1`,
		"# TYPE briefd_request_duration_seconds histogram",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRouteName(t *testing.T) {
	tests := map[string]string{
		"GET /api/search":         "api_search",
		"GET /api/docs/{path...}": "api_docs_path",
		"/mcp":                    "mcp",
		"GET /":                   "root",
	}
	for in, want := range tests {
		if got := RouteName(in); got != want {
			t.Errorf("RouteName(%q) = %q, want %q", in, got, want)
		}
	}
}
