package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"sort"

	"github.com/ismailperim/briefd/internal/config"
	"github.com/ismailperim/briefd/internal/embed"
	"github.com/ismailperim/briefd/internal/embed/minilm"
)

func runModel(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	usage := "Usage: briefd model list | briefd model pull [--model NAME] [--dir DIR]\n\nManage the local embedding models (downloaded once, then used offline).\n"
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errUsage
	}
	switch args[0] {
	case "list":
		names := make([]string, 0, len(minilm.Specs))
		for n := range minilm.Specs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s := minilm.Specs[n]
			state := "not downloaded"
			if minilm.Present(s, embed.ModelDir(n)) {
				state = "ready"
			}
			def := ""
			if n == minilm.DefaultModel {
				def = " (default)"
			}
			fmt.Fprintf(stdout, "%-24s %d-dim, %d layers · %s · %s%s\n", n, s.Dim, s.Layers, s.Languages, state, def)
		}
		return nil
	case "pull":
		fs := flag.NewFlagSet("model pull", flag.ContinueOnError)
		fs.SetOutput(stderr)
		cfgFile := fs.String("config", envOr("CONFIG", ""), "config file (default briefd.yaml if present)")
		model := fs.String("model", "", "model name (default from config: embeddings.model)")
		dir := fs.String("dir", "", "target directory (default: embeddings.model_dir or the user cache)")
		if err := fs.Parse(args[1:]); err != nil {
			return errUsage
		}
		cfg, err := config.Load(*cfgFile)
		if err != nil {
			return err
		}
		name := cfg.Embeddings.Model
		if *model != "" {
			name = *model
		}
		spec, err := minilm.SpecFor(name)
		if err != nil {
			return err
		}
		target := cfg.Embeddings.ModelDir
		if *dir != "" {
			target = *dir
		}
		if target == "" || (*dir == "" && *model != "") {
			target = embed.ModelDir(spec.Name)
		}
		if minilm.Present(spec, target) {
			fmt.Fprintf(stdout, "%s already present in %s\n", spec.Name, target)
			return nil
		}
		if err := minilm.Pull(ctx, spec, target, "", logger); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s downloaded to %s\n", spec.Name, target)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return errUsage
	}
}
