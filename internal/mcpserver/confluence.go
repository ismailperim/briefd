package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/confluence"
	"github.com/ismailperim/briefd/internal/metrics"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

const (
	confluenceSearchDefaultTokens = 1000
	confluenceSearchDefaultLimit  = 10
	confluenceSearchMaxLimit      = 25
	confluencePageDefaultTokens   = 3000
)

func (t *tools) addConfluence(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "confluence_search",
		Description: "Search the team's Confluence pages live (full-text, Confluence's own ranking) and return titles, spaces, last-updated dates, links and short excerpts within a token budget. Use it when the reviewed knowledge base (compile_bundle, search_context) does not cover a topic, or to find a page someone mentioned; then read a hit with confluence_page. Read-only; results are not part of briefd's index and may be less curated than the knowledge base.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, t.confluenceSearch)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "confluence_page",
		Description: "Read one Confluence page by the id from confluence_search, converted to compact Markdown and cut to a token budget (a marker says when it was truncated). Read-only. Content comes straight from Confluence; to turn something here into a reviewed team rule, use propose_update on the knowledge base.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, t.confluencePage)
}

func ptr[T any](v T) *T { return &v }

func requestHeader(req *mcp.CallToolRequest) http.Header {
	if req == nil || req.Extra == nil {
		return nil
	}
	return req.Extra.Header
}

// ConfluenceSearchInput is the confluence_search tool input.
type ConfluenceSearchInput struct {
	Query     string   `json:"query" jsonschema:"Words to look for in Confluence pages, in any language (e.g. 'chargeback dispute flow'). Required, non-empty."`
	Spaces    []string `json:"spaces,omitempty" jsonschema:"Optional Confluence space keys to search, e.g. [\"ENG\"]. Narrows the spaces this server allows; it cannot widen them."`
	Limit     int      `json:"limit,omitempty" jsonschema:"Maximum number of pages (default 10, max 25)."`
	MaxTokens int      `json:"max_tokens,omitempty" jsonschema:"Token budget for the result list (default 1000). Never exceeded; lower-ranked hits are omitted first."`
}

// ConfluenceSearchOutput is the structured confluence_search result.
type ConfluenceSearchOutput struct {
	Hits    []confluence.Hit `json:"hits"`
	Tokens  int              `json:"tokens"`
	Budget  int              `json:"budget"`
	Omitted int              `json:"omitted"`
}

func (t *tools) confluenceSearch(ctx context.Context, req *mcp.CallToolRequest, in ConfluenceSearchInput) (_ *mcp.CallToolResult, out ConfluenceSearchOutput, err error) {
	start := time.Now()
	defer func() {
		t.record(start, metrics.Request{
			Name: "confluence_search", Query: in.Query, Scopes: in.Spaces, Tokens: out.Tokens, Chunks: len(out.Hits), Omitted: out.Omitted,
		}, err)
	}()
	if strings.TrimSpace(in.Query) == "" {
		return nil, out, errors.New("query must not be empty")
	}
	if in.MaxTokens < 0 || in.Limit < 0 {
		return nil, out, errors.New("max_tokens and limit must be positive")
	}
	budget := cmpOr(in.MaxTokens, confluenceSearchDefaultTokens)
	limit := min(cmpOr(in.Limit, confluenceSearchDefaultLimit), confluenceSearchMaxLimit)
	cr, err := t.deps.Confluence.Resolve(requestHeader(req))
	if err != nil {
		return nil, out, err
	}
	hits, err := t.deps.Confluence.Search(ctx, cr, in.Query, in.Spaces, limit)
	if err != nil {
		return nil, out, err
	}
	text, kept, tokens := renderHits(in.Query, hits, budget)
	out = ConfluenceSearchOutput{Hits: kept, Tokens: tokens, Budget: budget, Omitted: len(hits) - len(kept)}
	return textResult(text), out, nil
}

// renderHits lists hits in rank order while the text stays within budget.
// An excerpt that does not fit is dropped before the hit itself is.
func renderHits(query string, hits []confluence.Hit, budget int) (string, []confluence.Hit, int) {
	if len(hits) == 0 {
		msg := fmt.Sprintf("No Confluence pages match %q.", query)
		if tokenizer.Count(msg) > budget {
			return "", []confluence.Hit{}, 0
		}
		return msg, []confluence.Hit{}, tokenizer.Count(msg)
	}
	header := func(n int) string {
		return fmt.Sprintf("Confluence: %d of %d page(s) for %q. Read one with confluence_page(id).\n", n, len(hits), query)
	}
	kept := []confluence.Hit{}
	var body strings.Builder
	for i, h := range hits {
		line := fmt.Sprintf("\n[%d] %s", i+1, h.Title)
		if h.Space != "" {
			line += " · " + h.Space
		}
		if !h.UpdatedAt.IsZero() {
			line += " · updated " + h.UpdatedAt.UTC().Format("2006-01-02")
		}
		line += fmt.Sprintf(" · id %s\n%s\n", h.ID, h.URL)
		full := line
		if h.Excerpt != "" {
			full += h.Excerpt + "\n"
		}
		switch {
		case tokenizer.Count(header(len(kept)+1)+body.String()+full) <= budget:
			body.WriteString(full)
		case tokenizer.Count(header(len(kept)+1)+body.String()+line) <= budget:
			body.WriteString(line)
		default:
			continue
		}
		kept = append(kept, h)
	}
	if len(kept) == 0 {
		return "", kept, 0
	}
	text := header(len(kept)) + body.String()
	return text, kept, tokenizer.Count(text)
}

// ConfluencePageInput is the confluence_page tool input.
type ConfluencePageInput struct {
	ID        string `json:"id" jsonschema:"The numeric page id from confluence_search. Required."`
	MaxTokens int    `json:"max_tokens,omitempty" jsonschema:"Token budget for the page (default 3000). Never exceeded; a longer page is cut at a line boundary with a marker."`
}

// ConfluencePageOutput is the structured confluence_page result.
type ConfluencePageOutput struct {
	confluence.Page
	Content    string `json:"content"`
	Tokens     int    `json:"tokens"`
	PageTokens int    `json:"page_tokens"`
	Budget     int    `json:"budget"`
	Truncated  bool   `json:"truncated"`
}

func (t *tools) confluencePage(ctx context.Context, req *mcp.CallToolRequest, in ConfluencePageInput) (_ *mcp.CallToolResult, out ConfluencePageOutput, err error) {
	start := time.Now()
	defer func() {
		t.record(start, metrics.Request{Name: "confluence_page", Query: in.ID, Tokens: out.Tokens, Chunks: 1}, err)
	}()
	if in.MaxTokens < 0 {
		return nil, out, errors.New("max_tokens must be positive")
	}
	budget := cmpOr(in.MaxTokens, confluencePageDefaultTokens)
	cr, err := t.deps.Confluence.Resolve(requestHeader(req))
	if err != nil {
		return nil, out, err
	}
	p, err := t.deps.Confluence.Page(ctx, cr, strings.TrimSpace(in.ID))
	if err != nil {
		return nil, out, err
	}
	head := fmt.Sprintf("# %s\n\nSource: Confluence", p.Title)
	if p.Space != "" {
		head += " · " + p.Space
	}
	if !p.UpdatedAt.IsZero() {
		head += fmt.Sprintf(" · version %d, updated %s", p.Version, p.UpdatedAt.UTC().Format("2006-01-02"))
	}
	head += "\n" + p.URL + "\n\n"
	// Token estimates are not strictly additive, so shrink the room until
	// the assembled text itself fits.
	var body, text string
	var truncated bool
	for room := budget - tokenizer.Count(head); ; room -= 4 {
		var ok bool
		body, truncated, ok = bundle.Fit(p.Markdown, room)
		if !ok {
			return nil, out, fmt.Errorf("max_tokens %d is too small for page %s; try at least %d", budget, p.ID, tokenizer.Count(head)+100)
		}
		if text = head + body; tokenizer.Count(text) <= budget {
			break
		}
	}
	out = ConfluencePageOutput{
		Page: *p, Content: body, Tokens: tokenizer.Count(text), PageTokens: tokenizer.Count(p.Markdown),
		Budget: budget, Truncated: truncated,
	}
	return textResult(text), out, nil
}

func cmpOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
