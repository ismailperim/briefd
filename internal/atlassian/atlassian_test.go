package atlassian

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFromHeadersPrefersAtlassianPair(t *testing.T) {
	type want struct {
		email, token string
		scoped, ok   bool
	}
	for _, tc := range []struct {
		name    string
		h       http.Header
		product string
		want    want
	}{
		{"none", nil, Jira, want{}},
		{"atlassian", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Token": {"t"}}, Jira, want{"a@x", "t", false, true}},
		{"legacy confluence", http.Header{"X-Confluence-Email": {"c@x"}, "X-Confluence-Token": {"t"}}, Jira, want{"c@x", "t", false, true}},
		{"both", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Token": {"t"}, "X-Confluence-Email": {"c@x"}, "X-Confluence-Token": {"t"}}, Confluence, want{"a@x", "t", false, true}},
		{"half atlassian falls back", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Confluence-Email": {"c@x"}, "X-Confluence-Token": {"t"}}, Jira, want{"c@x", "t", false, true}},
		{"half only", http.Header{"X-Atlassian-Token": {"t"}}, Jira, want{}},
		{"scoped jira", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Jira-Token": {"j"}}, Jira, want{"a@x", "j", true, true}},
		{"scoped jira is not for confluence", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Jira-Token": {"j"}}, Confluence, want{}},
		{"scoped confluence", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Confluence-Token": {"c"}}, Confluence, want{"a@x", "c", true, true}},
		{"scoped wins over classic", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Token": {"t"}, "X-Atlassian-Jira-Token": {"j"}}, Jira, want{"a@x", "j", true, true}},
		{"classic for the other product", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Token": {"t"}, "X-Atlassian-Jira-Token": {"j"}}, Confluence, want{"a@x", "t", false, true}},
		{"scoped token without email", http.Header{"X-Atlassian-Jira-Token": {"j"}, "X-Confluence-Email": {"c@x"}, "X-Confluence-Token": {"t"}}, Jira, want{"c@x", "t", false, true}},
	} {
		cr, ok := FromHeaders(tc.h, tc.product)
		if got := (want{cr.Email, cr.Token, cr.Scoped, ok}); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	h := http.Header{"X-Atlassian-Email": {"me@x"}, "X-Atlassian-Token": {"t"}}
	svc := Credentials{Email: "svc@x", Token: "s", Scoped: true}
	if cr, _ := Resolve(h, Jira, true, svc); cr.Email != "me@x" || cr.Scoped {
		t.Errorf("caller credentials not preferred: %+v", cr)
	}
	if cr, _ := Resolve(h, Jira, false, svc); cr.Email != "svc@x" || !cr.Scoped {
		t.Errorf("caller credentials used although user_credentials is off: %+v", cr)
	}
	if _, err := Resolve(nil, Jira, true, Credentials{}); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("want ErrNoCredentials, got %v", err)
	}
}

// fakeAtlassian serves a site (tenant_info, the classic API) and the API
// gateway, recording which one each request reached.
type fakeAtlassian struct {
	site, gateway *httptest.Server
	tenantCalls   atomic.Int32
	lastGateway   atomic.Value // path of the last gateway request
	siteDown      atomic.Bool
}

const testCloudID = "11111111-2222-3333-4444-555555555555"

func newFakeAtlassian(t *testing.T) *fakeAtlassian {
	t.Helper()
	f := &fakeAtlassian{}
	f.site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.siteDown.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/_edge/tenant_info" {
			f.tenantCalls.Add(1)
			_, _ = w.Write([]byte(`{"cloudId":"` + testCloudID + `"}`))
			return
		}
		if _, tok, _ := r.BasicAuth(); tok != "classic" {
			w.WriteHeader(http.StatusUnauthorized) // a scoped token at the site URL
			return
		}
		_, _ = w.Write([]byte(`{"where":"site"}`))
	}))
	f.gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastGateway.Store(r.URL.Path)
		if _, tok, _ := r.BasicAuth(); tok != "scoped" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/noscope") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"where":"gateway"}`))
	}))
	t.Cleanup(f.site.Close)
	t.Cleanup(f.gateway.Close)
	return f
}

func TestClassicAndScopedRouting(t *testing.T) {
	f := newFakeAtlassian(t)
	ctx := context.Background()
	c := NewClient("Jira", f.site.URL, Options{Gateway: f.gateway.URL})
	var out struct{ Where string }

	if err := c.Get(ctx, Credentials{Email: "a@x", Token: "classic"}, "/rest/api/3/issue/A-1", &out); err != nil || out.Where != "site" {
		t.Fatalf("classic: %v %+v", err, out)
	}
	if f.tenantCalls.Load() != 0 {
		t.Error("classic requests must not look up the cloud ID")
	}
	scoped := Credentials{Email: "a@x", Token: "scoped", Scoped: true}
	for range 2 {
		if err := c.Get(ctx, scoped, "/rest/api/3/search/jql?jql=x", &out); err != nil || out.Where != "gateway" {
			t.Fatalf("scoped: %v %+v", err, out)
		}
	}
	if got := f.lastGateway.Load(); got != "/ex/jira/"+testCloudID+"/rest/api/3/search/jql" {
		t.Errorf("gateway path = %v", got)
	}
	if f.tenantCalls.Load() != 1 {
		t.Errorf("cloud ID looked up %d times, want once", f.tenantCalls.Load())
	}

	err := c.Get(ctx, scoped, "/rest/api/3/noscope?x=secret", &out)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden || !strings.Contains(err.Error(), "scope for /rest/api/3/noscope") || strings.Contains(err.Error(), "secret") {
		t.Errorf("scope error = %v", err)
	}

	// A configured cloud ID is used as is; Confluence paths keep /wiki.
	conf := NewClient("Confluence", f.site.URL+"/wiki", Options{Gateway: f.gateway.URL, CloudID: testCloudID})
	if err := conf.Get(ctx, Credentials{Email: "a@x", Token: "scoped", Scoped: true}, "/wiki/api/v2/pages/1", &out); err != nil {
		t.Fatal(err)
	}
	if got := f.lastGateway.Load(); got != "/ex/confluence/"+testCloudID+"/wiki/api/v2/pages/1" {
		t.Errorf("confluence gateway path = %v", got)
	}
	if f.tenantCalls.Load() != 1 {
		t.Error("a configured cloud ID must not be looked up")
	}
}

func TestCloudIDLookupFailureIsRetried(t *testing.T) {
	f := newFakeAtlassian(t)
	f.siteDown.Store(true)
	c := NewClient("Jira", f.site.URL, Options{Gateway: f.gateway.URL})
	scoped := Credentials{Email: "a@x", Token: "scoped", Scoped: true}
	var out any
	err := c.Get(context.Background(), scoped, "/rest/api/3/issue/A-1", &out)
	if !errors.Is(err, ErrCloudID) || !strings.Contains(err.Error(), "BRIEFD_ATLASSIAN_CLOUD_ID") {
		t.Fatalf("err = %v", err)
	}
	f.siteDown.Store(false)
	if err := c.Get(context.Background(), scoped, "/rest/api/3/issue/A-1", &out); !errors.Is(err, ErrCloudID) {
		t.Errorf("a failed lookup should be remembered briefly: %v", err)
	}
	c.mu.Lock()
	c.cloudErrAt = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	if err := c.Get(context.Background(), scoped, "/rest/api/3/issue/A-1", &out); err != nil {
		t.Errorf("after the retry delay: %v", err)
	}
}

func TestHealth(t *testing.T) {
	f := newFakeAtlassian(t)
	ctx := context.Background()
	c := NewClient("Jira", f.site.URL, Options{Gateway: f.gateway.URL})
	if h := c.Health(ctx); h.Status != "idle" || !h.Reachable || h.CloudID != "unresolved" || h.Requests != 0 {
		t.Errorf("fresh = %+v", h)
	}
	var out any
	_ = c.Get(ctx, Credentials{Email: "a@x", Token: "classic"}, "/rest/api/3/issue/A-1", &out)
	if h := c.Health(ctx); h.Status != "ok" || h.Classic != 1 || h.LastOKAt.IsZero() {
		t.Errorf("after a success = %+v", h)
	}
	_ = c.Get(ctx, Credentials{Email: "a@x", Token: "wrong", Scoped: true}, "/rest/api/3/search/jql?jql=text~secret", &out)
	h := c.Health(ctx)
	if h.Status != "degraded" || h.Failures != 1 || h.Scoped != 1 || h.CloudID != "resolved" ||
		!strings.Contains(h.LastError, "401") || !strings.Contains(h.LastError, "scoped") || strings.Contains(h.LastError, "secret") {
		t.Errorf("after a scoped 401 = %+v", h)
	}

	// The probe runs at most once a minute.
	f.siteDown.Store(true)
	if h := c.Health(ctx); !h.Reachable {
		t.Error("probe repeated within a minute")
	}
	c.mu.Lock()
	c.probedAt = time.Time{}
	c.mu.Unlock()
	if h := c.Health(ctx); h.Reachable || h.Status != "down" || h.ProbeError != "HTTP 503" {
		t.Errorf("site down = %+v", h)
	}

	if h := NewClient("Jira", "http://127.0.0.1:1", Options{}).Health(ctx); h.Reachable || h.ProbeError != "network error" {
		t.Errorf("unreachable = %+v", h)
	}
}

func TestValidCloudID(t *testing.T) {
	if !ValidCloudID(testCloudID) || ValidCloudID("../../x") || ValidCloudID("") {
		t.Error("cloud ID validation")
	}
}

func TestQuoteCannotEndTheLiteral(t *testing.T) {
	for in, want := range map[string]string{
		`refund`:              `"refund"`,
		`a "b" c`:             `"a \"b\" c"`,
		`x\" OR project = Y`:  `"x\\\" OR project = Y"`,
		"multi\n  line\ttext": `"multi line text"`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSiteBase(t *testing.T) {
	for _, in := range []string{"https://x.atlassian.net", "https://x.atlassian.net/", "https://x.atlassian.net/wiki", "https://x.atlassian.net/wiki/"} {
		if got := SiteBase(in); got != "https://x.atlassian.net" {
			t.Errorf("SiteBase(%q) = %q", in, got)
		}
	}
}
