package search

import "sort"

// RRFK is the reciprocal-rank-fusion constant from SPEC §5.
const RRFK = 60

// Fused is an id with its fused score.
type Fused struct {
	ID    string
	Score float64
}

// RankedList is one retriever's output for weighted fusion.
type RankedList struct {
	IDs []string
	// Weight scales this list's contribution (1 = plain RRF).
	Weight float64
	// Offset shifts ranks, so a list that continues another one (e.g.
	// BM25 any-term matches after all-term matches) keeps lower ranks.
	Offset int
}

// RRF fuses ranked id lists with reciprocal rank fusion:
// score(id) = Σ_lists 1 / (k + rank), rank starting at 1. Ids absent from a
// list contribute nothing for it. Output is sorted by score descending, ties
// broken by id for determinism.
func RRF(k int, lists ...[]string) []Fused {
	weighted := make([]RankedList, len(lists))
	for i, l := range lists {
		weighted[i] = RankedList{IDs: l, Weight: 1}
	}
	return RRFWeighted(k, weighted...)
}

// RRFWeighted is RRF with per-list weights and rank offsets:
// score(id) = Σ_lists weight / (k + offset + rank).
func RRFWeighted(k int, lists ...RankedList) []Fused {
	scores := map[string]float64{}
	for _, list := range lists {
		for rank, id := range list.IDs {
			scores[id] += list.Weight / float64(k+list.Offset+rank+1)
		}
	}
	out := make([]Fused, 0, len(scores))
	for id, s := range scores {
		out = append(out, Fused{ID: id, Score: s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}
