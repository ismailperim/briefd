package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runMCP serves the MCP tools over stdio: JSON-RPC on stdin/stdout, logs on
// stderr. This is what Claude Desktop, Cursor's stdio config and directory
// inspectors (Glama, Smithery) launch; teams sharing one instance use
// `serve` and its HTTP endpoint instead.
func runMCP(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var common commonFlags
	common.register(fs)
	source := fs.String("source", "", "knowledge directory or git URL to index and keep in sync")
	syncInterval := fs.Duration("sync-interval", -1, "how often to re-scan the source; 0 disables (default 60s)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd mcp [flags]\n\nServe the MCP tools over stdio (for Claude Desktop, Cursor, and MCP directory inspectors).\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	cfg, err := common.load()
	if err != nil {
		return err
	}
	if *source != "" {
		cfg.Source = *source
	}
	if *syncInterval >= 0 {
		cfg.Sync.Interval = *syncInterval
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	// stdout carries the protocol; nothing else may write to it.
	rt, err := buildProcess(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	defer rt.close()
	// Background sync must stop before the store closes.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rt.startSync(ctx, cfg)
	rt.logger.Info("briefd mcp on stdio", "db", cfg.DB, "source", cfg.Source, "git", rt.repo != nil, "proposals", rt.proposals.Available())
	if err := rt.mcpServer(cfg).Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		return fmt.Errorf("mcp over stdio: %w", err)
	}
	_ = stdout
	return nil
}
