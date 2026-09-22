package gitsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubRepoFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/ismailperim/briefd.git": "ismailperim/briefd",
		"https://github.com/ismailperim/briefd":     "ismailperim/briefd",
		"git@github.com:ismailperim/briefd.git":     "ismailperim/briefd",
		"https://gitlab.com/o/r.git":                "",
	} {
		if got := GitHubRepoFromURL(in); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}

func TestOpenPullRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["head"] != "briefd/proposal-1" || body["base"] != "main" {
			w.WriteHeader(422)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/o/r/pull/7"}`))
	}))
	defer srv.Close()
	f, err := NewForge(ForgeConfig{Type: "github", Token: "tok", APIURL: srv.URL}, "https://github.com/o/r.git")
	if err != nil {
		t.Fatal(err)
	}
	url, err := f.OpenPullRequest(context.Background(), "t", "briefd/proposal-1", "main", "b")
	if err != nil || url != "https://github.com/o/r/pull/7" {
		t.Errorf("url=%s err=%v", url, err)
	}
	if f, err := NewForge(ForgeConfig{}, ""); f != nil || err != nil {
		t.Error("empty config should yield nil forge")
	}
	if _, err := NewForge(ForgeConfig{Type: "gitea", Token: "x"}, ""); err == nil {
		t.Error("unsupported forge should error")
	}
}

func TestPullRequestStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status string
	}{
		{name: "open", body: `{"state":"open","merged":false}`, status: "open"},
		{name: "closed", body: `{"state":"closed","merged":false}`, status: "closed"},
		{name: "merged", body: `{"state":"closed","merged":true}`, status: "merged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/o/r/pulls/7" || r.Header.Get("Authorization") != "Bearer tok" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			f, err := NewForge(ForgeConfig{Type: "github", Token: "tok", APIURL: srv.URL, Repo: "o/r"}, "")
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.PullRequestStatus(context.Background(), "https://github.com/o/r/pull/7")
			if err != nil || got != tc.status {
				t.Fatalf("status = %q, %v; want %q", got, err, tc.status)
			}
		})
	}
}

func TestPullRequestStatusRejectsForeignRepositoryURL(t *testing.T) {
	f, err := NewForge(ForgeConfig{Type: "github", Token: "tok", Repo: "o/r", APIURL: "https://api.github.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.PullRequestStatus(context.Background(), "https://github.com/other/repo/pull/7"); err == nil {
		t.Fatal("foreign repository URL should be rejected")
	}
}

func TestGitLabProjectFromURL(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://gitlab.com/team/knowledge.git":               {"gitlab.com", "team/knowledge"},
		"https://gitlab.example.com/grp/sub/knowledge":        {"gitlab.example.com", "grp/sub/knowledge"},
		"git@gitlab.example.com:grp/sub/knowledge.git":        {"gitlab.example.com", "grp/sub/knowledge"},
		"ssh://git@gitlab.example.com:2222/grp/knowledge.git": {"gitlab.example.com", "grp/knowledge"},
		"https://gitlab.com/knowledge.git":                    {"gitlab.com", ""},
		"":                                                    {"", ""},
	} {
		host, path := gitLabProjectFromURL(in)
		if host != want[0] || path != want[1] {
			t.Errorf("%q -> %q %q, want %q %q", in, host, path, want[0], want[1])
		}
	}
}

func TestGitLabMergeRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v4/projects/grp%2Fsub%2Fknowledge/merge_requests":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["source_branch"] != "briefd/proposal-1" || body["target_branch"] != "main" || body["title"] == "" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"iid":7,"web_url":"https://gitlab.example.com/grp/sub/knowledge/-/merge_requests/7"}`))
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v4/projects/grp%2Fsub%2Fknowledge/merge_requests/7":
			_, _ = w.Write([]byte(`{"state":"merged"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	f, err := NewForge(ForgeConfig{Type: "gitlab", Token: "tok", APIURL: srv.URL + "/api/v4/"}, "git@gitlab.example.com:grp/sub/knowledge.git")
	if err != nil {
		t.Fatal(err)
	}
	url, err := f.OpenPullRequest(context.Background(), "t", "briefd/proposal-1", "main", "b")
	if err != nil || url != "https://gitlab.example.com/grp/sub/knowledge/-/merge_requests/7" {
		t.Fatalf("url=%s err=%v", url, err)
	}
	status, err := f.PullRequestStatus(context.Background(), url)
	if err != nil || status != "merged" {
		t.Errorf("status=%s err=%v", status, err)
	}
	if _, err := f.PullRequestStatus(context.Background(), "https://gitlab.example.com/other/proj/-/merge_requests/7"); err == nil {
		t.Error("foreign project URL should be rejected")
	}
	// Default API URL follows the git host.
	f2, err := NewForge(ForgeConfig{Type: "gitlab", Token: "tok"}, "https://gitlab.example.com/grp/knowledge.git")
	if err != nil || f2.cfg.APIURL != "https://gitlab.example.com/api/v4" || f2.cfg.Repo != "grp/knowledge" {
		t.Errorf("defaults: %+v err=%v", f2.cfg, err)
	}
}
