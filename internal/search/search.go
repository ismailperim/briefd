// Package search turns user queries into ranked chunks. In v0.1 it wraps
// FTS5/BM25; M3 adds vector retrieval and RRF fusion behind the same API.
package search

import (
	"context"
	"strings"
	"unicode"

	"github.com/ismailperim/briefd/internal/store"
)

// DefaultScopes applies when a caller does not specify scopes (SPEC §2).
var DefaultScopes = []string{"domain", "conventions"}

// DefaultTopK is the default number of results.
const DefaultTopK = 8

// Query describes a search request.
type Query struct {
	Text   string
	Scopes []string
	TopK   int
}

// Searcher runs queries against a store.
type Searcher struct {
	store *store.Store
}

// New returns a Searcher backed by st.
func New(st *store.Store) *Searcher {
	return &Searcher{store: st}
}

// Search returns ranked chunks for q. An empty or stop-word-only query
// yields no results rather than an error.
func (s *Searcher) Search(ctx context.Context, q Query) ([]store.ChunkHit, error) {
	match := BuildMatch(q.Text)
	if match == "" {
		return nil, nil
	}
	scopes := q.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	topK := q.TopK
	if topK <= 0 {
		topK = DefaultTopK
	}
	return s.store.SearchFTS(ctx, match, scopes, topK)
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
