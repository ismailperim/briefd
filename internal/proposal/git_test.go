package proposal

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

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/store"
)

// gitFixture is a bare origin, an author clone that pushes to main, and a
// proposal service on briefd's own clone, without a forge (as with Azure
// DevOps, Bitbucket or plain git).
type gitFixture struct {
	t      *testing.T
	bare   string
	commit func(path, content string)
	svc    *Service
	st     *store.Store
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "author")
	author, err := git.PlainInit(work, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := author.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	wt, _ := author.Worktree()
	commit := func(path, content string) {
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
		if _, err := wt.Commit("edit "+path, &git.CommitOptions{Author: &object.Signature{Name: "a", Email: "a@x", When: time.Now()}}); err != nil {
			t.Fatal(err)
		}
		if err := author.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/master:refs/heads/main"}}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			t.Fatal(err)
		}
	}
	commit("domain/a.md", "# A\n\nalpha\n")

	repo, err := gitsync.Open(context.Background(), gitsync.Config{URL: bare, Dir: filepath.Join(root, "briefd"), Branch: "main"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &gitFixture{t: t, bare: bare, commit: commit, svc: &Service{Repo: repo, Store: st}, st: st}
}

func (f *gitFixture) propose(doc, content string) *Result {
	f.t.Helper()
	res, err := f.svc.Create(context.Background(), Request{DocPath: doc, Description: "change " + doc, Content: content})
	if err != nil || !res.Pushed {
		f.t.Fatalf("propose %s: %+v %v", doc, res, err)
	}
	return res
}

// sync pulls main and refreshes statuses with the lookup interval elapsed.
func (f *gitFixture) sync() {
	f.t.Helper()
	if _, _, err := f.svc.Repo.Sync(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.svc.lastStatusSync = time.Time{}
	if err := f.svc.SyncStatuses(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *gitFixture) status(id string) string {
	f.t.Helper()
	p, err := f.st.GetProposal(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return p.Status
}

func (f *gitFixture) onOrigin(branch string) bool {
	origin, err := git.PlainOpen(f.bare)
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = origin.Reference(plumbing.NewBranchReferenceName(branch), true)
	return err == nil
}

func (f *gitFixture) deleteOnOrigin(branch string) {
	f.t.Helper()
	origin, err := git.PlainOpen(f.bare)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := origin.Storer.RemoveReference(plumbing.NewBranchReferenceName(branch)); err != nil {
		f.t.Fatal(err)
	}
}

func TestSyncStatusesWithoutForge(t *testing.T) {
	f := newGitFixture(t)
	merged := f.propose("domain/a.md", "# A\n\nmerged text\n")
	declined := f.propose("domain/b.md", "# B\n\ndeclined\n")
	pending := f.propose("domain/c.md", "# C\n\npending\n")

	f.sync()
	for _, id := range []string{merged.ID, declined.ID, pending.ID} {
		if got := f.status(id); got != "open" {
			t.Fatalf("%s = %s before review", id, got)
		}
	}

	// A squash merge of one proposal (branch deleted afterwards), and a
	// reviewer who declined another and deleted its branch.
	f.commit("domain/a.md", "# A\n\nmerged text\n")
	f.deleteOnOrigin(merged.Branch)
	f.deleteOnOrigin(declined.Branch)
	f.sync()
	if got := f.status(merged.ID); got != "merged" {
		t.Errorf("merged proposal = %s", got)
	}
	if got := f.status(declined.ID); got != "closed" {
		t.Errorf("declined proposal with deleted branch = %s", got)
	}
	if got := f.status(pending.ID); got != "open" {
		t.Errorf("proposal still under review = %s", got)
	}
}

func TestSyncStatusesKeepsUnknownProposalsOpen(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	// A proposal whose commit this clone never saw (made before the clone
	// was recreated) and whose branch is gone: it may have been merged, so
	// it is not closed.
	if err := f.st.PutProposal(ctx, store.Proposal{ID: "old", Branch: "briefd/proposal-old", DocPath: "domain/x.md",
		Description: "d", Commit: "0123456789012345678901234567890123456789", Pushed: true}); err != nil {
		t.Fatal(err)
	}
	f.sync()
	if got := f.status("old"); got != "open" {
		t.Errorf("proposal with unknown commit = %s", got)
	}

	// Status lookups wait for the interval: a branch deleted right after a
	// lookup is noticed on the next one.
	p := f.propose("domain/b.md", "# B\n\nx\n")
	f.sync()
	f.deleteOnOrigin(p.Branch)
	if err := f.svc.SyncStatuses(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.status(p.ID); got != "open" {
		t.Errorf("closed before the lookup interval: %s", got)
	}
	f.sync()
	if got := f.status(p.ID); got != "closed" {
		t.Errorf("after the interval = %s", got)
	}
}

func TestClose(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	keep := f.propose("domain/b.md", "# B\n\nkeep branch\n")
	drop := f.propose("domain/c.md", "# C\n\ndrop branch\n")

	p, err := f.svc.Close(ctx, keep.ID, false)
	if err != nil || p.Status != "closed" {
		t.Fatalf("close = %+v %v", p, err)
	}
	if !f.onOrigin(keep.Branch) {
		t.Error("close without delete_branch removed the branch")
	}
	if _, err := f.svc.Close(ctx, drop.ID, true); err != nil {
		t.Fatal(err)
	}
	if f.onOrigin(drop.Branch) {
		t.Error("branch still on origin after delete_branch")
	}
	if local, _ := f.svc.Repo.LocalProposalBranches(); local[drop.Branch] || !local[keep.Branch] {
		t.Errorf("local branches = %v", local)
	}
	if got := f.status(drop.ID); got != "closed" {
		t.Errorf("status = %s", got)
	}

	gone := f.propose("domain/d.md", "# D\n\nbranch already deleted\n")
	f.deleteOnOrigin(gone.Branch)
	if _, err := f.svc.Close(ctx, gone.ID, true); err != nil {
		t.Errorf("deleting a branch that is already gone: %v", err)
	}

	if _, err := f.svc.Close(ctx, keep.ID, false); !errors.Is(err, ErrNotOpen) {
		t.Errorf("closing twice: %v", err)
	}
	if _, err := f.svc.Close(ctx, "nope", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if err := f.svc.Repo.DeleteProposalBranch(ctx, "main"); err == nil {
		t.Error("deleting a branch that is not a proposal branch must fail")
	}
	if err := f.svc.Repo.DeleteProposalBranch(ctx, "briefd/proposal-../main"); err == nil {
		t.Error("deleting a crafted branch name must fail")
	}
}
