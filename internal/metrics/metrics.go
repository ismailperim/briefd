// Package metrics is a small, dependency-free instrumentation layer:
// counters, gauges and latency histograms with labels, rendered in the
// Prometheus text exposition format and summarized as JSON for the
// dashboard. Everything is in-process (locked decision #7).
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Surface identifies which API a request came through.
type Surface string

// Surfaces.
const (
	SurfaceMCP  Surface = "mcp"
	SurfaceREST Surface = "rest"
)

// Request is one served request, recorded after it completes.
type Request struct {
	At       time.Time     `json:"at"`
	Surface  Surface       `json:"surface"`
	Name     string        `json:"name"` // tool name or REST route
	Query    string        `json:"query,omitempty"`
	Scopes   []string      `json:"scopes,omitempty"`
	Duration time.Duration `json:"-"`
	Millis   float64       `json:"ms"`
	Tokens   int           `json:"tokens"`
	Chunks   int           `json:"chunks"`
	Omitted  int           `json:"omitted"`
	Error    bool          `json:"error"`
}

// latencyBuckets are the histogram upper bounds in seconds.
var latencyBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

const (
	recentSize  = 100 // ring buffer of recent requests for the dashboard
	samplesSize = 512 // per-name latency samples for percentiles
)

// Registry holds every metric. The zero value is not usable; use New.
type Registry struct {
	startedAt time.Time
	version   string

	mu       sync.Mutex
	byName   map[key]*series
	recent   []Request
	recentAt int
	nRecent  int

	indexDocs   map[string]int
	indexChunks map[string]int
	indexTokens map[string]int

	syncRuns    map[string]int64 // status -> count
	syncLastOK  time.Time
	syncLastErr string

	vectors        int
	vectorsPending int
	embeddingsOn   bool
}

type key struct {
	surface Surface
	name    string
}

type series struct {
	count   int64
	errors  int64
	tokens  int64
	chunks  int64
	omitted int64
	sumSec  float64
	buckets []int64 // cumulative counts per latencyBuckets entry
	samples []float64
	sampleI int
}

// New returns an empty registry.
func New(version string) *Registry {
	return &Registry{
		startedAt:   time.Now(),
		version:     version,
		byName:      map[key]*series{},
		recent:      make([]Request, recentSize),
		indexDocs:   map[string]int{},
		indexChunks: map[string]int{},
		indexTokens: map[string]int{},
		syncRuns:    map[string]int64{},
	}
}

// Record stores a completed request.
func (r *Registry) Record(req Request) {
	if req.At.IsZero() {
		req.At = time.Now()
	}
	req.Millis = float64(req.Duration.Microseconds()) / 1000
	if len(req.Query) > 120 {
		req.Query = req.Query[:117] + "..."
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{req.Surface, req.Name}
	s := r.byName[k]
	if s == nil {
		s = &series{buckets: make([]int64, len(latencyBuckets)), samples: make([]float64, 0, samplesSize)}
		r.byName[k] = s
	}
	s.count++
	if req.Error {
		s.errors++
	}
	s.tokens += int64(req.Tokens)
	s.chunks += int64(req.Chunks)
	s.omitted += int64(req.Omitted)
	sec := req.Duration.Seconds()
	s.sumSec += sec
	for i, ub := range latencyBuckets {
		if sec <= ub {
			s.buckets[i]++
		}
	}
	if len(s.samples) < samplesSize {
		s.samples = append(s.samples, sec)
	} else {
		s.samples[s.sampleI] = sec
		s.sampleI = (s.sampleI + 1) % samplesSize
	}

	r.recent[r.recentAt] = req
	r.recentAt = (r.recentAt + 1) % recentSize
	if r.nRecent < recentSize {
		r.nRecent++
	}
}

// SetIndex replaces the per-scope index gauges.
func (r *Registry) SetIndex(scopes []ScopeCount) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.indexDocs)
	clear(r.indexChunks)
	clear(r.indexTokens)
	for _, s := range scopes {
		r.indexDocs[s.Scope] = s.Documents
		r.indexChunks[s.Scope] = s.Chunks
		r.indexTokens[s.Scope] = s.Tokens
	}
}

// ScopeCount is the per-scope size of the index.
type ScopeCount struct {
	Scope     string `json:"scope"`
	Documents int    `json:"documents"`
	Chunks    int    `json:"chunks"`
	Tokens    int    `json:"tokens"`
}

// SetVectors records the size of the vector index and how many chunks
// still await embedding. Calling it marks embeddings as enabled.
func (r *Registry) SetVectors(indexed, pending int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vectors, r.vectorsPending, r.embeddingsOn = indexed, pending, true
}

// RecordSync records the outcome of one sync/index run.
func (r *Registry) RecordSync(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.syncRuns["error"]++
		r.syncLastErr = err.Error()
		return
	}
	r.syncRuns["ok"]++
	r.syncLastOK = time.Now()
	r.syncLastErr = ""
}

// WritePrometheus renders the registry in the text exposition format.
func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprintf(w, "# HELP briefd_build_info Build information.\n# TYPE briefd_build_info gauge\nbriefd_build_info{version=%q} 1\n", r.version)
	fmt.Fprintf(w, "# HELP briefd_process_start_time_seconds Unix time the process started.\n# TYPE briefd_process_start_time_seconds gauge\nbriefd_process_start_time_seconds %d\n", r.startedAt.Unix())

	keys := make([]key, 0, len(r.byName))
	for k := range r.byName {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].surface != keys[j].surface {
			return keys[i].surface < keys[j].surface
		}
		return keys[i].name < keys[j].name
	})

	fmt.Fprint(w, "# HELP briefd_requests_total Requests served, by surface, name and outcome.\n# TYPE briefd_requests_total counter\n")
	for _, k := range keys {
		s := r.byName[k]
		fmt.Fprintf(w, "briefd_requests_total{surface=%q,name=%q,status=\"ok\"} %d\n", k.surface, k.name, s.count-s.errors)
		fmt.Fprintf(w, "briefd_requests_total{surface=%q,name=%q,status=\"error\"} %d\n", k.surface, k.name, s.errors)
	}
	fmt.Fprint(w, "# HELP briefd_tokens_served_total Estimated tokens returned to clients.\n# TYPE briefd_tokens_served_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(w, "briefd_tokens_served_total{surface=%q,name=%q} %d\n", k.surface, k.name, r.byName[k].tokens)
	}
	fmt.Fprint(w, "# HELP briefd_chunks_served_total Chunks returned to clients.\n# TYPE briefd_chunks_served_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(w, "briefd_chunks_served_total{surface=%q,name=%q} %d\n", k.surface, k.name, r.byName[k].chunks)
	}
	fmt.Fprint(w, "# HELP briefd_chunks_omitted_total Ranked chunks dropped because they did not fit the token budget.\n# TYPE briefd_chunks_omitted_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(w, "briefd_chunks_omitted_total{surface=%q,name=%q} %d\n", k.surface, k.name, r.byName[k].omitted)
	}
	fmt.Fprint(w, "# HELP briefd_request_duration_seconds Request latency.\n# TYPE briefd_request_duration_seconds histogram\n")
	for _, k := range keys {
		s := r.byName[k]
		for i, ub := range latencyBuckets {
			fmt.Fprintf(w, "briefd_request_duration_seconds_bucket{surface=%q,name=%q,le=\"%g\"} %d\n", k.surface, k.name, ub, s.buckets[i])
		}
		fmt.Fprintf(w, "briefd_request_duration_seconds_bucket{surface=%q,name=%q,le=\"+Inf\"} %d\n", k.surface, k.name, s.count)
		fmt.Fprintf(w, "briefd_request_duration_seconds_sum{surface=%q,name=%q} %g\n", k.surface, k.name, s.sumSec)
		fmt.Fprintf(w, "briefd_request_duration_seconds_count{surface=%q,name=%q} %d\n", k.surface, k.name, s.count)
	}

	scopes := make([]string, 0, len(r.indexDocs))
	for sc := range r.indexDocs {
		scopes = append(scopes, sc)
	}
	sort.Strings(scopes)
	fmt.Fprint(w, "# HELP briefd_index_documents Indexed documents per scope.\n# TYPE briefd_index_documents gauge\n")
	for _, sc := range scopes {
		fmt.Fprintf(w, "briefd_index_documents{scope=%q} %d\n", sc, r.indexDocs[sc])
	}
	fmt.Fprint(w, "# HELP briefd_index_chunks Indexed chunks per scope.\n# TYPE briefd_index_chunks gauge\n")
	for _, sc := range scopes {
		fmt.Fprintf(w, "briefd_index_chunks{scope=%q} %d\n", sc, r.indexChunks[sc])
	}
	fmt.Fprint(w, "# HELP briefd_index_tokens Estimated tokens in the index per scope.\n# TYPE briefd_index_tokens gauge\n")
	for _, sc := range scopes {
		fmt.Fprintf(w, "briefd_index_tokens{scope=%q} %d\n", sc, r.indexTokens[sc])
	}

	if r.embeddingsOn {
		fmt.Fprintf(w, "# HELP briefd_vectors Chunks with an up-to-date embedding.\n# TYPE briefd_vectors gauge\nbriefd_vectors %d\n", r.vectors)
		fmt.Fprintf(w, "# HELP briefd_vectors_pending Chunks still waiting to be embedded.\n# TYPE briefd_vectors_pending gauge\nbriefd_vectors_pending %d\n", r.vectorsPending)
	}
	fmt.Fprint(w, "# HELP briefd_sync_runs_total Source sync runs by outcome.\n# TYPE briefd_sync_runs_total counter\n")
	for _, st := range []string{"ok", "error"} {
		fmt.Fprintf(w, "briefd_sync_runs_total{status=%q} %d\n", st, r.syncRuns[st])
	}
	if !r.syncLastOK.IsZero() {
		fmt.Fprintf(w, "# HELP briefd_sync_last_success_timestamp_seconds Unix time of the last successful sync.\n# TYPE briefd_sync_last_success_timestamp_seconds gauge\nbriefd_sync_last_success_timestamp_seconds %d\n", r.syncLastOK.Unix())
	}
}

// Snapshot is the JSON summary served at /api/stats.
type Snapshot struct {
	Version       string         `json:"version"`
	StartedAt     time.Time      `json:"started_at"`
	UptimeSeconds float64        `json:"uptime_seconds"`
	Totals        Totals         `json:"totals"`
	ByName        []NameStats    `json:"by_name"`
	Index         []ScopeCount   `json:"index"`
	Sync          SyncStats      `json:"sync"`
	Recent        []Request      `json:"recent"`
	Extra         map[string]any `json:"extra,omitempty"`
}

// Totals aggregates every surface.
type Totals struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	TokensServed int64 `json:"tokens_served"`
	ChunksServed int64 `json:"chunks_served"`
	Omitted      int64 `json:"chunks_omitted"`
	Documents    int   `json:"documents"`
	Chunks       int   `json:"chunks"`
	IndexTokens  int   `json:"index_tokens"`
	// Vectors is -1 when embeddings are disabled.
	Vectors        int `json:"vectors"`
	VectorsPending int `json:"vectors_pending"`
}

// NameStats summarizes one (surface, name) series.
type NameStats struct {
	Surface  Surface `json:"surface"`
	Name     string  `json:"name"`
	Requests int64   `json:"requests"`
	Errors   int64   `json:"errors"`
	Tokens   int64   `json:"tokens"`
	Chunks   int64   `json:"chunks"`
	Omitted  int64   `json:"omitted"`
	AvgMs    float64 `json:"avg_ms"`
	P50Ms    float64 `json:"p50_ms"`
	P95Ms    float64 `json:"p95_ms"`
}

// SyncStats summarizes source synchronization.
type SyncStats struct {
	Runs      int64      `json:"runs"`
	Failures  int64      `json:"failures"`
	LastOK    *time.Time `json:"last_ok,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

// Snapshot returns a point-in-time summary. Recent requests are newest first.
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := Snapshot{
		Version:       r.version,
		StartedAt:     r.startedAt,
		UptimeSeconds: time.Since(r.startedAt).Seconds(),
		ByName:        []NameStats{},
		Index:         []ScopeCount{},
		Recent:        []Request{},
	}
	for k, s := range r.byName {
		ns := NameStats{
			Surface: k.surface, Name: k.name, Requests: s.count, Errors: s.errors,
			Tokens: s.tokens, Chunks: s.chunks, Omitted: s.omitted,
		}
		if s.count > 0 {
			ns.AvgMs = s.sumSec / float64(s.count) * 1000
		}
		ns.P50Ms, ns.P95Ms = percentiles(s.samples)
		snap.ByName = append(snap.ByName, ns)
		snap.Totals.Requests += s.count
		snap.Totals.Errors += s.errors
		snap.Totals.TokensServed += s.tokens
		snap.Totals.ChunksServed += s.chunks
		snap.Totals.Omitted += s.omitted
	}
	sort.Slice(snap.ByName, func(i, j int) bool {
		if snap.ByName[i].Surface != snap.ByName[j].Surface {
			return snap.ByName[i].Surface < snap.ByName[j].Surface
		}
		return snap.ByName[i].Name < snap.ByName[j].Name
	})

	for sc, docs := range r.indexDocs {
		snap.Index = append(snap.Index, ScopeCount{Scope: sc, Documents: docs, Chunks: r.indexChunks[sc], Tokens: r.indexTokens[sc]})
		snap.Totals.Documents += docs
		snap.Totals.Chunks += r.indexChunks[sc]
		snap.Totals.IndexTokens += r.indexTokens[sc]
	}
	sort.Slice(snap.Index, func(i, j int) bool { return snap.Index[i].Scope < snap.Index[j].Scope })

	snap.Totals.Vectors, snap.Totals.VectorsPending = -1, 0
	if r.embeddingsOn {
		snap.Totals.Vectors, snap.Totals.VectorsPending = r.vectors, r.vectorsPending
	}
	snap.Sync = SyncStats{Runs: r.syncRuns["ok"] + r.syncRuns["error"], Failures: r.syncRuns["error"], LastError: r.syncLastErr}
	if !r.syncLastOK.IsZero() {
		t := r.syncLastOK
		snap.Sync.LastOK = &t
	}

	for i := range r.nRecent {
		idx := (r.recentAt - 1 - i + recentSize) % recentSize
		snap.Recent = append(snap.Recent, r.recent[idx])
	}
	return snap
}

func percentiles(samples []float64) (p50, p95 float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)
	at := func(q float64) float64 {
		i := int(q*float64(len(sorted)-1) + 0.5)
		return sorted[i] * 1000
	}
	return at(0.50), at(0.95)
}

// RouteName normalizes an HTTP route pattern ("GET /api/search") into a
// metric name ("api_search").
func RouteName(pattern string) string {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = pattern
	}
	path = strings.Trim(path, "/")
	path = strings.ReplaceAll(path, "/", "_")
	path = strings.ReplaceAll(path, "{path...}", "path")
	if path == "" {
		return "root"
	}
	return path
}
