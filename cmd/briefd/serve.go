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
	"sync"
	"time"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/embed"
	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/httpapi"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/mcpserver"
	"github.com/ismailperim/briefd/internal/metrics"
	"github.com/ismailperim/briefd/internal/proposal"
	"github.com/ismailperim/briefd/internal/search"
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
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))

	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()
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
			return err
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
			return err
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
		return err
	}
	if embedder != nil {
		searcher.WithEmbedder(embedder)
		if err := searcher.Reload(ctx); err != nil {
			return err
		}
	} else {
		logger.Warn("embeddings disabled; search is BM25-only")
	}
	syncer := &syncer{st: st, reg: reg, logger: logger, searcher: searcher, embedder: embedder, batch: cfg.Embeddings.BatchSize, repo: repo, root: root, proposals: proposals,
		queryLogRetention: time.Duration(cfg.QueryLog.RetentionDays) * 24 * time.Hour}

	if cfg.Source != "" {
		// Documents are indexed before we listen so BM25 works immediately;
		// embeddings can take minutes and run in the background.
		if err := syncer.run(ctx, false); err != nil {
			return fmt.Errorf("initial index: %w", err)
		}
	} else {
		logger.Warn("no source configured; serving the existing index only")
		syncer.refreshGauges(ctx)
	}
	if cfg.APIToken == "" {
		logger.Warn("BRIEFD_API_TOKEN is not set; /mcp and /api are unauthenticated")
	}
	// The MCP SDK logs every session at info level, which in stateless mode
	// means every request; only surface that when debugging.
	var mcpLogger *slog.Logger
	if cfg.LogLevel == "debug" {
		mcpLogger = logger
	}
	compiler := bundle.New(st, searcher, bundle.Options{
		DefaultScopes:    cfg.Search.DefaultScopes,
		DefaultMaxTokens: cfg.Search.DefaultMaxTokens,
		OnCache:          reg.RecordCache,
	})
	mcpSrv := mcpserver.New(mcpserver.Deps{Store: st, Searcher: searcher, Compiler: compiler, Proposals: proposals, Metrics: reg, QueryLog: cfg.QueryLog.Enabled, Version: version, Logger: mcpLogger})
	handler := httpapi.New(httpapi.Deps{
		Store:         st,
		Searcher:      searcher,
		Compiler:      compiler,
		Proposals:     proposals,
		WebhookSecret: cfg.Sync.WebhookSecret,
		OnWebhook: func() {
			if err := syncer.run(ctx, true); err != nil && ctx.Err() == nil {
				logger.Error("webhook sync failed", "err", err)
			}
		},
		MCP:                mcpserver.Handler(mcpSrv, mcpLogger),
		APIToken:           cfg.APIToken,
		Metrics:            reg,
		MetricsRequireAuth: cfg.Metrics.RequireAuth,
		QueryLog:           cfg.QueryLog.Enabled,
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

	if cfg.Source != "" {
		go func() {
			if err := syncer.run(ctx, true); err != nil && ctx.Err() == nil {
				logger.Error("background embedding failed", "err", err)
			}
		}()
		if cfg.Sync.Interval > 0 {
			go syncer.loop(ctx, cfg.Sync.Interval)
		}
	}

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
	mu                sync.Mutex
}

// run pulls the git source (if any) and indexes the checkout. With
// embeddings=true it also computes missing vectors, reloading the vector
// index as batches land.
func (s *syncer) run(ctx context.Context, embeddings bool) error {
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
	opts := indexer.Options{Root: s.root, Commit: commit, Logger: s.logger}
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
