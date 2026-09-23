// Package httpapi wires the HTTP surface: the MCP endpoint, the REST API,
// health checks and (M2b) metrics and the dashboard, all behind one bearer
// token.
package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/metrics"
	"github.com/ismailperim/briefd/internal/proposal"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/staleness"
	"github.com/ismailperim/briefd/internal/store"
)

//go:embed dashboard/index.html
var dashboardFS embed.FS

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Store    *store.Store
	Searcher *search.Searcher
	Compiler *bundle.Compiler
	// Proposals backs POST /api/proposals (may be nil / unavailable).
	Proposals *proposal.Service
	// WebhookSecret enables POST /webhook/git; OnWebhook is called after a
	// verified delivery to trigger a sync.
	WebhookSecret string
	OnWebhook     func()
	// MCP is the streamable-HTTP MCP handler, mounted at /mcp.
	MCP http.Handler
	// APIToken protects /mcp and /api/*; empty disables authentication.
	APIToken string
	// Metrics backs /metrics and /api/stats; nil disables both.
	Metrics *metrics.Registry
	// MetricsRequireAuth puts /metrics behind the bearer token too.
	MetricsRequireAuth bool
	// QueryLog records /api/search and /api/bundle calls for GET /api/gaps.
	QueryLog bool
	// Coverage reports, per followed code repository, which directories no
	// document's refs cover; nil when no code repositories are configured.
	Coverage func(ctx context.Context) ([]staleness.RepoCoverage, error)
	// Instance describes the configuration for GET /api/instance. It must
	// never include tokens, passwords or key material.
	Instance func() map[string]any
	Version  string
	Logger   *slog.Logger
}

// logQuery appends to the query log; failures are logged, never returned.
func (a *api) logQuery(ctx context.Context, r store.QueryRecord) {
	if !a.deps.QueryLog {
		return
	}
	r.Surface = "http"
	if err := a.deps.Store.LogQuery(context.WithoutCancel(ctx), r); err != nil {
		a.deps.Logger.Warn("query log write failed", "err", err)
	}
}

// New returns the root handler.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	a := &api{deps: d}
	mux := http.NewServeMux()

	// Unauthenticated: liveness, the static dashboard shell (it fetches its
	// data with the token), and by default the Prometheus endpoint.
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /healthz", a.health) // conventional alias for platform health checks
	mux.HandleFunc("GET /{$}", a.dashboard)
	auth := bearer(d.APIToken)
	if d.Metrics != nil {
		metricsHandler := http.HandlerFunc(a.prometheus)
		if d.MetricsRequireAuth {
			mux.Handle("GET /metrics", auth(metricsHandler))
		} else {
			mux.Handle("GET /metrics", metricsHandler)
		}
		mux.Handle("GET /api/stats", auth(http.HandlerFunc(a.stats)))
	}

	// Authenticated surface.
	mux.Handle("/mcp", auth(d.MCP))
	mux.Handle("/mcp/", auth(d.MCP))
	mux.Handle("GET /api/search", auth(http.HandlerFunc(a.search)))
	mux.Handle("POST /api/bundle", auth(http.HandlerFunc(a.bundle)))
	mux.Handle("POST /api/usage", auth(http.HandlerFunc(a.usage)))
	mux.Handle("POST /api/proposals", auth(http.HandlerFunc(a.proposals)))
	mux.Handle("GET /api/proposals", auth(http.HandlerFunc(a.listProposals)))
	mux.Handle("GET /api/gaps", auth(http.HandlerFunc(a.gaps)))
	mux.Handle("GET /api/coverage", auth(http.HandlerFunc(a.coverage)))
	mux.Handle("GET /api/graph", auth(http.HandlerFunc(a.graph)))
	mux.Handle("GET /api/instance", auth(http.HandlerFunc(a.instance)))
	mux.Handle("GET /api/links/suggestions", auth(http.HandlerFunc(a.linkSuggestions)))
	if d.WebhookSecret != "" {
		mux.HandleFunc("POST /webhook/git", a.webhook)
	}
	mux.Handle("GET /api/scopes", auth(http.HandlerFunc(a.scopes)))
	mux.Handle("GET /api/docs/{path...}", auth(http.HandlerFunc(a.document)))

	return logRequests(d.Logger, mux)
}

// record reports a finished REST request to the metrics registry.
func (a *api) record(start time.Time, req metrics.Request, failed bool) {
	if a.deps.Metrics == nil {
		return
	}
	req.Surface = metrics.SurfaceREST
	req.Duration = time.Since(start)
	req.Error = failed
	a.deps.Metrics.Record(req)
}

func (a *api) dashboard(w http.ResponseWriter, _ *http.Request) {
	page, err := dashboardFS.ReadFile("dashboard/index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}

func (a *api) prometheus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	a.deps.Metrics.WritePrometheus(w)
}

func (a *api) stats(w http.ResponseWriter, r *http.Request) {
	snap := a.deps.Metrics.Snapshot()
	snap.Extra = map[string]any{}
	if sync, err := a.deps.Store.GetSyncState(r.Context()); err == nil {
		snap.Extra["source"] = sync.Source
		snap.Extra["last_commit"] = sync.LastCommit
		snap.Extra["last_sync_at"] = nullableTime(sync.LastSyncAt)
		snap.Extra["last_error"] = sync.LastError
	}
	if counts, err := a.deps.Store.CountProposals(r.Context()); err == nil {
		snap.Proposals = &metrics.ProposalStats{
			Open: counts.Open, Merged: counts.Merged, Closed: counts.Closed,
		}
	}
	if fp, err := a.deps.Store.GetIndexFingerprint(r.Context()); err == nil {
		snap.Extra["index_fingerprint"] = fp
	}
	if bs, err := a.deps.Store.GetBundleStats(r.Context()); err == nil {
		snap.Extra["bundles_cached"] = bs.Bundles
	}
	if n, err := a.deps.Store.CountUsage(r.Context()); err == nil {
		snap.Extra["usage_events"] = n
	}
	if a.deps.Searcher != nil {
		snap.Extra["hybrid"] = a.deps.Searcher.Hybrid()
	}
	if docs, err := a.deps.Store.StalestDocuments(r.Context(), 8); err == nil {
		if docs == nil {
			docs = []store.DocumentAge{}
		}
		snap.Extra["stalest"] = docs
	}
	if drift, err := a.deps.Store.ListDrift(r.Context(), 8); err == nil {
		if drift == nil {
			drift = []store.Drift{}
		}
		snap.Extra["code_drift"] = drift
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snap)
}

type api struct {
	deps Deps

	// Link suggestions scan every document against every other one, so
	// they are computed once per index state.
	suggestMu  sync.Mutex
	suggestFor string
	suggested  []store.LinkSuggestion
}

func (a *api) health(w http.ResponseWriter, r *http.Request) {
	sync, err := a.deps.Store.GetSyncState(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"version":      a.deps.Version,
		"last_sync_at": nullableTime(sync.LastSyncAt),
		"last_commit":  sync.LastCommit,
		"last_error":   sync.LastError,
	})
}

func (a *api) search(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	q := r.URL.Query()
	text := strings.TrimSpace(q.Get("q"))
	if text == "" {
		a.record(start, metrics.Request{Name: "api_search"}, true)
		writeError(w, http.StatusBadRequest, "missing_query", "q is required")
		return
	}
	topK, err := intParam(q.Get("top_k"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_top_k", err.Error())
		return
	}
	maxTokens, err := intParam(q.Get("max_tokens"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_max_tokens", err.Error())
		return
	}
	res, err := a.deps.Searcher.Search(r.Context(), search.Query{
		Text:      text,
		Scopes:    splitList(q.Get("scopes")),
		TopK:      topK,
		MaxTokens: maxTokens,
		Paths:     splitList(q.Get("paths")),
	})
	if err != nil {
		a.record(start, metrics.Request{Name: "api_search", Query: text}, true)
		a.deps.Logger.Error("search failed", "err", err)
		writeError(w, http.StatusInternalServerError, "search_failed", "search failed")
		return
	}
	a.record(start, metrics.Request{
		Name: "api_search", Query: text, Scopes: res.Scopes,
		Tokens: res.TotalTokens, Chunks: len(res.Chunks), Omitted: res.Omitted,
	}, false)
	paths := make([]string, len(res.Chunks))
	for i, c := range res.Chunks {
		paths[i] = c.DocPath
	}
	a.deps.Metrics.RecordServed("api_search", paths)
	a.logQuery(r.Context(), store.QueryRecord{
		Name: "api_search", Query: text, Scopes: res.Scopes, Mode: res.Mode, Results: len(res.Chunks),
		TopScore: res.TopScore, Margin: res.Margin, Tokens: res.TotalTokens, Client: q.Get("client"),
	})
	writeJSON(w, http.StatusOK, res)
}

type bundleRequest struct {
	Task      string   `json:"task"`
	MaxTokens int      `json:"max_tokens"`
	Scopes    []string `json:"scopes"`
	// Client is an optional caller name recorded in the query log.
	Client string `json:"client"`
	// Paths are code paths the task touches (see search.Query.Paths).
	Paths []string `json:"paths"`
}

func (a *api) bundle(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req bundleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		a.record(start, metrics.Request{Name: "api_bundle"}, true)
		writeError(w, http.StatusBadRequest, "invalid_json", "body must be JSON: {task, max_tokens?, scopes?}")
		return
	}
	if strings.TrimSpace(req.Task) == "" || req.MaxTokens < 0 {
		a.record(start, metrics.Request{Name: "api_bundle"}, true)
		writeError(w, http.StatusBadRequest, "invalid_request", "task is required and max_tokens must be positive")
		return
	}
	res, err := a.deps.Compiler.Compile(r.Context(), bundle.Request{Task: req.Task, Scopes: req.Scopes, MaxTokens: req.MaxTokens, Paths: req.Paths})
	if err != nil {
		a.record(start, metrics.Request{Name: "api_bundle", Query: req.Task}, true)
		a.deps.Logger.Error("bundle failed", "err", err)
		writeError(w, http.StatusInternalServerError, "bundle_failed", "compiling the bundle failed")
		return
	}
	a.record(start, metrics.Request{Name: "api_bundle", Query: req.Task, Scopes: res.Scopes, Tokens: res.Tokens, Chunks: len(res.Sections)}, false)
	bpaths := make([]string, len(res.Sections))
	for i, s := range res.Sections {
		bpaths[i] = s.DocPath
	}
	a.deps.Metrics.RecordServed("api_bundle", bpaths)
	a.logQuery(r.Context(), store.QueryRecord{
		Name: "api_bundle", Query: req.Task, Scopes: res.Scopes, Mode: a.deps.Searcher.Mode(), Results: len(res.Sections),
		TopScore: res.TopScore, Margin: res.Margin, Tokens: res.Tokens, BundleID: res.ID, Client: req.Client,
	})
	writeJSON(w, http.StatusOK, res)
}

type usageRequest struct {
	BundleID       string   `json:"bundle_id"`
	UsefulChunkIDs []string `json:"useful_chunk_ids"`
	Client         string   `json:"client"`
}

func (a *api) usage(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req usageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.BundleID == "" {
		a.record(start, metrics.Request{Name: "api_usage"}, true)
		writeError(w, http.StatusBadRequest, "invalid_request", "body must be JSON: {bundle_id, useful_chunk_ids?, client?}")
		return
	}
	b, err := a.deps.Store.GetBundle(r.Context(), req.BundleID)
	if errors.Is(err, store.ErrNotFound) {
		a.record(start, metrics.Request{Name: "api_usage", Query: req.BundleID}, true)
		writeError(w, http.StatusNotFound, "not_found", "bundle not found")
		return
	}
	if err != nil {
		a.record(start, metrics.Request{Name: "api_usage", Query: req.BundleID}, true)
		writeError(w, http.StatusInternalServerError, "usage_failed", "loading the bundle failed")
		return
	}
	useful := map[string]bool{}
	for _, id := range req.UsefulChunkIDs {
		useful[id] = true
	}
	events := make([]store.UsageEvent, 0, len(b.Sections))
	for _, s := range b.Sections {
		events = append(events, store.UsageEvent{BundleID: b.ID, ChunkID: s.ChunkID, Useful: useful[s.ChunkID], Client: req.Client})
	}
	if err := a.deps.Store.PutUsage(r.Context(), events); err != nil {
		a.record(start, metrics.Request{Name: "api_usage", Query: req.BundleID}, true)
		writeError(w, http.StatusInternalServerError, "usage_failed", "storing usage failed")
		return
	}
	a.record(start, metrics.Request{Name: "api_usage", Query: req.BundleID, Chunks: len(events)}, false)
	writeJSON(w, http.StatusOK, map[string]int{"recorded": len(events)})
}

type proposalRequest struct {
	DocPath     string `json:"doc_path"`
	Description string `json:"change_description"`
	Content     string `json:"new_content"`
	Client      string `json:"client"`
}

func (a *api) proposals(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req proposalRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		a.record(start, metrics.Request{Name: "api_proposals"}, true)
		writeError(w, http.StatusBadRequest, "invalid_json", "body must be JSON: {doc_path, change_description, new_content, client?}")
		return
	}
	if a.deps.Proposals == nil || !a.deps.Proposals.Available() {
		a.record(start, metrics.Request{Name: "api_proposals", Query: req.DocPath}, true)
		writeError(w, http.StatusConflict, "proposals_unavailable", proposal.ErrUnavailable.Error())
		return
	}
	res, err := a.deps.Proposals.Create(r.Context(), proposal.Request{
		DocPath: req.DocPath, Description: req.Description, Content: req.Content, Client: req.Client,
	})
	if err != nil {
		a.record(start, metrics.Request{Name: "api_proposals", Query: req.DocPath}, true)
		a.deps.Logger.Error("proposal failed", "err", err)
		writeError(w, http.StatusBadRequest, "proposal_failed", err.Error())
		return
	}
	a.record(start, metrics.Request{Name: "api_proposals", Query: req.DocPath}, false)
	writeJSON(w, http.StatusCreated, res)
}

// gapsResponse is the GET /api/gaps body.
type gapsResponse struct {
	Since time.Time `json:"since"`
	// Enabled is false when the query log is off; the lists are then empty.
	Enabled bool `json:"enabled"`
	// Logged is the number of records currently in the log.
	Logged int `json:"logged"`
	// NoUseful are questions that returned nothing, or whose bundle the
	// agent reported as containing no useful section.
	NoUseful []store.Gap `json:"no_useful"`
	// LowConfidence are answered questions where the top result barely
	// stood out from the rest — worth a look, not necessarily a gap.
	LowConfidence []store.Gap `json:"low_confidence"`
}

// gaps lists recent questions the knowledge base did not answer well.
// Query parameters: days (default 7), limit (default 20).
func (a *api) gaps(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, err := intParam(q.Get("days"), 7)
	if err != nil || days <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_days", "days must be a positive integer")
		return
	}
	limit, err := intParam(q.Get("limit"), 20)
	if err != nil || limit <= 0 || limit > 200 {
		writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
		return
	}
	resp := gapsResponse{Since: time.Now().Add(-time.Duration(days) * 24 * time.Hour), Enabled: a.deps.QueryLog,
		NoUseful: []store.Gap{}, LowConfidence: []store.Gap{}}
	if resp.Logged, _, err = a.deps.Store.QueryLogStats(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "gaps_failed", err.Error())
		return
	}
	noUseful, low, err := a.deps.Store.Gaps(r.Context(), resp.Since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "gaps_failed", err.Error())
		return
	}
	if noUseful != nil {
		resp.NoUseful = noUseful
	}
	if low != nil {
		resp.LowConfidence = low
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

// graphResponse is the link graph plus what agents were served recently.
type graphResponse struct {
	*store.Graph
	Served []metrics.Served `json:"served"`
}

// graph returns the document link graph with orphans and broken links, and
// the documents retrieval returned in the last 24 hours.
func (a *api) graph(w http.ResponseWriter, r *http.Request) {
	g, err := a.deps.Store.LinkGraph(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "graph_failed", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, graphResponse{Graph: g, Served: a.deps.Metrics.RecentlyServed()})
}

// linkSuggestions lists documents that mention another by name without
// linking to it, recomputed only when the index changes.
func (a *api) linkSuggestions(w http.ResponseWriter, r *http.Request) {
	fp, err := a.deps.Store.GetIndexFingerprint(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "suggestions_failed", err.Error())
		return
	}
	a.suggestMu.Lock()
	defer a.suggestMu.Unlock()
	if a.suggestFor != fp || a.suggested == nil {
		list, err := a.deps.Store.SuggestLinks(r.Context(), 200)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "suggestions_failed", err.Error())
			return
		}
		if list == nil {
			list = []store.LinkSuggestion{}
		}
		a.suggestFor, a.suggested = fp, list
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": a.suggested})
}

// instance describes this server's configuration without secrets.
func (a *api) instance(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{}
	if a.deps.Instance != nil {
		for k, v := range a.deps.Instance() {
			info[k] = v
		}
	}
	if sync, err := a.deps.Store.GetSyncState(r.Context()); err == nil {
		info["head_commit"] = sync.LastCommit
		info["last_sync_at"] = nullableTime(sync.LastSyncAt)
		info["last_sync_error"] = sync.LastError
	}
	if counts, err := a.deps.Store.CountProposals(r.Context()); err == nil {
		info["proposals"] = counts
	}
	if n, _, err := a.deps.Store.QueryLogStats(r.Context()); err == nil {
		info["query_log_records"] = n
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, info)
}

// coverage lists, per code repository, the directories no document's refs
// claim — the knowledge base's blind spots (ADR-0008).
func (a *api) coverage(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"enabled": a.deps.Coverage != nil, "repos": []staleness.RepoCoverage{}}
	if a.deps.Coverage != nil {
		repos, err := a.deps.Coverage(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "coverage_failed", err.Error())
			return
		}
		if repos != nil {
			resp["repos"] = repos
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func (a *api) listProposals(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Store.ListProposals(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", "listing proposals failed")
		return
	}
	if list == nil {
		list = []store.Proposal{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": list})
}

// webhook accepts GitHub-style push deliveries: the body's HMAC-SHA256 with
// the shared secret must match X-Hub-Signature-256.
func (a *api) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "could not read body")
		return
	}
	sig, ok := strings.CutPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing_signature", "X-Hub-Signature-256 header required")
		return
	}
	mac := hmac.New(sha256.New, []byte(a.deps.WebhookSecret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		writeError(w, http.StatusUnauthorized, "bad_signature", "signature mismatch")
		return
	}
	if a.deps.OnWebhook != nil {
		go a.deps.OnWebhook()
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "sync scheduled"})
}

func (a *api) scopes(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	scopes, err := a.deps.Store.ListScopes(r.Context())
	a.record(start, metrics.Request{Name: "api_scopes"}, err != nil)
	if err != nil {
		a.deps.Logger.Error("list scopes failed", "err", err)
		writeError(w, http.StatusInternalServerError, "list_failed", "listing scopes failed")
		return
	}
	if scopes == nil {
		scopes = []store.ScopeInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"scopes": scopes})
}

func (a *api) document(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	path := r.PathValue("path")
	doc, content, err := a.deps.Store.GetDocument(r.Context(), path)
	a.record(start, metrics.Request{Name: "api_docs", Query: path, Chunks: 1}, err != nil)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err != nil {
		a.deps.Logger.Error("get document failed", "path", path, "err", err)
		writeError(w, http.StatusInternalServerError, "get_failed", "loading document failed")
		return
	}
	if scopes := splitList(r.URL.Query().Get("scopes")); len(scopes) > 0 && !contains(scopes, doc.Scope) {
		writeError(w, http.StatusForbidden, "scope_mismatch", "document is outside the requested scopes")
		return
	}
	links, backlinks, err := a.deps.Store.LinksOf(r.Context(), doc.Path)
	if err != nil {
		a.deps.Logger.Error("links failed", "path", path, "err", err)
	}
	if links == nil {
		links = []store.LinkRef{}
	}
	if backlinks == nil {
		backlinks = []store.LinkRef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":       doc.Path,
		"scope":      doc.Scope,
		"title":      doc.Title,
		"tags":       doc.Tags,
		"content":    content,
		"indexed_at": doc.IndexedAt,
		"updated_at": nullableTime(doc.UpdatedAt),
		"links":      links,
		"backlinks":  backlinks,
	})
}

// bearer returns middleware enforcing "Authorization: Bearer <token>".
// With an empty token the middleware is a no-op (authentication disabled).
func bearer(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		want := []byte(token)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), want) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="briefd"`)
				writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code before delegating.
func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush keeps streaming responses (SSE from the MCP handler) working.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration", time.Since(start).Round(time.Microsecond))
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func intParam(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, errors.New("must be a non-negative integer")
	}
	return n, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
