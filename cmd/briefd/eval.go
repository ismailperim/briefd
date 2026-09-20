package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/ismailperim/briefd/internal/eval"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

func runEval(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var common commonFlags
	common.register(fs)
	source := fs.String("source", "testdata/knowledge", "corpus directory to index for the evaluation")
	golden := fs.String("golden", "eval/golden/queries.yaml", "golden queries file")
	thresholds := fs.String("thresholds", "eval/thresholds.yaml", "thresholds file (\"\" to skip the gate)")
	modes := fs.String("modes", "", "comma-separated retrieval modes (default: bm25,vector,hybrid; bm25 only without embeddings)")
	asJSON := fs.Bool("json", false, "print the full report as JSON")
	verbose := fs.Bool("v", false, "print every query with its top results and misses")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd eval [flags]\n\nIndex a corpus into a temporary database and measure Recall@5, Recall@10 and MRR against the golden set.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	cfg, err := common.load()
	if err != nil {
		return err
	}
	g, err := eval.LoadGolden(*golden)
	if err != nil {
		return err
	}
	var th eval.Thresholds
	if *thresholds != "" {
		if th, err = eval.LoadThresholds(*thresholds); err != nil {
			return err
		}
	}

	// A throwaway database keeps the eval independent of any running index,
	// unless --db was given explicitly (useful to iterate on queries quickly).
	dbPath := common.db
	if dbPath == "" {
		tmp, err := os.MkdirTemp("", "briefd-eval-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp) //nolint:errcheck // best-effort temp cleanup
		dbPath = filepath.Join(tmp, "eval.db")
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	embedder, err := openEmbedder(ctx, cfg, logger, stderr)
	if err != nil {
		return err
	}
	stats, err := indexer.Run(ctx, st, indexer.Options{Root: *source, Logger: logger, Embedder: embedder, BatchSize: cfg.Embeddings.BatchSize})
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "corpus: %d document(s), %d chunk(s) indexed, %d embedded (%s)\n", stats.Indexed+stats.Skipped, stats.Chunks, stats.Embedded, stats.Duration.Round(1e6))

	searcher := search.New(st, search.Options{MaxTopK: 100})
	if embedder != nil {
		searcher.WithEmbedder(embedder)
		if err := searcher.Reload(ctx); err != nil {
			return err
		}
	}
	modeList := []string{search.ModeBM25}
	if embedder != nil {
		modeList = []string{search.ModeBM25, search.ModeVector, search.ModeHybrid}
	}
	if *modes != "" {
		modeList = strings.Split(*modes, ",")
	}

	rep, err := eval.Run(ctx, st, searcher, g, eval.Options{Modes: modeList, TopK: 10})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		printReport(stdout, rep, *verbose)
	}

	if th != nil {
		if failures := eval.Check(rep, th); len(failures) > 0 {
			fmt.Fprintf(stderr, "\neval FAILED %d threshold(s):\n", len(failures))
			for _, f := range failures {
				fmt.Fprintf(stderr, "  - %s\n", f)
			}
			return errors.New("retrieval quality below thresholds")
		}
		fmt.Fprintf(stdout, "\nthresholds OK (%s)\n", *thresholds)
	}
	return nil
}

func printReport(w io.Writer, rep *eval.Report, verbose bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "mode\ttype\tqueries\tRecall@5\tRecall@10\tMRR")
	for _, m := range rep.Modes {
		fmt.Fprintf(tw, "%s\tall\t%d\t%.3f\t%.3f\t%.3f\n", m.Mode, m.Overall.Queries, m.Overall.Recall5, m.Overall.Recall10, m.Overall.MRR)
		for _, t := range sortedKeys(m.ByType) {
			bt := m.ByType[t]
			fmt.Fprintf(tw, "\t%s\t%d\t%.3f\t%.3f\t%.3f\n", t, bt.Queries, bt.Recall5, bt.Recall10, bt.MRR)
		}
	}
	_ = tw.Flush()
	if !verbose {
		return
	}
	for _, m := range rep.Modes {
		fmt.Fprintf(w, "\n== %s\n", m.Mode)
		for _, q := range m.Queries {
			status := "ok  "
			if q.FirstRank == 0 {
				status = "MISS"
			} else if q.FirstRank > 5 {
				status = "late"
			}
			fmt.Fprintf(w, "%s  %-28s rank=%-2d r5=%.2f  %s\n", status, q.ID, q.FirstRank, q.Recall5, q.Type)
			if q.FirstRank == 0 || q.FirstRank > 5 || q.Recall5 < 1 {
				for i, t := range q.Top {
					fmt.Fprintf(w, "        %d. %s\n", i+1, t)
				}
				for _, miss := range q.Missed {
					fmt.Fprintf(w, "        missed: %s\n", miss)
				}
			}
		}
	}
}

func sortedKeys(m map[string]eval.Metrics) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
