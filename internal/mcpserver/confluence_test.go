package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/confluence"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

type headerTransport struct{ h http.Header }

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	for k, v := range t.h {
		r.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(r)
}

// fakeConfluence serves a search with many hits and one long page, and
// records which account each request authenticated as.
func fakeConfluence(t *testing.T, users *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/rest/api/search", func(w http.ResponseWriter, r *http.Request) {
		u, _, _ := r.BasicAuth()
		*users = append(*users, u)
		var results []any
		for i := 1; i <= 25; i++ {
			results = append(results, map[string]any{
				"content": map[string]any{"id": fmt.Sprint(i), "title": fmt.Sprintf("Settlement runbook %d", i), "space": map[string]any{"key": "PAY"},
					"_links": map[string]any{"webui": fmt.Sprintf("/spaces/PAY/pages/%d", i)}},
				"excerpt": strings.Repeat("settlement batches close at midnight ", 6),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	mux.HandleFunc("/wiki/api/v2/pages/7", func(w http.ResponseWriter, r *http.Request) {
		u, _, _ := r.BasicAuth()
		*users = append(*users, u)
		var body strings.Builder
		for i := range 200 {
			fmt.Fprintf(&body, "<h2>Step %d</h2><p>Reconcile the batch against the ledger before closing it.</p>", i)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "7", "title": "Settlement runbook", "spaceId": "1",
			"version": map[string]any{"number": 3, "createdAt": "2026-10-01T10:00:00Z"},
			"body":    map[string]any{"view": map[string]any{"value": body.String()}},
		})
	})
	mux.HandleFunc("/wiki/api/v2/spaces/1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"key":"PAY"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func connect(t *testing.T, d Deps, h http.Header) *mcp.ClientSession {
	t.Helper()
	web := httptest.NewServer(Handler(New(d), nil))
	t.Cleanup(web.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: web.URL, HTTPClient: &http.Client{Transport: headerTransport{h}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func text(res *mcp.CallToolResult) string {
	return res.Content[0].(*mcp.TextContent).Text
}

func TestConfluenceToolsOnlyWhenConfigured(t *testing.T) {
	if names := toolNames(t, connect(t, Deps{}, nil)); slices.Contains(names, "confluence_search") {
		t.Errorf("confluence tools listed without configuration: %v", names)
	}
	c := confluence.New(confluence.Config{BaseURL: "https://x.atlassian.net", UserCredentials: true})
	names := toolNames(t, connect(t, Deps{Confluence: c}, nil))
	if !slices.Contains(names, "confluence_search") || !slices.Contains(names, "confluence_page") {
		t.Errorf("confluence tools missing: %v", names)
	}
}

func TestConfluenceToolsRespectBudgetAndCallerCredentials(t *testing.T) {
	var users []string
	site := fakeConfluence(t, &users)
	c := confluence.New(confluence.Config{BaseURL: site.URL, Email: "svc@example.com", Token: "t", UserCredentials: true})
	ctx := context.Background()

	anon := connect(t, Deps{Confluence: c}, nil)
	for _, budget := range []int{60, 150, 400, 1000} {
		res, err := anon.CallTool(ctx, &mcp.CallToolParams{Name: "confluence_search", Arguments: map[string]any{"query": "settlement", "max_tokens": budget, "limit": 25}})
		if err != nil || res.IsError {
			t.Fatalf("search at %d: %v %v", budget, err, res)
		}
		if n := tokenizer.Count(text(res)); n > budget {
			t.Errorf("search used %d tokens, budget %d", n, budget)
		}
		var out ConfluenceSearchOutput
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
		if budget >= 150 && (len(out.Hits) == 0 || out.Omitted == 0) {
			t.Errorf("budget %d: %d hits, %d omitted", budget, len(out.Hits), out.Omitted)
		}
	}
	for _, budget := range []int{200, 800, 3000} {
		res, err := anon.CallTool(ctx, &mcp.CallToolParams{Name: "confluence_page", Arguments: map[string]any{"id": "7", "max_tokens": budget}})
		if err != nil || res.IsError {
			t.Fatalf("page at %d: %v %v", budget, err, res)
		}
		if n := tokenizer.Count(text(res)); n > budget {
			t.Errorf("page used %d tokens, budget %d", n, budget)
		}
		if !strings.Contains(text(res), "truncated") || !strings.Contains(text(res), "## Step 0") {
			t.Errorf("page at %d not truncated from the top:\n%.200s", budget, text(res))
		}
	}
	if res, _ := anon.CallTool(ctx, &mcp.CallToolParams{Name: "confluence_page", Arguments: map[string]any{"id": "7", "max_tokens": 10}}); !res.IsError {
		t.Error("a budget too small for the page header should be an error")
	}
	if users[0] != "svc@example.com" {
		t.Errorf("without headers the service account should be used, got %q", users[0])
	}

	users = nil
	mine := connect(t, Deps{Confluence: c}, http.Header{"X-Confluence-Email": {"me@example.com"}, "X-Confluence-Token": {"mine"}})
	if _, err := mine.CallTool(ctx, &mcp.CallToolParams{Name: "confluence_search", Arguments: map[string]any{"query": "settlement"}}); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0] != "me@example.com" {
		t.Errorf("caller credentials not forwarded: %v", users)
	}
}
