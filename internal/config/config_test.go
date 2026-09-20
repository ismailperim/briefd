package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "briefd.yaml")
	if err := os.WriteFile(file, []byte("listen: \":9000\"\ndb: from-yaml.db\nsync:\n  interval: 5m\nsearch:\n  default_scopes: [domain]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIEFD_DB", "from-env.db")
	t.Setenv("BRIEFD_SYNC_INTERVAL", "30s")
	t.Setenv("BRIEFD_DEFAULT_SCOPES", "domain, projects/x")

	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" {
		t.Errorf("Listen = %q, want yaml value", cfg.Listen)
	}
	if cfg.DB != "from-env.db" {
		t.Errorf("DB = %q, want env to override yaml", cfg.DB)
	}
	if cfg.Sync.Interval != 30*time.Second {
		t.Errorf("Sync.Interval = %v, want env to override yaml", cfg.Sync.Interval)
	}
	if len(cfg.Search.DefaultScopes) != 2 || cfg.Search.DefaultScopes[1] != "projects/x" {
		t.Errorf("DefaultScopes = %v", cfg.Search.DefaultScopes)
	}
	if cfg.Search.DefaultMaxTokens != 2000 || cfg.LogLevel != "info" {
		t.Errorf("defaults not preserved: %+v", cfg)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := Load(""); err != nil {
		t.Errorf("optional default file should not error: %v", err)
	}
	if _, err := Load("nope.yaml"); err == nil {
		t.Error("explicit missing file should error")
	}
}

func TestValidate(t *testing.T) {
	cfg := Default()
	cfg.LogLevel = "loud"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for bad log level")
	}
	cfg = Default()
	cfg.Sync.Interval = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative interval")
	}
}
