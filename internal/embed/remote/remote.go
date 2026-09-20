// Package remote implements Embedders backed by HTTP embedding services:
// Ollama's native API and the OpenAI-compatible /embeddings endpoint.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Ollama calls POST {url}/api/embed.
type Ollama struct {
	url, model string
	client     *http.Client
	dim        int
	mu         sync.Mutex
}

// NewOllama returns an Ollama embedder for model at base URL url.
func NewOllama(url, model string) *Ollama {
	return &Ollama{url: strings.TrimRight(url, "/"), model: model, client: &http.Client{Timeout: 5 * time.Minute}}
}

// Name implements Embedder.
func (o *Ollama) Name() string { return "ollama/" + o.model }

// Dim implements Embedder. It is learned from the first response (0 before
// that); callers that need it up front should embed a probe string.
func (o *Ollama) Dim() int { o.mu.Lock(); defer o.mu.Unlock(); return o.dim }

// Embed implements Embedder.
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var resp struct {
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := postJSON(ctx, o.client, o.url+"/api/embed", "", map[string]any{"model": o.model, "input": texts}, &resp); err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("ollama embed: %s", resp.Error)
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed: got %d vectors for %d inputs", len(resp.Embeddings), len(texts))
	}
	for _, v := range resp.Embeddings {
		normalize(v)
	}
	o.mu.Lock()
	o.dim = len(resp.Embeddings[0])
	o.mu.Unlock()
	return resp.Embeddings, nil
}

// EmbedQuery implements Embedder (no query prefix for Ollama models).
func (o *Ollama) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	out, err := o.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// OpenAI calls POST {url}/embeddings with a bearer token.
type OpenAI struct {
	url, model, key string
	client          *http.Client
	dim             int
	mu              sync.Mutex
}

// NewOpenAI returns an embedder for any OpenAI-compatible server.
func NewOpenAI(url, model, key string) *OpenAI {
	return &OpenAI{url: strings.TrimRight(url, "/"), model: model, key: key, client: &http.Client{Timeout: 5 * time.Minute}}
}

// Name implements Embedder.
func (o *OpenAI) Name() string { return "openai/" + o.model }

// Dim implements Embedder (0 until the first successful call).
func (o *OpenAI) Dim() int { o.mu.Lock(); defer o.mu.Unlock(); return o.dim }

// Embed implements Embedder.
func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var resp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := postJSON(ctx, o.client, o.url+"/embeddings", o.key, map[string]any{"model": o.model, "input": texts}, &resp); err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("openai embed: %s", resp.Error.Message)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("openai embed: got %d vectors for %d inputs", len(resp.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("openai embed: index %d out of range", d.Index)
		}
		normalize(d.Embedding)
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("openai embed: missing vector for input %d", i)
		}
	}
	o.mu.Lock()
	o.dim = len(out[0])
	o.mu.Unlock()
	return out, nil
}

// EmbedQuery implements Embedder.
func (o *OpenAI) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	out, err := o.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

func postJSON(ctx context.Context, client *http.Client, url, bearer string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return fmt.Errorf("%s: %s", resp.Status, msg)
	}
	return json.Unmarshal(data, out)
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}
