package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSite serves the three endpoints the client uses and records the CQL
// and credentials of the last search.
func fakeSite(t *testing.T) (*httptest.Server, *string, *string) {
	t.Helper()
	var cql, user string
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/rest/api/search", func(w http.ResponseWriter, r *http.Request) {
		cql = r.URL.Query().Get("cql")
		user, _, _ = r.BasicAuth()
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
			map[string]any{
				"content":      map[string]any{"id": "101", "title": "Chargeback flow", "space": map[string]any{"key": "PAY"}, "_links": map[string]any{"webui": "/spaces/PAY/pages/101/Chargeback+flow"}},
				"excerpt":      "Disputes open a @@@hl@@@chargeback@@@endhl@@@\n case",
				"lastModified": "2026-10-01T10:00:00.000Z",
			},
			map[string]any{"title": "a space, not a page"},
		}})
	})
	mux.HandleFunc("/wiki/api/v2/pages/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/pages/")
		if id == "404" {
			http.NotFound(w, r)
			return
		}
		space := "1"
		if id == "202" {
			space = "2"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "title": "Chargeback flow", "spaceId": space,
			"version": map[string]any{"number": 7, "createdAt": "2026-10-01T10:00:00.000Z"},
			"body":    map[string]any{"view": map[string]any{"value": "<h2>Steps</h2><p>Open a <strong>case</strong>.</p>"}},
			"_links":  map[string]any{"webui": "/spaces/PAY/pages/" + id},
		})
	})
	mux.HandleFunc("/wiki/api/v2/spaces/", func(w http.ResponseWriter, r *http.Request) {
		key := map[string]string{"1": "PAY", "2": "HR"}[strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/spaces/")]
		_ = json.NewEncoder(w).Encode(map[string]any{"key": key})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &cql, &user
}

func TestSearchAndPage(t *testing.T) {
	srv, cql, user := fakeSite(t)
	c := New(Config{BaseURL: srv.URL, Email: "svc@example.com", Token: "t", Spaces: []string{"PAY"}, UserCredentials: true})
	ctx := context.Background()

	cr, err := c.Resolve(http.Header{"X-Confluence-Email": {"me@example.com"}, "X-Confluence-Token": {"mine"}})
	if err != nil || cr.Email != "me@example.com" {
		t.Fatalf("caller credentials not preferred: %+v %v", cr, err)
	}
	hits, err := c.Search(ctx, cr, `chargeback "flow"`, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if *user != "me@example.com" {
		t.Errorf("search used %q", *user)
	}
	if want := `type = page AND siteSearch ~ "chargeback \"flow\"" AND space IN ("PAY")`; *cql != want {
		t.Errorf("cql = %s\nwant  %s", *cql, want)
	}
	if len(hits) != 1 || hits[0].ID != "101" || hits[0].Space != "PAY" || hits[0].Excerpt != "Disputes open a chargeback case" ||
		hits[0].URL != srv.URL+"/wiki/spaces/PAY/pages/101/Chargeback+flow" || hits[0].UpdatedAt.IsZero() {
		t.Errorf("hits = %+v", hits)
	}

	if _, err := c.Search(ctx, cr, "x", []string{"HR"}, 5); err == nil {
		t.Error("search outside the allowed spaces should fail")
	}

	p, err := c.Page(ctx, cr, "101")
	if err != nil {
		t.Fatal(err)
	}
	if p.Space != "PAY" || p.Version != 7 || p.Markdown != "## Steps\n\nOpen a **case**." {
		t.Errorf("page = %+v / %q", p, p.Markdown)
	}
	if _, err := c.Page(ctx, cr, "202"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("page in a disallowed space: %v", err)
	}
	var se *StatusError
	if _, err := c.Page(ctx, cr, "404"); !errors.As(err, &se) || se.Code != 404 {
		t.Errorf("missing page: %v", err)
	}
	if _, err := c.Page(ctx, cr, "../spaces/1"); err == nil {
		t.Error("non-numeric page id should be rejected")
	}
}

func TestResolve(t *testing.T) {
	svc := New(Config{BaseURL: "https://x.atlassian.net", Email: "svc@x", Token: "t"})
	if cr, _ := svc.Resolve(http.Header{"X-Confluence-Email": {"me"}, "X-Confluence-Token": {"t"}}); cr.Email != "svc@x" {
		t.Errorf("caller credentials used although user_credentials is off: %+v", cr)
	}
	callerOnly := New(Config{BaseURL: "https://x.atlassian.net", UserCredentials: true})
	if _, err := callerOnly.Resolve(nil); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("want ErrNoCredentials, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		cfg Config
		ok  bool
	}{
		{Config{}, true},
		{Config{BaseURL: "https://x.atlassian.net", UserCredentials: true}, true},
		{Config{BaseURL: "https://x.atlassian.net", Email: "a", Token: "b", Spaces: []string{"ENG", "~me"}}, true},
		{Config{BaseURL: "http://x.atlassian.net", UserCredentials: true}, false},
		{Config{BaseURL: "https://x.atlassian.net", Email: "a"}, false},
		{Config{BaseURL: "https://x.atlassian.net"}, false},
		{Config{BaseURL: "https://x.atlassian.net", UserCredentials: true, Spaces: []string{`ENG") OR ("`}}, false},
	} {
		if err := tc.cfg.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: err = %v", tc.cfg, err)
		}
	}
}

func TestToMarkdown(t *testing.T) {
	src := `<div class="wrap"><h1>Refunds</h1>
<p>Refunds go through the <a href="https://example.com/ledger">ledger</a> and take <em>two</em> days.</p>
<ul><li>Partial refunds<ul><li>allowed once</li></ul></li><li>Full refunds</li></ul>
<ol><li>Open</li><li>Close</li></ol>
<table><tbody><tr><th>Code</th><th>Meaning</th></tr><tr><td>R1</td><td>a|b</td></tr></tbody></table>
<pre class="language-go">x := 1
y := 2</pre>
<blockquote><p>Never retry a 4xx.</p></blockquote>
<script>alert(1)</script><img src="x.png"></div>`
	want := "# Refunds\n\n" +
		"Refunds go through the [ledger](https://example.com/ledger) and take _two_ days.\n\n" +
		"- Partial refunds\n  - allowed once\n- Full refunds\n\n" +
		"1. Open\n2. Close\n\n" +
		"| Code | Meaning |\n| --- | --- |\n| R1 | a\\|b |\n\n" +
		"```go\nx := 1\ny := 2\n```\n\n" +
		"> Never retry a 4xx."
	if got := ToMarkdown(src); got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
}
