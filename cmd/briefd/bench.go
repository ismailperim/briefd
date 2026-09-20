package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/ismailperim/briefd/internal/bundle"
	"github.com/ismailperim/briefd/internal/eval"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/mcpserver"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

func runBench(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var common commonFlags
	common.register(fs)
	source := fs.String("source", "testdata/knowledge", "corpus directory")
	golden := fs.String("golden", "eval/golden/queries.yaml", "golden queries file (the tasks)")
	budgets := fs.String("budgets", "1000,2000", "comma-separated compile_bundle budgets to measure")
	baseline := fs.String("baseline", "conventions/,domain/glossary.md", "comma-separated paths a team would paste into a static CLAUDE.md")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	markdown := fs.Bool("markdown", false, "print the report as a Markdown table")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd bench [flags]\n\nMeasure knowledge tokens per task and answer coverage: static CLAUDE.md vs compile_bundle.\n\n")
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
	var budgetList []int
	for _, b := range strings.Split(*budgets, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(b))
		if err != nil || n <= 0 {
			return fmt.Errorf("bad budget %q", b)
		}
		budgetList = append(budgetList, n)
	}

	dbPath := common.db
	if dbPath == "" {
		tmp, err := os.MkdirTemp("", "briefd-bench-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp) //nolint:errcheck // best-effort temp cleanup
		dbPath = filepath.Join(tmp, "bench.db")
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
	if _, err := indexer.Run(ctx, st, indexer.Options{Root: *source, Logger: logger, Embedder: embedder, BatchSize: cfg.Embeddings.BatchSize}); err != nil {
		return err
	}
	searcher := search.New(st, search.Options{MaxTopK: 100})
	if embedder != nil {
		searcher.WithEmbedder(embedder)
		if err := searcher.Reload(ctx); err != nil {
			return err
		}
	}
	compiler := bundle.New(st, searcher, bundle.Options{})

	rep, err := eval.Bench(ctx, st, compiler, g, eval.BenchOptions{
		Budgets:      budgetList,
		Baseline:     splitCSV(*baseline),
		ToolOverhead: eval.ToolOverheadTokens(mcpserver.ToolDefinitionsJSON()),
	})
	if err != nil {
		return err
	}
	switch {
	case *asJSON:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	case *markdown:
		printBenchMarkdown(stdout, rep)
	default:
		printBench(stdout, rep)
	}
	return nil
}

func printBench(w io.Writer, rep *eval.BenchReport) {
	fmt.Fprintf(w, "corpus: %d documents, %d sections, ~%d tokens · %d tasks · tool definitions: ~%d tokens once per session\n\n",
		rep.CorpusDocs, rep.CorpusSections, rep.CorpusTokens, rep.Queries, rep.ToolOverhead)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "scenario\ttokens/task (mean)\tmedian\tmax\tanswer present\tvs everything")
	full := rep.Scenarios[0].TokensPerTask
	for _, s := range rep.Scenarios {
		fmt.Fprintf(tw, "%s\t%.0f\t%d\t%d\t%.0f%%\t%s\n", s.Name, s.TokensPerTask, s.MedianTokens, s.MaxTokens, s.Coverage*100, savings(full, s.TokensPerTask))
	}
	_ = tw.Flush()
}

func printBenchMarkdown(w io.Writer, rep *eval.BenchReport) {
	fmt.Fprintf(w, "Corpus: %d documents, %d sections, ~%d tokens. Tasks: %d. Tool definitions: ~%d tokens once per session.\n\n",
		rep.CorpusDocs, rep.CorpusSections, rep.CorpusTokens, rep.Queries, rep.ToolOverhead)
	fmt.Fprintln(w, "| Scenario | Tokens per task (mean) | Median | Max | Answer present | vs. everything |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|")
	full := rep.Scenarios[0].TokensPerTask
	for _, s := range rep.Scenarios {
		fmt.Fprintf(w, "| %s | %.0f | %d | %d | %.0f%% | %s |\n", s.Name, s.TokensPerTask, s.MedianTokens, s.MaxTokens, s.Coverage*100, savings(full, s.TokensPerTask))
	}
}

func savings(full, v float64) string {
	if full <= 0 {
		return "—"
	}
	return fmt.Sprintf("−%.0f%%", (1-v/full)*100)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
