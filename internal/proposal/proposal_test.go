package proposal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/store"
)

func TestSyncStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/o/r/pulls/7" {
			_, _ = w.Write([]byte(`{"state":"closed","merged":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"state":"open","merged":false}`))
	}))
	defer srv.Close()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, p := range []store.Proposal{
		{ID: "merged", Branch: "briefd/proposal-merged", PRURL: "https://github.com/o/r/pull/7"},
		{ID: "open", Branch: "briefd/proposal-open", PRURL: "https://github.com/o/r/pull/8"},
	} {
		if err := st.PutProposal(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	forge, err := gitsync.NewForge(gitsync.ForgeConfig{
		Type: "github", Token: "token", Repo: "o/r", APIURL: srv.URL,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Forge: forge, Store: st}
	if err := service.SyncStatuses(context.Background()); err != nil {
		t.Fatal(err)
	}
	counts, err := st.CountProposals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts != (store.ProposalCounts{Open: 1, Merged: 1}) {
		t.Fatalf("proposal counts = %+v", counts)
	}
}
