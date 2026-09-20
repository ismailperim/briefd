package eval

import (
	"testing"

	"github.com/ismailperim/briefd/internal/store"
)

func TestScoreAndAggregate(t *testing.T) {
	q := Query{ID: "q", Type: "paraphrase", Expected: []Expected{
		{Path: "domain/a.md", Heading: "Refund window"},
		{Path: "domain/b.md", Heading: "Fees"},
	}}
	hits := []store.ChunkHit{
		{DocPath: "domain/x.md", HeadingPath: "X > Other"},
		{DocPath: "domain/a.md", HeadingPath: "Refunds > Refund window"},
		{DocPath: "domain/a.md", HeadingPath: "Refunds > Refund window"}, // duplicate must not double count
		{DocPath: "domain/x.md", HeadingPath: "X > 4"},
		{DocPath: "domain/x.md", HeadingPath: "X > 5"},
		{DocPath: "domain/x.md", HeadingPath: "X > 6"},
		{DocPath: "domain/b.md", HeadingPath: "Settlement > Fees"},
	}
	r := score(q, "hybrid", hits)
	if r.FirstRank != 2 || r.Recall5 != 0.5 || r.Recall10 != 1 || len(r.Missed) != 0 || len(r.Top) != 5 {
		t.Errorf("result = %+v", r)
	}
	// nDCG@10 with hits at ranks 2 and 7: (1/log2(3) + 1/log2(8)) / (1/log2(2) + 1/log2(3))
	if want := (1/1.5849625 + 1/3.0) / (1 + 1/1.5849625); r.NDCG10 < want-1e-6 || r.NDCG10 > want+1e-6 {
		t.Errorf("ndcg@10 = %v, want %v", r.NDCG10, want)
	}
	none := score(q, "bm25", nil)
	if none.FirstRank != 0 || none.Recall5 != 0 || len(none.Missed) != 2 {
		t.Errorf("empty result = %+v", none)
	}
	m := aggregate([]QueryResult{r, none}, "")
	if m.Queries != 2 || m.Recall5 != 0.25 || m.Recall10 != 0.5 || m.MRR != 0.25 {
		t.Errorf("aggregate = %+v", m)
	}
	if aggregate([]QueryResult{r, none}, "keyword").Queries != 0 {
		t.Error("type filter not applied")
	}
}

func TestMatches(t *testing.T) {
	hit := store.ChunkHit{DocPath: "domain/glossary.md", HeadingPath: "Glossary > Money movement > Chargeback"}
	if !Matches(hit, Expected{Path: "domain/glossary.md", Heading: "chargeback"}) {
		t.Error("suffix match failed")
	}
	if Matches(hit, Expected{Path: "domain/glossary.md", Heading: "Money movement"}) {
		t.Error("middle segment should not match")
	}
	if Matches(hit, Expected{Path: "domain/other.md", Heading: "Chargeback"}) {
		t.Error("path mismatch should not match")
	}
}

func TestCheck(t *testing.T) {
	rep := &Report{Modes: []ModeReport{{Mode: "hybrid", Overall: Metrics{Recall5: 0.8, Recall10: 0.95, MRR: 0.7}}}}
	th := Thresholds{"hybrid": {"recall_at_5": 0.85, "recall_at_10": 0.9, "mrr": 0.7}, "vector": {"mrr": 0.1}}
	f := Check(rep, th)
	if len(f) != 2 {
		t.Errorf("failures = %v", f)
	}
}
