// Package jira reads Jira Cloud issues live, for the jira_search and
// jira_issue MCP tools and compile_bundle's issue option (ADR-0010).
// Nothing it returns is indexed, cached or stored: Jira stays the system of
// record for its issues and git stays briefd's only write path.
package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ismailperim/briefd/internal/atlassian"
	"github.com/ismailperim/briefd/internal/confluence"
)

// Config configures the Jira Cloud connection.
type Config struct {
	// BaseURL is the site, e.g. https://example.atlassian.net. Empty
	// disables the Jira tools.
	BaseURL string `yaml:"base_url"`
	// Email and Token are a service account's Atlassian account email and
	// API token. When both are empty the Confluence service account (if
	// any) is used: one Atlassian account and token cover both products.
	Email string `yaml:"email"`
	Token string `yaml:"token"`
	// Projects limits searches and issue reads to these project keys.
	// Empty allows every project the credentials can see.
	Projects []string `yaml:"projects"`
	// Fields are extra issue fields to include in jira_issue and
	// compile_bundle, by id (e.g. customfield_10035 for "Acceptance
	// criteria"). Their display names come from Jira.
	Fields []string `yaml:"fields"`
	// UserCredentials lets MCP clients send their own Atlassian credentials
	// in the X-Atlassian-Email and X-Atlassian-Token headers (or the
	// X-Confluence-* pair), so they see only what they may see (default true).
	UserCredentials bool `yaml:"user_credentials"`
	// Timeout bounds each Jira request (default 15s).
	Timeout time.Duration `yaml:"timeout"`
}

// Enabled reports whether the tools should be offered.
func (c Config) Enabled() bool { return c.BaseURL != "" }

// Validate checks the configuration; it is a no-op when disabled.
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	if err := atlassian.ValidateSite("jira.base_url", c.BaseURL); err != nil {
		return err
	}
	if err := atlassian.ValidateAuth("jira", atlassian.Credentials{Email: c.Email, Token: c.Token}, c.UserCredentials); err != nil {
		return err
	}
	if c.Timeout < 0 {
		return errors.New("jira.timeout must be >= 0")
	}
	for _, p := range c.Projects {
		if !projectKey.MatchString(p) {
			return fmt.Errorf("jira.projects: invalid project key %q", p)
		}
	}
	for _, f := range c.Fields {
		if !fieldID.MatchString(f) {
			return fmt.Errorf("jira.fields: invalid field id %q (expected e.g. customfield_10035)", f)
		}
	}
	return nil
}

// ErrNotAllowed is returned for issues outside the configured projects.
var ErrNotAllowed = errors.New("issue is outside the Jira projects this server allows")

var (
	projectKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,254}$`)
	issueKey   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,254}-[0-9]{1,15}$`)
	fieldID    = regexp.MustCompile(`^(customfield_[0-9]{1,10}|[a-z][a-zA-Z]{1,40})$`)
	pageIDRe   = regexp.MustCompile(`(?:pageId=|/pages/)([0-9]{1,20})`)
	// lucene operators make Jira's text search fail; they carry no meaning
	// for a natural-language query, so they become spaces.
	luceneOps = strings.NewReplacer("+", " ", "-", " ", "&", " ", "|", " ", "!", " ", "(", " ", ")", " ", "{", " ", "}", " ",
		"[", " ", "]", " ", "^", " ", "~", " ", "*", " ", "?", " ", ":", " ", "/", " ")
)

// ValidKey reports whether s looks like an issue key (PROJ-123).
func ValidKey(s string) bool { return issueKey.MatchString(strings.TrimSpace(s)) }

// baseFields are the fields every issue read requests.
var baseFields = []string{
	"summary", "status", "issuetype", "priority", "assignee", "reporter", "project", "labels", "components",
	"fixVersions", "parent", "issuelinks", "subtasks", "created", "updated", "resolution", "description", "comment",
}

// Client talks to one Jira Cloud site.
type Client struct {
	cfg Config
	api *atlassian.Client
}

// New returns a client; cfg must be enabled and valid.
func New(cfg Config) *Client {
	return &Client{cfg: cfg, api: atlassian.NewClient("Jira", cfg.BaseURL, cfg.Timeout)}
}

// Projects returns the allowed project keys (nil = all).
func (c *Client) Projects() []string { return c.cfg.Projects }

// Resolve picks the credentials for a request from the caller's headers
// (when allowed) or the service account.
func (c *Client) Resolve(h http.Header) (atlassian.Credentials, error) {
	return atlassian.Resolve(h, c.cfg.UserCredentials, atlassian.Credentials{Email: c.cfg.Email, Token: c.cfg.Token})
}

// Ref is a short reference to another issue.
type Ref struct {
	Relation string `json:"relation,omitempty"`
	Key      string `json:"key"`
	Summary  string `json:"summary,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Comment is one issue comment in Markdown.
type Comment struct {
	Author   string    `json:"author,omitempty"`
	Created  time.Time `json:"created,omitzero"`
	Markdown string    `json:"-"`
}

// Field is an extra configured field.
type Field struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Markdown string `json:"-"`
}

// PageLink is a Confluence page linked from the issue.
type PageLink struct {
	Title  string `json:"title,omitempty"`
	URL    string `json:"url"`
	PageID string `json:"page_id,omitempty"`
}

// Issue is one issue with its rich-text fields converted to Markdown.
type Issue struct {
	Key         string     `json:"key"`
	Summary     string     `json:"summary"`
	Project     string     `json:"project"`
	Type        string     `json:"type,omitempty"`
	Status      string     `json:"status,omitempty"`
	Priority    string     `json:"priority,omitempty"`
	Resolution  string     `json:"resolution,omitempty"`
	Assignee    string     `json:"assignee,omitempty"`
	Reporter    string     `json:"reporter,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
	Components  []string   `json:"components,omitempty"`
	FixVersions []string   `json:"fix_versions,omitempty"`
	Parent      *Ref       `json:"parent,omitempty"`
	Links       []Ref      `json:"links,omitempty"`
	Subtasks    []Ref      `json:"subtasks,omitempty"`
	Pages       []PageLink `json:"confluence_pages,omitempty"`
	Fields      []Field    `json:"fields,omitempty"`
	Comments    []Comment  `json:"-"`
	Created     time.Time  `json:"created,omitzero"`
	Updated     time.Time  `json:"updated,omitzero"`
	URL         string     `json:"url"`
	Description string     `json:"-"`
}

type named struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Value       string `json:"value"`
}

type refJSON struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  named  `json:"status"`
	} `json:"fields"`
}

func (r refJSON) ref(relation string) Ref {
	return Ref{Relation: strings.TrimSpace(relation), Key: r.Key, Summary: strings.TrimSpace(r.Fields.Summary), Status: strings.TrimSpace(r.Fields.Status.Name)}
}

// Issue fetches one issue by key.
func (c *Client) Issue(ctx context.Context, cr atlassian.Credentials, key string) (*Issue, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if !issueKey.MatchString(key) {
		return nil, fmt.Errorf("invalid issue key %q (expected e.g. PAY-123)", key)
	}
	if !c.allowed(key[:strings.LastIndexByte(key, '-')]) {
		return nil, ErrNotAllowed
	}
	q := url.Values{}
	q.Set("fields", strings.Join(append(append([]string{}, baseFields...), c.cfg.Fields...), ","))
	q.Set("expand", "renderedFields,names")
	var resp struct {
		Key    string                     `json:"key"`
		Fields map[string]json.RawMessage `json:"fields"`
		// Rendered holds HTML for rich-text fields (description, comment
		// bodies, text custom fields).
		Rendered map[string]json.RawMessage `json:"renderedFields"`
		Names    map[string]string          `json:"names"`
	}
	if err := c.api.Get(ctx, cr, "/rest/api/3/issue/"+url.PathEscape(key)+"?"+q.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("jira issue %s: %w", key, err)
	}
	var f struct {
		Summary     string    `json:"summary"`
		Status      named     `json:"status"`
		Type        named     `json:"issuetype"`
		Priority    named     `json:"priority"`
		Resolution  named     `json:"resolution"`
		Assignee    named     `json:"assignee"`
		Reporter    named     `json:"reporter"`
		Project     named     `json:"project"`
		Labels      []string  `json:"labels"`
		Components  []named   `json:"components"`
		FixVersions []named   `json:"fixVersions"`
		Parent      *refJSON  `json:"parent"`
		Subtasks    []refJSON `json:"subtasks"`
		IssueLinks  []struct {
			Type struct {
				Inward  string `json:"inward"`
				Outward string `json:"outward"`
			} `json:"type"`
			Inward  *refJSON `json:"inwardIssue"`
			Outward *refJSON `json:"outwardIssue"`
		} `json:"issuelinks"`
		Created string `json:"created"`
		Updated string `json:"updated"`
		Comment struct {
			Comments []struct {
				ID      string `json:"id"`
				Author  named  `json:"author"`
				Created string `json:"created"`
			} `json:"comments"`
		} `json:"comment"`
	}
	raw, _ := json.Marshal(resp.Fields)
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("jira issue %s: decoding fields: %w", key, err)
	}
	if !c.allowed(f.Project.Key) {
		return nil, ErrNotAllowed
	}
	is := &Issue{
		Key: resp.Key, Summary: f.Summary, Project: f.Project.Key, Type: f.Type.Name, Status: f.Status.Name,
		Priority: f.Priority.Name, Resolution: f.Resolution.Name, Assignee: f.Assignee.DisplayName, Reporter: f.Reporter.DisplayName,
		Labels: f.Labels, Created: parseTime(f.Created), Updated: parseTime(f.Updated), URL: c.api.Base + "/browse/" + resp.Key,
	}
	for _, x := range f.Components {
		is.Components = append(is.Components, strings.TrimSpace(x.Name))
	}
	for _, x := range f.FixVersions {
		is.FixVersions = append(is.FixVersions, strings.TrimSpace(x.Name))
	}
	if f.Parent != nil && f.Parent.Key != "" {
		p := f.Parent.ref("parent")
		is.Parent = &p
	}
	for _, s := range f.Subtasks {
		is.Subtasks = append(is.Subtasks, s.ref("subtask"))
	}
	for _, l := range f.IssueLinks {
		switch {
		case l.Outward != nil:
			is.Links = append(is.Links, l.Outward.ref(l.Type.Outward))
		case l.Inward != nil:
			is.Links = append(is.Links, l.Inward.ref(l.Type.Inward))
		}
	}
	is.Description = richText(resp.Rendered["description"], resp.Fields["description"])
	for _, id := range c.cfg.Fields {
		md := richText(resp.Rendered[id], resp.Fields[id])
		if md == "" {
			continue
		}
		name := resp.Names[id]
		if name == "" {
			name = id
		}
		is.Fields = append(is.Fields, Field{ID: id, Name: name, Markdown: md})
	}
	// Comment bodies are rendered HTML; author and timestamp come from the
	// raw field (the rendered one carries a relative date).
	var rendered struct {
		Comments []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	_ = json.Unmarshal(resp.Rendered["comment"], &rendered)
	bodies := map[string]string{}
	for _, rc := range rendered.Comments {
		bodies[rc.ID] = rc.Body
	}
	for _, rc := range f.Comment.Comments {
		md := confluence.ToMarkdown(bodies[rc.ID])
		if md == "" {
			continue
		}
		is.Comments = append(is.Comments, Comment{Author: rc.Author.DisplayName, Created: parseTime(rc.Created), Markdown: md})
	}
	is.Pages = c.pages(ctx, cr, resp.Key)
	return is, nil
}

// pages lists Confluence pages linked to the issue. Best effort: a failure
// here never fails the issue read.
func (c *Client) pages(ctx context.Context, cr atlassian.Credentials, key string) []PageLink {
	var links []struct {
		GlobalID    string `json:"globalId"`
		Application struct {
			Type string `json:"type"`
		} `json:"application"`
		Object struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"object"`
	}
	if err := c.api.Get(ctx, cr, "/rest/api/3/issue/"+url.PathEscape(key)+"/remotelink", &links); err != nil {
		return nil
	}
	var out []PageLink
	for _, l := range links {
		if l.Application.Type != "com.atlassian.confluence" && !strings.Contains(l.Object.URL, "/wiki/") {
			continue
		}
		p := PageLink{Title: l.Object.Title, URL: l.Object.URL}
		for _, s := range []string{l.GlobalID, l.Object.URL} {
			if m := pageIDRe.FindStringSubmatch(s); m != nil {
				p.PageID = m[1]
				break
			}
		}
		out = append(out, p)
	}
	return out
}

// Query is a structured issue search. Agents never send raw JQL: briefd
// builds it, so the project allow-list cannot be bypassed.
type Query struct {
	Text     string
	Projects []string
	Status   string
	Type     string
	// Assignee is "me", "unassigned" or empty.
	Assignee string
	OpenOnly bool
	Limit    int
}

// Hit is one search result.
type Hit struct {
	Key      string    `json:"key"`
	Summary  string    `json:"summary"`
	Type     string    `json:"type,omitempty"`
	Status   string    `json:"status,omitempty"`
	Priority string    `json:"priority,omitempty"`
	Assignee string    `json:"assignee,omitempty"`
	Updated  time.Time `json:"updated,omitzero"`
	URL      string    `json:"url"`
}

// Search runs a JQL search built from q, newest first.
func (c *Client) Search(ctx context.Context, cr atlassian.Credentials, q Query) ([]Hit, error) {
	projects, err := c.effectiveProjects(q.Projects)
	if err != nil {
		return nil, err
	}
	q.Projects = projects
	jql, err := JQL(q)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("jql", jql)
	v.Set("maxResults", strconv.Itoa(q.Limit))
	v.Set("fields", "summary,status,issuetype,priority,assignee,updated")
	var resp struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary  string `json:"summary"`
				Status   named  `json:"status"`
				Type     named  `json:"issuetype"`
				Priority named  `json:"priority"`
				Assignee named  `json:"assignee"`
				Updated  string `json:"updated"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := c.api.Get(ctx, cr, "/rest/api/3/search/jql?"+v.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("jira search: %w", err)
	}
	hits := make([]Hit, 0, len(resp.Issues))
	for _, is := range resp.Issues {
		if is.Key == "" {
			continue
		}
		hits = append(hits, Hit{
			Key: is.Key, Summary: is.Fields.Summary, Type: is.Fields.Type.Name, Status: is.Fields.Status.Name,
			Priority: is.Fields.Priority.Name, Assignee: is.Fields.Assignee.DisplayName,
			Updated: parseTime(is.Fields.Updated), URL: c.api.Base + "/browse/" + is.Key,
		})
	}
	return hits, nil
}

// JQL builds the search query. Every user value enters as a quoted string
// literal; at least one condition is required because Jira rejects
// unbounded searches.
func JQL(q Query) (string, error) {
	var parts []string
	if len(q.Projects) > 0 {
		keys := make([]string, len(q.Projects))
		for i, p := range q.Projects {
			keys[i] = atlassian.Quote(strings.ToUpper(p))
		}
		parts = append(parts, "project IN ("+strings.Join(keys, ", ")+")")
	}
	if t := strings.TrimSpace(luceneOps.Replace(q.Text)); t != "" {
		parts = append(parts, "text ~ "+atlassian.Quote(t))
	}
	if s := strings.TrimSpace(q.Status); s != "" {
		parts = append(parts, "status = "+atlassian.Quote(s))
	}
	if s := strings.TrimSpace(q.Type); s != "" {
		parts = append(parts, "issuetype = "+atlassian.Quote(s))
	}
	switch strings.ToLower(strings.TrimSpace(q.Assignee)) {
	case "":
	case "me":
		parts = append(parts, "assignee = currentUser()")
	case "unassigned":
		parts = append(parts, "assignee IS EMPTY")
	default:
		return "", fmt.Errorf(`assignee must be "me" or "unassigned", not %q`, q.Assignee)
	}
	if q.OpenOnly {
		parts = append(parts, "statusCategory != Done")
	}
	if len(parts) == 0 {
		return "", errors.New("give text or at least one filter (projects, status, type, assignee, open_only)")
	}
	return strings.Join(parts, " AND ") + " ORDER BY updated DESC", nil
}

func (c *Client) effectiveProjects(requested []string) ([]string, error) {
	for _, p := range requested {
		if !projectKey.MatchString(p) {
			return nil, fmt.Errorf("invalid project key %q", p)
		}
	}
	if len(c.cfg.Projects) == 0 {
		return requested, nil
	}
	if len(requested) == 0 {
		return c.cfg.Projects, nil
	}
	var out []string
	for _, p := range requested {
		if c.allowed(p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none of the projects %v is allowed; allowed: %v", requested, c.cfg.Projects)
	}
	return out, nil
}

func (c *Client) allowed(project string) bool {
	if len(c.cfg.Projects) == 0 {
		return true
	}
	for _, p := range c.cfg.Projects {
		if strings.EqualFold(p, project) {
			return true
		}
	}
	return false
}

// richText returns a field as Markdown: its rendered HTML when Jira has
// one, else a plain rendering of the raw value (options, users, numbers,
// lists, or Atlassian Document Format text).
func richText(rendered, raw json.RawMessage) string {
	var html string
	if json.Unmarshal(rendered, &html) == nil && strings.TrimSpace(html) != "" {
		return confluence.ToMarkdown(html)
	}
	return strings.TrimSpace(plain(raw))
}

func plain(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return plainValue(v)
}

func plainValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		var out []string
		for _, e := range x {
			if s := plainValue(e); s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, ", ")
	case map[string]any:
		if x["type"] == "doc" {
			return strings.TrimSpace(adfText(x))
		}
		for _, k := range []string{"value", "name", "displayName", "key"} {
			if s, ok := x[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// adfText flattens Atlassian Document Format to text, one line per block.
func adfText(n map[string]any) string {
	if t, ok := n["text"].(string); ok {
		return t
	}
	var sb strings.Builder
	kids, _ := n["content"].([]any)
	for _, k := range kids {
		if m, ok := k.(map[string]any); ok {
			sb.WriteString(adfText(m))
		}
	}
	switch n["type"] {
	case "paragraph", "heading", "listItem", "codeBlock", "blockquote", "tableRow":
		return sb.String() + "\n"
	case "hardBreak":
		return "\n"
	}
	return sb.String()
}

// parseTime reads Jira's timestamps (2026-10-01T10:00:00.000+0000).
func parseTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Plain returns the issue's summary and description as plain text, for use
// as a retrieval query; at most maxRunes runes.
func (is *Issue) Plain(maxRunes int) string {
	s := is.Summary + "\n" + is.Description
	for _, f := range is.Fields {
		s += "\n" + f.Markdown
	}
	s = strings.Join(strings.Fields(strings.NewReplacer("#", " ", "*", " ", "_", " ", "`", " ", "|", " ", ">", " ").Replace(s)), " ")
	if r := []rune(s); len(r) > maxRunes {
		s = string(r[:maxRunes])
	}
	return s
}

// Markdown renders the issue compactly, most useful first: header and
// metadata, related issues and pages, description and extra fields, then
// comments newest first — so cutting it from the end loses the least.
func (is *Issue) Markdown(withComments bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s: %s\n\nSource: Jira", is.Key, is.Summary)
	for _, s := range []string{is.Type, is.Status, prefixed("priority ", is.Priority), prefixed("resolution ", is.Resolution), prefixed("assignee ", is.Assignee)} {
		if s != "" {
			sb.WriteString(" · " + s)
		}
	}
	if !is.Updated.IsZero() {
		sb.WriteString(" · updated " + is.Updated.UTC().Format("2006-01-02"))
	}
	sb.WriteString("\n" + is.URL + "\n")
	var meta []string
	if is.Parent != nil {
		meta = append(meta, "Parent: "+refLine(*is.Parent))
	}
	for _, kv := range [][2]string{{"Labels", strings.Join(is.Labels, ", ")}, {"Components", strings.Join(is.Components, ", ")}, {"Fix versions", strings.Join(is.FixVersions, ", ")}} {
		if kv[1] != "" {
			meta = append(meta, kv[0]+": "+kv[1])
		}
	}
	if len(meta) > 0 {
		sb.WriteString(strings.Join(meta, "\n") + "\n")
	}
	if len(is.Links)+len(is.Subtasks) > 0 {
		sb.WriteString("\n## Related issues\n\n")
		for _, r := range append(append([]Ref{}, is.Links...), is.Subtasks...) {
			sb.WriteString("- " + r.Relation + " " + refLine(r) + "\n")
		}
	}
	if len(is.Pages) > 0 {
		sb.WriteString("\n## Confluence pages\n\n")
		for _, p := range is.Pages {
			line := "- " + p.Title
			if p.PageID != "" {
				line += " · id " + p.PageID
			}
			sb.WriteString(line + " · " + p.URL + "\n")
		}
	}
	if is.Description != "" {
		sb.WriteString("\n## Description\n\n" + is.Description + "\n")
	}
	for _, f := range is.Fields {
		sb.WriteString("\n## " + f.Name + "\n\n" + f.Markdown + "\n")
	}
	if withComments && len(is.Comments) > 0 {
		fmt.Fprintf(&sb, "\n## Comments (%d, newest first)\n", len(is.Comments))
		for i := len(is.Comments) - 1; i >= 0; i-- {
			cm := is.Comments[i]
			head := cm.Author
			if !cm.Created.IsZero() {
				head += " · " + cm.Created.UTC().Format("2006-01-02")
			}
			sb.WriteString("\n### " + strings.TrimSpace(head) + "\n\n" + cm.Markdown + "\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func refLine(r Ref) string {
	s := r.Key
	if r.Summary != "" {
		s += " " + r.Summary
	}
	if r.Status != "" {
		s += " (" + r.Status + ")"
	}
	return s
}

func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}
