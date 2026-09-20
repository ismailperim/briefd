// Package eval measures retrieval quality against a golden query set
// (SPEC §8): Recall@5, Recall@10 and MRR per retrieval mode and query type.
package eval

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

// Expected identifies a relevant chunk by document path and the tail of
// its heading breadcrumb.
type Expected struct {
	Path    string `yaml:"path"`
	Heading string `yaml:"heading"`
}

// Query is one golden entry.
type Query struct {
	ID       string     `yaml:"id"`
	Type     string     `yaml:"type"`
	Query    string     `yaml:"query"`
	Scopes   []string   `yaml:"scopes"`
	Expected []Expected `yaml:"expected"`
}

// Golden is the parsed queries file.
type Golden struct {
	Queries []Query `yaml:"queries"`
}

// LoadGolden reads and validates a queries.yaml.
func LoadGolden(path string) (*Golden, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading golden set: %w", err)
	}
	var g Golden
	if err := yaml.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("parsing golden set %s: %w", path, err)
	}
	seen := map[string]bool{}
	for i, q := range g.Queries {
		switch {
		case q.ID == "":
			return nil, fmt.Errorf("golden query %d: missing id", i)
		case seen[q.ID]:
			return nil, fmt.Errorf("golden query %s: duplicate id", q.ID)
		case q.Query == "":
			return nil, fmt.Errorf("golden query %s: missing query", q.ID)
		case len(q.Expected) == 0:
			return nil, fmt.Errorf("golden query %s: no expected chunks", q.ID)
		}
		seen[q.ID] = true
		if q.Type == "" {
			g.Queries[i].Type = "keyword"
		}
	}
	return &g, nil
}

// Thresholds maps mode -> metric -> minimum value.
type Thresholds map[string]map[string]float64

// LoadThresholds reads thresholds.yaml.
func LoadThresholds(path string) (Thresholds, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading thresholds: %w", err)
	}
	var t Thresholds
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("parsing thresholds %s: %w", path, err)
	}
	return t, nil
}

// Metrics aggregates results over a set of queries.
type Metrics struct {
	Queries  int     `json:"queries"`
	Recall5  float64 `json:"recall_at_5"`
	Recall10 float64 `json:"recall_at_10"`
	MRR      float64 `json:"mrr"`
}

// QueryResult is the outcome for one query in one mode.
type QueryResult struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Mode      string   `json:"mode"`
	FirstRank int      `json:"first_rank"` // 0 when no expected chunk was retrieved
	Recall5   float64  `json:"recall_at_5"`
	Recall10  float64  `json:"recall_at_10"`
	Top       []string `json:"top"` // "path — heading" of the top results
	Missed    []string `json:"missed,omitempty"`
}

// ModeReport is the full report for one retrieval mode.
type ModeReport struct {
	Mode    string             `json:"mode"`
	Overall Metrics            `json:"overall"`
	ByType  map[string]Metrics `json:"by_type"`
	Queries []QueryResult      `json:"queries"`
}

// Report is the output of Run.
type Report struct {
	Modes []ModeReport `json:"modes"`
}

// Options configures a run.
type Options struct {
	Modes  []string // retrieval modes to evaluate
	TopK   int      // results fetched per query (>= 10)
	Scopes []string // default scopes when a query specifies none (default: all in store)
}

// Run evaluates every query in g in each mode.
func Run(ctx context.Context, st *store.Store, s *search.Searcher, g *Golden, opts Options) (*Report, error) {
	topK := opts.TopK
	if topK < 10 {
		topK = 10
	}
	allScopes := opts.Scopes
	if len(allScopes) == 0 {
		infos, err := st.ListScopes(ctx)
		if err != nil {
			return nil, err
		}
		for _, si := range infos {
			allScopes = append(allScopes, si.Scope)
		}
	}
	rep := &Report{}
	for _, mode := range opts.Modes {
		mr := ModeReport{Mode: mode, ByType: map[string]Metrics{}}
		for _, q := range g.Queries {
			scopes := q.Scopes
			if len(scopes) == 0 {
				scopes = allScopes
			}
			res, err := s.Search(ctx, search.Query{Text: q.Query, Scopes: scopes, TopK: topK, MaxTokens: 1 << 30, Mode: mode})
			if err != nil {
				return nil, fmt.Errorf("query %s (%s): %w", q.ID, mode, err)
			}
			mr.Queries = append(mr.Queries, score(q, mode, res.Chunks))
		}
		mr.Overall = aggregate(mr.Queries, "")
		for _, t := range types(g) {
			mr.ByType[t] = aggregate(mr.Queries, t)
		}
		rep.Modes = append(rep.Modes, mr)
	}
	return rep, nil
}

// Matches reports whether hit satisfies exp.
func Matches(hit store.ChunkHit, exp Expected) bool {
	if hit.DocPath != exp.Path {
		return false
	}
	h := strings.ToLower(hit.HeadingPath)
	e := strings.ToLower(strings.TrimSpace(exp.Heading))
	return h == e || strings.HasSuffix(h, " > "+e) || strings.HasSuffix(h, e)
}

func score(q Query, mode string, hits []store.ChunkHit) QueryResult {
	qr := QueryResult{ID: q.ID, Type: q.Type, Mode: mode}
	found := make([]bool, len(q.Expected))
	for rank, h := range hits {
		for i, exp := range q.Expected {
			if !found[i] && Matches(h, exp) {
				found[i] = true
				if qr.FirstRank == 0 {
					qr.FirstRank = rank + 1
				}
			}
		}
		if rank < 5 {
			qr.Recall5 = fraction(found)
		}
		if rank < 10 {
			qr.Recall10 = fraction(found)
		}
		if rank < 5 {
			qr.Top = append(qr.Top, h.DocPath+" — "+h.HeadingPath)
		}
	}
	for i, exp := range q.Expected {
		if !found[i] {
			qr.Missed = append(qr.Missed, exp.Path+" — "+exp.Heading)
		}
	}
	return qr
}

func fraction(found []bool) float64 {
	n := 0
	for _, f := range found {
		if f {
			n++
		}
	}
	return float64(n) / float64(len(found))
}

func aggregate(results []QueryResult, typ string) Metrics {
	var m Metrics
	for _, r := range results {
		if typ != "" && r.Type != typ {
			continue
		}
		m.Queries++
		m.Recall5 += r.Recall5
		m.Recall10 += r.Recall10
		if r.FirstRank > 0 {
			m.MRR += 1 / float64(r.FirstRank)
		}
	}
	if m.Queries > 0 {
		m.Recall5 /= float64(m.Queries)
		m.Recall10 /= float64(m.Queries)
		m.MRR /= float64(m.Queries)
	}
	return m
}

func types(g *Golden) []string {
	set := map[string]bool{}
	for _, q := range g.Queries {
		set[q.Type] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Check compares a report against thresholds and returns one message per
// violated threshold.
func Check(rep *Report, th Thresholds) []string {
	var failures []string
	for mode, metrics := range th {
		var mr *ModeReport
		for i := range rep.Modes {
			if rep.Modes[i].Mode == mode {
				mr = &rep.Modes[i]
			}
		}
		if mr == nil {
			failures = append(failures, fmt.Sprintf("%s: mode was not evaluated", mode))
			continue
		}
		for metric, min := range metrics {
			var got float64
			switch metric {
			case "recall_at_5":
				got = mr.Overall.Recall5
			case "recall_at_10":
				got = mr.Overall.Recall10
			case "mrr":
				got = mr.Overall.MRR
			default:
				failures = append(failures, fmt.Sprintf("%s: unknown metric %q", mode, metric))
				continue
			}
			if got < min {
				failures = append(failures, fmt.Sprintf("%s: %s = %.3f is below threshold %.3f", mode, metric, got, min))
			}
		}
	}
	sort.Strings(failures)
	return failures
}
