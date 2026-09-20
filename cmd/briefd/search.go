package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

func runSearch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db := fs.String("db", envOr("DB", defaultDB), "SQLite database path")
	scopes := fs.String("scopes", "", "comma-separated scopes (default: domain,conventions)")
	topK := fs.Int("top-k", search.DefaultTopK, "maximum number of results")
	asJSON := fs.Bool("json", false, "print results as JSON")
	full := fs.Bool("full", false, "print full chunk content instead of a preview")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd search [flags] <query>\n\nRun a BM25 search against the index.\n\n")
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

	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	q := search.Query{Text: query, TopK: *topK}
	if *scopes != "" {
		q.Scopes = strings.Split(*scopes, ",")
	}
	hits, err := search.New(st).Search(ctx, q)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if hits == nil {
			hits = []store.ChunkHit{}
		}
		return enc.Encode(hits)
	}
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "no results")
		return nil
	}
	for i, h := range hits {
		fmt.Fprintf(stdout, "%2d. %-7.3f %s  [%s]\n    %s  (%d tokens, id %s)\n",
			i+1, h.Score, h.HeadingPath, h.Scope, h.DocPath, h.Tokens, h.ChunkID)
		if *full {
			fmt.Fprintln(stdout, indent(h.Content, "    │ "))
		} else {
			fmt.Fprintf(stdout, "    %s\n", preview(h.Content, 160))
		}
		fmt.Fprintln(stdout)
	}
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
