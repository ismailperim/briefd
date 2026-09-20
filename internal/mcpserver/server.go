// Package mcpserver exposes briefd's knowledge tools over the Model Context
// Protocol using the official Go SDK (streamable HTTP transport).
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/metrics"
	"github.com/ismailperim/briefd/internal/proposal"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

// Deps are the collaborators the tools need.
type Deps struct {
	Store    *store.Store
	Searcher *search.Searcher
	Compiler *bundle.Compiler
	// Proposals creates git branches for propose_update; nil or unavailable
	// makes the tool return a clear error.
	Proposals *proposal.Service
	// Metrics receives one record per tool call; nil disables instrumentation.
	Metrics *metrics.Registry
	Version string
	Logger  *slog.Logger
}

// New builds an MCP server with the briefd tool set registered.
func New(d Deps) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "briefd",
		Title:   "briefd — team knowledge, compiled to a token budget",
		Version: d.Version,
	}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       d.Logger,
	})
	t := &tools{deps: d}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_context",
		Description: "Search the team knowledge base and return the most relevant sections that fit a token budget. Use it before implementing anything that may be governed by domain rules, conventions or past decisions.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, t.searchContext)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "compile_bundle",
		Description: "Compile the knowledge relevant to a task into one context block that fits a token budget: deduplicated, ordered domain → conventions → project, with a source line per section and a bundle_id. Deterministic and cached; prefer it over search_context when you want context to read rather than results to inspect.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, t.compileBundle)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "report_usage",
		Description: "Optional feedback after using a bundle: which sections were actually useful. Helps improve ranking over time.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}, t.reportUsage)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "propose_update",
		Description: "Propose a change to a knowledge document. Creates a git branch briefd/proposal-<id> with the new content (and a pull request when configured) for humans to review; it never changes what briefd serves until merged. Provide the complete new file content.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: false},
	}, t.proposeUpdate)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_document",
		Description: "Fetch one knowledge document in full by its repository path (as returned in search results as doc_path).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, t.getDocument)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_scopes",
		Description: "List the knowledge scopes available (shared 'domain' and 'conventions', plus one 'projects/<name>' per project) with document and chunk counts.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, t.listScopes)

	return srv
}

// ToolDefinitionsJSON returns the tool list as a client would receive it,
// for estimating the per-session token cost of exposing briefd's tools.
func ToolDefinitionsJSON() string {
	ctx := context.Background()
	srv := New(Deps{})
	ct, stt := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, stt, nil)
	if err != nil {
		return ""
	}
	defer ss.Close() //nolint:errcheck // in-memory session
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "briefd-bench", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return ""
	}
	defer cs.Close() //nolint:errcheck // in-memory session
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		return ""
	}
	b, _ := json.Marshal(list.Tools)
	return string(b)
}

// Handler returns the streamable-HTTP handler for srv. Sessions are
// stateless: every request is self-contained, which suits a service that
// sits behind a bearer token and may be restarted at any time.
func Handler(srv *mcp.Server, logger *slog.Logger) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		Logger:    logger,
	})
}

const instructions = `briefd serves your team's shared knowledge (domain rules, conventions,
architecture decisions, project notes) on demand. Before writing code that could be
affected by team rules, call compile_bundle with a short description of the task to get a
budgeted context block, or search_context to inspect ranked sections. Pass
scopes=["domain","conventions","projects/<name>"] to include a project's own notes. Call
get_document when you need a full document, and report_usage with the bundle_id and the
chunk_ids that helped once you are done. If you find knowledge that is wrong or missing,
propose_update creates a reviewable branch/PR — you never edit the knowledge directly.`

type tools struct {
	deps Deps
}

// record reports a finished tool call to the metrics registry.
func (t *tools) record(start time.Time, req metrics.Request, err error) {
	if t.deps.Metrics == nil {
		return
	}
	req.Surface = metrics.SurfaceMCP
	req.Duration = time.Since(start)
	req.Error = err != nil
	t.deps.Metrics.Record(req)
}

// SearchContextInput is the search_context tool input.
type SearchContextInput struct {
	Query     string   `json:"query" jsonschema:"What you are working on or looking for, in natural language (e.g. 'retry policy for acquirer calls')"`
	MaxTokens int      `json:"max_tokens,omitempty" jsonschema:"Token budget for the returned sections (default 2000). The result never exceeds it."`
	Scopes    []string `json:"scopes,omitempty" jsonschema:"Knowledge scopes to search, e.g. [\"domain\",\"conventions\",\"projects/ledger-service\"]. Default: domain and conventions."`
	TopK      int      `json:"top_k,omitempty" jsonschema:"Maximum number of sections to return (default 8)"`
}

// SearchContextOutput is the structured search_context result.
type SearchContextOutput struct {
	Chunks      []ChunkOut `json:"chunks"`
	TotalTokens int        `json:"total_tokens"`
	Budget      int        `json:"budget"`
	Omitted     int        `json:"omitted"`
	Scopes      []string   `json:"scopes"`
}

// ChunkOut is one returned section.
type ChunkOut struct {
	ChunkID string  `json:"chunk_id"`
	DocPath string  `json:"doc_path"`
	Scope   string  `json:"scope"`
	Heading string  `json:"heading"`
	Content string  `json:"content"`
	Tokens  int     `json:"tokens"`
	Score   float64 `json:"score"`
}

func (t *tools) searchContext(ctx context.Context, _ *mcp.CallToolRequest, in SearchContextInput) (_ *mcp.CallToolResult, out SearchContextOutput, err error) {
	start := time.Now()
	defer func() {
		t.record(start, metrics.Request{
			Name: "search_context", Query: in.Query, Scopes: out.Scopes,
			Tokens: out.TotalTokens, Chunks: len(out.Chunks), Omitted: out.Omitted,
		}, err)
	}()
	if strings.TrimSpace(in.Query) == "" {
		return nil, SearchContextOutput{}, errors.New("query must not be empty")
	}
	if in.MaxTokens < 0 || in.TopK < 0 {
		return nil, SearchContextOutput{}, errors.New("max_tokens and top_k must be positive")
	}
	res, err := t.deps.Searcher.Search(ctx, search.Query{
		Text: in.Query, Scopes: in.Scopes, TopK: in.TopK, MaxTokens: in.MaxTokens,
	})
	if err != nil {
		return nil, SearchContextOutput{}, err
	}
	out = SearchContextOutput{
		Chunks:      make([]ChunkOut, 0, len(res.Chunks)),
		TotalTokens: res.TotalTokens,
		Budget:      res.Budget,
		Omitted:     res.Omitted,
		Scopes:      res.Scopes,
	}
	for _, h := range res.Chunks {
		out.Chunks = append(out.Chunks, ChunkOut{
			ChunkID: h.ChunkID, DocPath: h.DocPath, Scope: h.Scope, Heading: h.HeadingPath,
			Content: h.Content, Tokens: h.Tokens, Score: h.Score,
		})
	}
	return textResult(RenderChunks(out)), out, nil
}

// RenderChunks formats a search result as Markdown for the model: a short
// header, then each section with its source. Kept compact because every
// character here is spent from the caller's budget.
func RenderChunks(out SearchContextOutput) string {
	if len(out.Chunks) == 0 {
		return fmt.Sprintf("No matching knowledge in scopes %s.", strings.Join(out.Scopes, ", "))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d section(s), %d tokens (budget %d, %d omitted). Scopes: %s\n",
		len(out.Chunks), out.TotalTokens, out.Budget, out.Omitted, strings.Join(out.Scopes, ", "))
	for i, c := range out.Chunks {
		fmt.Fprintf(&sb, "\n--- [%d] %s — %s (%s)\n%s\n", i+1, c.DocPath, c.Heading, c.Scope, bundle.Body(c.Content))
	}
	return sb.String()
}

// CompileBundleInput is the compile_bundle tool input.
type CompileBundleInput struct {
	TaskDescription string   `json:"task_description" jsonschema:"What you are about to do, in one or two sentences (e.g. 'add partial refunds to the merchant portal')"`
	MaxTokens       int      `json:"max_tokens,omitempty" jsonschema:"Token budget for the whole bundle (default 2000). Never exceeded."`
	Scopes          []string `json:"scopes,omitempty" jsonschema:"Knowledge scopes to draw from. Default: domain and conventions."`
}

// CompileBundleOutput is the structured compile_bundle result.
type CompileBundleOutput struct {
	BundleID  string                `json:"bundle_id"`
	Tokens    int                   `json:"tokens"`
	Budget    int                   `json:"budget"`
	Sections  []store.BundleSection `json:"sections"`
	Scopes    []string              `json:"scopes"`
	Truncated bool                  `json:"truncated"`
	Cached    bool                  `json:"cached"`
	Content   string                `json:"content"`
}

func (t *tools) compileBundle(ctx context.Context, _ *mcp.CallToolRequest, in CompileBundleInput) (_ *mcp.CallToolResult, out CompileBundleOutput, err error) {
	start := time.Now()
	defer func() {
		t.record(start, metrics.Request{
			Name: "compile_bundle", Query: in.TaskDescription, Scopes: out.Scopes, Tokens: out.Tokens, Chunks: len(out.Sections),
		}, err)
	}()
	if in.MaxTokens < 0 {
		return nil, out, errors.New("max_tokens must be positive")
	}
	res, err := t.deps.Compiler.Compile(ctx, bundle.Request{Task: in.TaskDescription, Scopes: in.Scopes, MaxTokens: in.MaxTokens})
	if err != nil {
		return nil, out, err
	}
	out = CompileBundleOutput{
		BundleID: res.ID, Tokens: res.Tokens, Budget: res.Budget, Sections: res.Sections,
		Scopes: res.Scopes, Truncated: res.Truncated, Cached: res.Cached, Content: res.Content,
	}
	return textResult(res.Content), out, nil
}

// ReportUsageInput is the report_usage tool input.
type ReportUsageInput struct {
	BundleID       string   `json:"bundle_id" jsonschema:"The bundle_id returned by compile_bundle"`
	UsefulChunkIDs []string `json:"useful_chunk_ids" jsonschema:"chunk_ids from the bundle's sections that were actually useful (may be empty)"`
}

// ReportUsageOutput acknowledges stored feedback.
type ReportUsageOutput struct {
	Recorded int `json:"recorded"`
}

func (t *tools) reportUsage(ctx context.Context, req *mcp.CallToolRequest, in ReportUsageInput) (_ *mcp.CallToolResult, out ReportUsageOutput, err error) {
	start := time.Now()
	defer func() { t.record(start, metrics.Request{Name: "report_usage", Query: in.BundleID}, err) }()
	if strings.TrimSpace(in.BundleID) == "" {
		return nil, out, errors.New("bundle_id must not be empty")
	}
	b, err := t.deps.Store.GetBundle(ctx, in.BundleID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, out, fmt.Errorf("bundle %q not found (bundles are dropped when the knowledge changes)", in.BundleID)
	}
	if err != nil {
		return nil, out, err
	}
	client := clientName(req)
	useful := make(map[string]bool, len(in.UsefulChunkIDs))
	for _, id := range in.UsefulChunkIDs {
		useful[id] = true
	}
	events := make([]store.UsageEvent, 0, len(b.Sections))
	for _, s := range b.Sections {
		events = append(events, store.UsageEvent{BundleID: b.ID, ChunkID: s.ChunkID, Useful: useful[s.ChunkID], Client: client})
	}
	if err := t.deps.Store.PutUsage(ctx, events); err != nil {
		return nil, out, err
	}
	out = ReportUsageOutput{Recorded: len(events)}
	return textResult(fmt.Sprintf("Recorded feedback for %d section(s) of bundle %s. Thank you.", len(events), b.ID)), out, nil
}

// ProposeUpdateInput is the propose_update tool input.
type ProposeUpdateInput struct {
	DocPath           string `json:"doc_path" jsonschema:"Repository-relative path of the document to change or create, e.g. domain/rules/refunds.md"`
	ChangeDescription string `json:"change_description" jsonschema:"Why this change is needed, for the reviewer (becomes the commit/PR description)"`
	NewContent        string `json:"new_content" jsonschema:"The complete new content of the file (Markdown, including front matter if any)"`
}

// ProposeUpdateOutput describes the created branch.
type ProposeUpdateOutput struct {
	ProposalID string `json:"proposal_id"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
	Pushed     bool   `json:"pushed"`
	PRURL      string `json:"pr_url,omitempty"`
}

func (t *tools) proposeUpdate(ctx context.Context, req *mcp.CallToolRequest, in ProposeUpdateInput) (_ *mcp.CallToolResult, out ProposeUpdateOutput, err error) {
	start := time.Now()
	defer func() { t.record(start, metrics.Request{Name: "propose_update", Query: in.DocPath}, err) }()
	if t.deps.Proposals == nil || !t.deps.Proposals.Available() {
		return nil, out, proposal.ErrUnavailable
	}
	res, err := t.deps.Proposals.Create(ctx, proposal.Request{
		DocPath: in.DocPath, Description: in.ChangeDescription, Content: in.NewContent, Client: clientName(req),
	})
	if err != nil {
		return nil, out, err
	}
	out = ProposeUpdateOutput{ProposalID: res.ID, Branch: res.Branch, Commit: res.Commit, Pushed: res.Pushed, PRURL: res.PRURL}
	msg := fmt.Sprintf("Proposal %s created on branch %s (commit %s).", res.ID, res.Branch, res.Commit[:10])
	switch {
	case res.PRURL != "":
		msg += " Pull request: " + res.PRURL
	case res.Pushed:
		msg += " The branch was pushed; open a pull request from it for review."
	default:
		msg += " The branch exists only in briefd's local checkout (no remote configured)."
	}
	return textResult(msg), out, nil
}

func clientName(req *mcp.CallToolRequest) string {
	if req != nil && req.Session != nil && req.Session.InitializeParams() != nil && req.Session.InitializeParams().ClientInfo != nil {
		return req.Session.InitializeParams().ClientInfo.Name
	}
	return ""
}

// GetDocumentInput is the get_document tool input.
type GetDocumentInput struct {
	DocPath string   `json:"doc_path" jsonschema:"Repository-relative path of the document, e.g. domain/rules/refunds.md"`
	Scopes  []string `json:"scopes,omitempty" jsonschema:"If given, the document must belong to one of these scopes"`
}

// GetDocumentOutput is the get_document tool result.
type GetDocumentOutput struct {
	Path    string   `json:"path"`
	Scope   string   `json:"scope"`
	Title   string   `json:"title"`
	Tags    []string `json:"tags"`
	Content string   `json:"content"`
	Tokens  int      `json:"tokens"`
}

func (t *tools) getDocument(ctx context.Context, _ *mcp.CallToolRequest, in GetDocumentInput) (_ *mcp.CallToolResult, out GetDocumentOutput, err error) {
	start := time.Now()
	defer func() {
		t.record(start, metrics.Request{Name: "get_document", Query: in.DocPath, Scopes: in.Scopes, Tokens: out.Tokens, Chunks: 1}, err)
	}()
	p := strings.TrimPrefix(strings.TrimSpace(in.DocPath), "/")
	if p == "" {
		return nil, GetDocumentOutput{}, errors.New("doc_path must not be empty")
	}
	doc, content, err := t.deps.Store.GetDocument(ctx, p)
	if errors.Is(err, store.ErrNotFound) {
		return nil, GetDocumentOutput{}, fmt.Errorf("document %q not found", p)
	}
	if err != nil {
		return nil, GetDocumentOutput{}, err
	}
	if len(in.Scopes) > 0 && !slices.Contains(in.Scopes, doc.Scope) {
		return nil, GetDocumentOutput{}, fmt.Errorf("document %q is in scope %q, not in the requested scopes", p, doc.Scope)
	}
	out = GetDocumentOutput{
		Path: doc.Path, Scope: doc.Scope, Title: doc.Title, Tags: doc.Tags,
		Content: content, Tokens: tokenizer.Count(content),
	}
	text := fmt.Sprintf("# %s\n\nSource: %s (%s)\n\n%s", doc.Title, doc.Path, doc.Scope, content)
	return textResult(text), out, nil
}

// ListScopesOutput is the list_scopes tool result.
type ListScopesOutput struct {
	Scopes []store.ScopeInfo `json:"scopes"`
}

func (t *tools) listScopes(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (_ *mcp.CallToolResult, out ListScopesOutput, err error) {
	start := time.Now()
	defer func() { t.record(start, metrics.Request{Name: "list_scopes"}, err) }()
	scopes, err := t.deps.Store.ListScopes(ctx)
	if err != nil {
		return nil, ListScopesOutput{}, err
	}
	if scopes == nil {
		scopes = []store.ScopeInfo{}
	}
	var sb strings.Builder
	for _, s := range scopes {
		fmt.Fprintf(&sb, "- %s: %d document(s), %d section(s), ~%d tokens\n", s.Scope, s.Documents, s.Chunks, s.Tokens)
	}
	if sb.Len() == 0 {
		sb.WriteString("The index is empty.")
	}
	return textResult(sb.String()), ListScopesOutput{Scopes: scopes}, nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
