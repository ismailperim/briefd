package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args prints usage", args: nil, wantCode: 2, wantStderr: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "briefd dev"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "eval bad flag", args: []string{"eval", "--nope"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "unknown command", args: []string{"bogus"}, wantCode: 2, wantStderr: `unknown command "bogus"`},
		{name: "index bad flag", args: []string{"index", "--nope"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "search without query", args: []string{"search"}, wantCode: 2, wantStderr: "Usage: briefd search"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(context.Background(), tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:tok@git.example.com/team/kb.git": "https://git.example.com/team/kb.git",
		"ssh://git@host:22/p/_git/kb":                  "ssh://host:22/p/_git/kb",
		"https://github.com/o/r.git":                   "https://github.com/o/r.git",
		"https://host/path/with@sign":                  "https://host/path/with@sign",
		"/srv/knowledge":                               "/srv/knowledge",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
