// Package gitsync keeps a local checkout of the knowledge repository in sync
// with its remote and creates proposal branches (SPEC §4.1–4.2, §3.1.5).
// It uses go-git, so no git binary is needed (ADR-0005).
package gitsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"

	"github.com/ismailperim/briefd/internal/ingest"
)

// Config describes the remote and how to reach it.
type Config struct {
	// URL is the remote (https://..., git@host:org/repo.git, ssh://...).
	URL string
	// Branch to follow; "" resolves the remote's default branch on clone.
	Branch string
	// Dir is the local checkout path.
	Dir string
	// Token is used for HTTPS remotes (GitHub PAT, GitLab token, ...).
	Token string
	// Username for HTTPS basic auth; most forges accept any non-empty value
	// with a token. Default "briefd".
	Username string
	// SSHKeyPath is a private key file for SSH remotes; "" uses the agent.
	SSHKeyPath string
	// Author identity for proposal commits.
	AuthorName  string
	AuthorEmail string
	// Bare clones without a worktree: history only, for code repositories
	// whose files briefd never reads.
	Bare bool
}

// isAzureDevOps reports whether u points at Azure DevOps (cloud or Server):
// dev.azure.com, *.visualstudio.com, or the "/_git/" path every Azure
// DevOps repository URL contains.
func isAzureDevOps(u string) bool {
	return strings.Contains(u, "/_git/") || strings.Contains(u, "dev.azure.com") || strings.Contains(u, ".visualstudio.com")
}

// allowAzureDevOps lets go-git negotiate with Azure DevOps. Azure DevOps
// only serves packs to clients that support multi_ack, which go-git leaves
// out of its advertised capabilities by default; without it a clone ends
// with "object not found". go-git's own Azure DevOps example lifts the
// restriction the same way. The setting is process-wide and only applied
// when an Azure DevOps remote is used.
func allowAzureDevOps(u string) {
	if isAzureDevOps(u) {
		transport.UnsupportedCapabilities = []capability.Capability{capability.ThinPack}
	}
}

// IsGitURL reports whether source names a git remote rather than a directory.
func IsGitURL(source string) bool {
	s := strings.TrimSpace(source)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "ssh://") || strings.HasPrefix(s, "git://") ||
		strings.HasPrefix(s, "git@") || strings.HasSuffix(s, ".git")
}

// Repo is a synced checkout.
type Repo struct {
	cfg    Config
	repo   *git.Repository
	auth   transport.AuthMethod
	logger *slog.Logger
	local  bool
}

// Open clones cfg.URL into cfg.Dir if needed, otherwise opens the existing
// checkout, and returns it without fetching. Call Sync to update.
func Open(ctx context.Context, cfg Config, logger *slog.Logger) (*Repo, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if cfg.URL == "" || cfg.Dir == "" {
		return nil, errors.New("gitsync: url and dir are required")
	}
	auth, err := authFor(cfg)
	if err != nil {
		return nil, err
	}
	allowAzureDevOps(cfg.URL)
	r := &Repo{cfg: cfg, auth: auth, logger: logger}

	repo, err := git.PlainOpen(cfg.Dir)
	switch {
	case err == nil:
		r.repo = repo
		if r.cfg.Branch == "" {
			head, err := repo.Head()
			if err != nil {
				return nil, fmt.Errorf("gitsync: reading HEAD of %s: %w", cfg.Dir, err)
			}
			r.cfg.Branch = head.Name().Short()
		}
		return r, nil
	case !errors.Is(err, git.ErrRepositoryNotExists):
		return nil, fmt.Errorf("gitsync: opening %s: %w", cfg.Dir, err)
	}

	logger.Info("cloning repository", "url", redact(cfg.URL), "dir", cfg.Dir, "bare", cfg.Bare)
	opts := &git.CloneOptions{URL: cfg.URL, Auth: auth, SingleBranch: true, Depth: 0, NoCheckout: cfg.Bare}
	if cfg.Branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(cfg.Branch)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Dir), 0o750); err != nil {
		return nil, fmt.Errorf("gitsync: %w", err)
	}
	repo, err = git.PlainCloneContext(ctx, cfg.Dir, cfg.Bare, opts)
	if err != nil {
		return nil, fmt.Errorf("gitsync: cloning %s: %w", redact(cfg.URL), err)
	}
	r.repo = repo
	if r.cfg.Branch == "" {
		head, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("gitsync: reading HEAD: %w", err)
		}
		r.cfg.Branch = head.Name().Short()
	}
	return r, nil
}

// OpenLocal opens an existing checkout (for example a developer's own clone
// of the knowledge repo) without a configured remote URL. Sync is a no-op
// unless the checkout has an "origin" remote; proposals are committed to a
// local branch and pushed when origin exists.
func OpenLocal(dir string, cfg Config, logger *slog.Logger) (*Repo, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: false})
	if err != nil {
		return nil, fmt.Errorf("gitsync: opening %s: %w", dir, err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("gitsync: reading HEAD of %s: %w", dir, err)
	}
	cfg.Dir = dir
	cfg.Branch = head.Name().Short()
	if remote, err := repo.Remote("origin"); err == nil && len(remote.Config().URLs) > 0 {
		cfg.URL = remote.Config().URLs[0]
	}
	allowAzureDevOps(cfg.URL)
	auth, err := authFor(cfg)
	if err != nil {
		return nil, err
	}
	return &Repo{cfg: cfg, repo: repo, auth: auth, logger: logger, local: true}, nil
}

// IsLocal reports whether the repo was opened with OpenLocal.
func (r *Repo) IsLocal() bool { return r.local }

// URL returns the remote URL ("" for a local checkout without origin).
func (r *Repo) URL() string { return r.cfg.URL }

// Dir returns the checkout directory.
func (r *Repo) Dir() string { return r.cfg.Dir }

// Branch returns the followed branch.
func (r *Repo) Branch() string { return r.cfg.Branch }

// Head returns the current commit hash of the checkout.
func (r *Repo) Head() (string, error) {
	ref, err := r.repo.Head()
	if err != nil {
		return "", fmt.Errorf("gitsync: reading HEAD: %w", err)
	}
	return ref.Hash().String(), nil
}

// Sync fetches the remote branch and hard-resets the checkout to it.
// It returns the new head and whether it changed.
func (r *Repo) Sync(ctx context.Context) (head string, changed bool, err error) {
	before, err := r.Head()
	if err != nil {
		return "", false, err
	}
	if r.local {
		// A developer's checkout is theirs to update; we only re-read it.
		return before, false, nil
	}
	refspec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", r.cfg.Branch, r.cfg.Branch))
	err = r.repo.FetchContext(ctx, &git.FetchOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{refspec}, Auth: r.auth, Force: true})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return before, false, fmt.Errorf("gitsync: fetching %s: %w", r.cfg.Branch, err)
	}
	remote, err := r.repo.Reference(plumbing.NewRemoteReferenceName("origin", r.cfg.Branch), true)
	if err != nil {
		return before, false, fmt.Errorf("gitsync: resolving origin/%s: %w", r.cfg.Branch, err)
	}
	if remote.Hash().String() == before {
		return before, false, nil
	}
	branchRef := plumbing.NewBranchReferenceName(r.cfg.Branch)
	if r.cfg.Bare {
		if err := r.repo.Storer.SetReference(plumbing.NewHashReference(branchRef, remote.Hash())); err != nil {
			return before, false, fmt.Errorf("gitsync: updating %s: %w", r.cfg.Branch, err)
		}
		if err := r.repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branchRef)); err != nil {
			return before, false, fmt.Errorf("gitsync: updating HEAD: %w", err)
		}
		r.logger.Info("repository updated", "dir", r.cfg.Dir, "from", short(before), "to", short(remote.Hash().String()))
		return remote.Hash().String(), true, nil
	}
	wt, err := r.repo.Worktree()
	if err != nil {
		return before, false, fmt.Errorf("gitsync: %w", err)
	}
	// Make sure the local branch exists and points at the remote head, then
	// reset the worktree to it so local artifacts never leak into the index.
	if err := r.repo.Storer.SetReference(plumbing.NewHashReference(branchRef, remote.Hash())); err != nil {
		return before, false, fmt.Errorf("gitsync: updating %s: %w", r.cfg.Branch, err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err != nil {
		return before, false, fmt.Errorf("gitsync: checking out %s: %w", r.cfg.Branch, err)
	}
	if err := wt.Reset(&git.ResetOptions{Commit: remote.Hash(), Mode: git.HardReset}); err != nil {
		return before, false, fmt.Errorf("gitsync: resetting to %s: %w", remote.Hash(), err)
	}
	r.logger.Info("knowledge repository updated", "from", short(before), "to", short(remote.Hash().String()))
	return remote.Hash().String(), true, nil
}

// Proposal is a suggested change to one document.
type Proposal struct {
	// DocPath is repository-relative; a new file is created if it does
	// not exist.
	DocPath string
	// Description becomes the commit message body.
	Description string
	// Content is the complete new file content.
	Content string
	// ID names the branch: briefd/proposal-<ID>.
	ID string
	// BaseHash, when not nil, is the SHA-256 of the file the content was
	// edited from (get_document's content_hash). It must match the file on
	// the followed branch; "" means the file must not exist yet.
	BaseHash *string
}

// StaleBaseError refuses a proposal whose base_content_hash does not match
// the file on the followed branch (ADR-0013).
type StaleBaseError struct {
	DocPath string
	Base    string // what the caller sent; "" means "a new file"
	Current string // SHA-256 of the file on the branch; "" if it does not exist
}

func (e *StaleBaseError) Error() string {
	switch {
	case e.Current == "":
		return fmt.Sprintf("%s does not exist on the followed branch, but base_content_hash %s says it did; send an empty base_content_hash to create it", e.DocPath, short(e.Base))
	case e.Base == "":
		return fmt.Sprintf("%s already exists (content_hash %s), but an empty base_content_hash says it should not; call get_document and edit its source", e.DocPath, short(e.Current))
	default:
		return fmt.Sprintf("%s changed since it was read (have %s, now %s); call get_document again and redo the edit on the new source", e.DocPath, short(e.Base), short(e.Current))
	}
}

// ErrFrontMatterDropped refuses new content without front matter for a file
// that has some: almost always an edit of get_document's content instead of
// its source.
var ErrFrontMatterDropped = errors.New("new_content has no front matter but the current file has one; edit the source returned by get_document, or send an empty ---/--- block to remove the front matter on purpose")

var hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// NormalizeBaseHash lower-cases and checks a base_content_hash. A nil or
// empty value is returned as is.
func NormalizeBaseHash(h *string) (*string, error) {
	if h == nil {
		return nil, nil
	}
	v := strings.ToLower(strings.TrimSpace(*h))
	if v != "" && !hexHash.MatchString(v) {
		return nil, errors.New("base_content_hash must be the content_hash returned by get_document (64 hex characters), or empty for a new file")
	}
	return &v, nil
}

// checkBase compares the proposal with the file at the base commit.
func checkBase(c *object.Commit, p Proposal) error {
	var current []byte
	exists := false
	if f, err := c.File(p.DocPath); err == nil {
		r, err := f.Reader()
		if err != nil {
			return fmt.Errorf("gitsync: reading %s: %w", p.DocPath, err)
		}
		current, err = io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			return fmt.Errorf("gitsync: reading %s: %w", p.DocPath, err)
		}
		exists = true
	} else if !errors.Is(err, object.ErrFileNotFound) {
		return fmt.Errorf("gitsync: looking up %s: %w", p.DocPath, err)
	}
	if p.BaseHash != nil {
		cur := ""
		if exists {
			sum := sha256.Sum256(current)
			cur = hex.EncodeToString(sum[:])
		}
		if cur != *p.BaseHash || (exists && *p.BaseHash == "") {
			return &StaleBaseError{DocPath: p.DocPath, Base: *p.BaseHash, Current: cur}
		}
	}
	if exists && ingest.HasFrontMatter(current) && !ingest.HasFrontMatter([]byte(p.Content)) {
		return ErrFrontMatterDropped
	}
	return nil
}

// ProposalResult reports what was created.
type ProposalResult struct {
	Branch string
	Commit string
	Pushed bool
}

var validDocPath = regexp.MustCompile(`^(domain|conventions|projects/[A-Za-z0-9._-]+)/[A-Za-z0-9._/-]+\.md$`)

// ValidateDocPath rejects paths outside the knowledge layout or that
// escape the repository.
func ValidateDocPath(p string) error {
	clean := path.Clean(strings.TrimPrefix(strings.TrimSpace(p), "/"))
	if clean != strings.TrimPrefix(strings.TrimSpace(p), "/") || strings.Contains(clean, "..") || !validDocPath.MatchString(clean) {
		return fmt.Errorf("doc_path %q must be a .md file under domain/, conventions/ or projects/<name>/", p)
	}
	return nil
}

// Propose creates branch briefd/proposal-<id> from the current remote head,
// commits the new content and pushes the branch when a remote is reachable.
// The followed branch and the index are never touched.
func (r *Repo) Propose(ctx context.Context, p Proposal) (*ProposalResult, error) {
	if err := ValidateDocPath(p.DocPath); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Content) == "" || strings.TrimSpace(p.Description) == "" {
		return nil, errors.New("proposal needs a description and non-empty content")
	}
	base, err := r.repo.Reference(plumbing.NewBranchReferenceName(r.cfg.Branch), true)
	if err != nil {
		return nil, fmt.Errorf("gitsync: resolving %s: %w", r.cfg.Branch, err)
	}
	branch := ProposalBranchPrefix + p.ID
	branchRef := plumbing.NewBranchReferenceName(branch)

	// Build the commit from the base tree in memory: no worktree checkout,
	// so a concurrent Sync cannot observe a half-written state.
	baseCommit, err := r.repo.CommitObject(base.Hash())
	if err != nil {
		return nil, fmt.Errorf("gitsync: %w", err)
	}
	if err := checkBase(baseCommit, p); err != nil {
		return nil, err
	}
	tree, err := r.treeWithFile(baseCommit, p.DocPath, []byte(p.Content))
	if err != nil {
		return nil, err
	}
	author := &object.Signature{Name: r.cfg.AuthorName, Email: r.cfg.AuthorEmail, When: time.Now()}
	if author.Name == "" {
		author.Name = "briefd"
	}
	if author.Email == "" {
		author.Email = "briefd@localhost"
	}
	commit := &object.Commit{
		Author:       *author,
		Committer:    *author,
		Message:      fmt.Sprintf("docs: proposal for %s\n\n%s\n\nProposed via briefd propose_update.\n", p.DocPath, strings.TrimSpace(p.Description)),
		TreeHash:     tree,
		ParentHashes: []plumbing.Hash{base.Hash()},
	}
	obj := r.repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return nil, fmt.Errorf("gitsync: encoding commit: %w", err)
	}
	hash, err := r.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return nil, fmt.Errorf("gitsync: storing commit: %w", err)
	}
	if err := r.repo.Storer.SetReference(plumbing.NewHashReference(branchRef, hash)); err != nil {
		return nil, fmt.Errorf("gitsync: creating %s: %w", branch, err)
	}
	res := &ProposalResult{Branch: branch, Commit: hash.String()}

	if r.cfg.URL == "" {
		return res, nil // local checkout without a remote: branch stays local
	}
	err = r.repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))},
		Auth:       r.auth,
	})
	switch {
	case err == nil, errors.Is(err, git.NoErrAlreadyUpToDate):
		res.Pushed = true
	default:
		r.logger.Warn("proposal branch created locally but push failed", "branch", branch, "err", err)
	}
	return res, nil
}

// treeWithFile returns a new tree equal to the commit's tree with one file
// replaced or added.
func (r *Repo) treeWithFile(c *object.Commit, docPath string, content []byte) (plumbing.Hash, error) {
	blob := r.repo.Storer.NewEncodedObject()
	blob.SetType(plumbing.BlobObject)
	w, err := blob.Writer()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("gitsync: %w", err)
	}
	if _, err := w.Write(content); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("gitsync: %w", err)
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("gitsync: %w", err)
	}
	blobHash, err := r.repo.Storer.SetEncodedObject(blob)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("gitsync: storing blob: %w", err)
	}
	root, err := c.Tree()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("gitsync: %w", err)
	}
	return r.rebuildTree(root, strings.Split(docPath, "/"), blobHash)
}

// rebuildTree recursively copies tree, replacing the entry at parts.
func (r *Repo) rebuildTree(tree *object.Tree, parts []string, blob plumbing.Hash) (plumbing.Hash, error) {
	var entries []object.TreeEntry
	replaced := false
	for _, e := range tree.Entries {
		if e.Name != parts[0] {
			entries = append(entries, e)
			continue
		}
		replaced = true
		if len(parts) == 1 {
			entries = append(entries, object.TreeEntry{Name: e.Name, Mode: e.Mode, Hash: blob})
			continue
		}
		sub, err := r.repo.TreeObject(e.Hash)
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("gitsync: %w", err)
		}
		h, err := r.rebuildTree(sub, parts[1:], blob)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{Name: e.Name, Mode: e.Mode, Hash: h})
	}
	if !replaced {
		h, mode, err := r.newPath(parts, blob)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{Name: parts[0], Mode: mode, Hash: h})
	}
	return r.storeTree(entries)
}

// newPath creates nested trees for a path that does not exist yet.
func (r *Repo) newPath(parts []string, blob plumbing.Hash) (plumbing.Hash, filemode.FileMode, error) {
	if len(parts) == 1 {
		return blob, filemode.Regular, nil
	}
	h, mode, err := r.newPath(parts[1:], blob)
	if err != nil {
		return plumbing.ZeroHash, 0, err
	}
	th, err := r.storeTree([]object.TreeEntry{{Name: parts[1], Mode: mode, Hash: h}})
	return th, filemode.Dir, err
}

func (r *Repo) storeTree(entries []object.TreeEntry) (plumbing.Hash, error) {
	// git requires tree entries sorted by name (directories as name + "/").
	sortEntries(entries)
	t := &object.Tree{Entries: entries}
	obj := r.repo.Storer.NewEncodedObject()
	if err := t.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("gitsync: encoding tree: %w", err)
	}
	h, err := r.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("gitsync: storing tree: %w", err)
	}
	return h, nil
}

func sortEntries(entries []object.TreeEntry) {
	key := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && key(entries[j]) < key(entries[j-1]); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func authFor(cfg Config) (transport.AuthMethod, error) {
	u := cfg.URL
	switch {
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
		if cfg.Token == "" {
			return nil, nil
		}
		user := cfg.Username
		if user == "" {
			user = "briefd"
		}
		return &http.BasicAuth{Username: user, Password: cfg.Token}, nil
	case strings.HasPrefix(u, "git@"), strings.HasPrefix(u, "ssh://"):
		user := "git"
		if strings.HasPrefix(u, "ssh://") {
			if at := strings.Index(u, "@"); at > 0 {
				user = strings.TrimPrefix(u[:at], "ssh://")
			}
		}
		if cfg.SSHKeyPath != "" {
			keys, err := ssh.NewPublicKeysFromFile(user, cfg.SSHKeyPath, "")
			if err != nil {
				return nil, fmt.Errorf("gitsync: loading ssh key: %w", err)
			}
			return keys, nil
		}
		agent, err := ssh.NewSSHAgentAuth(user)
		if err != nil {
			return nil, fmt.Errorf("gitsync: ssh agent: %w", err)
		}
		return agent, nil
	default:
		return nil, nil // local path remote
	}
}

func redact(url string) string {
	if i := strings.Index(url, "://"); i > 0 {
		if at := strings.Index(url[i+3:], "@"); at > 0 {
			return url[:i+3] + "***@" + url[i+3+at+1:]
		}
	}
	return url
}

func short(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	return h
}

// LastModified returns, for each repository-relative path, the committer
// time of the most recent commit that changed it. The walk runs newest
// first and stops once every path is resolved, so asking about files that
// changed recently is cheap; paths untouched since the first commit cost a
// full history walk. Paths not found in history are absent from the result.
// A file changed in a merge commit counts only if it differs from every
// parent, matching what `git log -- path` reports.
func (r *Repo) LastModified(ctx context.Context, paths []string) (map[string]time.Time, error) {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	out := make(map[string]time.Time, len(paths))
	if len(want) == 0 {
		return out, nil
	}
	ref, err := r.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("gitsync: reading HEAD: %w", err)
	}
	iter, err := r.repo.Log(&git.LogOptions{From: ref.Hash(), Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil, fmt.Errorf("gitsync: reading history: %w", err)
	}
	defer iter.Close()
	err = iter.ForEach(func(c *object.Commit) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		changed, err := changedPaths(c, want)
		if err != nil {
			return err
		}
		for p := range changed {
			if want[p] {
				out[p] = c.Committer.When
				delete(want, p)
			}
		}
		if len(want) == 0 {
			return storer.ErrStop
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gitsync: walking history: %w", err)
	}
	return out, nil
}

// changedPaths lists the wanted paths whose blob in c differs from every
// parent (all files for a root commit).
func changedPaths(c *object.Commit, want map[string]bool) (map[string]bool, error) {
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	if c.NumParents() == 0 {
		out := map[string]bool{}
		for p := range want {
			if _, err := tree.File(p); err == nil {
				out[p] = true
			}
		}
		return out, nil
	}
	var out map[string]bool
	for i := 0; i < c.NumParents(); i++ {
		parent, err := c.Parent(i)
		if err != nil {
			return nil, err
		}
		ptree, err := parent.Tree()
		if err != nil {
			return nil, err
		}
		changes, err := ptree.Diff(tree)
		if err != nil {
			return nil, err
		}
		cur := map[string]bool{}
		for _, ch := range changes {
			for _, name := range []string{ch.From.Name, ch.To.Name} {
				if name != "" && want[name] {
					cur[name] = true
				}
			}
		}
		if out == nil {
			out = cur
			continue
		}
		for p := range out {
			if !cur[p] {
				delete(out, p)
			}
		}
	}
	return out, nil
}

// Change is one commit and the paths it touched (relative to every parent;
// for a merge only paths that differ from all parents count, as in git log).
type Change struct {
	Hash  string
	When  time.Time
	Paths []string
}

// ChangesSince lists commits newer than since, newest first, with the paths
// each one changed. The walk stops at the first commit at or before since
// (history is visited in committer-time order) or after maxCommits (<= 0
// means no cap), whichever comes first; truncated reports the latter.
func (r *Repo) ChangesSince(ctx context.Context, since time.Time, maxCommits int) (changes []Change, truncated bool, err error) {
	ref, err := r.repo.Head()
	if err != nil {
		return nil, false, fmt.Errorf("gitsync: reading HEAD: %w", err)
	}
	iter, err := r.repo.Log(&git.LogOptions{From: ref.Hash(), Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil, false, fmt.Errorf("gitsync: reading history: %w", err)
	}
	defer iter.Close()
	err = iter.ForEach(func(c *object.Commit) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !c.Committer.When.After(since) {
			return storer.ErrStop
		}
		if maxCommits > 0 && len(changes) >= maxCommits {
			truncated = true
			return storer.ErrStop
		}
		paths, err := allChangedPaths(c)
		if err != nil {
			return err
		}
		changes = append(changes, Change{Hash: c.Hash.String(), When: c.Committer.When, Paths: paths})
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("gitsync: walking history: %w", err)
	}
	return changes, truncated, nil
}

// allChangedPaths is changedPaths without a filter: every path that differs
// from all parents (every file for a root commit).
func allChangedPaths(c *object.Commit) ([]string, error) {
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	if c.NumParents() == 0 {
		var out []string
		err := tree.Files().ForEach(func(f *object.File) error {
			out = append(out, f.Name)
			return nil
		})
		return out, err
	}
	var set map[string]bool
	for i := 0; i < c.NumParents(); i++ {
		parent, err := c.Parent(i)
		if err != nil {
			return nil, err
		}
		ptree, err := parent.Tree()
		if err != nil {
			return nil, err
		}
		diff, err := ptree.Diff(tree)
		if err != nil {
			return nil, err
		}
		cur := map[string]bool{}
		for _, ch := range diff {
			for _, name := range []string{ch.From.Name, ch.To.Name} {
				if name != "" {
					cur[name] = true
				}
			}
		}
		if set == nil {
			set = cur
			continue
		}
		for p := range set {
			if !cur[p] {
				delete(set, p)
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// Dirs lists the directories in HEAD's tree up to depth (1 = top level),
// as repository-relative paths, sorted. Hidden directories are skipped.
func (r *Repo) Dirs(ctx context.Context, depth int) ([]string, error) {
	ref, err := r.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("gitsync: reading HEAD: %w", err)
	}
	c, err := r.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("gitsync: reading HEAD commit: %w", err)
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, fmt.Errorf("gitsync: reading HEAD tree: %w", err)
	}
	var out []string
	var walk func(t *object.Tree, prefix string, level int) error
	walk = func(t *object.Tree, prefix string, level int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, e := range t.Entries {
			if e.Mode != filemode.Dir || strings.HasPrefix(e.Name, ".") {
				continue
			}
			p := prefix + e.Name
			out = append(out, p)
			if level < depth {
				sub, err := t.Tree(e.Name)
				if err != nil {
					return err
				}
				if err := walk(sub, p+"/", level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(tree, "", 1); err != nil {
		return nil, fmt.Errorf("gitsync: listing directories: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// Landed reports whether a proposal commit's change to docPath is on HEAD:
// the commit is an ancestor of HEAD (merge or fast-forward), or HEAD holds
// exactly the proposed content of docPath (squash or rebase merges, or the
// same edit made by hand).
func (r *Repo) Landed(commit, docPath string) (bool, error) {
	head, err := r.repo.Head()
	if err != nil {
		return false, fmt.Errorf("gitsync: reading HEAD: %w", err)
	}
	headCommit, err := r.repo.CommitObject(head.Hash())
	if err != nil {
		return false, fmt.Errorf("gitsync: reading HEAD commit: %w", err)
	}
	proposed, err := r.repo.CommitObject(plumbing.NewHash(commit))
	if err != nil {
		// The proposal commit was never fetched into this clone (it lives on
		// its own branch); only the content check can decide.
		proposed = nil
	}
	if proposed != nil {
		if ok, err := proposed.IsAncestor(headCommit); err == nil && ok {
			return true, nil
		}
	}
	if proposed == nil {
		return false, nil
	}
	want, err := proposed.File(docPath)
	if err != nil {
		return false, nil //nolint:nilerr // the proposal deleted or never had the file
	}
	got, err := headCommit.File(docPath)
	if err != nil {
		return false, nil //nolint:nilerr // not on HEAD (yet)
	}
	return want.Hash == got.Hash, nil
}

// ProposalBranchPrefix starts every branch Propose creates.
const ProposalBranchPrefix = "briefd/proposal-"

// HasCommit reports whether the clone holds the commit. A proposal commit
// is missing when the checkout was cloned again after the proposal was made.
func (r *Repo) HasCommit(hash string) bool {
	_, err := r.repo.CommitObject(plumbing.NewHash(hash))
	return err == nil
}

// RemoteProposalBranches lists the proposal branches on origin, one network
// round trip (the equivalent of git ls-remote). For a checkout without a
// remote it lists the local ones.
func (r *Repo) RemoteProposalBranches(ctx context.Context) (map[string]bool, error) {
	if r.cfg.URL == "" {
		return r.LocalProposalBranches()
	}
	remote, err := r.repo.Remote("origin")
	if err != nil {
		return nil, fmt.Errorf("gitsync: %w", err)
	}
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: r.auth})
	if err != nil {
		return nil, fmt.Errorf("gitsync: listing remote branches: %w", err)
	}
	out := map[string]bool{}
	for _, ref := range refs {
		if name := ref.Name(); name.IsBranch() && strings.HasPrefix(name.Short(), ProposalBranchPrefix) {
			out[name.Short()] = true
		}
	}
	return out, nil
}

// LocalProposalBranches lists the proposal branches in the local clone.
func (r *Repo) LocalProposalBranches() (map[string]bool, error) {
	iter, err := r.repo.Branches()
	if err != nil {
		return nil, fmt.Errorf("gitsync: listing branches: %w", err)
	}
	out := map[string]bool{}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		if strings.HasPrefix(ref.Name().Short(), ProposalBranchPrefix) {
			out[ref.Name().Short()] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gitsync: listing branches: %w", err)
	}
	return out, nil
}

// DeleteProposalBranch removes a proposal branch from origin (when the
// checkout has one) and from the local clone. Only briefd/proposal-*
// branches can be deleted; a branch that is already gone is not an error.
func (r *Repo) DeleteProposalBranch(ctx context.Context, branch string) error {
	id := strings.TrimPrefix(branch, ProposalBranchPrefix)
	if id == branch || id == "" || !plumbing.NewBranchReferenceName(branch).IsBranch() || strings.ContainsAny(id, "/: \t\n~^?*[\\") {
		return fmt.Errorf("gitsync: %q is not a proposal branch", branch)
	}
	ref := plumbing.NewBranchReferenceName(branch)
	if r.cfg.URL != "" {
		err := r.repo.PushContext(ctx, &git.PushOptions{
			RemoteName: "origin",
			RefSpecs:   []config.RefSpec{config.RefSpec(":" + ref.String())},
			Auth:       r.auth,
		})
		if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			// Deleting a branch that is already gone fails on some servers;
			// only report the error when the branch is still there.
			branches, lerr := r.RemoteProposalBranches(ctx)
			if lerr != nil || branches[branch] {
				return fmt.Errorf("gitsync: deleting %s on origin: %w", branch, err)
			}
		}
	}
	if err := r.repo.Storer.RemoveReference(ref); err != nil && !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return fmt.Errorf("gitsync: deleting local %s: %w", branch, err)
	}
	return nil
}
