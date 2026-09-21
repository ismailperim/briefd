package staleness

import (
	"testing"
	"time"

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/store"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"services/payment/**", "services/payment/refund.go", true},
		{"services/payment/**", "services/payment/a/b/c.go", true},
		{"services/payment/**", "services/payments/x.go", false},
		{"**/*.proto", "api/v1/ledger.proto", true},
		{"**/*.proto", "ledger.proto", true},
		{"**/*.proto", "api/ledger.protobuf", false},
		{"apps/*/src/refunds/**", "apps/portal/src/refunds/list.tsx", true},
		{"apps/*/src/refunds/**", "apps/a/b/src/refunds/list.tsx", false},
		{"platform/retry", "platform/retry/backoff.go", true},
		{"platform/retry/", "platform/retry/backoff.go", true},
		{"platform/retry", "platform/retry.go", false},
		{"docs/README.md", "docs/README.md", true},
		{"src/?.go", "src/a.go", true},
		{"src/?.go", "src/ab.go", false},
	}
	for _, c := range cases {
		if got := Match(c.glob, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
}

func TestCompute(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	docs := []store.DocRefs{
		{Path: "domain/refunds.md", Refs: []string{"services/ledger/refund/**"}, UpdatedAt: day(10)},
		{Path: "conventions/retries.md", Refs: []string{"platform/retry/**"}, UpdatedAt: day(20)},
		{Path: "domain/unknown-age.md", Refs: []string{"**"}},
	}
	changes := []gitsync.Change{
		{Hash: "c3", When: day(25), Paths: []string{"services/ledger/refund/partial.go", "README.md"}},
		{Hash: "c2", When: day(15), Paths: []string{"platform/retry/backoff.go"}}, // before retries.md changed
		{Hash: "c1", When: day(12), Paths: []string{"services/ledger/refund/refund.go"}},
		{Hash: "c0", When: day(5), Paths: []string{"services/ledger/refund/refund.go"}}, // before refunds.md changed
	}
	got := Compute(docs, "payments", changes, day(30))
	if len(got) != 1 {
		t.Fatalf("drift = %+v, want one row", got)
	}
	d := got[0]
	if d.DocPath != "domain/refunds.md" || d.Repo != "payments" || d.Commits != 2 || !d.LastChangeAt.Equal(day(25)) || d.LastPath != "services/ledger/refund/partial.go" {
		t.Errorf("drift = %+v", d)
	}
	if s := Since(docs); !s.Equal(day(10)) {
		t.Errorf("Since = %v, want %v", s, day(10))
	}
}
