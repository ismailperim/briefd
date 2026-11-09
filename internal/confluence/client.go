// Package confluence searches and reads Confluence Cloud pages live, for
// the confluence_search and confluence_page MCP tools (ADR-0009). Nothing
// it returns is indexed or cached: Confluence stays the system of record for
// its pages and git stays briefd's only write path.
package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config configures the Confluence Cloud connection.
type Config struct {
	// BaseURL is the site, e.g. https://example.atlassian.net. Empty
	// disables the Confluence tools.
	BaseURL string `yaml:"base_url"`
	// Email and Token are a service account's Atlassian account email and
	// API token. Both empty means callers must send their own credentials.
	Email string `yaml:"email"`
	Token string `yaml:"token"`
	// Spaces limits searches and page reads to these space keys. Empty
	// allows every space the credentials can see.
	Spaces []string `yaml:"spaces"`
	// UserCredentials lets MCP clients send their own Atlassian credentials
	// in the X-Confluence-Email and X-Confluence-Token headers, so they see
	// only what they may see (default true).
	UserCredentials bool `yaml:"user_credentials"`
	// Timeout bounds each Confluence request (default 15s).
	Timeout time.Duration `yaml:"timeout"`
}

// Enabled reports whether the tools should be offered.
func (c Config) Enabled() bool { return c.BaseURL != "" }

// Validate checks the configuration; it is a no-op when disabled.
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("confluence.base_url %q is not a URL", c.BaseURL)
	}
	if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return errors.New("confluence.base_url must use https")
	}
	if (c.Email == "") != (c.Token == "") {
		return errors.New("confluence: set both email and token, or neither")
	}
	if c.Email == "" && !c.UserCredentials {
		return errors.New("confluence: no service credentials and user_credentials is off; nobody could authenticate")
	}
	if c.Timeout < 0 {
		return errors.New("confluence.timeout must be >= 0")
	}
	for _, s := range c.Spaces {
		if !spaceKey.MatchString(s) {
			return fmt.Errorf("confluence.spaces: invalid space key %q", s)
		}
	}
	return nil
}

// Credentials authenticate one request (Atlassian email + API token).
type Credentials struct {
	Email string
	Token string
}

// ErrNoCredentials means neither the caller nor the configuration supplied
// credentials.
var ErrNoCredentials = errors.New("no Confluence credentials: send X-Confluence-Email and X-Confluence-Token headers, or configure confluence.email and confluence.token")

// ErrNotAllowed is returned for pages outside the configured spaces.
var ErrNotAllowed = errors.New("page is outside the Confluence spaces this server allows")

var (
	spaceKey = regexp.MustCompile(`^[A-Za-z0-9_~-]{1,255}$`)
	pageID   = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// Client talks to one Confluence Cloud site.
type Client struct {
	cfg  Config
	base string
	http *http.Client
}

// New returns a client; cfg must be enabled and valid.
func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	// Accept the site URL with or without the /wiki context path.
	base := strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/wiki")
	return &Client{cfg: cfg, base: base, http: &http.Client{Timeout: timeout}}
}

// Spaces returns the allowed space keys (nil = all).
func (c *Client) Spaces() []string { return c.cfg.Spaces }

// Resolve picks the credentials for a request from the caller's headers
// (when allowed) or the service account.
func (c *Client) Resolve(h http.Header) (Credentials, error) {
	if c.cfg.UserCredentials && h != nil {
		email, token := strings.TrimSpace(h.Get("X-Confluence-Email")), strings.TrimSpace(h.Get("X-Confluence-Token"))
		if email != "" && token != "" {
			return Credentials{Email: email, Token: token}, nil
		}
	}
	if c.cfg.Email != "" {
		return Credentials{Email: c.cfg.Email, Token: c.cfg.Token}, nil
	}
	return Credentials{}, ErrNoCredentials
}

// Hit is one search result.
type Hit struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Space     string    `json:"space,omitempty"`
	URL       string    `json:"url"`
	Excerpt   string    `json:"excerpt,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Search runs a CQL full-text search over pages. spaces narrows the
// configured allow-list; it can never widen it.
func (c *Client) Search(ctx context.Context, cr Credentials, query string, spaces []string, limit int) ([]Hit, error) {
	keys, err := c.effectiveSpaces(spaces)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("cql", CQL(query, keys))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("excerpt", "highlight")
	q.Set("expand", "content.space")
	var resp struct {
		Results []struct {
			Content struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Space struct {
					Key string `json:"key"`
				} `json:"space"`
				Links struct {
					WebUI string `json:"webui"`
				} `json:"_links"`
			} `json:"content"`
			Title        string `json:"title"`
			Excerpt      string `json:"excerpt"`
			URL          string `json:"url"`
			LastModified string `json:"lastModified"`
			Container    struct {
				Title string `json:"title"`
			} `json:"resultGlobalContainer"`
		} `json:"results"`
	}
	if err := c.get(ctx, cr, "/wiki/rest/api/search?"+q.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("confluence search: %w", err)
	}
	hits := make([]Hit, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Content.ID == "" {
			continue
		}
		title := r.Content.Title
		if title == "" {
			title = cleanExcerpt(r.Title)
		}
		link := r.Content.Links.WebUI
		if link == "" {
			link = r.URL
		}
		space := r.Content.Space.Key
		if space == "" {
			space = r.Container.Title
		}
		h := Hit{ID: r.Content.ID, Title: title, Space: space, URL: c.webURL(link), Excerpt: cleanExcerpt(r.Excerpt)}
		if t, err := time.Parse(time.RFC3339, r.LastModified); err == nil {
			h.UpdatedAt = t
		}
		hits = append(hits, h)
	}
	return hits, nil
}

// Page is one page converted to Markdown.
type Page struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Space     string    `json:"space,omitempty"`
	URL       string    `json:"url"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	Markdown  string    `json:"-"`
}

// Page fetches a page by ID and converts its rendered body to Markdown.
func (c *Client) Page(ctx context.Context, cr Credentials, id string) (*Page, error) {
	if !pageID.MatchString(id) {
		return nil, fmt.Errorf("invalid page id %q (expected the numeric id from confluence_search)", id)
	}
	var resp struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		SpaceID string `json:"spaceId"`
		Version struct {
			Number    int    `json:"number"`
			CreatedAt string `json:"createdAt"`
		} `json:"version"`
		Body struct {
			View    struct{ Value string } `json:"view"`
			Storage struct{ Value string } `json:"storage"`
		} `json:"body"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	}
	if err := c.get(ctx, cr, "/wiki/api/v2/pages/"+id+"?body-format=view", &resp); err != nil {
		return nil, fmt.Errorf("confluence page %s: %w", id, err)
	}
	space := ""
	if resp.SpaceID != "" {
		key, err := c.spaceKey(ctx, cr, resp.SpaceID)
		if err != nil {
			return nil, fmt.Errorf("confluence page %s: %w", id, err)
		}
		space = key
	}
	if !c.allowed(space) {
		return nil, ErrNotAllowed
	}
	body := resp.Body.View.Value
	if body == "" {
		body = resp.Body.Storage.Value
	}
	p := &Page{
		ID: resp.ID, Title: resp.Title, Space: space, URL: c.webURL(resp.Links.WebUI),
		Version: resp.Version.Number, Markdown: ToMarkdown(body),
	}
	if t, err := time.Parse(time.RFC3339, resp.Version.CreatedAt); err == nil {
		p.UpdatedAt = t
	}
	return p, nil
}

func (c *Client) spaceKey(ctx context.Context, cr Credentials, spaceID string) (string, error) {
	if !pageID.MatchString(spaceID) {
		return "", fmt.Errorf("unexpected space id %q", spaceID)
	}
	var resp struct {
		Key string `json:"key"`
	}
	if err := c.get(ctx, cr, "/wiki/api/v2/spaces/"+spaceID, &resp); err != nil {
		return "", fmt.Errorf("space %s: %w", spaceID, err)
	}
	return resp.Key, nil
}

// effectiveSpaces intersects the requested spaces with the allow-list.
func (c *Client) effectiveSpaces(requested []string) ([]string, error) {
	for _, s := range requested {
		if !spaceKey.MatchString(s) {
			return nil, fmt.Errorf("invalid space key %q", s)
		}
	}
	if len(c.cfg.Spaces) == 0 {
		return requested, nil
	}
	if len(requested) == 0 {
		return c.cfg.Spaces, nil
	}
	var out []string
	for _, s := range requested {
		if c.allowed(s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none of the spaces %v is allowed; allowed: %v", requested, c.cfg.Spaces)
	}
	return out, nil
}

func (c *Client) allowed(space string) bool {
	if len(c.cfg.Spaces) == 0 {
		return true
	}
	for _, s := range c.cfg.Spaces {
		if strings.EqualFold(s, space) {
			return true
		}
	}
	return false
}

func (c *Client) webURL(link string) string {
	switch {
	case link == "":
		return ""
	case strings.HasPrefix(link, "http://"), strings.HasPrefix(link, "https://"):
		return link
	case strings.HasPrefix(link, "/wiki/"):
		return c.base + link
	default:
		return c.base + "/wiki" + link
	}
}

// StatusError is a non-2xx answer from Confluence.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	switch e.Code {
	case http.StatusUnauthorized:
		return "401 unauthorized: check the Atlassian email and API token"
	case http.StatusForbidden:
		return "403 forbidden: the account may not read this content"
	case http.StatusNotFound:
		return "404 not found (or not visible to this account)"
	case http.StatusTooManyRequests:
		return "429 rate limited by Confluence; retry later"
	}
	if e.Body != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
	}
	return fmt.Sprintf("HTTP %d", e.Code)
}

func (c *Client) get(ctx context.Context, cr Credentials, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
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
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// CQL builds the search query: full-text over pages, optionally limited to
// spaces. User text is always a quoted string literal.
func CQL(query string, spaces []string) string {
	cql := `type = page AND siteSearch ~ ` + quote(query)
	if len(spaces) > 0 {
		q := make([]string, len(spaces))
		for i, s := range spaces {
			q[i] = quote(s)
		}
		cql += ` AND space IN (` + strings.Join(q, ", ") + `)`
	}
	return cql
}

func quote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// cleanExcerpt drops Confluence's highlight markers and collapses space.
func cleanExcerpt(s string) string {
	s = html.UnescapeString(strings.NewReplacer("@@@hl@@@", "", "@@@endhl@@@", "").Replace(s))
	return strings.Join(strings.Fields(s), " ")
}
