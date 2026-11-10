package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/jira"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

// secretMarker appears only in the fake issue's description; it must never
// reach the store.
const secretMarker = "zebracorn"

// fakeJira serves one long issue and a search with many hits, recording
// which account each request authenticated as.
func fakeJira(t *testing.T, users *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/issue/PAY-7", func(w http.ResponseWriter, r *http.Request) {
		u, _, _ := r.BasicAuth()
		*users = append(*users, u)
		var desc strings.Builder
		desc.WriteString("<p>Merchants need partial refunds of captured card payments; refunds post a ledger reversal (" + secretMarker + ").</p>")
		for i := range 60 {
			fmt.Fprintf(&desc, "<p>Edge case %d: a refund larger than the remaining captured amount is rejected.</p>", i)
		}
		var raw, rendered []any
		for i := range 30 {
			raw = append(raw, map[string]any{"id": fmt.Sprint(i), "author": map[string]any{"displayName": "Reviewer"}, "created": "2026-10-01T10:00:00.000+0000"})
			rendered = append(rendered, map[string]any{"id": fmt.Sprint(i), "body": fmt.Sprintf("<p>Comment %d about idempotency keys.</p>", i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"key": "PAY-7",
			"fields": map[string]any{
				"summary": "Partial refunds for card payments", "status": map[string]any{"name": "In Progress"},
				"project": map[string]any{"key": "PAY"}, "comment": map[string]any{"comments": raw},
			},
			"renderedFields": map[string]any{"description": desc.String(), "comment": map[string]any{"comments": rendered}},
		})
	})
	mux.HandleFunc("/rest/api/3/issue/PAY-7/remotelink", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		u, _, _ := r.BasicAuth()
		*users = append(*users, u)
		var issues []any
		for i := 1; i <= 25; i++ {
			issues = append(issues, map[string]any{"key": fmt.Sprintf("PAY-%d", i), "fields": map[string]any{
				"summary": fmt.Sprintf("Settlement batch reconciliation follow-up %d", i), "status": map[string]any{"name": "To Do"},
				"issuetype": map[string]any{"name": "Task"}, "updated": "2026-10-01T10:00:00.000+0000",
			}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issues": issues})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// knowledgeDeps indexes the sample knowledge base into the database at
// the returned path.
func knowledgeDeps(t *testing.T) (Deps, string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := indexer.Run(context.Background(), st, indexer.Options{Root: "../../testdata/knowledge"}); err != nil {
		t.Fatal(err)
	}
	s := search.New(st, search.Options{})
	return Deps{Store: st, Searcher: s, Compiler: bundle.New(st, s, bundle.Options{}), QueryLog: true}, db
}

func schemaProps(t *testing.T, cs *mcp.ClientSession, tool string) map[string]any {
	t.Helper()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Tools {
		if tl.Name == tool {
			b, _ := json.Marshal(tl.InputSchema)
			var s struct {
				Properties map[string]any `json:"properties"`
			}
			_ = json.Unmarshal(b, &s)
			return s.Properties
		}
	}
	t.Fatalf("tool %s not listed", tool)
	return nil
}

func TestJiraToolsOnlyWhenConfigured(t *testing.T) {
	plain := connect(t, Deps{}, nil)
	if names := toolNames(t, plain); slices.Contains(names, "jira_search") || slices.Contains(names, "jira_issue") {
		t.Errorf("jira tools listed without configuration: %v", names)
	}
	if _, ok := schemaProps(t, plain, "compile_bundle")["issue"]; ok {
		t.Error("compile_bundle offers issue without Jira")
	}
	j := jira.New(jira.Config{BaseURL: "https://x.atlassian.net", UserCredentials: true})
	withJira := connect(t, Deps{Jira: j}, nil)
	names := toolNames(t, withJira)
	if !slices.Contains(names, "jira_search") || !slices.Contains(names, "jira_issue") {
		t.Errorf("jira tools missing: %v", names)
	}
	if _, ok := schemaProps(t, withJira, "compile_bundle")["issue"]; !ok {
		t.Error("compile_bundle lacks issue with Jira configured")
	}
}

func TestJiraToolsRespectBudget(t *testing.T) {
	var users []string
	site := fakeJira(t, &users)
	j := jira.New(jira.Config{BaseURL: site.URL, Email: "svc@example.com", Token: "t", UserCredentials: true})
	cs := connect(t, Deps{Jira: j}, nil)
	ctx := context.Background()
	for _, budget := range []int{40, 120, 400, 800} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "jira_search", Arguments: map[string]any{"text": "settlement", "limit": 25, "max_tokens": budget}})
		if err != nil || res.IsError {
			t.Fatalf("search at %d: %v %v", budget, err, res)
		}
		if n := tokenizer.Count(text(res)); n > budget {
			t.Errorf("search used %d tokens, budget %d", n, budget)
		}
	}
	for _, budget := range []int{200, 600, 2000} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "jira_issue", Arguments: map[string]any{"key": "PAY-7", "max_tokens": budget}})
		if err != nil || res.IsError {
			t.Fatalf("issue at %d: %v %v", budget, err, res)
		}
		got := text(res)
		if n := tokenizer.Count(got); n > budget {
			t.Errorf("issue used %d tokens, budget %d", n, budget)
		}
		if !strings.HasPrefix(got, "# PAY-7: Partial refunds") || (budget < 2000) != strings.Contains(got, "truncated") {
			t.Errorf("issue at %d not cut from the end:\n%.200s", budget, got)
		}
	}
	if res, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "jira_search", Arguments: map[string]any{}}); !res.IsError {
		t.Error("an unbounded search should be an error")
	}
}

func TestJiraCallerCredentials(t *testing.T) {
	var users []string
	site := fakeJira(t, &users)
	j := jira.New(jira.Config{BaseURL: site.URL, Email: "svc@example.com", Token: "t", UserCredentials: true})
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		h    http.Header
		want string
	}{
		{"none", nil, "svc@example.com"},
		{"atlassian", http.Header{"X-Atlassian-Email": {"me@example.com"}, "X-Atlassian-Token": {"mine"}}, "me@example.com"},
		{"confluence headers carry over", http.Header{"X-Confluence-Email": {"old@example.com"}, "X-Confluence-Token": {"mine"}}, "old@example.com"},
	} {
		users = nil
		cs := connect(t, Deps{Jira: j}, tc.h)
		if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "jira_search", Arguments: map[string]any{"text": "x"}}); err != nil {
			t.Fatal(err)
		}
		if len(users) != 1 || users[0] != tc.want {
			t.Errorf("%s: authenticated as %v, want %s", tc.name, users, tc.want)
		}
	}
}

func TestCompileBundleWithIssue(t *testing.T) {
	var users []string
	site := fakeJira(t, &users)
	d, db := knowledgeDeps(t)
	d.Jira = jira.New(jira.Config{BaseURL: site.URL, Email: "svc@example.com", Token: "t", UserCredentials: true})
	cs := connect(t, d, nil)
	ctx := context.Background()

	for _, budget := range []int{300, 800, 2000, 4000} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "compile_bundle", Arguments: map[string]any{"issue": "pay-7", "max_tokens": budget}})
		if err != nil || res.IsError {
			t.Fatalf("bundle at %d: %v %+v", budget, err, res)
		}
		got := text(res)
		if n := tokenizer.Count(got); n > search.Budget(budget) {
			t.Errorf("bundle used %d tokens, budget %d", n, budget)
		}
		var out CompileBundleIssueOutput
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
		if !strings.HasPrefix(got, "# PAY-7: Partial refunds") || out.Issue == nil || out.Issue.Key != "PAY-7" || out.Tokens != tokenizer.Count(got) {
			t.Errorf("bundle at %d: issue missing or miscounted: %+v\n%.200s", budget, out.Issue, got)
		}
		if budget >= 2000 && (len(out.Sections) == 0 || !strings.Contains(got, "<!-- briefd bundle")) {
			t.Errorf("bundle at %d has no knowledge sections:\n%s", budget, got)
		}
	}

	// The plain path still works and still requires a task.
	if res, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "compile_bundle", Arguments: map[string]any{}}); !res.IsError {
		t.Error("compile_bundle without task or issue should be an error")
	}
	if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "compile_bundle", Arguments: map[string]any{"task_description": "add partial refunds"}}); err != nil || res.IsError {
		t.Errorf("plain compile_bundle: %v %+v", err, res)
	}

	// Only the issue key is recorded: the issue's text never reaches the
	// database (bundle cache, query log) in any form.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "compile_bundle", Arguments: map[string]any{"issue": "PAY-7", "task_description": "add partial refunds"}})
	if err != nil || res.IsError {
		t.Fatal(err, res)
	}
	var out CompileBundleIssueOutput
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	if stored, err := d.Store.GetBundle(ctx, out.BundleID); err != nil || stored.Task != "PAY-7: add partial refunds" {
		t.Errorf("stored bundle task: %v %+v", err, stored)
	}
	_ = d.Store.Close()
	for _, f := range []string{db, db + "-wal"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if bytes.Contains(raw, []byte(secretMarker)) || bytes.Contains(raw, []byte("Partial refunds for card")) {
			t.Errorf("issue content stored in %s", filepath.Base(f))
		}
	}
}
