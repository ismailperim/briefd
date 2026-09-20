// Command briefd is the entry point for the briefd context compiler.
//
// Subcommands: serve | index | eval | version. Only "version" is implemented
// at this stage; the others are added milestone by milestone (see PROMPTS.md).
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
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
  serve     Start the MCP + REST server
  index     Build or update the knowledge index
  eval      Run the retrieval quality evaluation
  version   Print version information
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the subcommand and returns the process exit code. It is kept
// free of os.Exit so it can be exercised directly from tests.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}

	switch cmd := args[0]; cmd {
	case "version":
		fmt.Fprintf(stdout, "briefd %s (commit %s, built %s, %s %s/%s)\n",
			version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "serve", "index", "eval":
		fmt.Fprintf(stderr, "briefd: %q is not implemented yet\n", cmd)
		return 1
	default:
		fmt.Fprintf(stderr, "briefd: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
}
