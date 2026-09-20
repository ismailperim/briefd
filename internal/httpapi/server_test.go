package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/httpapi"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/mcpserver"
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
	mcpSrv := mcpserver.New(mcpserver.Deps{Store: st, Searcher: searcher, Version: "test"})
	h := httpapi.New(httpapi.Deps{
		Store:    st,
		Searcher: searcher,
		MCP:      mcpserver.Handler(mcpSrv, nil),
		APIToken: token,
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
	if got := strings.Join(names, ","); got != "get_document,list_scopes,search_context" {
		t.Errorf("tools = %s", got)
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
	raw, _ := json.Marshal(res.StructuredContent)
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
