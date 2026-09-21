package gitsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ForgeConfig enables pull request creation for proposals.
type ForgeConfig struct {
	// Type is "github" (the only forge supported in v0.1) or "".
	Type string `yaml:"type"`
	// Token with permission to open pull requests.
	Token string `yaml:"token"`
	// Repo as "owner/name"; derived from the git URL when empty.
	Repo string `yaml:"repo"`
	// APIURL for GitHub Enterprise; default https://api.github.com.
	APIURL string `yaml:"api_url"`
}

// Forge opens pull requests.
type Forge struct {
	cfg    ForgeConfig
	client *http.Client
}

// NewForge returns nil when no forge is configured.
func NewForge(cfg ForgeConfig, gitURL string) (*Forge, error) {
	if cfg.Type == "" {
		return nil, nil
	}
	if cfg.Type != "github" {
		return nil, fmt.Errorf("forge: unsupported type %q (only github is supported)", cfg.Type)
	}
	if cfg.Token == "" {
		return nil, errors.New("forge: token is required")
	}
	if cfg.Repo == "" {
		cfg.Repo = GitHubRepoFromURL(gitURL)
		if cfg.Repo == "" {
			return nil, fmt.Errorf("forge: cannot derive owner/name from %q; set forge.repo", gitURL)
		}
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com"
	}
	return &Forge{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

var githubURL = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)

// GitHubRepoFromURL extracts "owner/name" from an HTTPS or SSH GitHub URL.
func GitHubRepoFromURL(u string) string {
	m := githubURL.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

// OpenPullRequest creates a PR from head into base and returns its URL.
func (f *Forge) OpenPullRequest(ctx context.Context, title, head, base, body string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"title": title, "head": head, "base": base, "body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.cfg.APIURL+"/repos/"+f.cfg.Repo+"/pulls", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+f.cfg.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "briefd")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("forge: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return "", fmt.Errorf("forge: %s: %s", resp.Status, msg)
	}
	var pr struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(data, &pr); err != nil {
		return "", fmt.Errorf("forge: decoding response: %w", err)
	}
	return pr.HTMLURL, nil
}

// PullRequestStatus returns open, merged, or closed for a stored GitHub pull request URL.
func (f *Forge) PullRequestStatus(ctx context.Context, pullURL string) (string, error) {
	u, err := url.Parse(pullURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("forge: invalid pull request URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	repo := strings.Split(f.cfg.Repo, "/")
	if len(repo) != 2 || repo[0] == "" || repo[1] == "" {
		return "", errors.New("forge: repository must be owner/name")
	}
	if len(parts) != 4 || parts[0] != repo[0] || parts[1] != repo[1] || parts[2] != "pull" {
		return "", errors.New("forge: pull request URL does not match the configured repository")
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 {
		return "", errors.New("forge: pull request URL has an invalid pull number")
	}
	endpoint := strings.TrimRight(f.cfg.APIURL, "/") + "/repos/" +
		url.PathEscape(repo[0]) + "/" + url.PathEscape(repo[1]) + "/pulls/" + strconv.Itoa(number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("forge: creating pull request status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.cfg.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "briefd")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("forge: checking pull request status: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return "", fmt.Errorf("forge: checking pull request status: %s: %s", resp.Status, msg)
	}
	var pull struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
	}
	if err := json.Unmarshal(data, &pull); err != nil {
		return "", fmt.Errorf("forge: decoding pull request status: %w", err)
	}
	if pull.Merged {
		return "merged", nil
	}
	if pull.State == "open" || pull.State == "closed" {
		return pull.State, nil
	}
	return "", fmt.Errorf("forge: unexpected pull request state %q", pull.State)
}
