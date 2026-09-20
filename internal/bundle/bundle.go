// Package bundle compiles a task description into one token-budgeted context
// block (SPEC §3.1.2, §5): retrieve candidates, drop near-duplicates, pack
// greedily in rank order within the budget, order by scope priority, render
// with source attribution, and cache the result keyed on everything that can
// change it.
package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
	"github.com/ismailperim/briefd/internal/tokenizer"
)

const (
	// candidates is how many ranked chunks the packer considers.
	candidates = 40
	// dedupeJaccard is the term-set similarity above which two chunks are
	// considered near-identical.
	dedupeJaccard = 0.85
	// truncationMarker closes a chunk that was cut to fit the budget.
	truncationMarker = "\n[… truncated to fit the token budget]"
	// minTruncatedTokens is the smallest useful head of a chunk.
	minTruncatedTokens = 40
	// lruSize is the number of bundles kept in process memory.
	lruSize = 256
)

// Request describes what to compile.
type Request struct {
	Task      string
	Scopes    []string
	MaxTokens int
}

// Result is a compiled bundle plus cache provenance.
type Result struct {
	*store.Bundle
	Budget int  `json:"budget"`
	Cached bool `json:"cached"`
}

// Compiler builds and caches bundles.
type Compiler struct {
	store    *store.Store
	searcher *search.Searcher
	opts     Options

	mu  sync.Mutex
	lru map[string]*lruEntry
	// order tracks recency; the front is the least recently used key.
	order []string
}

type lruEntry struct {
	bundle *store.Bundle
}

// Options configures a Compiler.
type Options struct {
	DefaultScopes    []string
	DefaultMaxTokens int
	// OnCache is called with true on a cache hit and false on a miss.
	OnCache func(hit bool)
}

// New returns a Compiler.
func New(st *store.Store, s *search.Searcher, opts Options) *Compiler {
	if len(opts.DefaultScopes) == 0 {
		opts.DefaultScopes = []string{"domain", "conventions"}
	}
	if opts.DefaultMaxTokens <= 0 {
		opts.DefaultMaxTokens = search.DefaultMaxTokens
	}
	return &Compiler{store: st, searcher: s, opts: opts, lru: map[string]*lruEntry{}}
}

// Compile returns the bundle for req, from cache when the same task was
// compiled against the same knowledge state, budget and scopes.
func (c *Compiler) Compile(ctx context.Context, req Request) (*Result, error) {
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return nil, errors.New("task_description must not be empty")
	}
	scopes := normalizeScopes(req.Scopes, c.opts.DefaultScopes)
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.opts.DefaultMaxTokens
	}
	fingerprint, err := c.store.GetIndexFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	model := ""
	if v := c.searcher.Vectors(); v != nil {
		model = v.Model()
	}
	key := cacheKey(task, scopes, maxTokens, fingerprint, model)
	budget := search.Budget(maxTokens)

	if b := c.lruGet(key); b != nil {
		c.report(true)
		return &Result{Bundle: b, Budget: budget, Cached: true}, nil
	}
	b, err := c.store.GetBundleByKey(ctx, key)
	switch {
	case err == nil:
		c.lruPut(key, b)
		c.report(true)
		return &Result{Bundle: b, Budget: budget, Cached: true}, nil
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	c.report(false)

	b, err = c.compile(ctx, task, scopes, maxTokens, fingerprint, model, key)
	if err != nil {
		return nil, err
	}
	if err := c.store.PutBundle(ctx, b); err != nil {
		return nil, err
	}
	c.lruPut(key, b)
	return &Result{Bundle: b, Budget: budget, Cached: false}, nil
}

func (c *Compiler) compile(ctx context.Context, task string, scopes []string, maxTokens int, fingerprint, model, key string) (*store.Bundle, error) {
	res, err := c.searcher.Search(ctx, search.Query{Text: task, Scopes: scopes, TopK: candidates, MaxTokens: 1 << 30})
	if err != nil {
		return nil, err
	}
	id := key[:16]
	budget := search.Budget(maxTokens)
	headerCost := tokenizer.Count(Render(id, task, scopes, fingerprint, nil)) + 4
	selected, truncated := Pack(res.Chunks, budget, headerCost)
	// The packer works on per-chunk estimates; the rendered whole is what
	// must fit. Drop the lowest-ranked sections until it does.
	content := Render(id, task, scopes, fingerprint, Order(selected))
	for tokenizer.Count(content) > budget && len(selected) > 0 {
		selected = selected[:len(selected)-1]
		content = Render(id, task, scopes, fingerprint, Order(selected))
	}
	sections := make([]store.BundleSection, 0, len(selected))
	for _, ch := range Order(selected) {
		sections = append(sections, store.BundleSection{
			ChunkID: ch.ChunkID, DocPath: ch.DocPath, Scope: ch.Scope, Heading: ch.HeadingPath, Tokens: ch.Tokens,
		})
	}
	return &store.Bundle{
		ID:               id,
		CacheKey:         key,
		Task:             task,
		Scopes:           scopes,
		MaxTokens:        maxTokens,
		IndexFingerprint: fingerprint,
		Model:            model,
		Content:          content,
		Tokens:           tokenizer.Count(content),
		Sections:         sections,
		Truncated:        truncated,
		CreatedAt:        time.Now(),
	}, nil
}

// Pack selects chunks for a budget: near-duplicates are dropped, chunks are
// taken greedily in rank order while the estimated bundle size (chunk
// tokens plus per-section and header overhead) stays within budget, and if
// nothing fits the top chunk is truncated. The result keeps rank order;
// use Order before rendering.
func Pack(ranked []store.ChunkHit, budget, headerCost int) (selected []store.ChunkHit, truncated bool) {
	var terms [][]string
	used := headerCost
	for _, ch := range ranked {
		t := termSet(ch.Content)
		if isDuplicate(ch, t, selected, terms) {
			continue
		}
		cost := ch.Tokens + sectionOverhead(ch)
		if used+cost > budget {
			continue
		}
		selected = append(selected, ch)
		terms = append(terms, t)
		used += cost
	}
	if len(selected) == 0 && len(ranked) > 0 {
		head := ranked[0]
		room := budget - headerCost - sectionOverhead(head) - tokenizer.Count(truncationMarker)
		if room >= minTruncatedTokens {
			head.Content = truncate(head.Content, room) + truncationMarker
			head.Tokens = tokenizer.Count(head.Content)
			selected = append(selected, head)
			truncated = true
		}
	}
	return selected, truncated
}

// Order returns chunks sorted by scope priority (domain, conventions, then
// projects) keeping rank order within a scope. It does not modify its input.
func Order(chunks []store.ChunkHit) []store.ChunkHit {
	out := make([]store.ChunkHit, len(chunks))
	copy(out, chunks)
	sort.SliceStable(out, func(i, j int) bool {
		return scopeRank(out[i].Scope) < scopeRank(out[j].Scope)
	})
	return out
}

func sectionOverhead(ch store.ChunkHit) int {
	return tokenizer.Count(sectionHeader(ch)) + 2
}

func sectionHeader(ch store.ChunkHit) string {
	return fmt.Sprintf("## %s — %s", ch.DocPath, ch.HeadingPath)
}

// Render produces the bundle text. It is the only place that decides the
// format, so byte-identical output for identical inputs is guaranteed.
func Render(id, task string, scopes []string, fingerprint string, chunks []store.ChunkHit) string {
	var sb strings.Builder
	total := 0
	for _, ch := range chunks {
		total += ch.Tokens
	}
	fmt.Fprintf(&sb, "<!-- briefd bundle %s · %d section(s), ~%d tokens · scopes: %s · index %s · task: %s -->\n",
		id, len(chunks), total, strings.Join(scopes, ", "), fingerprint, oneLine(task))
	for _, ch := range chunks {
		sb.WriteString("\n")
		sb.WriteString(sectionHeader(ch))
		sb.WriteString("\n\n")
		sb.WriteString(Body(ch.Content))
		sb.WriteString("\n")
	}
	if len(chunks) == 0 {
		sb.WriteString("\n(no matching knowledge)\n")
	}
	return sb.String()
}

// Body returns a chunk's content without its leading Markdown heading line,
// which the attribution line already carries. Tokens are precious.
func Body(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "#") {
		if i := strings.IndexByte(content, '\n'); i >= 0 {
			return strings.TrimSpace(content[i+1:])
		}
		return ""
	}
	return content
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "--", "—")
	if r := []rune(s); len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}

func scopeRank(scope string) int {
	switch scope {
	case "domain":
		return 0
	case "conventions":
		return 1
	default:
		return 2
	}
}

func normalizeScopes(scopes, def []string) []string {
	if len(scopes) == 0 {
		scopes = def
	}
	out := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func cacheKey(task string, scopes []string, maxTokens int, fingerprint, model string) string {
	h := sha256.New()
	fmt.Fprintf(h, "v1\x00%s\x00%s\x00%d\x00%s\x00%s", task, strings.Join(scopes, ","), maxTokens, fingerprint, model)
	return hex.EncodeToString(h.Sum(nil))
}

// termSet returns the sorted unique search terms of content.
func termSet(content string) []string {
	set := map[string]bool{}
	for _, t := range search.Terms(content) {
		set[t] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func isDuplicate(ch store.ChunkHit, terms []string, selected []store.ChunkHit, selectedTerms [][]string) bool {
	for i, s := range selected {
		if s.ChunkID == ch.ChunkID || strings.TrimSpace(s.Content) == strings.TrimSpace(ch.Content) {
			return true
		}
		if jaccard(terms, selectedTerms[i]) >= dedupeJaccard {
			return true
		}
	}
	return false
}

// jaccard computes |a∩b| / |a∪b| over two sorted string slices.
func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	i, j, inter := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			inter++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// truncate returns the longest prefix of s, cut at a line boundary where
// possible, that fits within tokens.
func truncate(s string, tokens int) string {
	if tokenizer.Count(s) <= tokens {
		return s
	}
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		candidate := strings.Join(append(out, l), "\n")
		if tokenizer.Count(candidate) > tokens {
			break
		}
		out = append(out, l)
	}
	if len(out) > 0 {
		return strings.TrimRight(strings.Join(out, "\n"), "\n")
	}
	// A single line larger than the budget: cut by runes.
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if tokenizer.Count(string(r[:mid])) <= tokens {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return strings.TrimSpace(string(r[:lo]))
}

func (c *Compiler) report(hit bool) {
	if c.opts.OnCache != nil {
		c.opts.OnCache(hit)
	}
}

func (c *Compiler) lruGet(key string) *store.Bundle {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.lru[key]
	if !ok {
		return nil
	}
	c.touch(key)
	return e.bundle
}

func (c *Compiler) lruPut(key string, b *store.Bundle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.lru[key]; ok {
		c.lru[key].bundle = b
		c.touch(key)
		return
	}
	c.lru[key] = &lruEntry{bundle: b}
	c.order = append(c.order, key)
	for len(c.order) > lruSize {
		delete(c.lru, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *Compiler) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(append(c.order[:i:i], c.order[i+1:]...), key)
			return
		}
	}
}

// Size returns the number of bundles held in memory.
func (c *Compiler) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lru)
}
