package minilm

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

type refCase struct {
	Text      string    `json:"text"`
	IDs       []int     `json:"ids"`
	Embedding []float32 `json:"embedding"`
}

// Reference vectors produced by onnxruntime + the HF tokenizers library.
// Skipped when the model files are absent:
//
//	BRIEFD_TEST_MODEL_DIR=<dir> BRIEFD_TEST_REF=<ref.json> [BRIEFD_TEST_MODEL=<spec name>]
func TestAgainstReference(t *testing.T) {
	dir, ref := os.Getenv("BRIEFD_TEST_MODEL_DIR"), os.Getenv("BRIEFD_TEST_REF")
	if dir == "" || ref == "" {
		t.Skip("BRIEFD_TEST_MODEL_DIR / BRIEFD_TEST_REF not set")
	}
	spec, err := SpecFor(os.Getenv("BRIEFD_TEST_MODEL"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []refCase
	raw, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	m, err := Load(spec, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, c := range cases {
		// Reference texts already carry any query:/passage: prefix, so
		// tokenize and encode them raw.
		ids := m.Tokenize(c.Text)
		if len(ids) != len(c.IDs) {
			t.Errorf("%.50q: %d ids, want %d\n got  %v\n want %v", c.Text, len(ids), len(c.IDs), ids, c.IDs)
			continue
		}
		for i := range ids {
			if ids[i] != c.IDs[i] {
				t.Errorf("%.50q: id[%d] = %d, want %d\n got  %v\n want %v", c.Text, i, ids[i], c.IDs[i], ids, c.IDs)
				break
			}
		}
		start := time.Now()
		got := m.encode(ids)
		cos := dot(got, c.Embedding)
		t.Logf("%.40q: %d tokens, cosine %.6f, %s", c.Text, len(ids), cos, time.Since(start))
		if cos < 0.9995 {
			t.Errorf("%.50q: cosine to reference = %.6f", c.Text, cos)
		}
	}
	// The public API adds the prefixes.
	if _, err := m.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EmbedQuery(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func BenchmarkEmbed(b *testing.B) {
	dir := os.Getenv("BRIEFD_TEST_MODEL_DIR")
	if dir == "" {
		b.Skip("BRIEFD_TEST_MODEL_DIR not set")
	}
	spec, _ := SpecFor(os.Getenv("BRIEFD_TEST_MODEL"))
	m, err := Load(spec, dir)
	if err != nil {
		b.Fatal(err)
	}
	defer m.Close()
	text := "Settlement batches are cut at midnight UTC and reconciled against acquirer reports. "
	for len(m.Tokenize(text)) < 70 {
		text += "Settlement batches are cut at midnight UTC and reconciled against acquirer reports. "
	}
	b.ResetTimer()
	for b.Loop() {
		m.encode(m.Tokenize(text))
	}
	b.ReportMetric(float64(b.Elapsed().Milliseconds())/float64(b.N), "ms/op")
}
