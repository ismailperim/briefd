package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

func runSearch(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var common commonFlags
	common.register(fs)
	scopes := fs.String("scopes", "", "comma-separated scopes (default: domain,conventions)")
	topK := fs.Int("top-k", search.DefaultTopK, "maximum number of results")
	maxTokens := fs.Int("max-tokens", search.DefaultMaxTokens, "token budget for the returned chunks")
	mode := fs.String("mode", "", "retrieval mode: bm25 | vector | hybrid (default: hybrid when embeddings are enabled)")
	asJSON := fs.Bool("json", false, "print results as JSON")
	full := fs.Bool("full", false, "print full chunk content instead of a preview")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd search [flags] <query>\n\nQuery the index the same way search_context does.\n\n")
		fs.PrintDefaults()
	}
	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return errUsage
	}
	if len(positional) == 0 {
		fs.Usage()
		return errUsage
	}
	query := strings.Join(positional, " ")
	cfg, err := common.load()
	if err != nil {
		return err
	}
	if *mode == search.ModeBM25 {
		cfg.Embeddings.Enabled = false
	}

	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	searcher := search.New(st, search.Options{
		DefaultScopes: cfg.Search.DefaultScopes, DefaultMaxTokens: cfg.Search.DefaultMaxTokens, MaxTopK: cfg.Search.MaxTopK,
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
		if searcher.Vectors().Len() == 0 {
			fmt.Fprintln(stderr, "note: no vectors in the index yet; run `briefd index` with embeddings enabled")
		}
	}

	q := search.Query{Text: query, TopK: *topK, MaxTokens: *maxTokens, Mode: *mode}
	if *scopes != "" {
		q.Scopes = strings.Split(*scopes, ",")
	}
	res, err := searcher.Search(ctx, q)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	if len(res.Chunks) == 0 {
		fmt.Fprintln(stdout, "no results")
		return nil
	}
	for i, h := range res.Chunks {
		fmt.Fprintf(stdout, "%2d. %-7.3f %s  [%s]\n    %s  (%d tokens, id %s)\n",
			i+1, h.Score, h.HeadingPath, h.Scope, h.DocPath, h.Tokens, h.ChunkID)
		if *full {
			fmt.Fprintln(stdout, indent(h.Content, "    │ "))
		} else {
			fmt.Fprintf(stdout, "    %s\n", preview(h.Content, 160))
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "%d chunk(s), %d tokens (budget %d), %d omitted, mode %s, scopes %s\n",
		len(res.Chunks), res.TotalTokens, res.Budget, res.Omitted, res.Mode, strings.Join(res.Scopes, ","))
	return nil
}

// preview returns the first non-heading line of content, trimmed to n runes.
func preview(content string, n int) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := []rune(line)
		if len(r) > n {
			return string(r[:n-1]) + "…"
		}
		return line
	}
	return ""
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
