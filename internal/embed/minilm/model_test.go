package minilm

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

// Reference vectors produced by onnxruntime + the HF tokenizers library
// (see testdata/README.md). Skipped when the model files are absent.
func TestAgainstReference(t *testing.T) {
	dir := os.Getenv("BRIEFD_TEST_MODEL_DIR")
	ref := os.Getenv("BRIEFD_TEST_REF")
	if dir == "" || ref == "" {
		t.Skip("BRIEFD_TEST_MODEL_DIR / BRIEFD_TEST_REF not set")
	}
	var cases []struct {
		Text      string    `json:"text"`
		IDs       []int     `json:"ids"`
		Embedding []float32 `json:"embedding"`
	}
	raw, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		ids := m.Tokenize(c.Text)
		if len(ids) != len(c.IDs) {
			t.Errorf("%q: %d ids, want %d\n got  %v\n want %v", c.Text, len(ids), len(c.IDs), ids, c.IDs)
			continue
		}
		for i := range ids {
			if ids[i] != c.IDs[i] {
				t.Errorf("%q: id[%d] = %d, want %d", c.Text, i, ids[i], c.IDs[i])
				break
			}
		}
		start := time.Now()
		got, err := m.Embed(context.Background(), []string{c.Text})
		if err != nil {
			t.Fatal(err)
		}
		cos := dot(got[0], c.Embedding)
		t.Logf("%.40q: %d tokens, cosine %.6f, %s", c.Text, len(ids), cos, time.Since(start))
		if cos < 0.9995 {
			t.Errorf("%q: cosine to reference = %.6f", c.Text, cos)
		}
	}
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func BenchmarkEmbed256(b *testing.B) {
	dir := os.Getenv("BRIEFD_TEST_MODEL_DIR")
	if dir == "" {
		b.Skip("BRIEFD_TEST_MODEL_DIR not set")
	}
	m, err := Load(dir)
	if err != nil {
		b.Fatal(err)
	}
	text := ""
	for len(m.Tokenize(text)) < MaxSeq {
		text += "Settlement batches are cut at midnight UTC and reconciled against acquirer reports. "
	}
	b.ResetTimer()
	for b.Loop() {
		m.encode(m.Tokenize(text))
	}
	b.ReportMetric(float64(b.Elapsed().Milliseconds())/float64(b.N), "ms/op")
	_ = math.Pi
}
