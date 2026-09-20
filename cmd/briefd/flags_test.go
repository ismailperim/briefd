package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseInterleaved(t *testing.T) {
	tests := []struct {
		args     []string
		wantPos  string
		wantTopK int
		wantErr  bool
	}{
		{[]string{"retry", "policy"}, "retry policy", 8, false},
		{[]string{"--top-k", "4", "retry", "policy"}, "retry policy", 4, false},
		{[]string{"retry", "policy", "--top-k", "4"}, "retry policy", 4, false},
		{[]string{"retry", "--top-k=4", "policy"}, "retry policy", 4, false},
		{[]string{"retry", "--", "--not-a-flag"}, "retry --not-a-flag", 8, false},
		{[]string{"retry", "--bogus"}, "", 0, true},
		{nil, "", 8, false},
	}
	for _, tt := range tests {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		topK := fs.Int("top-k", 8, "")
		pos, err := parseInterleaved(fs, tt.args)
		if (err != nil) != tt.wantErr {
			t.Errorf("%v: err = %v, wantErr %v", tt.args, err, tt.wantErr)
			continue
		}
		if tt.wantErr {
			continue
		}
		if got := strings.Join(pos, " "); got != tt.wantPos || *topK != tt.wantTopK {
			t.Errorf("%v: positional = %q topK = %d, want %q %d", tt.args, got, *topK, tt.wantPos, tt.wantTopK)
		}
	}
}
