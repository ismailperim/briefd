// Package search turns user queries into ranked chunks that fit a token
// budget: FTS5/BM25 and brute-force vector retrieval fused with reciprocal
// rank fusion (SPEC §5), or BM25 alone when embeddings are disabled.
package search

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/ismailperim/briefd/internal/embed"
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
	// fusionDepth is how many candidates each retriever contributes to RRF.
	fusionDepth = 50
	// anyTermWeight is the RRF weight of BM25 matches that contain only
	// some of the query terms; they are far less reliable than matches
	// containing every term, which get weight 1 like the vector list.
	anyTermWeight = 0.25
)

// Retrieval modes.
const (
	ModeBM25   = "bm25"
	ModeVector = "vector"
	ModeHybrid = "hybrid"
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
	// Mode overrides the retrieval mode ("" = hybrid when embeddings are
	// available, else bm25). Used by eval to compare retrievers.
	Mode string
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
	// Mode is the retrieval mode that produced the result.
	Mode string `json:"mode"`
	// TopScore is the best vector cosine among the candidates (0 in bm25
	// mode). Margin is TopScore minus the median cosine of the top ten:
	// a flat top means the corpus has nothing specific for this query.
	// Absolute cosines are compressed for E5-style models, so treat these
	// as relative signals for ranking gaps, not as thresholds.
	TopScore float64 `json:"top_score"`
	Margin   float64 `json:"margin"`
}

// Searcher runs queries against a store.
type Searcher struct {
	store    *store.Store
	opts     Options
	embedder embed.Embedder
	vectors  *VectorIndex
}

// New returns a BM25-only Searcher backed by st. Call WithEmbedder to
// enable hybrid retrieval.
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

// WithEmbedder enables hybrid retrieval using e and an in-memory vector
// index that the caller refreshes with Reload after indexing.
func (s *Searcher) WithEmbedder(e embed.Embedder) *Searcher {
	s.embedder = e
	s.vectors = NewVectorIndex(e.Name())
	return s
}

// Hybrid reports whether vector retrieval is configured.
func (s *Searcher) Hybrid() bool { return s.embedder != nil }

// Mode is the default retrieval mode: hybrid when an embedder is
// configured, bm25 otherwise.
func (s *Searcher) Mode() string {
	if s.Hybrid() {
		return ModeHybrid
	}
	return ModeBM25
}

// Vectors returns the vector index (nil in BM25-only mode).
func (s *Searcher) Vectors() *VectorIndex { return s.vectors }

// Reload refreshes the in-memory vector index from the store.
func (s *Searcher) Reload(ctx context.Context) error {
	if s.vectors == nil {
		return nil
	}
	return s.vectors.Reload(ctx, s.store)
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
	mode := q.Mode
	if mode == "" {
		mode = ModeBM25
		if s.embedder != nil {
			mode = ModeHybrid
		}
	}
	if mode != ModeBM25 && s.embedder == nil {
		return Result{}, fmt.Errorf("retrieval mode %q requires embeddings", mode)
	}
	res := Result{
		Chunks: []store.ChunkHit{},
		Budget: Budget(maxTokens),
		Scopes: scopes,
		Mode:   mode,
	}
	if strings.TrimSpace(q.Text) == "" {
		return res, nil
	}

	var hits []store.ChunkHit
	var err error
	var conf confidence
	switch mode {
	case ModeBM25:
		hits, err = s.bm25(ctx, q.Text, scopes, topK*candidateMultiplier)
	case ModeVector:
		hits, conf, err = s.vector(ctx, q.Text, scopes, topK*candidateMultiplier)
	default:
		hits, conf, err = s.hybrid(ctx, q.Text, scopes)
	}
	if err != nil {
		return res, err
	}
	res.TopScore, res.Margin = conf.top, conf.margin
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

// bm25 returns chunks matching every query term first (high precision),
// then fills up with chunks matching any term. Ranking the conjunctive
// matches ahead keeps BM25's contribution to RRF trustworthy on natural
// language queries, where OR-matches on common words are mostly noise.
func (s *Searcher) bm25(ctx context.Context, text string, scopes []string, limit int) ([]store.ChunkHit, error) {
	hits, _, err := s.bm25Split(ctx, text, scopes, limit)
	return hits, err
}

// bm25Split is bm25 that also reports how many leading hits matched every
// query term.
func (s *Searcher) bm25Split(ctx context.Context, text string, scopes []string, limit int) (hits []store.ChunkHit, allTerms int, err error) {
	all, anyTerm := BuildMatch(text)
	if anyTerm == "" {
		return nil, 0, nil
	}
	hits, err = s.store.SearchFTS(ctx, all, scopes, limit)
	if err != nil {
		return nil, 0, err
	}
	allTerms = len(hits)
	if len(hits) >= limit || all == anyTerm {
		return hits, allTerms, nil
	}
	more, err := s.store.SearchFTS(ctx, anyTerm, scopes, limit)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		seen[h.ChunkID] = true
	}
	for _, h := range more {
		if len(hits) >= limit {
			break
		}
		if !seen[h.ChunkID] {
			hits = append(hits, h)
		}
	}
	return hits, allTerms, nil
}

// confidence summarizes the vector side of a query: see Result.TopScore.
type confidence struct{ top, margin float64 }

func confidenceOf(vhits []VecHit) confidence {
	if len(vhits) == 0 {
		return confidence{}
	}
	n := len(vhits)
	if n > 10 {
		n = 10
	}
	top := float64(vhits[0].Score)
	return confidence{top: top, margin: top - float64(vhits[n/2].Score)}
}

func (s *Searcher) vector(ctx context.Context, text string, scopes []string, limit int) ([]store.ChunkHit, confidence, error) {
	vec, err := s.embedder.EmbedQuery(ctx, text)
	if err != nil {
		return nil, confidence{}, fmt.Errorf("embedding query: %w", err)
	}
	vhits := s.vectors.Search(vec, scopes, limit)
	ids := make([]string, len(vhits))
	for i, h := range vhits {
		ids[i] = h.ChunkID
	}
	byID, err := s.chunksByID(ctx, ids)
	if err != nil {
		return nil, confidence{}, err
	}
	out := make([]store.ChunkHit, 0, len(vhits))
	for _, vh := range vhits {
		if h, ok := byID[vh.ChunkID]; ok {
			h.Score = float64(vh.Score)
			out = append(out, h)
		}
	}
	return out, confidenceOf(vhits), nil
}

// hybrid fuses the top fusionDepth BM25 and vector candidates with RRF.
// Score is the fused score. A failure to embed the query degrades to BM25
// rather than failing the search.
func (s *Searcher) hybrid(ctx context.Context, text string, scopes []string) ([]store.ChunkHit, confidence, error) {
	bm, allTerms, err := s.bm25Split(ctx, text, scopes, fusionDepth)
	if err != nil {
		return nil, confidence{}, err
	}
	vec, err := s.embedder.EmbedQuery(ctx, text)
	if err != nil {
		return bm, confidence{}, nil //nolint:nilerr // degrade gracefully; the caller still gets BM25 results
	}
	vhits := s.vectors.Search(vec, scopes, fusionDepth)

	bmIDs := make([]string, len(bm))
	byID := make(map[string]store.ChunkHit, len(bm)+len(vhits))
	for i, h := range bm {
		bmIDs[i] = h.ChunkID
		byID[h.ChunkID] = h
	}
	vIDs := make([]string, len(vhits))
	var missing []string
	for i, h := range vhits {
		vIDs[i] = h.ChunkID
		if _, ok := byID[h.ChunkID]; !ok {
			missing = append(missing, h.ChunkID)
		}
	}
	extra, err := s.chunksByID(ctx, missing)
	if err != nil {
		return nil, confidence{}, err
	}
	for id, h := range extra {
		byID[id] = h
	}

	fused := RRFWeighted(RRFK,
		RankedList{IDs: bmIDs[:allTerms], Weight: 1},
		RankedList{IDs: bmIDs[allTerms:], Weight: anyTermWeight, Offset: allTerms},
		RankedList{IDs: vIDs, Weight: 1},
	)
	out := make([]store.ChunkHit, 0, len(fused))
	for _, f := range fused {
		if h, ok := byID[f.ID]; ok {
			h.Score = f.Score
			out = append(out, h)
		}
	}
	return out, confidenceOf(vhits), nil
}

func (s *Searcher) chunksByID(ctx context.Context, ids []string) (map[string]store.ChunkHit, error) {
	hits, err := s.store.GetChunks(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.ChunkHit, len(hits))
	for _, h := range hits {
		out[h.ChunkID] = h
	}
	return out, nil
}

// Budget returns the usable token budget for maxTokens after headroom.
func Budget(maxTokens int) int {
	b := int(float64(maxTokens) * (1 - BudgetHeadroom))
	if b < 1 && maxTokens > 0 {
		return 1
	}
	return b
}

// BuildMatch converts free text into two safe FTS5 expressions: one that
// requires every term (implicit AND) and one that accepts any term (OR).
// Terms are quoted so user input can never inject FTS5 operators or break
// syntax; stop words are dropped so "the" does not match every chunk. Both
// are "" when no searchable term remains.
func BuildMatch(text string) (all, anyTerm string) {
	terms := Terms(text)
	if len(terms) == 0 {
		return "", ""
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " "), strings.Join(quoted, " OR ")
}

// Terms splits text into search terms: runs of letters and digits, lower
// cased, minus stop words. Everything else is a separator. If every word is
// a stop word the words are kept, so a query like "the who" still searches.
func Terms(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := words[:0:0]
	for _, w := range words {
		if !stopWords[w] {
			terms = append(terms, w)
		}
	}
	if len(terms) == 0 {
		return words
	}
	return terms
}

// stopWords are dropped from queries only; documents are indexed in full.
// English function words plus the Turkish ones that show up in questions.
var stopWords = func() map[string]bool {
	list := strings.Fields(`a an the and or but if of to in on at by for with from as is are was were be been being
		do does did done have has had having can could should would will shall may might must
		i we you he she it they me us them my our your his her its their this that these those
		what which who whom whose when where why how there here then than so not no yes
		am up out about into over after before again still also just very too any some all each
		ve veya ile bir bu şu o için mi mı mu mü ne nedir nasıl neden niye kaç hangi ki de da
		ben biz sen siz onlar var yok gibi kadar sonra önce ama fakat`)
	m := make(map[string]bool, len(list))
	for _, w := range list {
		m[w] = true
	}
	return m
}()
