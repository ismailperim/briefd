package tokenizer

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCount(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		min, max int // acceptable range, loosely calibrated on cl100k
	}{
		{"empty", "", 0, 0},
		{"single word", "hello", 1, 1},
		{"short sentence", "The quick brown fox jumps over the lazy dog.", 9, 12},
		{"prose", "Retries use exponential backoff with full jitter, capped at five attempts and thirty seconds total.", 17, 24},
		{"code", `if err != nil { return fmt.Errorf("indexing %s: %w", path, err) }`, 18, 28},
		{"numbers", "Order 1234567 shipped 2026-09-20", 8, 12},
		{"turkish", "Ödeme iadesi süreci ve ters ibraz kuralları", 10, 20},
		{"newlines", "a\n\n\nb", 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Count(tt.in)
			if got < tt.min || got > tt.max {
				t.Errorf("Count(%q) = %d, want in [%d, %d]", tt.in, got, tt.min, tt.max)
			}
		})
	}
}

// Appending text must never reduce the estimate: budget packers rely on this.
func TestCountMonotonic(t *testing.T) {
	rng := rand.New(rand.NewSource(1)) // deterministic test data
	alphabet := []rune("abcdefghijklmnopqrstuvwxyz ĞÜŞİÖÇğüşıöç0123456789 \n.,;:!?()[]{}-_/\\'\"#*`")
	for range 500 {
		var sb strings.Builder
		prev := 0
		for range rng.Intn(200) {
			sb.WriteRune(alphabet[rng.Intn(len(alphabet))])
			cur := Count(sb.String())
			if cur < prev {
				t.Fatalf("Count decreased from %d to %d after appending to %q", prev, cur, sb.String())
			}
			prev = cur
		}
	}
}

func BenchmarkCount(b *testing.B) {
	s := strings.Repeat("The retry policy applies exponential backoff with jitter. ", 200)
	b.SetBytes(int64(len(s)))
	for b.Loop() {
		Count(s)
	}
}
