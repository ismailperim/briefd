// Package config loads briefd settings with the precedence
// flags > BRIEFD_* environment > YAML file > defaults (CLAUDE.md).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ismailperim/briefd/internal/embed"
)

// Config is the fully resolved configuration.
type Config struct {
	// Listen is the HTTP address for MCP, REST and the dashboard.
	Listen string `yaml:"listen"`
	// DB is the SQLite database path.
	DB string `yaml:"db"`
	// Source is the knowledge repository: a local directory today, a git
	// URL once gitsync lands (M5).
	Source string `yaml:"source"`
	// APIToken protects /mcp and /api. Empty disables authentication.
	APIToken string  `yaml:"api_token"`
	LogLevel string  `yaml:"log_level"`
	Sync     Sync    `yaml:"sync"`
	Search   Search  `yaml:"search"`
	Metrics  Metrics `yaml:"metrics"`
	// Embeddings selects the vector adapter (SPEC §7, ADR-0003).
	Embeddings embed.Config `yaml:"embeddings"`
}

// Metrics controls the Prometheus endpoint.
type Metrics struct {
	// RequireAuth puts GET /metrics behind the bearer token. Off by default
	// so scrapers work without secrets; the endpoint exposes counts only.
	RequireAuth bool `yaml:"require_auth"`
}

// Sync controls how often the source is re-scanned.
type Sync struct {
	// Interval between scans; 0 disables periodic sync (index once at start).
	Interval time.Duration `yaml:"interval"`
}

// Search holds retrieval defaults.
type Search struct {
	DefaultScopes []string `yaml:"default_scopes"`
	// DefaultMaxTokens applies when a request omits max_tokens.
	DefaultMaxTokens int `yaml:"default_max_tokens"`
	// MaxTopK caps top_k regardless of what a client asks for.
	MaxTopK int `yaml:"max_top_k"`
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Listen:   ":7788",
		DB:       "briefd.db",
		Source:   "",
		LogLevel: "info",
		Sync:     Sync{Interval: 60 * time.Second},
		Search: Search{
			DefaultScopes:    []string{"domain", "conventions"},
			DefaultMaxTokens: 2000,
			MaxTopK:          50,
		},
		Embeddings: embed.Default(),
	}
}

// DefaultFile is the config file looked up when none is given.
const DefaultFile = "briefd.yaml"

// Load resolves the configuration from file (may be "" for none/optional
// default) and the environment. Flags are applied by the caller afterwards.
func Load(file string) (Config, error) {
	cfg := Default()
	path, required := file, true
	if path == "" {
		path, required = DefaultFile, false
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parsing %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist) && !required:
		// no file, use defaults
	default:
		return cfg, fmt.Errorf("reading config %s: %w", path, err)
	}
	if err := cfg.applyEnv(); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

// applyEnv overlays BRIEFD_* variables.
func (c *Config) applyEnv() error {
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv("BRIEFD_" + key); ok {
			*dst = v
		}
	}
	str("LISTEN", &c.Listen)
	str("DB", &c.DB)
	str("SOURCE", &c.Source)
	str("API_TOKEN", &c.APIToken)
	str("LOG_LEVEL", &c.LogLevel)
	if v, ok := os.LookupEnv("BRIEFD_SYNC_INTERVAL"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("BRIEFD_SYNC_INTERVAL: %w", err)
		}
		c.Sync.Interval = d
	}
	if v, ok := os.LookupEnv("BRIEFD_DEFAULT_SCOPES"); ok {
		c.Search.DefaultScopes = splitList(v)
	}
	if v, ok := os.LookupEnv("BRIEFD_DEFAULT_MAX_TOKENS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("BRIEFD_DEFAULT_MAX_TOKENS: %w", err)
		}
		c.Search.DefaultMaxTokens = n
	}
	str("EMBEDDINGS_PROVIDER", &c.Embeddings.Provider)
	str("EMBEDDINGS_MODEL", &c.Embeddings.Model)
	str("EMBEDDINGS_MODEL_DIR", &c.Embeddings.ModelDir)
	str("EMBEDDINGS_URL", &c.Embeddings.URL)
	str("EMBEDDINGS_API_KEY", &c.Embeddings.APIKey)
	if v, ok := os.LookupEnv("BRIEFD_EMBEDDINGS_ENABLED"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("BRIEFD_EMBEDDINGS_ENABLED: %w", err)
		}
		c.Embeddings.Enabled = b
	}
	if v, ok := os.LookupEnv("BRIEFD_EMBEDDINGS_AUTO_DOWNLOAD"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("BRIEFD_EMBEDDINGS_AUTO_DOWNLOAD: %w", err)
		}
		c.Embeddings.AutoDownload = b
	}
	return nil
}

// Validate checks invariants after all layers are applied.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("config: listen must not be empty")
	}
	if c.DB == "" {
		return errors.New("config: db must not be empty")
	}
	if c.Sync.Interval < 0 {
		return errors.New("config: sync.interval must be >= 0")
	}
	if c.Search.DefaultMaxTokens <= 0 {
		return errors.New("config: search.default_max_tokens must be > 0")
	}
	if c.Search.MaxTopK <= 0 {
		return errors.New("config: search.max_top_k must be > 0")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: unknown log_level %q", c.LogLevel)
	}
	switch c.Embeddings.Provider {
	case "", "local", "ollama", "openai", "openai-compatible", "none":
	default:
		return fmt.Errorf("config: unknown embeddings.provider %q", c.Embeddings.Provider)
	}
	if c.Embeddings.BatchSize < 0 {
		return errors.New("config: embeddings.batch_size must be >= 0")
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
