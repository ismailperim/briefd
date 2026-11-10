package atlassian

import (
	"errors"
	"net/http"
	"testing"
)

func TestFromHeadersPrefersAtlassianPair(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.Header
		want string
		ok   bool
	}{
		{"none", nil, "", false},
		{"atlassian", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Token": {"t"}}, "a@x", true},
		{"legacy confluence", http.Header{"X-Confluence-Email": {"c@x"}, "X-Confluence-Token": {"t"}}, "c@x", true},
		{"both", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Atlassian-Token": {"t"}, "X-Confluence-Email": {"c@x"}, "X-Confluence-Token": {"t"}}, "a@x", true},
		{"half atlassian falls back", http.Header{"X-Atlassian-Email": {"a@x"}, "X-Confluence-Email": {"c@x"}, "X-Confluence-Token": {"t"}}, "c@x", true},
		{"half only", http.Header{"X-Atlassian-Token": {"t"}}, "", false},
	} {
		cr, ok := FromHeaders(tc.h)
		if ok != tc.ok || cr.Email != tc.want {
			t.Errorf("%s: got %+v %v", tc.name, cr, ok)
		}
	}
}

func TestResolve(t *testing.T) {
	h := http.Header{"X-Atlassian-Email": {"me@x"}, "X-Atlassian-Token": {"t"}}
	svc := Credentials{Email: "svc@x", Token: "s"}
	if cr, _ := Resolve(h, true, svc); cr.Email != "me@x" {
		t.Errorf("caller credentials not preferred: %+v", cr)
	}
	if cr, _ := Resolve(h, false, svc); cr.Email != "svc@x" {
		t.Errorf("caller credentials used although user_credentials is off: %+v", cr)
	}
	if _, err := Resolve(nil, true, Credentials{}); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("want ErrNoCredentials, got %v", err)
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
