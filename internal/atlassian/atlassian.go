// Package atlassian holds what briefd's live Atlassian Cloud tools share
// (ADR-0009, ADR-0010, ADR-0012): the caller-credential headers, site URL
// handling, routing of scoped API tokens through the Atlassian API gateway,
// a read-only JSON GET with friendly status errors, and the health record
// the dashboard shows. Confluence and Jira on one Atlassian Cloud site
// accept the same account email and classic API token; a scoped token is
// made for one product.
package atlassian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Caller-credential headers. HeaderEmail/HeaderToken carry a classic token
// for every Atlassian tool; the X-Confluence-* pair predates them (v0.7) and
// is still accepted, by Jira too, since it carries the same Atlassian
// account. HeaderJiraToken and HeaderConfluenceToken carry a scoped token
// for one product, with HeaderEmail.
//
//nolint:gosec // G101: header names, not credentials
const (
	HeaderEmail           = "X-Atlassian-Email"
	HeaderToken           = "X-Atlassian-Token"
	HeaderJiraToken       = "X-Atlassian-Jira-Token"
	HeaderConfluenceToken = "X-Atlassian-Confluence-Token"
	LegacyHeaderEmail     = "X-Confluence-Email"
	LegacyHeaderToken     = "X-Confluence-Token"
	defaultRequestTimeout = 15 * time.Second
)

// Products, as used in gateway paths and scoped-token headers.
const (
	Jira       = "jira"
	Confluence = "confluence"
)

// DefaultGateway is the Atlassian API gateway scoped tokens must use.
const DefaultGateway = "https://api.atlassian.com"

// Credentials authenticate one request (Atlassian account email + API
// token). Scoped marks a token made for one product, which only works
// through the API gateway.
type Credentials struct {
	Email  string
	Token  string
	Scoped bool
}

// Kind names the token type for messages and the dashboard.
func (c Credentials) Kind() string {
	if c.Scoped {
		return "scoped"
	}
	return "classic"
}

// ErrNoCredentials means neither the caller nor the configuration supplied
// credentials.
var ErrNoCredentials = errors.New("no Atlassian credentials: send X-Atlassian-Email with X-Atlassian-Token (classic) or the product's scoped token header (X-Atlassian-Jira-Token, X-Atlassian-Confluence-Token), or configure a service account (email and token)")

// ScopedHeader returns the scoped-token header of a product.
func ScopedHeader(product string) string {
	if product == Jira {
		return HeaderJiraToken
	}
	return HeaderConfluenceToken
}

// FromHeaders returns the caller's credentials for product, first match
// wins: X-Atlassian-Email with the product's scoped token, with
// X-Atlassian-Token, then the legacy X-Confluence-* pair. A half-filled
// pair is ignored, and a scoped token never serves the other product.
func FromHeaders(h http.Header, product string) (Credentials, bool) {
	if h == nil {
		return Credentials{}, false
	}
	for _, p := range []struct {
		email, token string
		scoped       bool
	}{
		{HeaderEmail, ScopedHeader(product), true},
		{HeaderEmail, HeaderToken, false},
		{LegacyHeaderEmail, LegacyHeaderToken, false},
	} {
		email, token := strings.TrimSpace(h.Get(p.email)), strings.TrimSpace(h.Get(p.token))
		if email != "" && token != "" {
			return Credentials{Email: email, Token: token, Scoped: p.scoped}, true
		}
	}
	return Credentials{}, false
}

// Resolve picks the credentials for a request to product: the caller's
// headers when userCredentials allows them, else the service account.
func Resolve(h http.Header, product string, userCredentials bool, service Credentials) (Credentials, error) {
	if userCredentials {
		if cr, ok := FromHeaders(h, product); ok {
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

// cloudIDRe matches an Atlassian cloud ID (a UUID); it becomes part of
// gateway paths, so nothing else is accepted.
var cloudIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidCloudID reports whether id looks like an Atlassian cloud ID.
func ValidCloudID(id string) bool { return cloudIDRe.MatchString(id) }

// ValidateAuth checks the service account and caller-credential settings.
func ValidateAuth(product string, service Credentials, userCredentials bool) error {
	if (service.Email == "") != (service.Token == "") {
		return fmt.Errorf("%s: set both email and token, or neither", product)
	}
	if service.Scoped && service.Token == "" {
		return fmt.Errorf("%s: scoped_token is set but there is no service-account token", product)
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

// Options tune a Client.
type Options struct {
	// Timeout bounds each request (0 = 15s).
	Timeout time.Duration
	// CloudID is the site's cloud ID for scoped tokens; empty resolves it
	// from the site on first use.
	CloudID string
	// Gateway replaces DefaultGateway (tests).
	Gateway string
}

// Client performs read-only JSON requests against one site: classic
// credentials go to the site URL, scoped ones through the API gateway.
type Client struct {
	Base    string
	Product string // "Confluence" or "Jira", for messages
	key     string // Confluence or Jira constant, for gateway paths
	gateway string
	http    *http.Client

	mu          sync.Mutex
	cloudID     string
	configured  bool // cloudID came from the configuration
	cloudErr    error
	cloudErrAt  time.Time
	health      Health
	probedAt    time.Time
	probeResult probe
}

// cloudIDRetry is how long a failed cloud-ID lookup is remembered before
// the next scoped request tries again.
const cloudIDRetry = 30 * time.Second

// NewClient returns a client for base (see SiteBase).
func NewClient(product, base string, opts Options) *Client {
	if opts.Timeout == 0 {
		opts.Timeout = defaultRequestTimeout
	}
	gw := strings.TrimRight(opts.Gateway, "/")
	if gw == "" {
		gw = DefaultGateway
	}
	return &Client{
		Base: SiteBase(base), Product: product, key: strings.ToLower(product), gateway: gw,
		http: &http.Client{Timeout: opts.Timeout}, cloudID: opts.CloudID, configured: opts.CloudID != "",
	}
}

// StatusError is a non-2xx answer from an Atlassian product.
type StatusError struct {
	Code    int
	Body    string
	Product string
	// Scoped and Endpoint add a hint to 401/403 answers to scoped tokens.
	Scoped   bool
	Endpoint string
}

func (e *StatusError) Error() string {
	p := e.Product
	if p == "" {
		p = "Atlassian"
	}
	if e.Scoped && (e.Code == http.StatusUnauthorized || e.Code == http.StatusForbidden) {
		return fmt.Sprintf("%d %s with a scoped token: check that the token was created for %s, has the scope for %s and has not expired (scoped tokens last at most 365 days)",
			e.Code, strings.ToLower(http.StatusText(e.Code)), p, e.Endpoint)
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

// Get fetches path as JSON into out, from the site for classic credentials
// and through the gateway for scoped ones. It is the only request method:
// the Atlassian tools never write.
func (c *Client) Get(ctx context.Context, cr Credentials, path string, out any) (err error) {
	start := time.Now()
	defer func() { c.record(start, cr, err) }()
	base := c.Base
	if cr.Scoped {
		id, err := c.CloudID(ctx)
		if err != nil {
			return err
		}
		base = c.gateway + "/ex/" + c.key + "/" + id
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
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
		endpoint, _, _ := strings.Cut(path, "?")
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b)), Product: c.Product, Scoped: cr.Scoped, Endpoint: endpoint}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// ErrCloudID wraps a failed cloud-ID lookup.
var ErrCloudID = errors.New("cloud ID unknown")

// CloudID returns the site's cloud ID: the configured one, or the one the
// site reports at /_edge/tenant_info (unauthenticated), looked up once. A
// failed lookup is retried after cloudIDRetry.
func (c *Client) CloudID(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cloudID != "" {
		return c.cloudID, nil
	}
	if c.cloudErr != nil && time.Since(c.cloudErrAt) < cloudIDRetry {
		return "", c.cloudErr
	}
	id, err := c.tenantCloudID(ctx)
	if err != nil {
		c.cloudErr = fmt.Errorf("%w: looking it up at %s/_edge/tenant_info failed (%w); set atlassian.cloud_id (BRIEFD_ATLASSIAN_CLOUD_ID) for scoped tokens", ErrCloudID, c.Base, err)
		c.cloudErrAt = time.Now()
		return "", c.cloudErr
	}
	c.cloudID, c.cloudErr = id, nil
	return id, nil
}

func (c *Client) tenantCloudID(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/_edge/tenant_info", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", errors.New(netErrorText(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		CloudID string `json:"cloudId"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding: %w", err)
	}
	if !ValidCloudID(body.CloudID) {
		return "", errors.New("no cloud ID in the answer")
	}
	return body.CloudID, nil
}

// Health is what the dashboard shows about one product: the outcome of
// the requests the tools made since the server started, and whether the
// site answers. It holds no credentials, queries or content.
type Health struct {
	Product       string    `json:"product"`
	Site          string    `json:"site"`
	Status        string    `json:"status"` // ok, degraded, down, idle
	Requests      int64     `json:"requests"`
	Failures      int64     `json:"failures"`
	Classic       int64     `json:"classic_requests"`
	Scoped        int64     `json:"scoped_requests"`
	LastOKAt      time.Time `json:"last_ok_at,omitzero"`
	LastErrorAt   time.Time `json:"last_error_at,omitzero"`
	LastError     string    `json:"last_error,omitempty"`
	LastLatencyMS int64     `json:"last_latency_ms"`
	Reachable     bool      `json:"reachable"`
	ProbeMS       int64     `json:"probe_ms"`
	ProbeError    string    `json:"probe_error,omitempty"`
	ProbedAt      time.Time `json:"probed_at,omitzero"`
	CloudID       string    `json:"cloud_id"` // configured, resolved, unresolved
}

type probe struct {
	reachable bool
	ms        int64
	err       string
}

// probeEvery bounds how often Health contacts the site.
const probeEvery = time.Minute

// record adds one request outcome to the health record.
func (c *Client) record(start time.Time, cr Credentials, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := &c.health
	h.Requests++
	if cr.Scoped {
		h.Scoped++
	} else {
		h.Classic++
	}
	h.LastLatencyMS = time.Since(start).Milliseconds()
	if err == nil {
		h.LastOKAt = time.Now()
		return
	}
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		// A missing issue or page is an answer, not a failure of the site.
		h.LastOKAt = time.Now()
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	h.Failures++
	h.LastErrorAt = time.Now()
	h.LastError = errorText(err, cr)
}

// errorText describes a failure without echoing URLs, bodies or queries.
func errorText(err error, cr Credentials) string {
	var se *StatusError
	switch {
	case errors.As(err, &se):
		text := fmt.Sprintf("HTTP %d", se.Code)
		switch se.Code {
		case http.StatusUnauthorized, http.StatusForbidden:
			text += " (" + cr.Kind() + " token rejected"
			if cr.Scoped {
				text += "; check its product, scopes and expiry"
			}
			text += ")"
		case http.StatusTooManyRequests:
			text += " (rate limited)"
		}
		return text
	case errors.Is(err, ErrCloudID):
		return "cloud ID could not be resolved; set atlassian.cloud_id"
	}
	return netErrorText(err)
}

func netErrorText(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "DNS lookup failed"
	}
	return "network error"
}

// Health returns the health record, contacting the site (an
// unauthenticated GET of /_edge/tenant_info) at most once per minute.
func (c *Client) Health(ctx context.Context) Health {
	c.mu.Lock()
	due := time.Since(c.probedAt) >= probeEvery
	c.mu.Unlock()
	if due {
		start := time.Now()
		pr := probe{}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/_edge/tenant_info", nil)
		if err == nil {
			var resp *http.Response
			if resp, err = c.http.Do(req); err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
				// Any answer below 500 means the site is up.
				pr.reachable = resp.StatusCode < 500
				if !pr.reachable {
					pr.err = fmt.Sprintf("HTTP %d", resp.StatusCode)
				}
			}
		}
		if err != nil {
			pr.err = netErrorText(err)
		}
		pr.ms = time.Since(start).Milliseconds()
		c.mu.Lock()
		c.probedAt, c.probeResult = time.Now(), pr
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.health
	h.Product, h.Site = c.Product, c.Base
	if u, err := url.Parse(c.Base); err == nil {
		u.User = nil // never show credentials embedded in the URL
		h.Site = u.String()
	}
	h.Reachable, h.ProbeMS, h.ProbeError, h.ProbedAt = c.probeResult.reachable, c.probeResult.ms, c.probeResult.err, c.probedAt
	switch {
	case c.configured:
		h.CloudID = "configured"
	case c.cloudID != "":
		h.CloudID = "resolved"
	default:
		h.CloudID = "unresolved"
	}
	switch {
	case !h.Reachable:
		h.Status = "down"
	case h.LastErrorAt.After(h.LastOKAt):
		h.Status = "degraded"
	case h.Requests == 0:
		h.Status = "idle"
	default:
		h.Status = "ok"
	}
	return h
}

// Quote returns s as a CQL/JQL string literal: whitespace collapsed,
// backslashes and double quotes escaped, so user text can never end the
// literal and inject query syntax.
func Quote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
