package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/store"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

// BenchOptions configures the token-savings benchmark.
type BenchOptions struct {
	// Budgets are the compile_bundle max_tokens values to measure.
	Budgets []int
	// Baseline lists path prefixes (or exact paths) that a team would
	// realistically paste into a static CLAUDE.md, e.g. "conventions/",
	// "domain/glossary.md".
	Baseline []string
	// ToolOverhead is the token cost of briefd's MCP tool definitions,
	// paid once per session; 0 to omit.
	ToolOverhead int
}

// Scenario is one way of giving an agent knowledge, with its cost and
// how often the golden answer is actually available to the agent.
type Scenario struct {
	Name string `json:"name"`
	// TokensPerTask is the mean knowledge context per task (per session
	// for static files, since they are loaded regardless of the task).
	TokensPerTask float64 `json:"tokens_per_task"`
	MedianTokens  int     `json:"median_tokens"`
	MaxTokens     int     `json:"max_tokens"`
	// Coverage is the fraction of golden queries whose expected section is
	// present in the context the agent receives.
	Coverage float64 `json:"coverage"`
	// Docs is how many documents the scenario exposes (static scenarios).
	Docs int `json:"docs,omitempty"`
}

// BenchReport is the output of Bench.
type BenchReport struct {
	CorpusDocs     int        `json:"corpus_docs"`
	CorpusSections int        `json:"corpus_sections"`
	CorpusTokens   int        `json:"corpus_tokens"`
	Queries        int        `json:"queries"`
	ToolOverhead   int        `json:"tool_overhead_tokens"`
	Scenarios      []Scenario `json:"scenarios"`
}

// Bench measures how many knowledge tokens each scenario spends per task
// and how often the right section is included.
func Bench(ctx context.Context, st *store.Store, c *bundle.Compiler, g *Golden, opts BenchOptions) (*BenchReport, error) {
	docs, err := allDocuments(ctx, st)
	if err != nil {
		return nil, err
	}
	rep := &BenchReport{Queries: len(g.Queries), ToolOverhead: opts.ToolOverhead}
	corpusTokens := 0
	for _, d := range docs {
		rep.CorpusDocs++
		rep.CorpusSections += len(d.chunks)
		corpusTokens += d.tokens()
	}
	rep.CorpusTokens = corpusTokens

	// Static scenarios: the whole corpus, and a curated subset.
	rep.Scenarios = append(rep.Scenarios, Scenario{
		Name: "Everything in CLAUDE.md", TokensPerTask: float64(corpusTokens), MedianTokens: corpusTokens,
		MaxTokens: corpusTokens, Coverage: 1, Docs: rep.CorpusDocs,
	})
	if len(opts.Baseline) > 0 {
		var subset []document
		for _, d := range docs {
			if matchesPrefix(d.path, opts.Baseline) {
				subset = append(subset, d)
			}
		}
		tokens := 0
		for _, d := range subset {
			tokens += d.tokens()
		}
		covered := 0
		for _, q := range g.Queries {
			if staticCovers(q, subset) {
				covered++
			}
		}
		rep.Scenarios = append(rep.Scenarios, Scenario{
			Name: "Curated CLAUDE.md (" + strings.Join(opts.Baseline, ", ") + ")", TokensPerTask: float64(tokens),
			MedianTokens: tokens, MaxTokens: tokens, Coverage: float64(covered) / float64(len(g.Queries)), Docs: len(subset),
		})
	}

	// briefd: one bundle per task per budget, all scopes.
	scopes := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, d := range docs {
		if !seen[d.scope] {
			seen[d.scope] = true
			scopes = append(scopes, d.scope)
		}
	}
	for _, budget := range opts.Budgets {
		var sizes []int
		covered := 0
		for _, q := range g.Queries {
			res, err := c.Compile(ctx, bundle.Request{Task: q.Query, Scopes: scopes, MaxTokens: budget})
			if err != nil {
				return nil, fmt.Errorf("bundle for %s: %w", q.ID, err)
			}
			sizes = append(sizes, res.Tokens)
			if bundleCovers(q, res.Sections) {
				covered++
			}
		}
		sort.Ints(sizes)
		sum := 0
		for _, s := range sizes {
			sum += s
		}
		rep.Scenarios = append(rep.Scenarios, Scenario{
			Name:          fmt.Sprintf("briefd compile_bundle (max_tokens=%d)", budget),
			TokensPerTask: float64(sum) / float64(len(sizes)),
			MedianTokens:  sizes[len(sizes)/2],
			MaxTokens:     sizes[len(sizes)-1],
			Coverage:      float64(covered) / float64(len(g.Queries)),
		})
	}
	return rep, nil
}

type document struct {
	path, scope string
	chunks      []store.ChunkHit
}

func (d document) tokens() int {
	n := 0
	for _, c := range d.chunks {
		n += c.Tokens
	}
	return n
}

func allDocuments(ctx context.Context, st *store.Store) ([]document, error) {
	hits, err := st.AllChunks(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]*document{}
	var order []string
	for _, h := range hits {
		d, ok := byPath[h.DocPath]
		if !ok {
			d = &document{path: h.DocPath, scope: h.Scope}
			byPath[h.DocPath] = d
			order = append(order, h.DocPath)
		}
		d.chunks = append(d.chunks, h)
	}
	sort.Strings(order)
	out := make([]document, 0, len(order))
	for _, p := range order {
		out = append(out, *byPath[p])
	}
	return out, nil
}

func matchesPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

func staticCovers(q Query, docs []document) bool {
	for _, exp := range q.Expected {
		for _, d := range docs {
			if d.path != exp.Path {
				continue
			}
			for _, c := range d.chunks {
				if Matches(c, exp) {
					return true
				}
			}
		}
	}
	return false
}

func bundleCovers(q Query, sections []store.BundleSection) bool {
	for _, exp := range q.Expected {
		for _, s := range sections {
			if Matches(store.ChunkHit{DocPath: s.DocPath, HeadingPath: s.Heading}, exp) {
				return true
			}
		}
	}
	return false
}

// ToolOverheadTokens estimates the once-per-session cost of exposing tool
// definitions to the model, from their JSON.
func ToolOverheadTokens(toolsJSON string) int {
	return tokenizer.Count(toolsJSON)
}
