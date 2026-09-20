package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/ismailperim/briefd/internal/config"
	"github.com/ismailperim/briefd/internal/embed"
	"github.com/ismailperim/briefd/internal/embed/minilm"
)

func runModel(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	if len(args) == 0 || args[0] != "pull" {
		fmt.Fprint(stderr, "Usage: briefd model pull [--dir DIR]\n\nDownload the local embedding model (all-MiniLM-L6-v2) so indexing works offline.\n")
		return errUsage
	}
	fs := flag.NewFlagSet("model pull", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgFile := fs.String("config", envOr("CONFIG", ""), "config file (default briefd.yaml if present)")
	dir := fs.String("dir", "", "target directory (default from config: embeddings.model_dir)")
	if err := fs.Parse(args[1:]); err != nil {
		return errUsage
	}
	cfg, err := config.Load(*cfgFile)
	if err != nil {
		return err
	}
	target := cfg.Embeddings.ModelDir
	if *dir != "" {
		target = *dir
	}
	if target == "" {
		target = embed.DefaultModelDir()
	}
	if minilm.Present(target) {
		fmt.Fprintf(stdout, "model already present in %s\n", target)
		return nil
	}
	if err := minilm.Pull(ctx, target, "", logger); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "model downloaded to %s\n", target)
	return nil
}
