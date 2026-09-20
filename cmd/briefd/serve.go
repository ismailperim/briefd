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
	"time"

	"github.com/ismailperim/briefd/internal/config"
	"github.com/ismailperim/briefd/internal/httpapi"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/mcpserver"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgFile := fs.String("config", envOr("CONFIG", ""), "config file (default briefd.yaml if present)")
	listen := fs.String("listen", "", "listen address (default :8080)")
	db := fs.String("db", "", "SQLite database path (default briefd.db)")
	source := fs.String("source", "", "knowledge directory to index and watch")
	token := fs.String("token", "", "bearer token for /mcp and /api (default: none, authentication disabled)")
	syncInterval := fs.Duration("sync-interval", -1, "how often to re-scan the source; 0 disables (default 60s)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd serve [flags]\n\nStart the MCP + REST server.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	cfg, err := config.Load(*cfgFile)
	if err != nil {
		return err
	}
	// Flags override everything else.
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *db != "" {
		cfg.DB = *db
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

	if cfg.Source != "" {
		if _, err := indexer.Run(ctx, st, indexer.Options{Root: cfg.Source, Logger: logger}); err != nil {
			return fmt.Errorf("initial index: %w", err)
		}
	} else {
		logger.Warn("no source configured; serving the existing index only")
	}
	if cfg.APIToken == "" {
		logger.Warn("BRIEFD_API_TOKEN is not set; /mcp and /api are unauthenticated")
	}

	searcher := search.New(st, search.Options{
		DefaultScopes:    cfg.Search.DefaultScopes,
		DefaultMaxTokens: cfg.Search.DefaultMaxTokens,
		MaxTopK:          cfg.Search.MaxTopK,
	})
	// The MCP SDK logs every session at info level, which in stateless mode
	// means every request; only surface that when debugging.
	var mcpLogger *slog.Logger
	if cfg.LogLevel == "debug" {
		mcpLogger = logger
	}
	mcpSrv := mcpserver.New(mcpserver.Deps{Store: st, Searcher: searcher, Version: version, Logger: mcpLogger})
	handler := httpapi.New(httpapi.Deps{
		Store:    st,
		Searcher: searcher,
		MCP:      mcpserver.Handler(mcpSrv, mcpLogger),
		APIToken: cfg.APIToken,
		Version:  version,
		Logger:   logger,
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Listen, err)
	}
	logger.Info("briefd listening", "addr", ln.Addr().String(), "mcp", "/mcp", "api", "/api", "db", cfg.DB, "source", cfg.Source)
	fmt.Fprintf(stdout, "briefd %s listening on http://%s  (MCP endpoint: /mcp)\n", version, displayAddr(ln.Addr()))

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	if cfg.Source != "" && cfg.Sync.Interval > 0 {
		go syncLoop(ctx, st, cfg.Source, cfg.Sync.Interval, logger)
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

// syncLoop re-scans a local source directory on a fixed interval. It is
// replaced by git polling in M5.
func syncLoop(ctx context.Context, st *store.Store, root string, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			stats, err := indexer.Run(ctx, st, indexer.Options{Root: root, Logger: logger})
			if err != nil {
				logger.Error("sync failed", "err", err)
				continue
			}
			if stats.Indexed > 0 || stats.Deleted > 0 {
				logger.Info("sync applied changes", "indexed", stats.Indexed, "deleted", stats.Deleted)
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
