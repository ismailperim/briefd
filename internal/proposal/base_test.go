package proposal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/ingest"
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ADR-0013: proposals are checked against the file on the followed branch.
func TestProposeChecksTheBase(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	const v1 = "---\ntitle: Refunds\nrelated:\n  - \"[[chargebacks]]\"\n---\n# Refunds\n\nWithin 30 days.\n"
	f.commit("domain/refunds.md", v1)
	f.sync()
	edited := strings.Replace(v1, "30 days", "45 days", 1)

	create := func(doc, content string, base *string) (*Result, error) {
		return f.svc.Create(ctx, Request{DocPath: doc, Description: "change " + doc, Content: content, BaseContentHash: base})
	}
	branches := func() int {
		t.Helper()
		b, err := f.svc.Repo.LocalProposalBranches()
		if err != nil {
			t.Fatal(err)
		}
		return len(b)
	}
	str := func(s string) *string { return &s }

	// A matching base (upper case is accepted) creates the proposal.
	res, err := create("domain/refunds.md", edited, str(strings.ToUpper(sha(v1))))
	if err != nil {
		t.Fatalf("matching base: %v", err)
	}
	if p, _ := f.st.GetProposal(ctx, res.ID); strings.Contains(p.Description, "base_content_hash") {
		t.Errorf("a checked proposal should not carry the no-base note: %q", p.Description)
	}

	// The file changes on main: the old base is refused, without a branch.
	f.commit("domain/refunds.md", strings.Replace(v1, "30 days", "60 days", 1))
	f.sync()
	before := branches()
	var stale *gitsync.StaleBaseError
	if _, err := create("domain/refunds.md", edited, str(sha(v1))); !errors.As(err, &stale) || !strings.Contains(err.Error(), "changed since it was read") {
		t.Errorf("stale base: %v", err)
	}
	if branches() != before {
		t.Error("a refused proposal must not create a branch")
	}

	// New files: "" asserts absence; a hash for a missing file is refused.
	if _, err := create("domain/new.md", "# New\n\ntext\n", str("")); err != nil {
		t.Errorf("new file with empty base: %v", err)
	}
	if _, err := create("domain/refunds.md", edited, str("")); !errors.As(err, &stale) || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("empty base for an existing file: %v", err)
	}
	if _, err := create("domain/other.md", "# O\n", str(sha(v1))); !errors.As(err, &stale) || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("base for a missing file: %v", err)
	}
	if _, err := create("domain/new2.md", "# N\n", str("not-a-hash")); err == nil || !strings.Contains(err.Error(), "64 hex") {
		t.Errorf("malformed base: %v", err)
	}

	// Without a base the old behavior stays, and the reviewer is told.
	res, err = create("domain/a.md", "# A\n\nbeta\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := f.st.GetProposal(ctx, res.ID); !strings.HasSuffix(p.Description, noBaseNote) {
		t.Errorf("description = %q", p.Description)
	}
}

func TestProposeKeepsTheFrontMatter(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	const v1 = "---\ntitle: Refunds\n---\n# Refunds\n\nWithin 30 days.\n"
	f.commit("domain/refunds.md", v1)
	f.sync()
	base := sha(v1)
	create := func(content string) error {
		_, err := f.svc.Create(ctx, Request{DocPath: "domain/refunds.md", Description: "edit", Content: content, BaseContentHash: &base})
		return err
	}
	// What an agent produces from get_document's content field.
	if err := create("Within 45 days.\n"); !errors.Is(err, gitsync.ErrFrontMatterDropped) {
		t.Errorf("dropped front matter: %v", err)
	}
	// An explicit empty block removes it on purpose.
	if err := create("---\n---\n# Refunds\n\nWithin 45 days.\n"); err != nil {
		t.Errorf("explicit empty block: %v", err)
	}
	// A file without front matter can be edited without one.
	f.commit("domain/plain.md", "# Plain\n\ntext\n")
	f.sync()
	plain := sha("# Plain\n\ntext\n")
	if _, err := f.svc.Create(ctx, Request{DocPath: "domain/plain.md", Description: "edit", Content: "# Plain\n\nmore\n", BaseContentHash: &plain}); err != nil {
		t.Errorf("plain file: %v", err)
	}
}

// When the agent's hash is the indexed one but the branch moved on, the
// index is lagging: the error says to wait instead of re-reading.
func TestStaleBaseExplainsIndexLag(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	const v1 = "# Refunds\n\nWithin 30 days.\n"
	f.commit("domain/refunds.md", v1)
	f.sync()
	doc, err := ingest.Parse("domain/refunds.md", []byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpsertDocument(ctx, doc, ""); err != nil {
		t.Fatal(err)
	}
	f.commit("domain/refunds.md", "# Refunds\n\nWithin 60 days.\n")
	f.sync() // pulled, but this test never re-indexes
	base := sha(v1)
	_, err = f.svc.Create(ctx, Request{DocPath: "domain/refunds.md", Description: "edit", Content: "# Refunds\n\nWithin 45 days.\n", BaseContentHash: &base})
	if err == nil || !strings.Contains(err.Error(), "index has not caught up") {
		t.Errorf("err = %v", err)
	}
}
