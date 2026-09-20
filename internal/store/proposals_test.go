package store

import (
	"context"
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
}
