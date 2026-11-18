package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Proposal is a stored record of a proposal branch.
type Proposal struct {
	ID          string    `json:"id"`
	Branch      string    `json:"branch"`
	DocPath     string    `json:"doc_path"`
	Description string    `json:"description"`
	Commit      string    `json:"commit"`
	PRURL       string    `json:"pr_url,omitempty"`
	Pushed      bool      `json:"pushed"`
	Status      string    `json:"status"`
	Client      string    `json:"client,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ProposalCounts groups stored proposals by forge status.
type ProposalCounts struct {
	Open   int `json:"open"`
	Merged int `json:"merged"`
	Closed int `json:"closed"`
}

// PutProposal records a created proposal.
func (s *Store) PutProposal(ctx context.Context, p Proposal) error {
	if p.Status == "" {
		p.Status = "open"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proposals (id, branch, doc_path, description, commit_hash, pr_url, pushed, status, client, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Branch, p.DocPath, p.Description, p.Commit, p.PRURL, p.Pushed, p.Status, p.Client, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("storing proposal: %w", err)
	}
	return nil
}

const proposalColumns = `id, branch, doc_path, description, commit_hash, pr_url, pushed, status, client, created_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanProposal(row rowScanner) (Proposal, error) {
	var p Proposal
	var created string
	if err := row.Scan(&p.ID, &p.Branch, &p.DocPath, &p.Description, &p.Commit, &p.PRURL, &p.Pushed, &p.Status, &p.Client, &created); err != nil {
		return Proposal{}, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return p, nil
}

func (s *Store) queryProposals(ctx context.Context, query string, args ...any) ([]Proposal, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListProposals returns proposals newest first, up to limit.
func (s *Store) ListProposals(ctx context.Context, limit int) ([]Proposal, error) {
	out, err := s.queryProposals(ctx, `SELECT `+proposalColumns+`
		FROM proposals ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing proposals: %w", err)
	}
	return out, nil
}

// ListOpenProposals returns every open proposal, oldest first.
func (s *Store) ListOpenProposals(ctx context.Context) ([]Proposal, error) {
	out, err := s.queryProposals(ctx, `SELECT `+proposalColumns+`
		FROM proposals WHERE status = 'open' ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing open proposals: %w", err)
	}
	return out, nil
}

// GetProposal returns one proposal, or ErrNotFound.
func (s *Store) GetProposal(ctx context.Context, id string) (Proposal, error) {
	p, err := scanProposal(s.db.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM proposals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	if err != nil {
		return Proposal{}, fmt.Errorf("reading proposal %s: %w", id, err)
	}
	return p, nil
}

// UpdateProposalStatus moves an open proposal to a terminal forge status.
func (s *Store) UpdateProposalStatus(ctx context.Context, id, status string) error {
	if status != "merged" && status != "closed" {
		return fmt.Errorf("updating proposal status: invalid terminal status %q", status)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE proposals SET status = ? WHERE id = ? AND status = 'open'`, status, id); err != nil {
		return fmt.Errorf("updating proposal status: %w", err)
	}
	return nil
}

// CountProposals returns counts for each supported status.
func (s *Store) CountProposals(ctx context.Context) (ProposalCounts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM proposals GROUP BY status`)
	if err != nil {
		return ProposalCounts{}, fmt.Errorf("counting proposals: %w", err)
	}
	defer rows.Close()
	var counts ProposalCounts
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return ProposalCounts{}, fmt.Errorf("counting proposals: %w", err)
		}
		switch status {
		case "open":
			counts.Open = count
		case "merged":
			counts.Merged = count
		case "closed":
			counts.Closed = count
		default:
			return ProposalCounts{}, fmt.Errorf("counting proposals: unknown status %q", status)
		}
	}
	if err := rows.Err(); err != nil {
		return ProposalCounts{}, fmt.Errorf("counting proposals: %w", err)
	}
	return counts, nil
}
