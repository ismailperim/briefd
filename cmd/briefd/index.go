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

const defaultDB = "briefd.db"

func runIndex(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", envOr("SOURCE", "."), "knowledge repository directory")
	db := fs.String("db", envOr("DB", defaultDB), "SQLite database path")
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

	if *rebuild {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(*db + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("removing %s: %w", *db+suffix, err)
			}
		}
	}
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	stats, err := indexer.Run(ctx, st, indexer.Options{Root: *source, Logger: logger})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "indexed %d document(s) (%d chunk(s)), skipped %d unchanged, deleted %d, in %s → %s\n",
		stats.Indexed, stats.Chunks, stats.Skipped, stats.Deleted, stats.Duration.Round(1e6), *db)
	return nil
}
