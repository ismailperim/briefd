package store

import (
	"context"
	"errors"
	"testing"
)

func TestProposals(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.PutProposal(ctx, Proposal{ID: "a", Branch: "briefd/proposal-a", DocPath: "domain/x.md", Description: "d", Commit: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutProposal(ctx, Proposal{ID: "b", Branch: "briefd/proposal-b", DocPath: "domain/y.md", Description: "d", PRURL: "https://x/pr/1"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListProposals(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != "b" || list[0].Status != "open" || list[1].Commit != "c1" {
		t.Errorf("list = %+v, %v", list, err)
	}
	open, err := s.ListOpenProposals(ctx)
	if err != nil || len(open) != 2 || open[0].ID != "a" || open[1].ID != "b" || open[0].Pushed {
		t.Fatalf("open proposals = %+v, %v", open, err)
	}
	if err := s.PutProposal(ctx, Proposal{ID: "c", Branch: "briefd/proposal-c", DocPath: "domain/z.md", Description: "d", Pushed: true}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.GetProposal(ctx, "c"); err != nil || !p.Pushed || p.DocPath != "domain/z.md" || p.CreatedAt.IsZero() {
		t.Errorf("get = %+v, %v", p, err)
	}
	if _, err := s.GetProposal(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing proposal: %v", err)
	}
	if err := s.UpdateProposalStatus(ctx, "b", "merged"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProposalStatus(ctx, "b", "closed"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProposalStatus(ctx, "a", "open"); err == nil {
		t.Fatal("open is not a terminal status")
	}
	counts, err := s.CountProposals(ctx)
	if err != nil || counts != (ProposalCounts{Open: 2, Merged: 1}) {
		t.Errorf("proposal counts = %+v, %v", counts, err)
	}
}
