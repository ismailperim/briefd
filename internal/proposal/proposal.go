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

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/store"
)

// ErrUnavailable is returned when the source is not a git repository.
var ErrUnavailable = errors.New("proposals require the knowledge source to be a git repository")

// Service creates proposals.
type Service struct {
	Repo   *gitsync.Repo // nil when the source is a plain directory
	Forge  *gitsync.Forge
	Store  *store.Store
	Logger *slog.Logger
}

// Request is what an agent submits.
type Request struct {
	DocPath     string
	Description string
	Content     string
	Client      string
}

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
	id, err := newID()
	if err != nil {
		return nil, err
	}
	pr, err := s.Repo.Propose(ctx, gitsync.Proposal{
		ID: id, DocPath: req.DocPath, Description: req.Description, Content: req.Content,
	})
	if err != nil {
		return nil, err
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
			Commit: pr.Commit, PRURL: res.PRURL, Client: req.Client,
		})
		if err != nil {
			return nil, err
		}
	}
	s.log().Info("proposal created", "id", id, "branch", pr.Branch, "doc", req.DocPath, "pushed", pr.Pushed, "pr", res.PRURL)
	return res, nil
}

// SyncStatuses refreshes open proposals that have a forge pull request.
func (s *Service) SyncStatuses(ctx context.Context) error {
	if s == nil || s.Forge == nil || s.Store == nil {
		return nil
	}
	proposals, err := s.Store.ListOpenProposals(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, p := range proposals {
		status, err := s.Forge.PullRequestStatus(ctx, p.PRURL)
		if err != nil {
			failures = append(failures, fmt.Errorf("proposal %s: %w", p.ID, err))
			continue
		}
		if status == "open" {
			continue
		}
		if err := s.Store.UpdateProposalStatus(ctx, p.ID, status); err != nil {
			failures = append(failures, fmt.Errorf("proposal %s: %w", p.ID, err))
		}
	}
	return errors.Join(failures...)
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
