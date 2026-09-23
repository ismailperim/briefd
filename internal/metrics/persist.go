package metrics

import (
	"encoding/json"
	"fmt"
	"time"
)

// persistVersion guards the saved format; a mismatch starts from zero.
const persistVersion = 1

// persisted is the part of the registry that is worth keeping across
// restarts: usage counters, latency histograms and samples, the recent
// request log, cache counters and the served-documents set. Gauges (index
// size, vectors, sync state) are recomputed by the process itself.
type persisted struct {
	Version     int       `json:"version"`
	Since       time.Time `json:"since"`
	Series      []pSeries `json:"series"`
	Recent      []Request `json:"recent"`
	CacheHits   int64     `json:"cache_hits"`
	CacheMisses int64     `json:"cache_misses"`
	Served      []Served  `json:"served"`
}

type pSeries struct {
	Surface Surface   `json:"surface"`
	Name    string    `json:"name"`
	Count   int64     `json:"count"`
	Errors  int64     `json:"errors"`
	Tokens  int64     `json:"tokens"`
	Chunks  int64     `json:"chunks"`
	Omitted int64     `json:"omitted"`
	SumSec  float64   `json:"sum_sec"`
	Buckets []int64   `json:"buckets"`
	Samples []float64 `json:"samples"`
}

// Since reports when the counters started counting: process start, the
// start of a restored history, or the last reset.
func (r *Registry) Since() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.since
}

// Export serializes the counters for storage.
func (r *Registry) Export() ([]byte, error) {
	r.mu.Lock()
	p := persisted{Version: persistVersion, Since: r.since, CacheHits: r.cacheHits, CacheMisses: r.cacheMisses}
	for k, s := range r.byName {
		p.Series = append(p.Series, pSeries{
			Surface: k.surface, Name: k.name, Count: s.count, Errors: s.errors, Tokens: s.tokens,
			Chunks: s.chunks, Omitted: s.omitted, SumSec: s.sumSec,
			Buckets: append([]int64(nil), s.buckets...), Samples: append([]float64(nil), s.samples...),
		})
	}
	for i := r.nRecent - 1; i >= 0; i-- { // oldest first, so Import can replay in order
		p.Recent = append(p.Recent, r.recent[(r.recentAt-1-i+recentSize)%recentSize])
	}
	r.mu.Unlock()
	p.Served = r.RecentlyServed()
	return json.Marshal(p)
}

// Import restores counters saved by Export into a fresh registry. Data in
// an unknown format is ignored rather than half-applied.
func (r *Registry) Import(data []byte) error {
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("metrics: restoring counters: %w", err)
	}
	if p.Version != persistVersion {
		return fmt.Errorf("metrics: saved counters have version %d, want %d", p.Version, persistVersion)
	}
	r.mu.Lock()
	for _, ps := range p.Series {
		s := &series{
			count: ps.Count, errors: ps.Errors, tokens: ps.Tokens, chunks: ps.Chunks, omitted: ps.Omitted, sumSec: ps.SumSec,
			buckets: make([]int64, len(latencyBuckets)), samples: make([]float64, 0, samplesSize),
		}
		copy(s.buckets, ps.Buckets)
		if len(ps.Samples) > samplesSize {
			ps.Samples = ps.Samples[len(ps.Samples)-samplesSize:]
		}
		s.samples = append(s.samples, ps.Samples...)
		r.byName[key{ps.Surface, ps.Name}] = s
	}
	for _, req := range p.Recent {
		r.recent[r.recentAt] = req
		r.recentAt = (r.recentAt + 1) % recentSize
		if r.nRecent < recentSize {
			r.nRecent++
		}
	}
	r.cacheHits, r.cacheMisses = p.CacheHits, p.CacheMisses
	if !p.Since.IsZero() {
		r.since = p.Since
	}
	r.mu.Unlock()
	r.served.mu.Lock()
	if r.served.docs == nil {
		r.served.docs = map[string]*Served{}
	}
	for _, d := range p.Served {
		d := d
		r.served.docs[d.Path] = &d
	}
	r.served.mu.Unlock()
	return nil
}

// Reset clears usage counters, latency data, the recent log, cache
// counters and the served set. Gauges and sync state are left alone.
func (r *Registry) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.byName = map[key]*series{}
	r.recent = make([]Request, recentSize)
	r.recentAt, r.nRecent = 0, 0
	r.cacheHits, r.cacheMisses = 0, 0
	r.since = time.Now()
	r.mu.Unlock()
	r.served.mu.Lock()
	r.served.docs = map[string]*Served{}
	r.served.mu.Unlock()
}
