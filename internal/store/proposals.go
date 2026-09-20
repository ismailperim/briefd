package store

import (
	"context"
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
	Status      string    `json:"status"`
	Client      string    `json:"client,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// PutProposal records a created proposal.
func (s *Store) PutProposal(ctx context.Context, p Proposal) error {
	if p.Status == "" {
		p.Status = "open"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proposals (id, branch, doc_path, description, commit_hash, pr_url, status, client, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Branch, p.DocPath, p.Description, p.Commit, p.PRURL, p.Status, p.Client, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("storing proposal: %w", err)
	}
	return nil
}

// ListProposals returns proposals newest first, up to limit.
func (s *Store) ListProposals(ctx context.Context, limit int) ([]Proposal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, branch, doc_path, description, commit_hash, pr_url, status, client, created_at
		FROM proposals ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing proposals: %w", err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		var p Proposal
		var created string
		if err := rows.Scan(&p.ID, &p.Branch, &p.DocPath, &p.Description, &p.Commit, &p.PRURL, &p.Status, &p.Client, &created); err != nil {
			return nil, fmt.Errorf("listing proposals: %w", err)
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, p)
	}
	return out, rows.Err()
}
