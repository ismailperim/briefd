package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/jira"
	"github.com/ismailperim/briefd/internal/metrics"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

const (
	jiraSearchDefaultTokens = 800
	jiraSearchDefaultLimit  = 10
	jiraSearchMaxLimit      = 25
	jiraIssueDefaultTokens  = 2000
	// issueShare is the part of a compile_bundle budget the Jira issue may
	// take; the rest goes to the knowledge base.
	issueSharePercent = 35
	// issueQueryRunes bounds how much issue text steers retrieval.
	issueQueryRunes = 1500
)

const compileBundleIssueNote = " With issue=\"PROJ-123\" (Jira), the issue's summary, description and configured fields steer retrieval and a condensed copy of the issue heads the bundle; task_description becomes optional. Only the issue key is recorded, never its content."

func (t *tools) addJira(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "jira_search",
		Description: "Search the team's Jira issues live and return key, summary, type, status, priority, assignee and last-updated date per issue, newest first, within a token budget. Use it to find the ticket behind a task or related work; read one with jira_issue. Filters instead of raw JQL. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, t.jiraSearch)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "jira_issue",
		Description: "Read one Jira issue by key (e.g. PAY-123): metadata, parent, linked issues, linked Confluence pages, description, configured fields such as acceptance criteria, and comments newest first, as compact Markdown cut to a token budget. Read-only. To get the issue together with the team rules that apply, use compile_bundle with issue instead.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, t.jiraIssue)
}

// JiraSearchInput is the jira_search tool input.
type JiraSearchInput struct {
	Text      string   `json:"text,omitempty" jsonschema:"Words to look for in summaries, descriptions and comments, in any language."`
	Projects  []string `json:"projects,omitempty" jsonschema:"Project keys, e.g. [\"PAY\"]. Narrows the projects this server allows; cannot widen them."`
	Status    string   `json:"status,omitempty" jsonschema:"Exact status name, e.g. \"In Progress\"."`
	Type      string   `json:"type,omitempty" jsonschema:"Issue type name, e.g. \"Bug\"."`
	Assignee  string   `json:"assignee,omitempty" jsonschema:"\"me\" (the calling account) or \"unassigned\"."`
	OpenOnly  bool     `json:"open_only,omitempty" jsonschema:"Only issues not in a done status."`
	Limit     int      `json:"limit,omitempty" jsonschema:"Maximum issues (default 10, max 25)."`
	MaxTokens int      `json:"max_tokens,omitempty" jsonschema:"Token budget (default 800). Never exceeded; older issues are omitted first."`
}

// JiraSearchOutput is the structured jira_search result.
type JiraSearchOutput struct {
	Issues  []jira.Hit `json:"issues"`
	Tokens  int        `json:"tokens"`
	Budget  int        `json:"budget"`
	Omitted int        `json:"omitted"`
}

func (t *tools) jiraSearch(ctx context.Context, req *mcp.CallToolRequest, in JiraSearchInput) (_ *mcp.CallToolResult, out JiraSearchOutput, err error) {
	start := time.Now()
	defer func() {
		t.record(start, metrics.Request{
			Name: "jira_search", Query: in.Text, Scopes: in.Projects, Tokens: out.Tokens, Chunks: len(out.Issues), Omitted: out.Omitted,
		}, err)
	}()
	if in.MaxTokens < 0 || in.Limit < 0 {
		return nil, out, errors.New("max_tokens and limit must be positive")
	}
	budget := cmpOr(in.MaxTokens, jiraSearchDefaultTokens)
	limit := min(cmpOr(in.Limit, jiraSearchDefaultLimit), jiraSearchMaxLimit)
	cr, err := t.deps.Jira.Resolve(requestHeader(req))
	if err != nil {
		return nil, out, err
	}
	hits, err := t.deps.Jira.Search(ctx, cr, jira.Query{
		Text: in.Text, Projects: in.Projects, Status: in.Status, Type: in.Type, Assignee: in.Assignee, OpenOnly: in.OpenOnly, Limit: limit,
	})
	if err != nil {
		return nil, out, err
	}
	text, kept, tokens := renderIssueHits(hits, budget)
	out = JiraSearchOutput{Issues: kept, Tokens: tokens, Budget: budget, Omitted: len(hits) - len(kept)}
	return textResult(text), out, nil
}

// renderIssueHits lists hits in order while the text stays within budget.
func renderIssueHits(hits []jira.Hit, budget int) (string, []jira.Hit, int) {
	if len(hits) == 0 {
		msg := "No Jira issues match."
		if tokenizer.Count(msg) > budget {
			return "", []jira.Hit{}, 0
		}
		return msg, []jira.Hit{}, tokenizer.Count(msg)
	}
	header := func(n int) string {
		return fmt.Sprintf("Jira: %d of %d issue(s), newest first. Read one with jira_issue(key).\n", n, len(hits))
	}
	kept := []jira.Hit{}
	var body strings.Builder
	for _, h := range hits {
		line := "\n" + h.Key + " " + h.Summary
		for _, s := range []string{h.Type, h.Status, h.Priority, h.Assignee} {
			if s != "" {
				line += " · " + s
			}
		}
		if !h.Updated.IsZero() {
			line += " · updated " + h.Updated.UTC().Format("2006-01-02")
		}
		line += "\n"
		if tokenizer.Count(header(len(kept)+1)+body.String()+line) > budget {
			break
		}
		body.WriteString(line)
		kept = append(kept, h)
	}
	if len(kept) == 0 {
		return "", kept, 0
	}
	text := header(len(kept)) + body.String()
	return text, kept, tokenizer.Count(text)
}

// JiraIssueInput is the jira_issue tool input.
type JiraIssueInput struct {
	Key       string `json:"key" jsonschema:"Issue key, e.g. PAY-123. Required."`
	Comments  *bool  `json:"comments,omitempty" jsonschema:"Include comments, newest first (default true)."`
	MaxTokens int    `json:"max_tokens,omitempty" jsonschema:"Token budget (default 2000). Never exceeded; a longer issue is cut from the end (oldest comments first) with a marker."`
}

// JiraIssueOutput is the structured jira_issue result. The Markdown in
// content carries everything else; the schema stays small because every
// client pays for it in each session.
type JiraIssueOutput struct {
	Key         string `json:"key"`
	Status      string `json:"status,omitempty"`
	URL         string `json:"url"`
	Content     string `json:"content"`
	Tokens      int    `json:"tokens"`
	IssueTokens int    `json:"issue_tokens"`
	Budget      int    `json:"budget"`
	Truncated   bool   `json:"truncated"`
}

func (t *tools) jiraIssue(ctx context.Context, req *mcp.CallToolRequest, in JiraIssueInput) (_ *mcp.CallToolResult, out JiraIssueOutput, err error) {
	start := time.Now()
	defer func() {
		t.record(start, metrics.Request{Name: "jira_issue", Query: in.Key, Tokens: out.Tokens, Chunks: 1}, err)
	}()
	if in.MaxTokens < 0 {
		return nil, out, errors.New("max_tokens must be positive")
	}
	budget := cmpOr(in.MaxTokens, jiraIssueDefaultTokens)
	cr, err := t.deps.Jira.Resolve(requestHeader(req))
	if err != nil {
		return nil, out, err
	}
	is, err := t.deps.Jira.Issue(ctx, cr, in.Key)
	if err != nil {
		return nil, out, err
	}
	md := is.Markdown(in.Comments == nil || *in.Comments)
	text, truncated, ok := bundle.Fit(md, budget)
	if !ok {
		return nil, out, fmt.Errorf("max_tokens %d is too small for issue %s; try at least 200", budget, is.Key)
	}
	out = JiraIssueOutput{
		Key: is.Key, Status: is.Status, URL: is.URL, Content: text, Tokens: tokenizer.Count(text), IssueTokens: tokenizer.Count(md), Budget: budget, Truncated: truncated,
	}
	return textResult(text), out, nil
}

// CompileBundleIssueInput is compile_bundle's input when Jira is
// configured: the plain input plus an issue key.
type CompileBundleIssueInput struct {
	TaskDescription string   `json:"task_description,omitempty" jsonschema:"What you are about to do, in one or two sentences, in any language (e.g. 'add partial refunds to the merchant portal'). Required unless issue is set; a sentence retrieves better than keywords."`
	Issue           string   `json:"issue,omitempty" jsonschema:"Jira issue key the task is about, e.g. PAY-123. Its text steers retrieval and a condensed copy heads the bundle."`
	MaxTokens       int      `json:"max_tokens,omitempty" jsonschema:"Token budget for the whole bundle including the issue, header and source lines (default 2000, must be positive). Never exceeded."`
	Scopes          []string `json:"scopes,omitempty" jsonschema:"Knowledge scopes to draw from, e.g. [\"domain\",\"conventions\",\"projects/ledger-service\"]. Default: domain and conventions. Add the project scope when working inside a project."`
	Paths           []string `json:"paths,omitempty" jsonschema:"Repository-relative code paths the task touches (e.g. [\"services/payment/refund.go\"]). Rules whose refs cover them come first in the bundle."`
}

// IssueSummary describes the issue part of a compile_bundle result.
type IssueSummary struct {
	Key       string `json:"key"`
	URL       string `json:"url"`
	Tokens    int    `json:"tokens"`
	Truncated bool   `json:"truncated"`
}

// CompileBundleIssueOutput is compile_bundle's result when Jira is
// configured.
type CompileBundleIssueOutput struct {
	CompileBundleOutput
	Issue *IssueSummary `json:"issue,omitempty"`
}

func (t *tools) compileBundleWithIssue(ctx context.Context, req *mcp.CallToolRequest, in CompileBundleIssueInput) (*mcp.CallToolResult, CompileBundleIssueOutput, error) {
	if strings.TrimSpace(in.Issue) == "" {
		res, out, err := t.compileBundle(ctx, req, CompileBundleInput{
			TaskDescription: in.TaskDescription, MaxTokens: in.MaxTokens, Scopes: in.Scopes, Paths: in.Paths,
		})
		return res, CompileBundleIssueOutput{CompileBundleOutput: out}, err
	}
	return t.issueBundle(ctx, req, in)
}

// issueBundle compiles a bundle for a Jira issue: the issue, cut to its
// share of the budget, followed by the knowledge its text retrieves. The
// whole never exceeds the same effective budget a plain bundle has.
func (t *tools) issueBundle(ctx context.Context, req *mcp.CallToolRequest, in CompileBundleIssueInput) (_ *mcp.CallToolResult, out CompileBundleIssueOutput, err error) {
	start := time.Now()
	key := strings.ToUpper(strings.TrimSpace(in.Issue))
	task := strings.TrimSpace(in.TaskDescription)
	label := key
	if task != "" {
		label = key + ": " + task
	}
	defer func() {
		t.record(start, metrics.Request{
			Name: "compile_bundle", Query: label, Scopes: out.Scopes, Tokens: out.Tokens, Chunks: len(out.Sections),
		}, err)
	}()
	if in.MaxTokens < 0 {
		return nil, out, errors.New("max_tokens must be positive")
	}
	if !jira.ValidKey(key) {
		return nil, out, fmt.Errorf("invalid issue key %q (expected e.g. PAY-123)", in.Issue)
	}
	cr, err := t.deps.Jira.Resolve(requestHeader(req))
	if err != nil {
		return nil, out, err
	}
	is, err := t.deps.Jira.Issue(ctx, cr, key)
	if err != nil {
		return nil, out, err
	}
	budget := search.Budget(cmpOr(in.MaxTokens, t.deps.Compiler.DefaultMaxTokens()))
	md := is.Markdown(true)
	short := fmt.Sprintf("# %s: %s\n\nSource: Jira · %s\n%s", is.Key, is.Summary, is.Status, is.URL)
	query := strings.TrimSpace(task + " " + is.Plain(issueQueryRunes))

	var res *bundle.Result
	var issueText, content string
	var truncated bool
	for room := budget * issueSharePercent / 100; ; {
		var ok bool
		issueText, truncated, ok = bundle.Fit(md, room)
		if !ok { // too little room for a useful head: keep the title line
			issueText, truncated = short, true
			if tokenizer.Count(short) > room {
				return nil, out, fmt.Errorf("max_tokens is too small for an issue bundle; try at least %d", tokenizer.Count(short)*4)
			}
		}
		rest := budget - tokenizer.Count(issueText) - 1
		if rest < 1 {
			return nil, out, fmt.Errorf("max_tokens is too small for an issue bundle; try at least %d", tokenizer.Count(short)*4)
		}
		res, err = t.deps.Compiler.Compile(ctx, bundle.Request{
			Task: label, Query: query, Scopes: in.Scopes, Paths: in.Paths,
			MaxTokens: int(float64(rest) / (1 - search.BudgetHeadroom)),
		})
		if err != nil {
			return nil, out, err
		}
		content = issueText + "\n" + res.Content
		if tokenizer.Count(content) <= budget {
			break
		}
		// Token estimates are not strictly additive; give the issue less.
		room = min(room, tokenizer.Count(issueText)) - 10
	}
	t.served("compile_bundle", sectionDocs(res.Sections))
	t.logQuery(ctx, store.QueryRecord{
		Name: "compile_bundle", Query: label, Scopes: res.Scopes, Mode: t.deps.Searcher.Mode(),
		Results: len(res.Sections), TopScore: res.TopScore, Margin: res.Margin, Tokens: res.Tokens,
		BundleID: res.ID, Client: clientName(req),
	})
	out = CompileBundleIssueOutput{
		CompileBundleOutput: CompileBundleOutput{
			BundleID: res.ID, Tokens: tokenizer.Count(content), Budget: budget, Sections: res.Sections,
			Scopes: res.Scopes, Truncated: res.Truncated, Cached: res.Cached, Content: content,
		},
		Issue: &IssueSummary{Key: is.Key, URL: is.URL, Tokens: tokenizer.Count(issueText), Truncated: truncated},
	}
	return textResult(content), out, nil
}
