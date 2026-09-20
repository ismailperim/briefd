// Command briefd is the entry point for the briefd context compiler.
//
// Subcommands: serve | index | search | eval | version. They are added
// milestone by milestone (see PROMPTS.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

// Build metadata. Overridden at link time via -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usageText = `briefd — your agents, briefed. Not flooded.

Usage:
  briefd <command> [flags]

Commands:
  init      Create a starter knowledge repository layout
  serve     Start the MCP + REST server
  index     Build or update the knowledge index from a directory
  search    Query the index from the command line
  model     Manage local embedding models (model list | model pull)
  eval      Run the retrieval quality evaluation
  bench     Measure knowledge tokens per task: static CLAUDE.md vs compile_bundle
  version   Print version information

Run "briefd <command> -h" for command flags. Flags can also be set through
BRIEFD_* environment variables (e.g. BRIEFD_DB, BRIEFD_SOURCE).
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches the subcommand and returns the process exit code. It is kept
// free of os.Exit so it can be exercised directly from tests.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	// One-shot commands print their own summary, so logs default to warn;
	// the long-running server defaults to info. Model downloads and
	// embedding progress are always worth showing.
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: logLevel("info")}))

	var err error
	switch cmd := args[0]; cmd {
	case "version":
		fmt.Fprintf(stdout, "briefd %s (commit %s, built %s, %s %s/%s)\n",
			version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "init":
		err = runInit(args[1:], stdout, stderr)
	case "index":
		err = runIndex(ctx, args[1:], stdout, stderr, logger)
	case "search":
		err = runSearch(ctx, args[1:], stdout, stderr, logger)
	case "model":
		err = runModel(ctx, args[1:], stdout, stderr, logger)
	case "serve":
		err = runServe(ctx, args[1:], stdout, stderr)
	case "eval":
		err = runEval(ctx, args[1:], stdout, stderr, logger)
	case "bench":
		err = runBench(ctx, args[1:], stdout, stderr, logger)
	default:
		fmt.Fprintf(stderr, "briefd: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
	if err != nil {
		if errors.Is(err, errUsage) {
			return 2
		}
		fmt.Fprintf(stderr, "briefd: %v\n", err)
		return 1
	}
	return 0
}

// envOr returns the BRIEFD_<name> environment variable or def. Flags parsed
// afterwards take precedence, matching the precedence in CLAUDE.md.
func envOr(name, def string) string {
	if v, ok := os.LookupEnv("BRIEFD_" + name); ok {
		return v
	}
	return def
}

func logLevel(def string) slog.Level {
	return parseLevel(envOr("LOG_LEVEL", def))
}
