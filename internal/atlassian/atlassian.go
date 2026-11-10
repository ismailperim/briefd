// Package atlassian holds what briefd's live Atlassian Cloud tools share
// (ADR-0009, ADR-0010): the caller-credential headers, site URL handling and
// a read-only JSON GET with friendly status errors. Confluence and Jira on
// one Atlassian Cloud site accept the same account email and API token.
package atlassian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Caller-credential headers. HeaderEmail/HeaderToken work for every
// Atlassian tool; the X-Confluence-* pair predates them (v0.7) and is still
// accepted, by Jira too, since it carries the same Atlassian account.
//
//nolint:gosec // G101: header names, not credentials
const (
	HeaderEmail           = "X-Atlassian-Email"
	HeaderToken           = "X-Atlassian-Token"
	LegacyHeaderEmail     = "X-Confluence-Email"
	LegacyHeaderToken     = "X-Confluence-Token"
	defaultRequestTimeout = 15 * time.Second
)

// Credentials authenticate one request (Atlassian account email + API token).
type Credentials struct {
	Email string
	Token string
}

// ErrNoCredentials means neither the caller nor the configuration supplied
// credentials.
var ErrNoCredentials = errors.New("no Atlassian credentials: send X-Atlassian-Email and X-Atlassian-Token headers, or configure a service account (email and token)")

// FromHeaders returns the caller's credentials: the X-Atlassian-* pair
// first, then the legacy X-Confluence-* pair. A half-filled pair is ignored.
func FromHeaders(h http.Header) (Credentials, bool) {
	if h == nil {
		return Credentials{}, false
	}
	for _, p := range [][2]string{{HeaderEmail, HeaderToken}, {LegacyHeaderEmail, LegacyHeaderToken}} {
		email, token := strings.TrimSpace(h.Get(p[0])), strings.TrimSpace(h.Get(p[1]))
		if email != "" && token != "" {
			return Credentials{Email: email, Token: token}, true
		}
	}
	return Credentials{}, false
}

// Resolve picks the credentials for a request: the caller's headers when
// userCredentials allows them, else the service account.
func Resolve(h http.Header, userCredentials bool, service Credentials) (Credentials, error) {
	if userCredentials {
		if cr, ok := FromHeaders(h); ok {
			return cr, nil
		}
	}
	if service.Email != "" && service.Token != "" {
		return service, nil
	}
	return Credentials{}, ErrNoCredentials
}

// ValidateSite checks a site URL; field names the setting in errors.
func ValidateSite(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s %q is not a URL", field, raw)
	}
	if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return fmt.Errorf("%s must use https", field)
	}
	return nil
}

// ValidateAuth checks the service account and caller-credential settings.
func ValidateAuth(product string, service Credentials, userCredentials bool) error {
	if (service.Email == "") != (service.Token == "") {
		return fmt.Errorf("%s: set both email and token, or neither", product)
	}
	if service.Email == "" && !userCredentials {
		return fmt.Errorf("%s: no service credentials and user_credentials is off; nobody could authenticate", product)
	}
	return nil
}

// SiteBase returns the site root: no trailing slash, no /wiki context path.
func SiteBase(raw string) string {
	return strings.TrimSuffix(strings.TrimRight(raw, "/"), "/wiki")
}

// Client performs read-only JSON requests against one site.
type Client struct {
	Base    string
	Product string // "Confluence" or "Jira", for error messages
	http    *http.Client
}

// NewClient returns a client for base (see SiteBase); timeout 0 means 15s.
func NewClient(product, base string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	return &Client{Base: SiteBase(base), Product: product, http: &http.Client{Timeout: timeout}}
}

// StatusError is a non-2xx answer from an Atlassian product.
type StatusError struct {
	Code    int
	Body    string
	Product string
}

func (e *StatusError) Error() string {
	p := e.Product
	if p == "" {
		p = "Atlassian"
	}
	switch e.Code {
	case http.StatusUnauthorized:
		return "401 unauthorized: check the Atlassian email and API token"
	case http.StatusForbidden:
		return "403 forbidden: the account may not read this content"
	case http.StatusNotFound:
		return "404 not found (or not visible to this account)"
	case http.StatusTooManyRequests:
		return "429 rate limited by " + p + "; retry later"
	}
	if e.Body != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
	}
	return fmt.Sprintf("HTTP %d", e.Code)
}

// Get fetches base+path as JSON into out. It is the only request method:
// the Atlassian tools never write.
func (c *Client) Get(ctx context.Context, cr Credentials, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cr.Email, cr.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b)), Product: c.Product}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// Quote returns s as a CQL/JQL string literal: whitespace collapsed,
// backslashes and double quotes escaped, so user text can never end the
// literal and inject query syntax.
func Quote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
