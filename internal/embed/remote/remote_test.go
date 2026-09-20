package remote

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := len(req["input"].([]any))
		vecs := make([][]float32, n)
		for i := range vecs {
			vecs[i] = []float32{3, 4}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	}))
	defer srv.Close()
	e := NewOllama(srv.URL, "m")
	out, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || math.Abs(float64(out[0][0])-0.6) > 1e-6 || e.Dim() != 2 || e.Name() != "ollama/m" {
		t.Errorf("unexpected: %v dim=%d name=%s", out, e.Dim(), e.Name())
	}
}

func TestOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" || r.URL.Path != "/v1/embeddings" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
			return
		}
		// Out-of-order indices must be handled.
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer srv.Close()
	e := NewOpenAI(srv.URL+"/v1", "m", "k")
	out, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if out[0][0] != 1 || out[1][1] != 1 {
		t.Errorf("unexpected vectors %v", out)
	}
	if _, err := NewOpenAI(srv.URL+"/v1", "m", "bad").Embed(context.Background(), []string{"a"}); err == nil {
		t.Error("expected auth error")
	}
}
