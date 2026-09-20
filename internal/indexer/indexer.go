// Package indexer drives ingestion: it walks a knowledge source, parses the
// files that changed, and reconciles the store with what is on disk.
package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/ismailperim/briefd/internal/ingest"
	"github.com/ismailperim/briefd/internal/store"
)

// Options controls one indexing run.
type Options struct {
	// Root is the knowledge repository directory.
	Root string
	// Commit is recorded on every indexed document ("" for a plain directory).
	Commit string
	// Force re-parses every file even if its content hash is unchanged.
	Force bool
	// Logger receives per-file progress; nil disables logging.
	Logger *slog.Logger
}

// Stats summarizes an indexing run.
type Stats struct {
	Scanned  int           `json:"scanned"`
	Indexed  int           `json:"indexed"`
	Skipped  int           `json:"skipped"`
	Deleted  int           `json:"deleted"`
	Chunks   int           `json:"chunks"`
	Duration time.Duration `json:"duration"`
}

// Run indexes opts.Root into st. Unchanged files (by content hash) are
// skipped, documents that disappeared from disk are deleted, and the
// sync_state row is updated on success.
func Run(ctx context.Context, st *store.Store, opts Options) (Stats, error) {
	start := time.Now()
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	files, err := ingest.Walk(opts.Root)
	if err != nil {
		return Stats{}, err
	}
	known, err := st.ContentHashes(ctx)
	if err != nil {
		return Stats{}, err
	}

	var stats Stats
	present := make(map[string]bool, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		stats.Scanned++
		present[f.RelPath] = true

		doc, err := ingest.Load(f)
		if err != nil {
			return stats, err
		}
		if !opts.Force && known[f.RelPath] == doc.ContentHash {
			stats.Skipped++
			continue
		}
		if err := st.UpsertDocument(ctx, doc, opts.Commit); err != nil {
			return stats, err
		}
		stats.Indexed++
		stats.Chunks += len(doc.Chunks)
		log.Debug("indexed", "path", f.RelPath, "scope", doc.Scope, "chunks", len(doc.Chunks))
	}

	// Anything in the store that is no longer on disk was deleted upstream.
	var stale []string
	for p := range known {
		if !present[p] {
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	for _, p := range stale {
		if err := st.DeleteDocument(ctx, p); err != nil {
			return stats, err
		}
		stats.Deleted++
		log.Debug("deleted", "path", p)
	}

	stats.Duration = time.Since(start)
	err = st.SetSyncState(ctx, store.SyncState{
		Source:     opts.Root,
		LastCommit: opts.Commit,
		LastSyncAt: time.Now(),
	})
	if err != nil {
		return stats, fmt.Errorf("recording sync state: %w", err)
	}
	log.Debug("index complete", "scanned", stats.Scanned, "indexed", stats.Indexed,
		"skipped", stats.Skipped, "deleted", stats.Deleted, "duration", stats.Duration)
	return stats, nil
}
