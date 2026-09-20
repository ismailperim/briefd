package gitsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// newRemote creates a bare "origin" plus an authoring clone used to push
// commits, simulating a team editing the knowledge repository.
func newRemote(t *testing.T) (bare string, commit func(path, content string) string) {
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
	commit = func(path, content string) string {
		t.Helper()
		full := filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(path); err != nil {
			t.Fatal(err)
		}
		h, err := wt.Commit("edit "+path, &git.CommitOptions{Author: &object.Signature{Name: "a", Email: "a@x", When: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/master:refs/heads/main"}}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			t.Fatal(err)
		}
		return h.String()
	}
	return bare, commit
}

func TestOpenSyncPropose(t *testing.T) {
	ctx := context.Background()
	bare, commit := newRemote(t)
	first := commit("domain/a.md", "# A\n\n## One\n\nalpha\n")

	dir := filepath.Join(t.TempDir(), "checkout")
	repo, err := Open(ctx, Config{URL: bare, Dir: dir, Branch: "main", AuthorName: "briefd", AuthorEmail: "b@x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if head, _ := repo.Head(); head != first {
		t.Errorf("head after clone = %s, want %s", head, first)
	}
	if repo.Branch() != "main" || repo.Dir() != dir {
		t.Errorf("branch/dir = %s/%s", repo.Branch(), repo.Dir())
	}

	// Nothing new upstream.
	head, changed, err := repo.Sync(ctx)
	if err != nil || changed || head != first {
		t.Errorf("sync no-op = %s %v %v", head, changed, err)
	}

	// Upstream edit is pulled and the worktree reflects it.
	second := commit("domain/a.md", "# A\n\n## One\n\nalpha changed\n")
	head, changed, err = repo.Sync(ctx)
	if err != nil || !changed || head != second {
		t.Fatalf("sync = %s %v %v, want %s", head, changed, err, second)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "domain", "a.md")); string(b) != "# A\n\n## One\n\nalpha changed\n" {
		t.Errorf("worktree not updated: %q", b)
	}

	// Reopening an existing checkout works and keeps the branch.
	again, err := Open(ctx, Config{URL: bare, Dir: dir}, nil)
	if err != nil || again.Branch() != "main" {
		t.Fatalf("reopen: %v branch=%s", err, again.Branch())
	}

	// A proposal creates and pushes a branch without touching main.
	res, err := repo.Propose(ctx, Proposal{ID: "abc123", DocPath: "domain/rules/new.md", Description: "add rule", Content: "# New\n\n## Rule\n\ntext\n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "briefd/proposal-abc123" || !res.Pushed || res.Commit == "" {
		t.Errorf("proposal = %+v", res)
	}
	if head, _ := repo.Head(); head != second {
		t.Error("proposal moved the followed branch")
	}
	origin, _ := git.PlainOpen(bare)
	ref, err := origin.Reference(plumbing.NewBranchReferenceName("briefd/proposal-abc123"), true)
	if err != nil {
		t.Fatalf("branch not pushed: %v", err)
	}
	c, _ := origin.CommitObject(ref.Hash())
	f, err := c.File("domain/rules/new.md")
	if err != nil {
		t.Fatalf("new file missing in proposal commit: %v", err)
	}
	body, _ := f.Contents()
	if body != "# New\n\n## Rule\n\ntext\n" {
		t.Errorf("proposal content = %q", body)
	}
	if _, err := c.File("domain/a.md"); err != nil {
		t.Error("existing files must be preserved in the proposal tree")
	}
	if parents := c.ParentHashes; len(parents) != 1 || parents[0].String() != second {
		t.Errorf("proposal parent = %v, want %s", parents, second)
	}

	// Replacing an existing file works too.
	res2, err := repo.Propose(ctx, Proposal{ID: "def", DocPath: "domain/a.md", Description: "edit", Content: "# A\n\nnew\n"})
	if err != nil || res2.Branch != "briefd/proposal-def" {
		t.Fatalf("second proposal: %+v %v", res2, err)
	}

	for _, bad := range []string{"../etc/passwd", "domain/../README.md", "README.md", "domain/x.txt", "projects/x"} {
		if err := ValidateDocPath(bad); err == nil {
			t.Errorf("ValidateDocPath(%q) should fail", bad)
		}
	}
	if err := ValidateDocPath("projects/ledger-service/runbooks/oncall.md"); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
}

func TestIsGitURL(t *testing.T) {
	for _, u := range []string{"https://github.com/o/r.git", "https://github.com/o/r", "git@github.com:o/r.git", "ssh://git@host/o/r", "/tmp/repo.git"} {
		if !IsGitURL(u) {
			t.Errorf("%s should be a git url", u)
		}
	}
	for _, u := range []string{"./knowledge", "/srv/knowledge", "testdata/knowledge"} {
		if IsGitURL(u) {
			t.Errorf("%s should not be a git url", u)
		}
	}
}
