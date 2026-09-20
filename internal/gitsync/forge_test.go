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
	if _, err := NewForge(ForgeConfig{Type: "gitlab", Token: "x"}, ""); err == nil {
		t.Error("unsupported forge should error")
	}
}
