package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/config"
	"github.com/ismailperim/briefd/internal/embed"
	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/httpapi"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/mcpserver"
	"github.com/ismailperim/briefd/internal/metrics"
	"github.com/ismailperim/briefd/internal/proposal"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/staleness"
	"github.com/ismailperim/briefd/internal/store"
)

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var common commonFlags
	common.register(fs)
	listen := fs.String("listen", "", "listen address (default :7788)")
	source := fs.String("source", "", "knowledge directory or git URL to index and keep in sync")
	token := fs.String("token", "", "bearer token for /mcp and /api (default: none, authentication disabled)")
	syncInterval := fs.Duration("sync-interval", -1, "how often to re-scan the source; 0 disables (default 60s)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd serve [flags]\n\nStart the MCP + REST server.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	cfg, err := common.load()
	if err != nil {
		return err
	}
	// Flags override everything else.
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *source != "" {
		cfg.Source = *source
	}
	if *token != "" {
		cfg.APIToken = *token
	}
	if *syncInterval >= 0 {
		cfg.Sync.Interval = *syncInterval
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	rt, err := buildProcess(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	defer rt.close()
	logger, st, repo, proposals, syncer := rt.logger, rt.st, rt.repo, rt.proposals, rt.syncer
	rt.restoreCounters(ctx)
	defer rt.saveCounters()
	go rt.saveCountersEvery(ctx, 30*time.Second)
	if cfg.APIToken == "" {
		logger.Warn("BRIEFD_API_TOKEN is not set; /mcp and /api are unauthenticated")
	}
	mcpSrv := rt.mcpServer(cfg)
	handler := httpapi.New(httpapi.Deps{
		Store:         st,
		Searcher:      rt.searcher,
		Compiler:      rt.compiler,
		Proposals:     proposals,
		WebhookSecret: cfg.Sync.WebhookSecret,
		OnWebhook: func() {
			if err := syncer.run(ctx, true); err != nil && ctx.Err() == nil {
				logger.Error("webhook sync failed", "err", err)
			}
		},
		OnSync: func(rebuild bool) {
			if err := syncer.runWith(ctx, true, rebuild); err != nil && ctx.Err() == nil {
				logger.Error("manual sync failed", "rebuild", rebuild, "err", err)
			}
		},
		OnResetStats: func(rctx context.Context) error {
			rt.reg.Reset()
			return st.DeleteState(rctx, countersKey)
		},
		MCP:                mcpserver.Handler(mcpSrv, rt.mcpLogger(cfg)),
		APIToken:           cfg.APIToken,
		Metrics:            rt.reg,
		MetricsRequireAuth: cfg.Metrics.RequireAuth,
		QueryLog:           cfg.QueryLog.Enabled,
		Coverage:           rt.syncer.coverageFunc(),
		Instance:           instanceInfo(cfg, rt),
		Version:            version,
		Logger:             logger,
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w (pick another address with --listen, e.g. --listen :7789)", cfg.Listen, err)
	}
	logger.Info("briefd listening", "addr", ln.Addr().String(), "mcp", "/mcp", "api", "/api", "db", cfg.DB, "source", cfg.Source,
		"git", repo != nil, "proposals", proposals.Available(), "webhook", cfg.Sync.WebhookSecret != "")
	fmt.Fprintf(stdout, "briefd %s listening on http://%s  (dashboard: /  MCP: /mcp  metrics: /metrics)\n", version, displayAddr(ln.Addr()))

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	rt.startSync(ctx, cfg)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// process is everything a briefd process needs behind a transport: the
// store, the followed repositories, retrieval, the bundle compiler and the
// syncer. Both `serve` (HTTP) and `mcp` (stdio) are thin wrappers over it.
type process struct {
	logger    *slog.Logger
	st        *store.Store
	reg       *metrics.Registry
	repo      *gitsync.Repo
	proposals *proposal.Service
	searcher  *search.Searcher
	compiler  *bundle.Compiler
	syncer    *syncer
}

// buildProcess opens the store and the source, wires retrieval and runs
// the initial (BM25) index so the first request has something to answer.
func buildProcess(ctx context.Context, cfg config.Config, stderr io.Writer) (rt *process, err error) {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))

	st, err := store.Open(cfg.DB)
	if err != nil {
		return nil, err
	}
	// Closed by the caller through process.close; on error, below.
	defer func() {
		if err != nil {
			st.Close()
		}
	}()
	reg := metrics.New(version)

	// Resolve the source: a git URL is cloned and followed; a directory that
	// is itself a git checkout is read in place (proposals become local
	// branches); a plain directory is just indexed.
	var repo *gitsync.Repo
	switch {
	case cfg.Source == "":
	case gitsync.IsGitURL(cfg.Source):
		repo, err = gitsync.Open(ctx, gitsync.Config{
			URL: cfg.Source, Branch: cfg.Git.Branch, Dir: cfg.GitDir(), Token: cfg.Git.Token, Username: cfg.Git.Username,
			SSHKeyPath: cfg.Git.SSHKey, AuthorName: cfg.Git.AuthorName, AuthorEmail: cfg.Git.AuthorEmail,
		}, logger)
		if err != nil {
			return nil, err
		}
	default:
		if r, err := gitsync.OpenLocal(cfg.Source, gitsync.Config{
			Token: cfg.Git.Token, Username: cfg.Git.Username, SSHKeyPath: cfg.Git.SSHKey,
			AuthorName: cfg.Git.AuthorName, AuthorEmail: cfg.Git.AuthorEmail,
		}, logger); err == nil {
			repo = r
			logger.Info("source is a local git checkout; proposals will be committed to local branches", "branch", r.Branch())
		}
	}
	root := cfg.Source
	if repo != nil {
		root = repo.Dir()
	}

	var forge *gitsync.Forge
	if repo != nil {
		if forge, err = gitsync.NewForge(cfg.Forge, repo.URL()); err != nil {
			return nil, err
		}
	}
	proposals := &proposal.Service{Repo: repo, Forge: forge, Store: st, Logger: logger}

	searcher := search.New(st, search.Options{
		DefaultScopes:    cfg.Search.DefaultScopes,
		DefaultMaxTokens: cfg.Search.DefaultMaxTokens,
		MaxTopK:          cfg.Search.MaxTopK,
	})
	embedder, err := openEmbedder(ctx, cfg, logger, stderr)
	if err != nil {
		// A local model that cannot be loaded or downloaded (no network, a
		// read-only cache) should not keep the server from coming up: BM25
		// still answers, and the dashboard shows "bm25" instead of "hybrid".
		// Remote providers fail hard, since that is a configuration error.
		if cfg.Embeddings.Provider != "local" {
			return nil, err
		}
		logger.Error("embeddings unavailable; serving BM25-only until restart (run `briefd model pull` or set --embeddings none to silence)", "err", err)
		embedder = nil
	}
	if embedder != nil {
		searcher.WithEmbedder(embedder)
		if err := searcher.Reload(ctx); err != nil {
			return nil, err
		}
	} else {
		logger.Warn("embeddings disabled; search is BM25-only")
	}
	compiler := bundle.New(st, searcher, bundle.Options{
		DefaultScopes:    cfg.Search.DefaultScopes,
		DefaultMaxTokens: cfg.Search.DefaultMaxTokens,
		OnCache:          reg.RecordCache,
	})

	// Code repositories are followed history-only; their working files are
	// never read. Drift is computed after every index run (ADR-0007).
	var code []codeRepo
	for _, cr := range cfg.Code.Repos {
		var r *gitsync.Repo
		if gitsync.IsGitURL(cr.Source) {
			r, err = gitsync.Open(ctx, gitsync.Config{
				URL: cr.Source, Branch: cr.Branch, Dir: cfg.CodeDir(cr), Bare: true,
				Token: cfg.Git.Token, Username: cfg.Git.Username, SSHKeyPath: cfg.Git.SSHKey,
			}, logger)
		} else {
			r, err = gitsync.OpenLocal(cr.Source, gitsync.Config{}, logger)
		}
		if err != nil {
			return nil, fmt.Errorf("code repository %s: %w", cr.DisplayName(), err)
		}
		code = append(code, codeRepo{name: cr.DisplayName(), repo: r})
	}

	syncer := &syncer{st: st, reg: reg, logger: logger, searcher: searcher, embedder: embedder, batch: cfg.Embeddings.BatchSize, repo: repo, root: root, proposals: proposals,
		queryLogRetention: time.Duration(cfg.QueryLog.RetentionDays) * 24 * time.Hour,
		compiler:          compiler, code: code, maxCommits: cfg.Code.MaxCommits}

	if cfg.Source != "" {
		// Documents are indexed before we listen so BM25 works immediately;
		// embeddings can take minutes and run in the background.
		if err := syncer.run(ctx, false); err != nil {
			return nil, fmt.Errorf("initial index: %w", err)
		}
	} else {
		logger.Warn("no source configured; serving the existing index only")
		syncer.refreshGauges(ctx)
	}
	return &process{logger: logger, st: st, reg: reg, repo: repo, proposals: proposals, searcher: searcher, compiler: compiler, syncer: syncer}, nil
}

func (rt *process) close() { rt.st.Close() }

// countersKey is where the dashboard counters are saved between runs.
const countersKey = "metrics"

// restoreCounters loads the counters saved by a previous run, so tokens
// served, request counts and the recent log survive restarts.
func (rt *process) restoreCounters(ctx context.Context) {
	data, err := rt.st.GetState(ctx, countersKey)
	if err != nil || data == nil {
		return
	}
	if err := rt.reg.Import(data); err != nil {
		rt.logger.Warn("saved dashboard counters ignored", "err", err)
		return
	}
	rt.logger.Info("dashboard counters restored", "since", rt.reg.Since().Format(time.RFC3339))
}

// saveCounters writes the counters; errors are logged, not fatal.
func (rt *process) saveCounters() {
	data, err := rt.reg.Export()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = rt.st.SetState(ctx, countersKey, data)
	}
	if err != nil {
		rt.logger.Warn("saving dashboard counters failed", "err", err)
	}
}

func (rt *process) saveCountersEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rt.saveCounters()
		}
	}
}

// instanceInfo describes the running configuration for the dashboard.
// Secrets are reduced to whether they are set; URLs lose any userinfo.
func instanceInfo(cfg config.Config, rt *process) func() map[string]any {
	started := time.Now()
	sourceKind := "none"
	switch {
	case gitsync.IsGitURL(cfg.Source):
		sourceKind = "git"
	case rt.repo != nil:
		sourceKind = "local git checkout"
	case cfg.Source != "":
		sourceKind = "directory"
	}
	branch := cfg.Git.Branch
	if rt.repo != nil {
		branch = rt.repo.Branch()
	}
	var code []map[string]string
	for _, cr := range cfg.Code.Repos {
		code = append(code, map[string]string{"name": cr.DisplayName(), "source": redactURL(cr.Source), "branch": cr.Branch})
	}
	gitAuth := "none"
	switch {
	case cfg.Git.SSHKey != "":
		gitAuth = "ssh key file"
	case cfg.Git.Token != "":
		gitAuth = "https token"
	case strings.HasPrefix(cfg.Source, "git@") || strings.HasPrefix(cfg.Source, "ssh://"):
		gitAuth = "ssh agent"
	}
	return func() map[string]any {
		info := map[string]any{
			"version": version, "commit": commit, "built": date,
			"go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
			"started_at": started, "listen": cfg.Listen,
			"auth":               cfg.APIToken != "",
			"metrics_auth":       cfg.Metrics.RequireAuth,
			"source":             redactURL(cfg.Source),
			"source_kind":        sourceKind,
			"branch":             branch,
			"git_auth":           gitAuth,
			"sync_interval":      cfg.Sync.Interval.String(),
			"webhook":            cfg.Sync.WebhookSecret != "",
			"forge":              cfg.Forge.Type,
			"forge_token":        cfg.Forge.Token != "",
			"proposals_enabled":  rt.proposals.Available(),
			"db":                 cfg.DB,
			"db_bytes":           fileSize(cfg.DB) + fileSize(cfg.DB+"-wal"),
			"default_scopes":     cfg.Search.DefaultScopes,
			"default_max_tokens": cfg.Search.DefaultMaxTokens,
			"max_top_k":          cfg.Search.MaxTopK,
			"query_log":          cfg.QueryLog.Enabled,
			"query_log_days":     cfg.QueryLog.RetentionDays,
			"code_repos":         code,
			"embeddings":         "off (BM25 only)",
		}
		if rt.searcher.Hybrid() {
			v := rt.searcher.Vectors()
			info["embeddings"] = v.Model()
			info["vectors"] = v.Len()
		}
		return info
	}
}

// redactURL drops userinfo (user:token@) from a URL-shaped source.
func redactURL(u string) string {
	if i := strings.Index(u, "://"); i > 0 {
		rest := u[i+3:]
		host := rest
		if slash := strings.Index(rest, "/"); slash >= 0 {
			host = rest[:slash]
		}
		if at := strings.LastIndex(host, "@"); at >= 0 {
			return u[:i+3] + rest[at+1:]
		}
	}
	return u
}

func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return 0
}

// coverageDepth is how deep into a code repository's tree coverage looks:
// top-level directories and their children (e.g. services/payment).
const coverageDepth = 2

// coverageFunc returns nil when no code repositories are configured.
func (s *syncer) coverageFunc() func(ctx context.Context) ([]staleness.RepoCoverage, error) {
	if len(s.code) == 0 {
		return nil
	}
	return func(ctx context.Context) ([]staleness.RepoCoverage, error) {
		docs, err := s.st.DocumentsWithRefs(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]staleness.RepoCoverage, 0, len(s.code))
		for _, cr := range s.code {
			dirs, err := cr.repo.Dirs(ctx, coverageDepth)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", cr.name, err)
			}
			out = append(out, staleness.Coverage(cr.name, dirs, docs))
		}
		return out, nil
	}
}

// startSync embeds in the background and, when configured, keeps the
// source in sync on an interval.
func (rt *process) startSync(ctx context.Context, cfg config.Config) {
	if cfg.Source == "" {
		return
	}
	go func() {
		if err := rt.syncer.run(ctx, true); err != nil && ctx.Err() == nil {
			rt.logger.Error("background embedding failed", "err", err)
		}
	}()
	if cfg.Sync.Interval > 0 {
		go rt.syncer.loop(ctx, cfg.Sync.Interval)
	}
}

// mcpLogger returns the logger for the MCP SDK. It logs every session at
// info level, which in stateless HTTP mode means every request, so it is
// only enabled when debugging.
func (rt *process) mcpLogger(cfg config.Config) *slog.Logger {
	if cfg.LogLevel == "debug" {
		return rt.logger
	}
	return nil
}

// mcpServer builds the MCP server over this process.
func (rt *process) mcpServer(cfg config.Config) *mcp.Server {
	return mcpserver.New(mcpserver.Deps{
		Store: rt.st, Searcher: rt.searcher, Compiler: rt.compiler, Proposals: rt.proposals, Metrics: rt.reg,
		QueryLog: cfg.QueryLog.Enabled, Version: version, Logger: rt.mcpLogger(cfg),
	})
}

// syncer runs the indexer (documents, then embeddings) and keeps metrics
// and the in-memory vector index current. Runs are serialized.
type syncer struct {
	st        *store.Store
	reg       *metrics.Registry
	logger    *slog.Logger
	searcher  *search.Searcher
	embedder  embed.Embedder
	batch     int
	repo      *gitsync.Repo // nil for a plain directory
	proposals *proposal.Service
	root      string // directory that is indexed
	// queryLogRetention prunes the query log during sync; 0 keeps everything.
	queryLogRetention time.Duration
	compiler          *bundle.Compiler
	code              []codeRepo
	maxCommits        int
	driftChecked      bool
	mu                sync.Mutex
}

// codeRepo is a followed code repository for drift detection.
type codeRepo struct {
	name string
	repo *gitsync.Repo
}

// run pulls the git source (if any) and indexes the checkout. With
// embeddings=true it also computes missing vectors, reloading the vector
// index as batches land.
func (s *syncer) run(ctx context.Context, embeddings bool) error {
	return s.runWith(ctx, embeddings, false)
}

// runWith is run with force: re-parse every document even when its
// content hash is unchanged (the dashboard's "Rebuild index").
func (s *syncer) runWith(ctx context.Context, embeddings, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	commit := ""
	if s.repo != nil {
		head, _, err := s.repo.Sync(ctx)
		if err != nil {
			s.reg.RecordSync(err)
			return err
		}
		commit = head
	}
	opts := indexer.Options{Root: s.root, Commit: commit, Logger: s.logger, Force: force}
	if s.repo != nil {
		opts.LastModified = s.repo.LastModified
	}
	if embeddings && s.embedder != nil {
		opts.Embedder = s.embedder
		opts.BatchSize = s.batch
		opts.OnProgress = func(done, pending int) {
			s.reg.SetVectors(s.searcher.Vectors().Len()+done, pending)
			if pending == 0 || done%128 == 0 {
				if err := s.searcher.Reload(ctx); err == nil {
					s.reg.SetVectors(s.searcher.Vectors().Len(), pending)
				}
			}
		}
	}
	stats, err := indexer.Run(ctx, s.st, opts)
	s.reg.RecordSync(err)
	if err != nil {
		return err
	}
	if stats.Indexed > 0 || stats.Deleted > 0 || stats.Embedded > 0 {
		s.logger.Info("index updated", "indexed", stats.Indexed, "deleted", stats.Deleted,
			"skipped", stats.Skipped, "embedded", stats.Embedded)
	}
	if stats.Embedded > 0 || stats.Deleted > 0 {
		if err := s.searcher.Reload(ctx); err != nil {
			return err
		}
	}
	if err := s.proposals.SyncStatuses(ctx); err != nil {
		s.logger.Warn("proposal status sync failed", "err", err)
	}
	if err := s.checkDrift(ctx, stats.Indexed > 0 || stats.Deleted > 0); err != nil {
		s.logger.Warn("code drift check failed", "err", err)
	}
	if s.queryLogRetention > 0 {
		if n, err := s.st.PruneQueryLog(ctx, s.queryLogRetention); err != nil {
			s.logger.Warn("query log prune failed", "err", err)
		} else if n > 0 {
			s.logger.Debug("query log pruned", "deleted", n)
		}
	}
	s.refreshGauges(ctx)
	return nil
}

// checkDrift fetches every code repository and recomputes which documents
// lag the code their refs name. The history walk runs only when a code
// head moved, the index changed, or nothing has been computed yet.
func (s *syncer) checkDrift(ctx context.Context, indexChanged bool) error {
	if len(s.code) == 0 {
		return nil
	}
	moved := indexChanged || !s.driftChecked
	for _, cr := range s.code {
		if _, changed, err := cr.repo.Sync(ctx); err != nil {
			return fmt.Errorf("%s: %w", cr.name, err)
		} else if changed {
			moved = true
		}
	}
	if !moved {
		return nil
	}
	docs, err := s.st.DocumentsWithRefs(ctx)
	if err != nil {
		return err
	}
	since := staleness.Since(docs)
	var all []store.Drift
	now := time.Now()
	if !since.IsZero() {
		for _, cr := range s.code {
			changes, truncated, err := cr.repo.ChangesSince(ctx, since, s.maxCommits)
			if err != nil {
				return fmt.Errorf("%s: %w", cr.name, err)
			}
			if truncated {
				s.logger.Warn("code history walk capped; older drift is not counted", "repo", cr.name, "max_commits", s.maxCommits)
			}
			all = append(all, staleness.Compute(docs, cr.name, changes, now)...)
		}
	}
	// A document can match several repositories; keep the row with the
	// most commits so the report has one line per document.
	byDoc := map[string]store.Drift{}
	for _, d := range all {
		if prev, ok := byDoc[d.DocPath]; !ok || d.Commits > prev.Commits {
			byDoc[d.DocPath] = d
		}
	}
	rows := make([]store.Drift, 0, len(byDoc))
	for _, d := range byDoc {
		rows = append(rows, d)
	}
	changed, err := s.st.ReplaceDrift(ctx, rows)
	if err != nil {
		return err
	}
	s.driftChecked = true
	s.reg.SetDrift(len(rows))
	if changed {
		s.logger.Info("code drift updated", "documents_behind", len(rows))
		// Drift is rendered into bundles, so cached ones are now wrong.
		if err := s.compiler.Invalidate(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *syncer) refreshGauges(ctx context.Context) {
	scopes, err := s.st.ListScopes(ctx)
	if err != nil {
		return
	}
	counts := make([]metrics.ScopeCount, len(scopes))
	for i, sc := range scopes {
		counts[i] = metrics.ScopeCount{Scope: sc.Scope, Documents: sc.Documents, Chunks: sc.Chunks, Tokens: sc.Tokens}
	}
	s.reg.SetIndex(counts)
	if s.embedder != nil {
		pending, _ := s.st.CountPendingVectors(ctx, s.embedder.Name())
		s.reg.SetVectors(s.searcher.Vectors().Len(), pending)
	}
}

// loop re-syncs the source on a fixed interval (git fetch + incremental
// reindex, or a re-scan for plain directories).
func (s *syncer) loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.run(ctx, true); err != nil && ctx.Err() == nil {
				s.logger.Error("sync failed", "err", err)
			}
		}
	}
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// displayAddr rewrites a wildcard listen address to something clickable.
func displayAddr(a net.Addr) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}
