package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/httpapi"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/mcpserver"
	"github.com/ismailperim/briefd/internal/metrics"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

const testToken = "s3cret"

// newTestServer indexes testdata/knowledge and serves it exactly as
// `briefd serve` would.
func newTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := indexer.Run(context.Background(), st, indexer.Options{Root: "../../testdata/knowledge"}); err != nil {
		t.Fatal(err)
	}
	searcher := search.New(st, search.Options{})
	reg := metrics.New("test")
	compiler := bundle.New(st, searcher, bundle.Options{OnCache: reg.RecordCache})
	mcpSrv := mcpserver.New(mcpserver.Deps{Store: st, Searcher: searcher, Compiler: compiler, Metrics: reg, QueryLog: true, Version: "test"})
	h := httpapi.New(httpapi.Deps{
		Store:    st,
		Searcher: searcher,
		Compiler: compiler,
		MCP:      mcpserver.Handler(mcpSrv, nil),
		APIToken: token,
		Metrics:  reg,
		QueryLog: true,
		Version:  "test",
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(r)
}

func connect(t *testing.T, srv *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}},
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestMCPToolsOverHTTP(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, testToken)
	session := connect(t, srv, testToken)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if got := strings.Join(names, ","); got != "compile_bundle,get_document,list_scopes,propose_update,report_usage,search_context" {
		t.Errorf("tools = %s", got)
	}

	// compile_bundle: budgeted, deterministic, then cached; report_usage records feedback.
	b1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "compile_bundle",
		Arguments: map[string]any{"task_description": "add partial refunds to the portal", "max_tokens": 700},
	})
	if err != nil || b1.IsError {
		t.Fatalf("compile_bundle: err=%v res=%+v", err, b1)
	}
	var bo mcpserver.CompileBundleOutput
	raw, _ := json.Marshal(b1.StructuredContent)
	if err := json.Unmarshal(raw, &bo); err != nil {
		t.Fatal(err)
	}
	if bo.Cached || bo.Tokens == 0 || bo.Tokens > bo.Budget || len(bo.Sections) == 0 || !strings.HasPrefix(bo.Content, "<!-- briefd bundle "+bo.BundleID) {
		t.Errorf("bundle = id:%s tokens:%d budget:%d sections:%d cached:%v", bo.BundleID, bo.Tokens, bo.Budget, len(bo.Sections), bo.Cached)
	}
	b2, _ := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "compile_bundle",
		Arguments: map[string]any{"task_description": "add partial refunds to the portal", "max_tokens": 700},
	})
	raw, _ = json.Marshal(b2.StructuredContent)
	var bo2 mcpserver.CompileBundleOutput
	_ = json.Unmarshal(raw, &bo2)
	if !bo2.Cached || bo2.Content != bo.Content {
		t.Error("second compile should be a byte-identical cache hit")
	}
	ru, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "report_usage",
		Arguments: map[string]any{"bundle_id": bo.BundleID, "useful_chunk_ids": []string{bo.Sections[0].ChunkID}},
	})
	if err != nil || ru.IsError || !strings.Contains(ru.Content[0].(*mcp.TextContent).Text, "Recorded feedback") {
		t.Errorf("report_usage: err=%v res=%+v", err, ru)
	}
	ru, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "report_usage", Arguments: map[string]any{"bundle_id": "nope"}})
	if !ru.IsError {
		t.Error("report_usage with unknown bundle should be a tool error")
	}

	// search_context: relevant chunk, budget respected, structured output.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_context",
		Arguments: map[string]any{"query": "retry policy for acquirer calls", "max_tokens": 300},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "conventions/error-handling-and-retries.md") {
		t.Errorf("expected retry doc in text result, got:\n%s", text)
	}
	var out mcpserver.SearchContextOutput
	raw, _ = json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.TotalTokens == 0 || out.TotalTokens > search.Budget(300) {
		t.Errorf("total tokens %d outside budget %d", out.TotalTokens, search.Budget(300))
	}
	if len(out.Scopes) != 2 {
		t.Errorf("default scopes = %v", out.Scopes)
	}

	// Project scope is only visible when asked for.
	res, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_context",
		Arguments: map[string]any{"query": "projection drift alert"},
	})
	if strings.Contains(res.Content[0].(*mcp.TextContent).Text, "runbook.md") {
		t.Error("project document leaked into default scopes")
	}
	res, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_context",
		Arguments: map[string]any{"query": "projection drift alert", "scopes": []string{"projects/ledger-service"}},
	})
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "runbook.md") {
		t.Error("project document not found with explicit scope")
	}

	// get_document
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_document",
		Arguments: map[string]any{"doc_path": "domain/rules/refunds.md"},
	})
	if err != nil || res.IsError {
		t.Fatalf("get_document: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "## Refund window") {
		t.Error("get_document did not return full content")
	}
	res, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_document",
		Arguments: map[string]any{"doc_path": "domain/nope.md"},
	})
	if !res.IsError {
		t.Error("expected tool error for unknown document")
	}

	// list_scopes
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "list_scopes", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_scopes: err=%v res=%+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "projects/merchant-portal") {
		t.Errorf("list_scopes text = %s", text)
	}
}

func TestBearerAuth(t *testing.T) {
	srv := newTestServer(t, testToken)

	for _, path := range []string{"/api/search?q=x", "/api/scopes", "/api/docs/domain/glossary.md"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without token: status %d, want 401", path, resp.StatusCode)
		}
	}
	resp, _ := http.Get(srv.URL + "/api/health")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health should not require auth, got %d", resp.StatusCode)
	}

	// MCP with the wrong token must fail to initialize.
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	_, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: "wrong", base: http.DefaultTransport}},
	}, nil)
	if err == nil {
		t.Error("expected MCP connect with wrong token to fail")
	}

	// No configured token => open.
	open := newTestServer(t, "")
	resp, _ = http.Get(open.URL + "/api/scopes")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("unauthenticated mode: status %d, want 200", resp.StatusCode)
	}
}

func TestRESTSearchAndDocs(t *testing.T) {
	srv := newTestServer(t, testToken)
	client := &http.Client{Transport: bearerTransport{token: testToken, base: http.DefaultTransport}}

	resp, err := client.Get(srv.URL + "/api/search?q=refund+approval&max_tokens=500&scopes=domain")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res search.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Chunks) == 0 || res.Chunks[0].DocPath != "domain/rules/refunds.md" {
		t.Errorf("unexpected search result: %+v", res)
	}

	resp, _ = client.Get(srv.URL + "/api/search?q=x&top_k=abc")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad top_k: status %d", resp.StatusCode)
	}

	// POST /api/bundle and /api/usage
	resp, err = client.Post(srv.URL+"/api/bundle", "application/json", strings.NewReader(`{"task":"settlement batch is late","max_tokens":500,"scopes":["domain","projects/ledger-service"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var br struct {
		BundleID string `json:"bundle_id"`
		Tokens   int    `json:"tokens"`
		Budget   int    `json:"budget"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || br.Tokens == 0 || br.Tokens > br.Budget || !strings.Contains(br.Content, "projects/ledger-service/runbook.md") {
		t.Errorf("bundle response: status %d, %+v", resp.StatusCode, br)
	}
	resp, _ = client.Post(srv.URL+"/api/usage", "application/json", strings.NewReader(`{"bundle_id":"`+br.BundleID+`","useful_chunk_ids":[],"client":"curl"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("usage: status %d", resp.StatusCode)
	}
	resp, _ = client.Post(srv.URL+"/api/bundle", "application/json", strings.NewReader(`{"task":""}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty task: status %d", resp.StatusCode)
	}

	resp, _ = client.Get(srv.URL + "/api/docs/projects/ledger-service/runbook.md?scopes=domain")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("scope mismatch: status %d, want 403", resp.StatusCode)
	}
	resp, err = client.Get(srv.URL + "/api/docs/projects/ledger-service/runbook.md")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc["scope"] != "projects/ledger-service" || !strings.Contains(doc["content"].(string), "Projection drift") {
		t.Errorf("unexpected doc: %v", doc["scope"])
	}
}

func TestMetricsStatsAndDashboard(t *testing.T) {
	srv := newTestServer(t, testToken)
	client := &http.Client{Transport: bearerTransport{token: testToken, base: http.DefaultTransport}}
	session := connect(t, srv, testToken)
	ctx := context.Background()

	for range 3 {
		if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_context", Arguments: map[string]any{"query": "settlement batch"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_document", Arguments: map[string]any{"doc_path": "nope.md"}}); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(srv.URL + "/api/search?q=payout")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// /api/stats reflects the calls above.
	resp, err = client.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap metrics.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.Totals.Requests != 5 || snap.Totals.Errors != 1 || snap.Totals.TokensServed == 0 {
		t.Errorf("totals = %+v", snap.Totals)
	}
	if len(snap.Recent) != 5 || snap.Recent[0].Name != "api_search" || snap.Recent[1].Name != "get_document" || !snap.Recent[1].Error {
		t.Errorf("recent = %+v", snap.Recent)
	}
	if snap.Extra["source"] == nil {
		t.Errorf("extra sync info missing: %v", snap.Extra)
	}

	// /metrics is open by default and in Prometheus format.
	resp, err = http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("/metrics status %d type %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), `briefd_requests_total{surface="mcp",name="search_context",status="ok"} 3`) {
		t.Errorf("metrics output missing search_context counter:\n%s", body)
	}

	// Dashboard shell is served without auth.
	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>briefd</title>") || !strings.Contains(string(body), "Proposals") {
		t.Errorf("dashboard status %d", resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL + "/api/stats")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/stats without token: %d", resp.StatusCode)
	}
}

func TestProposalCountsInStats(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, p := range []store.Proposal{
		{ID: "open", Branch: "briefd/proposal-open"},
		{ID: "merged", Branch: "briefd/proposal-merged"},
		{ID: "closed", Branch: "briefd/proposal-closed"},
	} {
		if err := st.PutProposal(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdateProposalStatus(context.Background(), "merged", "merged"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateProposalStatus(context.Background(), "closed", "closed"); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(httpapi.Deps{Store: st, APIToken: testToken, Metrics: metrics.New("test")})
	srv := httptest.NewServer(h)
	defer srv.Close()
	client := &http.Client{Transport: bearerTransport{token: testToken, base: http.DefaultTransport}}
	resp, err := client.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap metrics.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.Proposals == nil || *snap.Proposals != (metrics.ProposalStats{Open: 1, Merged: 1, Closed: 1}) {
		t.Fatalf("proposal counts = %+v", snap.Proposals)
	}
}

// TestKnowledgeGaps drives the query log through both surfaces: a bundle
// the agent reports as useless and a search with no results become gaps;
// an ordinary answered query does not.
func TestKnowledgeGaps(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, testToken)
	session := connect(t, srv, testToken)
	client := &http.Client{Transport: bearerTransport{token: testToken, base: http.DefaultTransport}}

	b, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "compile_bundle",
		Arguments: map[string]any{"task_description": "rotate the on-call pager schedule", "max_tokens": 600},
	})
	if err != nil || b.IsError {
		t.Fatalf("compile_bundle: err=%v res=%+v", err, b)
	}
	var bo mcpserver.CompileBundleOutput
	raw, _ := json.Marshal(b.StructuredContent)
	_ = json.Unmarshal(raw, &bo)
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "report_usage",
		Arguments: map[string]any{"bundle_id": bo.BundleID, "useful_chunk_ids": []string{}},
	}); err != nil {
		t.Fatal(err)
	}
	// Nothing in the corpus contains this token, so BM25-only search returns nothing.
	if resp, err := client.Get(srv.URL + "/api/search?q=zzqxv&scopes=domain"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if resp, err := client.Get(srv.URL + "/api/search?q=refund+approval&scopes=domain"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}

	resp, err := client.Get(srv.URL + "/api/gaps?days=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var gaps struct {
		Enabled  bool        `json:"enabled"`
		Logged   int         `json:"logged"`
		NoUseful []store.Gap `json:"no_useful"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gaps); err != nil {
		t.Fatal(err)
	}
	if !gaps.Enabled || gaps.Logged != 3 {
		t.Errorf("enabled=%v logged=%d, want true/3", gaps.Enabled, gaps.Logged)
	}
	got := map[string]string{}
	for _, g := range gaps.NoUseful {
		got[g.Query] = g.Reason
	}
	want := map[string]string{"rotate the on-call pager schedule": "no_useful_sections", "zzqxv": "no_results"}
	if len(got) != len(want) {
		t.Fatalf("gaps = %v, want %v", got, want)
	}
	for q, r := range want {
		if got[q] != r {
			t.Errorf("gap %q reason = %q, want %q", q, got[q], r)
		}
	}

	resp, _ = client.Get(srv.URL + "/api/gaps?days=0")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("days=0: status %d", resp.StatusCode)
	}
}

func TestDashboardAssets(t *testing.T) {
	srv := newTestServer(t, testToken)
	resp, err := http.Get(srv.URL + "/assets/hanken-grotesk-latin.woff2")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "font/woff2" {
		t.Errorf("font: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, p := range []string{"/assets/missing.woff2", "/assets/..%2findex.html"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestActionsNeedJSON(t *testing.T) {
	srv := newTestServer(t, testToken)
	client := &http.Client{Transport: bearerTransport{token: testToken, base: http.DefaultTransport}}
	// A form-encoded POST (what another site could send without a
	// preflight) is refused.
	resp, err := client.Post(srv.URL+"/api/stats/reset", "text/plain", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain reset: status %d", resp.StatusCode)
	}
	resp, err = client.Post(srv.URL+"/api/stats/reset", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("json reset: status %d", resp.StatusCode)
	}
	// No OnSync configured in the test server.
	resp, err = client.Post(srv.URL+"/api/sync", "application/json", strings.NewReader(`{"rebuild":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("sync without source: status %d", resp.StatusCode)
	}
}
