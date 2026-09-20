package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/ismailperim/briefd/internal/config"
	"github.com/ismailperim/briefd/internal/embed"
)

// commonFlags are accepted by every command that touches the index.
type commonFlags struct {
	cfgFile    string
	db         string
	embeddings string
	model      string
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.cfgFile, "config", envOr("CONFIG", ""), "config file (default briefd.yaml if present)")
	fs.StringVar(&c.db, "db", "", "SQLite database path (default briefd.db)")
	fs.StringVar(&c.embeddings, "embeddings", "", "embedding provider: local | ollama | openai | none (default from config)")
	fs.StringVar(&c.model, "model", "", "embedding model, e.g. multilingual-e5-small (default from config)")
}

// load resolves the configuration and applies the common flag overrides.
func (c *commonFlags) load() (config.Config, error) {
	cfg, err := config.Load(c.cfgFile)
	if err != nil {
		return cfg, err
	}
	if c.db != "" {
		cfg.DB = c.db
	}
	if c.embeddings != "" {
		cfg.Embeddings.Provider = c.embeddings
		cfg.Embeddings.Enabled = c.embeddings != "none"
	}
	if c.model != "" {
		cfg.Embeddings.Model = c.model
	}
	return cfg, cfg.Validate()
}

// openEmbedder constructs the configured embedder, returning nil for
// BM25-only mode. Local model weights are downloaded on first use.
func openEmbedder(ctx context.Context, cfg config.Config, logger *slog.Logger, stderr io.Writer) (embed.Embedder, error) {
	if !cfg.Embeddings.Enabled || cfg.Embeddings.Provider == "none" {
		return nil, nil
	}
	e, err := embed.New(ctx, cfg.Embeddings, logger)
	if err != nil {
		return nil, fmt.Errorf("embeddings (use --embeddings none for BM25-only): %w", err)
	}
	if e != nil {
		fmt.Fprintf(stderr, "embeddings: %s\n", e.Name())
	}
	return e, nil
}
