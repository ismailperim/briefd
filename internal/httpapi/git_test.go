package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/httpapi"
	"github.com/ismailperim/briefd/internal/indexer"
	"github.com/ismailperim/briefd/internal/mcpserver"
	"github.com/ismailperim/briefd/internal/proposal"
	"github.com/ismailperim/briefd/internal/search"
	"github.com/ismailperim/briefd/internal/store"
)

// A bare origin plus an author clone that pushes to it.
func gitFixture(t *testing.T) (bare string, push func(path, content string)) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "author")
	repo, err := git.PlainInit(work, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	push = func(path, content string) {
		t.Helper()
		full := filepath.Join(work, filepath.FromSlash(path))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(path); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit("edit "+path, &git.CommitOptions{Author: &object.Signature{Name: "a", Email: "a@x", When: time.Now()}}); err != nil {
			t.Fatal(err)
		}
		if err := repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/master:refs/heads/main"}}); err != nil {
			t.Fatal(err)
		}
	}
	return bare, push
}

func TestGitSourceProposalsAndWebhook(t *testing.T) {
	ctx := context.Background()
	bare, push := gitFixture(t)
	push("domain/glossary.md", "# Glossary\n\n## Payout\n\nA transfer to the merchant.\n")

	dir := filepath.Join(t.TempDir(), "checkout")
	repo, err := gitsync.Open(ctx, gitsync.Config{URL: bare, Dir: dir, Branch: "main", AuthorName: "briefd", AuthorEmail: "b@x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reindex := func() {
		t.Helper()
		head, _, err := repo.Sync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := indexer.Run(ctx, st, indexer.Options{Root: repo.Dir(), Commit: head}); err != nil {
			t.Fatal(err)
		}
	}
	reindex()

	var webhooks atomic.Int32
	searcher := search.New(st, search.Options{})
	props := &proposal.Service{Repo: repo, Store: st}
	mcpSrv := mcpserver.New(mcpserver.Deps{Store: st, Searcher: searcher, Proposals: props, Version: "test"})
	h := httpapi.New(httpapi.Deps{
		Store: st, Searcher: searcher, Proposals: props, MCP: mcpserver.Handler(mcpSrv, nil),
		WebhookSecret: "hook-secret", OnWebhook: func() { webhooks.Add(1) }, Version: "test",
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	session := connect(t, srv, "")

	// propose_update creates and pushes a branch; main is untouched.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "propose_update", Arguments: map[string]any{
		"doc_path": "domain/glossary.md", "change_description": "add chargeback term",
		"new_content": "# Glossary\n\n## Payout\n\nA transfer to the merchant.\n\n## Chargeback\n\nA forced reversal.\n",
	}})
	if err != nil || res.IsError {
		t.Fatalf("propose_update: err=%v res=%+v", err, res)
	}
	var out mcpserver.ProposeUpdateOutput
	raw, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(raw, &out)
	if !strings.HasPrefix(out.Branch, "briefd/proposal-") || !out.Pushed || out.PRURL != "" {
		t.Errorf("proposal = %+v", out)
	}
	origin, _ := git.PlainOpen(bare)
	if _, err := origin.Reference(plumbing.NewBranchReferenceName(out.Branch), true); err != nil {
		t.Errorf("branch not on origin: %v", err)
	}
	// The index still serves the old content until the change is merged.
	sr, _ := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_context", Arguments: map[string]any{"query": "chargeback reversal"}})
	if strings.Contains(sr.Content[0].(*mcp.TextContent).Text, "forced reversal") {
		t.Error("proposal content leaked into the index before merge")
	}
	resp, err := http.Get(srv.URL + "/api/proposals")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Proposals []store.Proposal `json:"proposals"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Proposals) != 1 || list.Proposals[0].Branch != out.Branch {
		t.Errorf("proposals list = %+v", list)
	}
	// Invalid paths are rejected.
	bad, _ := session.CallTool(ctx, &mcp.CallToolParams{Name: "propose_update", Arguments: map[string]any{
		"doc_path": "../secrets.md", "change_description": "x", "new_content": "y"}})
	if !bad.IsError {
		t.Error("expected error for path outside the knowledge layout")
	}

	// "Merge": push new content upstream; sync + reindex makes it visible.
	push("domain/glossary.md", "# Glossary\n\n## Payout\n\nA transfer to the merchant.\n\n## Chargeback\n\nA forced reversal.\n")
	reindex()
	sr, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "search_context", Arguments: map[string]any{"query": "chargeback reversal"}})
	if !strings.Contains(sr.Content[0].(*mcp.TextContent).Text, "forced reversal") {
		t.Error("merged content not served after sync")
	}
	sync, _ := st.GetSyncState(ctx)
	head, _ := repo.Head()
	if sync.LastCommit != head || len(head) != 40 {
		t.Errorf("sync_state commit = %s, head = %s", sync.LastCommit, head)
	}

	// Webhook: bad signature rejected, good signature triggers OnWebhook.
	body := `{"ref":"refs/heads/main"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/webhook/git", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad signature: status %d", resp.StatusCode)
	}
	mac := hmac.New(sha256.New, []byte("hook-secret"))
	mac.Write([]byte(body))
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/webhook/git", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("good signature: status %d", resp.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for webhooks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if webhooks.Load() != 1 {
		t.Errorf("OnWebhook calls = %d", webhooks.Load())
	}
}

func TestProposalsUnavailableForPlainDirectory(t *testing.T) {
	srv := newTestServer(t, "")
	session := connect(t, srv, "")
	res, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "propose_update", Arguments: map[string]any{
		"doc_path": "domain/x.md", "change_description": "x", "new_content": "y"}})
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "git repository") {
		t.Errorf("expected unavailable error, got %+v", res)
	}
	resp, _ := http.Post(srv.URL+"/api/proposals", "application/json", strings.NewReader(`{"doc_path":"domain/x.md","change_description":"x","new_content":"y"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("REST proposals on plain dir: status %d", resp.StatusCode)
	}
}
