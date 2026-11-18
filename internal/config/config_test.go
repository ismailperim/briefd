package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestPortEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PORT", "9999")
	cfg, err := Load("")
	if err != nil || cfg.Listen != ":9999" {
		t.Errorf("PORT should set listen, got %q (%v)", cfg.Listen, err)
	}
	t.Setenv("BRIEFD_LISTEN", ":7000")
	cfg, _ = Load("")
	if cfg.Listen != ":7000" {
		t.Errorf("BRIEFD_LISTEN must win over PORT, got %q", cfg.Listen)
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

func TestJiraInheritsConfluenceServiceAccount(t *testing.T) {
	file := filepath.Join(t.TempDir(), "briefd.yaml")
	yaml := "confluence:\n  base_url: https://x.atlassian.net\n  email: svc@example.com\n  token: tok\njira:\n  base_url: https://x.atlassian.net\n  projects: [PAY]\n"
	if err := os.WriteFile(file, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIEFD_JIRA_FIELDS", "customfield_10035, customfield_10040")
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jira.Email != "svc@example.com" || cfg.Jira.Token != "tok" || !cfg.Jira.UserCredentials {
		t.Errorf("jira did not inherit the Confluence account: %+v", cfg.Jira)
	}
	if len(cfg.Jira.Fields) != 2 || cfg.Jira.Projects[0] != "PAY" {
		t.Errorf("jira fields/projects: %+v", cfg.Jira)
	}

	t.Setenv("BRIEFD_JIRA_EMAIL", "jira@example.com")
	t.Setenv("BRIEFD_JIRA_TOKEN", "jt")
	if cfg, err = Load(file); err != nil || cfg.Jira.Email != "jira@example.com" {
		t.Errorf("an explicit Jira account should win: %v %+v", err, cfg.Jira)
	}
}

func TestScopedAtlassianTokens(t *testing.T) {
	write := func(yaml string) string {
		t.Helper()
		file := filepath.Join(t.TempDir(), "briefd.yaml")
		if err := os.WriteFile(file, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		return file
	}
	const id = "11111111-2222-3333-4444-555555555555"

	// A scoped Confluence token is never shared with Jira.
	file := write("confluence:\n  base_url: https://x.atlassian.net\n  email: svc@example.com\n  token: tok\n  scoped_token: true\njira:\n  base_url: https://x.atlassian.net\n")
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jira.Token != "" || !cfg.Confluence.ScopedToken {
		t.Errorf("jira inherited a scoped Confluence token: %+v", cfg.Jira)
	}
	// ... so Jira without caller credentials has nobody to authenticate as.
	t.Setenv("BRIEFD_JIRA_USER_CREDENTIALS", "false")
	if _, err := Load(file); err == nil || !strings.Contains(err.Error(), "jira") {
		t.Errorf("want a jira auth error, got %v", err)
	}
	t.Setenv("BRIEFD_JIRA_USER_CREDENTIALS", "true")

	// The cloud ID reaches both products; env flags set scoped tokens.
	t.Setenv("BRIEFD_ATLASSIAN_CLOUD_ID", id)
	t.Setenv("BRIEFD_JIRA_SCOPED_TOKEN", "true")
	t.Setenv("BRIEFD_JIRA_EMAIL", "svc@example.com")
	t.Setenv("BRIEFD_JIRA_TOKEN", "jt")
	if cfg, err = Load(file); err != nil || cfg.Jira.CloudID != id || cfg.Confluence.CloudID != id || !cfg.Jira.ScopedToken {
		t.Errorf("cloud id / scoped flag: %v jira=%+v", err, cfg.Jira)
	}

	t.Setenv("BRIEFD_ATLASSIAN_CLOUD_ID", "../../evil")
	if _, err := Load(file); err == nil || !strings.Contains(err.Error(), "cloud_id") {
		t.Errorf("malformed cloud id accepted: %v", err)
	}
	t.Setenv("BRIEFD_ATLASSIAN_CLOUD_ID", id)
	t.Setenv("BRIEFD_JIRA_BASE_URL", "https://other.atlassian.net")
	if _, err := Load(file); err == nil || !strings.Contains(err.Error(), "different sites") {
		t.Errorf("one cloud id for two sites accepted: %v", err)
	}
}
