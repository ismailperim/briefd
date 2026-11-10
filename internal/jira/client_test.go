package jira

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ismailperim/briefd/internal/atlassian"
)

// fakeSite serves the issue, remote-link and search endpoints the client
// uses and records the last JQL and authenticated account.
func fakeSite(t *testing.T) (*httptest.Server, *string, *string) {
	t.Helper()
	var jql, user string
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/issue/PAY-7", func(w http.ResponseWriter, r *http.Request) {
		user, _, _ = r.BasicAuth()
		if !strings.Contains(r.URL.Query().Get("fields"), "customfield_10035") || !strings.Contains(r.URL.Query().Get("expand"), "renderedFields") {
			t.Errorf("unexpected query %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"key": "PAY-7",
			"fields": map[string]any{
				"summary":   "Partial refunds for card payments",
				"status":    map[string]any{"name": "In Progress"},
				"issuetype": map[string]any{"name": "Story"},
				"priority":  map[string]any{"name": "High"},
				"assignee":  map[string]any{"displayName": "Dana Example"},
				"project":   map[string]any{"key": "PAY"},
				"labels":    []any{"refunds"},
				"parent":    map[string]any{"key": "PAY-1", "fields": map[string]any{"summary": "Refunds epic", "status": map[string]any{"name": "Open"}}},
				"issuelinks": []any{
					map[string]any{"type": map[string]any{"inward": "is blocked by", "outward": "blocks"}, "outwardIssue": map[string]any{"key": "PAY-8", "fields": map[string]any{"summary": "Ledger reversal API"}}},
				},
				"updated":           "2026-10-02T09:30:00.000+0000",
				"description":       map[string]any{"type": "doc", "content": []any{}},
				"customfield_10035": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Refund never exceeds the captured amount"}}}}},
				"customfield_10040": map[string]any{"value": "Team Ledger"},
				"comment": map[string]any{"comments": []any{
					map[string]any{"id": "1", "author": map[string]any{"displayName": "Ali"}, "created": "2026-10-01T10:00:00.000+0000"},
					map[string]any{"id": "2", "author": map[string]any{"displayName": "Bo"}, "created": "2026-10-02T10:00:00.000+0000"},
				}},
			},
			"renderedFields": map[string]any{
				"description": "<p>Merchants refund <strong>part</strong> of a captured payment.</p>",
				"comment": map[string]any{"comments": []any{
					map[string]any{"id": "1", "body": "<p>First comment</p>"},
					map[string]any{"id": "2", "body": "<p>Second comment</p>"},
				}},
			},
			"names": map[string]any{"customfield_10035": "Acceptance criteria", "customfield_10040": "Team"},
		})
	})
	mux.HandleFunc("/rest/api/3/issue/PAY-7/remotelink", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{
			map[string]any{"globalId": "appId=abc&pageId=4242", "application": map[string]any{"type": "com.atlassian.confluence"},
				"object": map[string]any{"title": "Refund design", "url": "https://x.atlassian.net/wiki/pages/viewpage.action?pageId=4242"}},
			map[string]any{"object": map[string]any{"title": "Status page", "url": "https://status.example.com"}},
		})
	})
	mux.HandleFunc("/rest/api/3/issue/HR-1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"key":"HR-1","fields":{"project":{"key":"HR"}}}`))
	})
	mux.HandleFunc("/rest/api/3/issue/PAY-9", func(w http.ResponseWriter, _ *http.Request) {
		// moved to another project after it was created
		_, _ = w.Write([]byte(`{"key":"HR-2","fields":{"project":{"key":"HR"}}}`))
	})
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		jql = r.URL.Query().Get("jql")
		user, _, _ = r.BasicAuth()
		_, _ = w.Write([]byte(`{"issues":[{"key":"PAY-7","fields":{"summary":"Partial refunds","status":{"name":"In Progress"},"issuetype":{"name":"Story"},"updated":"2026-10-02T09:30:00.000+0000"}}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &jql, &user
}

func TestIssue(t *testing.T) {
	srv, _, user := fakeSite(t)
	c := New(Config{BaseURL: srv.URL, Projects: []string{"PAY"}, Fields: []string{"customfield_10035", "customfield_10040"}, UserCredentials: true})
	ctx := context.Background()
	cr := atlassian.Credentials{Email: "me@example.com", Token: "t"}

	is, err := c.Issue(ctx, cr, "pay-7")
	if err != nil {
		t.Fatal(err)
	}
	if *user != "me@example.com" {
		t.Errorf("issue read as %q", *user)
	}
	if is.Key != "PAY-7" || is.Status != "In Progress" || is.Assignee != "Dana Example" || is.Parent == nil || is.Parent.Key != "PAY-1" ||
		len(is.Links) != 1 || is.Links[0].Relation != "blocks" || is.URL != srv.URL+"/browse/PAY-7" || is.Updated.IsZero() {
		t.Errorf("issue = %+v", is)
	}
	if is.Description != "Merchants refund **part** of a captured payment." {
		t.Errorf("description = %q", is.Description)
	}
	if len(is.Fields) != 2 || is.Fields[0].Name != "Acceptance criteria" || is.Fields[0].Markdown != "Refund never exceeds the captured amount" || is.Fields[1].Markdown != "Team Ledger" {
		t.Errorf("fields = %+v", is.Fields)
	}
	if len(is.Pages) != 1 || is.Pages[0].PageID != "4242" {
		t.Errorf("pages = %+v", is.Pages)
	}
	md := is.Markdown(true)
	for _, want := range []string{"# PAY-7: Partial refunds", "- blocks PAY-8 Ledger reversal API", "Refund design · id 4242", "## Acceptance criteria", "## Comments (2, newest first)"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Index(md, "Second comment") > strings.Index(md, "First comment") {
		t.Error("comments should be newest first")
	}
	if strings.Contains(is.Markdown(false), "Comments") {
		t.Error("comments rendered although not asked for")
	}

	if _, err := c.Issue(ctx, cr, "HR-1"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("issue in a disallowed project: %v", err)
	}
	if _, err := c.Issue(ctx, cr, "PAY-9"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("issue moved to a disallowed project: %v", err)
	}
	if _, err := c.Issue(ctx, cr, "../../myself"); err == nil {
		t.Error("malformed key should be rejected")
	}
	var se *atlassian.StatusError
	if _, err := c.Issue(ctx, cr, "PAY-404"); !errors.As(err, &se) || se.Code != 404 {
		t.Errorf("missing issue: %v", err)
	}
}

func TestSearch(t *testing.T) {
	srv, jql, _ := fakeSite(t)
	c := New(Config{BaseURL: srv.URL, Projects: []string{"PAY", "LED"}, UserCredentials: true})
	ctx := context.Background()
	cr := atlassian.Credentials{Email: "me@example.com", Token: "t"}
	hits, err := c.Search(ctx, cr, Query{Text: "partial refund", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if want := `project IN ("PAY", "LED") AND text ~ "partial refund" ORDER BY updated DESC`; *jql != want {
		t.Errorf("jql = %s\nwant  %s", *jql, want)
	}
	if len(hits) != 1 || hits[0].Key != "PAY-7" || hits[0].Updated.IsZero() || hits[0].URL != srv.URL+"/browse/PAY-7" {
		t.Errorf("hits = %+v", hits)
	}
	if _, err := c.Search(ctx, cr, Query{Text: "x", Projects: []string{"HR"}}); err == nil {
		t.Error("search outside the allowed projects should fail")
	}
}

func TestJQL(t *testing.T) {
	for _, tc := range []struct {
		q    Query
		want string
		ok   bool
	}{
		{Query{}, "", false},
		{Query{Text: "  "}, "", false},
		{Query{Text: "refund", Projects: []string{"pay"}}, `project IN ("PAY") AND text ~ "refund" ORDER BY updated DESC`, true},
		{Query{Assignee: "me", OpenOnly: true}, `assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`, true},
		{Query{Assignee: "unassigned", Type: "Bug", Status: "To Do"}, `status = "To Do" AND issuetype = "Bug" AND assignee IS EMPTY ORDER BY updated DESC`, true},
		{Query{Assignee: "someone"}, "", false},
		{Query{Text: `x" OR project = HR OR text ~ "y`}, `text ~ "x\" OR project = HR OR text \"y" ORDER BY updated DESC`, true},
		{Query{Text: "retry-policy (v2)*"}, `text ~ "retry policy v2" ORDER BY updated DESC`, true},
		{Query{Status: `Done" OR status != "x`}, `status = "Done\" OR status != \"x" ORDER BY updated DESC`, true},
	} {
		got, err := JQL(tc.q)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%+v:\n got %s (%v)\nwant %s", tc.q, got, err, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		cfg Config
		ok  bool
	}{
		{Config{}, true},
		{Config{BaseURL: "https://x.atlassian.net", UserCredentials: true, Projects: []string{"PAY"}, Fields: []string{"customfield_10035"}}, true},
		{Config{BaseURL: "https://x.atlassian.net", Email: "a", Token: "b"}, true},
		{Config{BaseURL: "http://x.atlassian.net", UserCredentials: true}, false},
		{Config{BaseURL: "https://x.atlassian.net"}, false},
		{Config{BaseURL: "https://x.atlassian.net", UserCredentials: true, Projects: []string{`PAY") OR ("`}}, false},
		{Config{BaseURL: "https://x.atlassian.net", UserCredentials: true, Fields: []string{"customfield_1,comment"}}, false},
	} {
		if err := tc.cfg.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: err = %v", tc.cfg, err)
		}
	}
}
