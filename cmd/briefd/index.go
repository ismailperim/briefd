package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/store"
)

// errUsage signals that flag parsing already reported the problem.
var errUsage = errors.New("usage")

func runIndex(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var common commonFlags
	common.register(fs)
	source := fs.String("source", "", "knowledge repository directory (default from config)")
	rebuild := fs.Bool("rebuild", false, "drop the database and rebuild it from scratch")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd index [flags]\n\nIndex a knowledge repository directory into the SQLite database.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return errUsage
	}
	cfg, err := common.load()
	if err != nil {
		return err
	}
	if *source != "" {
		cfg.Source = *source
	}
	if cfg.Source == "" {
		cfg.Source = "."
	}

	if *rebuild {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(cfg.DB + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("removing %s: %w", cfg.DB+suffix, err)
			}
		}
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	embedder, err := openEmbedder(ctx, cfg, logger, stderr)
	if err != nil {
		return err
	}
	stats, err := indexer.Run(ctx, st, indexer.Options{
		Root: cfg.Source, Logger: logger, Embedder: embedder, BatchSize: cfg.Embeddings.BatchSize,
		OnProgress: func(done, pending int) {
			if pending == 0 || done%64 == 0 {
				fmt.Fprintf(stderr, "\rembedding: %d done, %d pending   ", done, pending)
			}
			if pending == 0 {
				fmt.Fprintln(stderr)
			}
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "indexed %d document(s) (%d chunk(s)), skipped %d unchanged, deleted %d, embedded %d, in %s → %s\n",
		stats.Indexed, stats.Chunks, stats.Skipped, stats.Deleted, stats.Embedded, stats.Duration.Round(1e6), cfg.DB)
	return nil
}
