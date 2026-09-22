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

// ForgeConfig enables pull / merge request creation for proposals.
type ForgeConfig struct {
	// Type is "github", "gitlab" or "" (no forge).
	Type string `yaml:"type"`
	// Token with permission to open pull requests (GitHub) or merge
	// requests (GitLab: a project/group token with the api scope).
	Token string `yaml:"token"`
	// Repo as "owner/name" (GitHub) or the full project path
	// "group/subgroup/name" (GitLab); derived from the git URL when empty.
	Repo string `yaml:"repo"`
	// APIURL for self-hosted instances: default https://api.github.com or,
	// for GitLab, https://<host of the git URL>/api/v4.
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
	if cfg.Token == "" {
		return nil, errors.New("forge: token is required")
	}
	switch cfg.Type {
	case "github":
		if cfg.Repo == "" {
			cfg.Repo = GitHubRepoFromURL(gitURL)
			if cfg.Repo == "" {
				return nil, fmt.Errorf("forge: cannot derive owner/name from %q; set forge.repo", gitURL)
			}
		}
		if cfg.APIURL == "" {
			cfg.APIURL = "https://api.github.com"
		}
	case "gitlab":
		host, path := gitLabProjectFromURL(gitURL)
		if cfg.Repo == "" {
			if path == "" {
				return nil, fmt.Errorf("forge: cannot derive the project path from %q; set forge.repo", gitURL)
			}
			cfg.Repo = path
		}
		if cfg.APIURL == "" {
			if host == "" {
				host = "gitlab.com"
			}
			cfg.APIURL = "https://" + host + "/api/v4"
		}
	default:
		return nil, fmt.Errorf("forge: unsupported type %q (github or gitlab)", cfg.Type)
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	return &Forge{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

// gitLabProjectFromURL extracts the host and the full project path
// ("group/subgroup/name") from an HTTPS or SSH GitLab-style URL. It accepts
// any host, since most GitLab instances are self-hosted.
func gitLabProjectFromURL(u string) (host, path string) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", ""
	}
	if strings.Contains(u, "://") {
		// https://host/path or ssh://user@host:port/path
		parsed, err := url.Parse(u)
		if err != nil || parsed.Host == "" {
			return "", ""
		}
		host, path = parsed.Hostname(), strings.Trim(parsed.Path, "/")
	} else if m := scpURL.FindStringSubmatch(u); m != nil {
		// user@host:path (scp-like syntax)
		host, path = m[1], m[2]
	} else {
		return "", ""
	}
	path = strings.TrimSuffix(path, ".git")
	if strings.Count(path, "/") < 1 {
		return host, ""
	}
	return host, path
}

var scpURL = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+?)/?$`)

// OpenPullRequest creates a pull request (GitHub) or merge request (GitLab)
// from head into base and returns its URL.
func (f *Forge) OpenPullRequest(ctx context.Context, title, head, base, body string) (string, error) {
	if f.cfg.Type == "gitlab" {
		return f.openMergeRequest(ctx, title, head, base, body)
	}
	return f.openGitHubPullRequest(ctx, title, head, base, body)
}

func (f *Forge) openMergeRequest(ctx context.Context, title, head, base, body string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"source_branch": head, "target_branch": base, "title": title, "description": body})
	endpoint := f.cfg.APIURL + "/projects/" + url.PathEscape(f.cfg.Repo) + "/merge_requests"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	f.gitLabHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("forge: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("forge: %s: %s", resp.Status, trimMessage(data))
	}
	var mr struct {
		WebURL string `json:"web_url"`
	}
	if err := json.Unmarshal(data, &mr); err != nil {
		return "", fmt.Errorf("forge: decoding response: %w", err)
	}
	return mr.WebURL, nil
}

func (f *Forge) gitLabHeaders(req *http.Request) {
	req.Header.Set("PRIVATE-TOKEN", f.cfg.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "briefd")
}

func trimMessage(data []byte) string {
	msg := strings.TrimSpace(string(data))
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
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

func (f *Forge) openGitHubPullRequest(ctx context.Context, title, head, base, body string) (string, error) {
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

// PullRequestStatus returns open, merged, or closed for a stored pull /
// merge request URL.
func (f *Forge) PullRequestStatus(ctx context.Context, pullURL string) (string, error) {
	if f.cfg.Type == "gitlab" {
		return f.mergeRequestStatus(ctx, pullURL)
	}
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

// mergeRequestStatus maps a GitLab merge request's state (opened, locked,
// merged, closed) onto open / merged / closed.
func (f *Forge) mergeRequestStatus(ctx context.Context, mrURL string) (string, error) {
	u, err := url.Parse(mrURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("forge: invalid merge request URL")
	}
	// https://host/group/sub/name/-/merge_requests/7
	path := strings.Trim(u.Path, "/")
	i := strings.LastIndex(path, "/-/merge_requests/")
	if i < 0 || path[:i] != f.cfg.Repo {
		return "", errors.New("forge: merge request URL does not match the configured project")
	}
	iid, err := strconv.Atoi(path[i+len("/-/merge_requests/"):])
	if err != nil || iid < 1 {
		return "", errors.New("forge: merge request URL has an invalid iid")
	}
	endpoint := f.cfg.APIURL + "/projects/" + url.PathEscape(f.cfg.Repo) + "/merge_requests/" + strconv.Itoa(iid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("forge: creating merge request status request: %w", err)
	}
	f.gitLabHeaders(req)
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("forge: checking merge request status: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("forge: checking merge request status: %s: %s", resp.Status, trimMessage(data))
	}
	var mr struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(data, &mr); err != nil {
		return "", fmt.Errorf("forge: decoding merge request status: %w", err)
	}
	switch mr.State {
	case "opened", "locked":
		return "open", nil
	case "merged", "closed":
		return mr.State, nil
	}
	return "", fmt.Errorf("forge: unexpected merge request state %q", mr.State)
}
