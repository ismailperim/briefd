// Package search turns user queries into ranked chunks that fit a token
// budget. In v0.1 it wraps FTS5/BM25; M3 adds vector retrieval and RRF
// fusion behind the same API.
package search

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/ismailperim/briefd/internal/store"
)

// Defaults used when a Query leaves a field zero.
const (
	DefaultTopK      = 8
	DefaultMaxTokens = 2000
	// BudgetHeadroom is the fraction of max_tokens deliberately left unused
	// to absorb tokenizer estimation error (SPEC §5).
	BudgetHeadroom = 0.05
	// candidateMultiplier controls how many extra hits are fetched so the
	// budget can still be filled when the top hits are large.
	candidateMultiplier = 3
)

// Options configures a Searcher.
type Options struct {
	DefaultScopes    []string
	DefaultMaxTokens int
	MaxTopK          int
}

// Query describes a search request.
type Query struct {
	Text      string
	Scopes    []string
	TopK      int
	MaxTokens int
}

// Result is a ranked, budget-fitted set of chunks.
type Result struct {
	Chunks []store.ChunkHit `json:"chunks"`
	// TotalTokens is the sum of Chunks' tokens; never exceeds the budget.
	TotalTokens int `json:"total_tokens"`
	// Budget is the effective budget after headroom.
	Budget int `json:"budget"`
	// Omitted counts ranked chunks dropped because they did not fit.
	Omitted int `json:"omitted"`
	// Scopes actually searched.
	Scopes []string `json:"scopes"`
}

// Searcher runs queries against a store.
type Searcher struct {
	store *store.Store
	opts  Options
}

// New returns a Searcher backed by st.
func New(st *store.Store, opts Options) *Searcher {
	if len(opts.DefaultScopes) == 0 {
		opts.DefaultScopes = []string{"domain", "conventions"}
	}
	if opts.DefaultMaxTokens <= 0 {
		opts.DefaultMaxTokens = DefaultMaxTokens
	}
	if opts.MaxTopK <= 0 {
		opts.MaxTopK = 50
	}
	return &Searcher{store: st, opts: opts}
}

// Search returns ranked chunks for q whose total tokens fit within
// q.MaxTokens (minus headroom). Chunks are taken in rank order; a chunk that
// does not fit is skipped and counted in Result.Omitted, and smaller chunks
// further down may still be included. An empty query yields no results.
func (s *Searcher) Search(ctx context.Context, q Query) (Result, error) {
	scopes := q.Scopes
	if len(scopes) == 0 {
		scopes = s.opts.DefaultScopes
	}
	topK := q.TopK
	if topK <= 0 {
		topK = DefaultTopK
	}
	if topK > s.opts.MaxTopK {
		topK = s.opts.MaxTopK
	}
	maxTokens := q.MaxTokens
	if maxTokens <= 0 {
		maxTokens = s.opts.DefaultMaxTokens
	}
	if maxTokens < 0 {
		return Result{}, fmt.Errorf("max_tokens must be positive")
	}
	res := Result{
		Chunks: []store.ChunkHit{},
		Budget: Budget(maxTokens),
		Scopes: scopes,
	}

	match := BuildMatch(q.Text)
	if match == "" {
		return res, nil
	}
	hits, err := s.store.SearchFTS(ctx, match, scopes, topK*candidateMultiplier)
	if err != nil {
		return res, err
	}
	for _, h := range hits {
		if len(res.Chunks) == topK {
			break
		}
		if res.TotalTokens+h.Tokens > res.Budget {
			res.Omitted++
			continue
		}
		res.Chunks = append(res.Chunks, h)
		res.TotalTokens += h.Tokens
	}
	return res, nil
}

// Budget returns the usable token budget for maxTokens after headroom.
func Budget(maxTokens int) int {
	b := int(float64(maxTokens) * (1 - BudgetHeadroom))
	if b < 1 && maxTokens > 0 {
		return 1
	}
	return b
}

// BuildMatch converts free text into a safe FTS5 expression. Every term is
// quoted (so user input can never inject FTS5 operators or break syntax)
// and terms are OR-ed: BM25 still ranks chunks matching more terms higher,
// while a single unknown word does not zero the result set.
func BuildMatch(text string) string {
	terms := Terms(text)
	if len(terms) == 0 {
		return ""
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " OR ")
}

// Terms splits text into search terms: runs of letters and digits, lower
// cased. Everything else is a separator.
func Terms(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
