package search

import (
	"math"
	"testing"
)

func TestRRF(t *testing.T) {
	tests := []struct {
		name  string
		lists [][]string
		want  []string
	}{
		{"empty", nil, nil},
		{"single list keeps order", [][]string{{"a", "b", "c"}}, []string{"a", "b", "c"}},
		{"agreement wins", [][]string{{"a", "b", "c"}, {"b", "a", "d"}}, []string{"a", "b", "c", "d"}},
		{"present in both beats top of one", [][]string{{"x", "b"}, {"y", "b"}}, []string{"b", "x", "y"}},
		{"ties broken by id", [][]string{{"z"}, {"a"}}, []string{"a", "z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RRF(RRFK, tt.lists...)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i].ID != tt.want[i] {
					t.Errorf("position %d = %s, want %s (%v)", i, got[i].ID, tt.want[i], got)
				}
			}
		})
	}
	got := RRF(60, []string{"a"}, []string{"a"})
	if want := 2.0 / 61; math.Abs(got[0].Score-want) > 1e-12 {
		t.Errorf("score = %v, want %v", got[0].Score, want)
	}
}
