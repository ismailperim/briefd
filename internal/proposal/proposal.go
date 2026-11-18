// Package proposal turns an agent's suggested document change into a git
// branch (and optionally a pull request) and records it. It never writes to
// the index: merge → sync → reindex is the only path in (SPEC §3.1.5).
package proposal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/store"
)

var (
	// ErrUnavailable is returned when the source is not a git repository.
	ErrUnavailable = errors.New("proposals require the knowledge source to be a git repository")
	// ErrNotFound is returned by Close for an unknown proposal id.
	ErrNotFound = errors.New("proposal not found")
	// ErrNotOpen is returned by Close for a proposal already merged or closed.
	ErrNotOpen = errors.New("proposal is not open")
)

// Service creates proposals.
type Service struct {
	Repo   *gitsync.Repo // nil when the source is a plain directory
	Forge  *gitsync.Forge
	Store  *store.Store
	Logger *slog.Logger

	mu             sync.Mutex
	lastStatusSync time.Time
}

// Request is what an agent submits.
type Request struct {
	DocPath     string
	Description string
	Content     string
	Client      string
	// BaseContentHash is get_document's content_hash for the version the
	// edit started from; nil skips the check, "" asserts a new file
	// (ADR-0013).
	BaseContentHash *string
}

// noBaseNote ends the description of a proposal made without a base hash,
// so the reviewer knows concurrent changes were not checked.
const noBaseNote = "(No base_content_hash was given: briefd did not check that this edit started from the current version of the file.)"

// Result describes the created branch.
type Result struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Pushed bool   `json:"pushed"`
	PRURL  string `json:"pr_url,omitempty"`
}

// Available reports whether proposals can be created.
func (s *Service) Available() bool { return s != nil && s.Repo != nil }

// Create validates the request, commits it to a new branch, opens a PR when
// a forge is configured, and stores the record.
func (s *Service) Create(ctx context.Context, req Request) (*Result, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if err := gitsync.ValidateDocPath(req.DocPath); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Description) == "" {
		return nil, errors.New("change_description must not be empty")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, errors.New("new_content must not be empty")
	}
	base, err := gitsync.NormalizeBaseHash(req.BaseContentHash)
	if err != nil {
		return nil, err
	}
	if base == nil {
		req.Description = strings.TrimSpace(req.Description) + "\n\n" + noBaseNote
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	pr, err := s.Repo.Propose(ctx, gitsync.Proposal{
		ID: id, DocPath: req.DocPath, Description: req.Description, Content: req.Content, BaseHash: base,
	})
	if err != nil {
		return nil, s.explainStale(ctx, err)
	}
	res := &Result{ID: id, Branch: pr.Branch, Commit: pr.Commit, Pushed: pr.Pushed}
	if s.Forge != nil && pr.Pushed {
		title := fmt.Sprintf("briefd proposal: %s", req.DocPath)
		body := fmt.Sprintf("%s\n\n---\nProposed by an agent via briefd `propose_update` (branch `%s`). Review before merging.", strings.TrimSpace(req.Description), pr.Branch)
		url, err := s.Forge.OpenPullRequest(ctx, title, pr.Branch, s.Repo.Branch(), body)
		if err != nil {
			s.log().Warn("proposal pushed but pull request failed", "branch", pr.Branch, "err", err)
		} else {
			res.PRURL = url
		}
	}
	if s.Store != nil {
		err := s.Store.PutProposal(ctx, store.Proposal{
			ID: id, Branch: pr.Branch, DocPath: req.DocPath, Description: req.Description,
			Commit: pr.Commit, PRURL: res.PRURL, Pushed: pr.Pushed, Client: req.Client,
		})
		if err != nil {
			return nil, err
		}
	}
	s.log().Info("proposal created", "id", id, "branch", pr.Branch, "doc", req.DocPath, "pushed", pr.Pushed, "pr", res.PRURL)
	return res, nil
}

// explainStale adds a hint when a stale base is the index lagging behind
// the repository (a sync pulled but has not re-indexed yet): get_document
// would return the same old hash, so the caller should wait, not re-read
// in a loop.
func (s *Service) explainStale(ctx context.Context, err error) error {
	var stale *gitsync.StaleBaseError
	if !errors.As(err, &stale) || s.Store == nil || stale.Base == "" {
		return err
	}
	if indexed, e := s.Store.DocumentHash(ctx, stale.DocPath); e == nil && indexed == stale.Base {
		return fmt.Errorf("%w (briefd's index has not caught up with the repository yet; retry after the next sync)", err)
	}
	return err
}

// StatusSyncInterval is the minimum time between network status lookups
// (forge pull requests, remote proposal branches), so the (default 60 s)
// source sync does not spend API calls every minute.
const StatusSyncInterval = 10 * time.Minute

// SyncStatuses moves open proposals to merged or closed. It runs after
// every source sync.
//
//   - A proposal with a forge pull request takes the pull request's state.
//   - Any other proposal is merged once its change is on the followed
//     branch (a local check, every call), and closed once its branch is
//     gone without the change having landed: the reviewer declined it and
//     deleted the branch.
//
// Network lookups run at most once per StatusSyncInterval.
func (s *Service) SyncStatuses(ctx context.Context) error {
	if s == nil || s.Store == nil {
		return nil
	}
	proposals, err := s.Store.ListOpenProposals(ctx)
	if err != nil || len(proposals) == 0 {
		return err
	}
	lookup := s.lookupDue()
	var failures []error
	fail := func(p store.Proposal, err error) {
		failures = append(failures, fmt.Errorf("proposal %s: %w", p.ID, err))
	}
	var branches *branchSet
	if s.Repo != nil {
		branches = &branchSet{repo: s.Repo}
	}
	for _, p := range proposals {
		status, err := s.status(ctx, p, lookup, branches)
		if err != nil {
			fail(p, err)
			continue
		}
		if status == "open" {
			continue
		}
		if err := s.Store.UpdateProposalStatus(ctx, p.ID, status); err != nil {
			fail(p, err)
			continue
		}
		s.log().Info("proposal "+status, "id", p.ID, "branch", p.Branch)
	}
	return errors.Join(failures...)
}

// status works out where one open proposal stands.
func (s *Service) status(ctx context.Context, p store.Proposal, lookup bool, branches *branchSet) (string, error) {
	if s.Forge != nil && p.PRURL != "" {
		if !lookup {
			return "open", nil
		}
		return s.Forge.PullRequestStatus(ctx, p.PRURL)
	}
	if branches == nil {
		return "open", nil
	}
	landed, err := s.Repo.Landed(p.Commit, p.DocPath)
	if err != nil {
		return "", err
	}
	if landed {
		return "merged", nil
	}
	// Without the proposal commit (the clone was made again since) a missing
	// branch could also mean "merged and branch deleted"; leave it open.
	if !lookup || !s.Repo.HasCommit(p.Commit) {
		return "open", nil
	}
	exists, err := branches.has(ctx, p)
	if err != nil {
		return "", err
	}
	if exists {
		return "open", nil
	}
	return "closed", nil
}

// branchSet lists proposal branches at most once per status sync.
type branchSet struct {
	repo                *gitsync.Repo
	remote, local       map[string]bool
	remoteErr, localErr error
	remoteDone, locDone bool
}

// has reports whether p's branch still exists where it was created: on the
// remote when it was pushed, otherwise in the local clone.
func (b *branchSet) has(ctx context.Context, p store.Proposal) (bool, error) {
	if p.Pushed && b.repo.URL() != "" {
		if !b.remoteDone {
			b.remote, b.remoteErr = b.repo.RemoteProposalBranches(ctx)
			b.remoteDone = true
		}
		return b.remote[p.Branch], b.remoteErr
	}
	if !b.locDone {
		b.local, b.localErr = b.repo.LocalProposalBranches()
		b.locDone = true
	}
	return b.local[p.Branch], b.localErr
}

func (s *Service) lookupDue() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastStatusSync) < StatusSyncInterval {
		return false
	}
	s.lastStatusSync = time.Now()
	return true
}

// Close marks an open proposal closed, for a reviewer who declined it.
// With deleteBranch the proposal branch is deleted first (on the remote and
// locally); if that fails nothing changes. A forge pull request is not
// touched: close it on the forge (GitHub closes it when the branch goes).
func (s *Service) Close(ctx context.Context, id string, deleteBranch bool) (*store.Proposal, error) {
	if s == nil || s.Store == nil {
		return nil, ErrUnavailable
	}
	p, err := s.Store.GetProposal(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Status != "open" {
		return nil, fmt.Errorf("%w (it is %s)", ErrNotOpen, p.Status)
	}
	if deleteBranch {
		if s.Repo == nil {
			return nil, ErrUnavailable
		}
		if err := s.Repo.DeleteProposalBranch(ctx, p.Branch); err != nil {
			return nil, err
		}
	}
	if err := s.Store.UpdateProposalStatus(ctx, p.ID, "closed"); err != nil {
		return nil, err
	}
	p.Status = "closed"
	s.log().Info("proposal closed", "id", p.ID, "branch", p.Branch, "branch_deleted", deleteBranch)
	return &p, nil
}

func (s *Service) log() *slog.Logger {
	if s.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Logger
}

func newID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating proposal id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
