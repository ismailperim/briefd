// Package embed defines the Embedder interface and constructs the configured
// adapter: the pure-Go MiniLM encoder (default), Ollama, an OpenAI-compatible
// endpoint, or none (BM25-only).
package embed

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ismailperim/briefd/internal/embed/minilm"
	"github.com/ismailperim/briefd/internal/embed/remote"
)

// Embedder turns texts into L2-normalised float32 vectors.
type Embedder interface {
	// Name identifies the model; it is stored with every vector so a model
	// change invalidates them (SPEC §7). Example: "local/all-MiniLM-L6-v2".
	Name() string
	// Dim is the vector length.
	Dim() int
	// Embed encodes texts in order. Implementations may batch internally.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Config selects and configures the adapter.
type Config struct {
	// Enabled false forces BM25-only mode regardless of Provider.
	Enabled bool `yaml:"enabled"`
	// Provider is local | ollama | openai | none.
	Provider string `yaml:"provider"`
	// Model is the remote model name (ollama/openai). Ignored by local.
	Model string `yaml:"model"`
	// ModelDir holds the local model files. Default: <user cache>/briefd/models/all-MiniLM-L6-v2.
	ModelDir string `yaml:"model_dir"`
	// AutoDownload lets the local adapter fetch missing weights.
	AutoDownload bool `yaml:"auto_download"`
	// URL is the base URL for ollama (http://localhost:11434) or an
	// OpenAI-compatible server (https://api.openai.com/v1).
	URL string `yaml:"url"`
	// APIKey is sent as a bearer token to OpenAI-compatible servers.
	// Can also be set with BRIEFD_EMBEDDINGS_API_KEY.
	APIKey string `yaml:"api_key"`
	// BatchSize is how many chunks are embedded per call during indexing.
	BatchSize int `yaml:"batch_size"`
}

// Default returns the default embedding configuration.
func Default() Config {
	return Config{
		Enabled:      true,
		Provider:     "local",
		ModelDir:     DefaultModelDir(),
		AutoDownload: true,
		BatchSize:    16,
	}
}

// DefaultModelDir is $XDG_CACHE_HOME/briefd/models/all-MiniLM-L6-v2 (or the
// OS equivalent), falling back to ./models/all-MiniLM-L6-v2.
func DefaultModelDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = "."
		return filepath.Join(base, "models", minilm.ModelName)
	}
	return filepath.Join(base, "briefd", "models", minilm.ModelName)
}

// New constructs the configured Embedder. It returns (nil, nil) when
// embeddings are disabled so callers can fall back to BM25-only mode.
func New(ctx context.Context, cfg Config, logger *slog.Logger) (Embedder, error) {
	if !cfg.Enabled || cfg.Provider == "none" {
		return nil, nil
	}
	switch cfg.Provider {
	case "", "local":
		dir := cfg.ModelDir
		if dir == "" {
			dir = DefaultModelDir()
		}
		m, err := minilm.LoadOrPull(ctx, dir, cfg.AutoDownload, "", logger)
		if err != nil {
			return nil, fmt.Errorf("local embeddings: %w", err)
		}
		return localEmbedder{m}, nil
	case "ollama":
		url := cfg.URL
		if url == "" {
			url = "http://localhost:11434"
		}
		model := cfg.Model
		if model == "" {
			model = "nomic-embed-text"
		}
		return remote.NewOllama(url, model), nil
	case "openai", "openai-compatible":
		url := cfg.URL
		if url == "" {
			url = "https://api.openai.com/v1"
		}
		model := cfg.Model
		if model == "" {
			model = "text-embedding-3-small"
		}
		key := cfg.APIKey
		if key == "" {
			key = os.Getenv("BRIEFD_EMBEDDINGS_API_KEY")
		}
		return remote.NewOpenAI(url, model, key), nil
	default:
		return nil, fmt.Errorf("embeddings: unknown provider %q (want local, ollama, openai or none)", cfg.Provider)
	}
}

type localEmbedder struct{ m *minilm.Model }

// Name implements Embedder.
func (localEmbedder) Name() string { return "local/" + minilm.ModelName }

// Dim implements Embedder.
func (localEmbedder) Dim() int { return minilm.Dim }

// Embed implements Embedder.
func (l localEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return l.m.Embed(ctx, texts)
}
