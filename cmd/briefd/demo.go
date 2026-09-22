package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ismailperim/briefd/internal/sample"
)

// runDemo stands up briefd on the embedded sample knowledge base (a
// fictional payments platform, 44 documents) so someone can see bundles,
// the dashboard and the MCP tools in under a minute, without a repository
// of their own.
func runDemo(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "where to put the sample corpus and database (default ~/.briefd/demo)")
	listen := fs.String("listen", "127.0.0.1:7788", "listen address")
	embeddings := fs.String("embeddings", "", "embedding provider: local | none (default: local, downloads the model once)")
	model := fs.String("model", "", "local embedding model (default multilingual-e5-small)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd demo [flags]\n\nServe the built-in sample knowledge base (fictional payments platform) with the dashboard and MCP tools, no repository needed.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if *dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("demo: %w (pass --dir)", err)
		}
		*dir = filepath.Join(home, ".briefd", "demo")
	}
	corpus := filepath.Join(*dir, "knowledge")
	created, err := sample.Write(corpus)
	if err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	if len(created) > 0 {
		fmt.Fprintf(stdout, "sample knowledge base: %d files written to %s\n", len(created), corpus)
	} else {
		fmt.Fprintf(stdout, "sample knowledge base: reusing %s\n", corpus)
	}
	fmt.Fprintf(stdout, `
Dashboard:   http://%s/
MCP (HTTP):  claude mcp add --transport http briefd http://%s/mcp
Try a task:  curl -s -X POST http://%s/api/bundle -H 'content-type: application/json' \
               -d '{"task":"add partial refunds to the merchant portal","max_tokens":1500}'
Or ask your agent: "What do I need to know before changing the refund window?"

`, *listen, *listen, *listen)
	serveArgs := []string{"--config", os.DevNull, "--source", corpus, "--db", filepath.Join(*dir, "briefd.db"), "--listen", *listen}
	if *embeddings != "" {
		serveArgs = append(serveArgs, "--embeddings", *embeddings)
	}
	if *model != "" {
		serveArgs = append(serveArgs, "--model", *model)
	}
	return runServe(ctx, serveArgs, stdout, stderr)
}
