// Package indexer drives ingestion: it walks a knowledge source, parses the
// files that changed, and reconciles the store with what is on disk.
package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/ismailperim/briefd/internal/embed"
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
	// Embedder, when set, computes vectors for chunks that lack an
	// up-to-date one after the documents are stored.
	Embedder embed.Embedder
	// BatchSize is how many chunks are embedded per call (default 16).
	BatchSize int
	// OnProgress, if set, is called after every embedded batch with the
	// number of vectors written so far and the number still pending.
	OnProgress func(done, pending int)
	// LastModified resolves when each repository-relative path last changed
	// (git history for a git source). Nil falls back to file mtimes, which
	// are meaningless right after a clone but fine for a plain directory.
	LastModified func(ctx context.Context, paths []string) (map[string]time.Time, error)
}

// Stats summarizes an indexing run.
type Stats struct {
	Scanned  int `json:"scanned"`
	Indexed  int `json:"indexed"`
	Skipped  int `json:"skipped"`
	Deleted  int `json:"deleted"`
	Chunks   int `json:"chunks"`
	Embedded int `json:"embedded"`
	// Fingerprint identifies the resulting index state (see store.IndexFingerprint).
	Fingerprint string        `json:"fingerprint"`
	Duration    time.Duration `json:"duration"`
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
	// Documents indexed before links were tracked are re-parsed once.
	unscanned, err := st.DocumentsWithoutLinkScan(ctx)
	if err != nil {
		return Stats{}, err
	}

	var stats Stats
	present := make(map[string]bool, len(files))
	abs := make(map[string]string, len(files))
	var changed []string
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		stats.Scanned++
		present[f.RelPath] = true
		abs[f.RelPath] = f.AbsPath

		doc, err := ingest.Load(f)
		if err != nil {
			return stats, err
		}
		if !opts.Force && known[f.RelPath] == doc.ContentHash && !unscanned[f.RelPath] {
			stats.Skipped++
			continue
		}
		if err := st.UpsertDocument(ctx, doc, opts.Commit); err != nil {
			return stats, err
		}
		stats.Indexed++
		stats.Chunks += len(doc.Chunks)
		changed = append(changed, f.RelPath)
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

	if err := resolveAges(ctx, st, opts, changed, abs); err != nil {
		return stats, err
	}
	if stats.Indexed > 0 || stats.Deleted > 0 {
		if err := st.ResolveLinks(ctx); err != nil {
			return stats, err
		}
	}

	if opts.Embedder != nil {
		n, err := embedPending(ctx, st, opts, log)
		stats.Embedded = n
		if err != nil {
			return stats, err
		}
	}

	// Record the resulting knowledge state and drop bundles compiled against
	// an older one; their cache keys can never match again.
	stats.Fingerprint, err = st.IndexFingerprint(ctx)
	if err != nil {
		return stats, err
	}
	if err := st.SetIndexFingerprint(ctx, stats.Fingerprint); err != nil {
		return stats, err
	}
	if stats.Indexed > 0 || stats.Deleted > 0 {
		if _, err := st.PruneBundles(ctx, stats.Fingerprint); err != nil {
			return stats, err
		}
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
		"skipped", stats.Skipped, "deleted", stats.Deleted, "embedded", stats.Embedded, "duration", stats.Duration)
	return stats, nil
}

// embedPending computes vectors for every chunk that has none (or a stale
// one) for the configured model, in batches, and prunes orphaned vectors.
func embedPending(ctx context.Context, st *store.Store, opts Options, log *slog.Logger) (int, error) {
	batch := opts.BatchSize
	if batch <= 0 {
		batch = 16
	}
	model := opts.Embedder.Name()
	if _, err := st.PruneVectors(ctx); err != nil {
		return 0, err
	}
	pending, err := st.CountPendingVectors(ctx, model)
	if err != nil {
		return 0, err
	}
	if pending == 0 {
		return 0, nil
	}
	log.Info("embedding chunks", "pending", pending, "model", model)
	done := 0
	started := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		chunks, err := st.PendingVectors(ctx, model, batch)
		if err != nil {
			return done, err
		}
		if len(chunks) == 0 {
			break
		}
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Text
		}
		vecs, err := opts.Embedder.Embed(ctx, texts)
		if err != nil {
			return done, fmt.Errorf("embedding batch: %w", err)
		}
		if err := st.PutVectors(ctx, model, chunks, vecs); err != nil {
			return done, err
		}
		done += len(chunks)
		if opts.OnProgress != nil {
			opts.OnProgress(done, pending-done)
		}
		if done%(batch*8) == 0 || done == pending {
			log.Info("embedding progress", "done", done, "pending", pending-done, "elapsed", time.Since(started).Round(time.Second))
		}
	}
	return done, nil
}

// resolveAges records updated_at for the documents indexed in this run and
// for any that still lack one (first run after upgrading).
func resolveAges(ctx context.Context, st *store.Store, opts Options, changed []string, abs map[string]string) error {
	missing, err := st.DocumentsWithoutAge(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(changed)+len(missing))
	var paths []string
	for _, p := range append(changed, missing...) {
		if !seen[p] && abs[p] != "" {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	times := map[string]time.Time{}
	if opts.LastModified != nil {
		if times, err = opts.LastModified(ctx, paths); err != nil {
			return fmt.Errorf("resolving document ages: %w", err)
		}
	}
	// Files git does not know about yet (uncommitted, or a plain directory)
	// fall back to their mtime.
	for _, p := range paths {
		if _, ok := times[p]; ok {
			continue
		}
		if fi, err := os.Stat(abs[p]); err == nil {
			times[p] = fi.ModTime()
		}
	}
	return st.SetDocumentUpdatedAt(ctx, times)
}
