package store

import (
	"context"
	"testing"
	"time"
)

func TestQueryLogAndGaps(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	log := func(q, bundle string, results int, margin float64, at time.Time) {
		t.Helper()
		if err := s.LogQuery(ctx, QueryRecord{At: at, Surface: "mcp", Name: "compile_bundle", Query: q, Scopes: []string{"domain"}, Mode: "hybrid", Results: results, TopScore: 0.8, Margin: margin, BundleID: bundle}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	log("how do I rotate the api key", "b1", 5, 0.05, now)       // feedback: nothing useful
	log("how do I rotate the api key", "b1", 5, 0.05, now)       // asked twice
	log("what is the refund window", "b2", 5, 0.12, now)         // feedback: useful
	log("kafka consumer group config", "", 0, 0, now)            // no results
	log("ancient question", "", 0, 0, now.Add(-90*24*time.Hour)) // outside window / pruned
	if err := s.PutUsage(ctx, []UsageEvent{{BundleID: "b1", ChunkID: "c1", Useful: false}, {BundleID: "b1", ChunkID: "c2", Useful: false}, {BundleID: "b2", ChunkID: "c3", Useful: true}}); err != nil {
		t.Fatal(err)
	}

	noUseful, low, err := s.Gaps(ctx, now.Add(-7*24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(noUseful) != 2 || noUseful[0].Query != "how do I rotate the api key" || noUseful[0].Times != 2 || noUseful[0].Reason != "no_useful_sections" {
		t.Errorf("noUseful = %+v", noUseful)
	}
	if noUseful[1].Reason != "no_results" || noUseful[1].Query != "kafka consumer group config" {
		t.Errorf("noUseful[1] = %+v", noUseful[1])
	}
	if len(low) != 2 || low[0].Query != "how do I rotate the api key" || low[0].Reason != "low_confidence" {
		t.Errorf("low = %+v", low)
	}

	n, err := s.PruneQueryLog(ctx, 30*24*time.Hour)
	if err != nil || n != 1 {
		t.Errorf("pruned %d, %v", n, err)
	}
	count, oldest, _ := s.QueryLogStats(ctx)
	if count != 4 || oldest.IsZero() {
		t.Errorf("stats = %d %v", count, oldest)
	}
}
