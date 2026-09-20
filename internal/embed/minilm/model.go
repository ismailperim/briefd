// Package minilm is a dependency-free (no CGO, no runtime) implementation
// of the sentence-transformers/all-MiniLM-L6-v2 encoder: a 6-layer BERT
// with mean pooling and L2 normalisation, producing 384-dim embeddings.
// Weights are loaded from the model's safetensors file (ADR-0003).
package minilm

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas32"
)

// Model constants for all-MiniLM-L6-v2.
const (
	ModelName    = "all-MiniLM-L6-v2"
	Dim          = 384
	layers       = 6
	heads        = 12
	headDim      = Dim / heads
	intermediate = 1536
	vocabSize    = 30522
	maxPositions = 512
	// MaxSeq is the sequence length sentence-transformers uses for this
	// model (the underlying BERT supports 512).
	MaxSeq       = 256
	layerNormEps = 1e-12
)

type layer struct {
	wq, bq, wk, bk, wv, bv []float32 // [Dim×Dim], [Dim]
	wo, bo                 []float32
	ln1g, ln1b             []float32
	wi, bi                 []float32 // [intermediate×Dim], [intermediate]
	wo2, bo2               []float32 // [Dim×intermediate], [Dim]
	ln2g, ln2b             []float32
}

// Model holds the weights and tokenizer. It is safe for concurrent use.
type Model struct {
	tok      *tokenizer
	wordEmb  []float32 // [vocab×Dim]
	posEmb   []float32 // [maxPositions×Dim]
	typeEmb  []float32 // [2×Dim] (only row 0 is used)
	embLNg   []float32
	embLNb   []float32
	layers   [layers]layer
	parallel int
}

// Load reads model.safetensors and vocab.txt from dir.
func Load(dir string) (*Model, error) {
	st, err := openSafetensors(filepath.Join(dir, "model.safetensors"))
	if err != nil {
		return nil, err
	}
	vocab, err := loadVocab(filepath.Join(dir, "vocab.txt"))
	if err != nil {
		return nil, err
	}
	if len(vocab) != vocabSize {
		return nil, fmt.Errorf("vocab.txt has %d entries, want %d", len(vocab), vocabSize)
	}
	tok, err := newTokenizer(vocab, MaxSeq)
	if err != nil {
		return nil, err
	}

	m := &Model{tok: tok, parallel: runtime.GOMAXPROCS(0)}
	var firstErr error
	get := func(name string, shape ...int) []float32 {
		if firstErr != nil {
			return nil
		}
		v, err := st.f32(name, shape...)
		if err != nil {
			firstErr = err
		}
		return v
	}
	m.wordEmb = get("embeddings.word_embeddings.weight", vocabSize, Dim)
	m.posEmb = get("embeddings.position_embeddings.weight", maxPositions, Dim)
	m.typeEmb = get("embeddings.token_type_embeddings.weight", 2, Dim)
	m.embLNg = get("embeddings.LayerNorm.weight", Dim)
	m.embLNb = get("embeddings.LayerNorm.bias", Dim)
	for i := range layers {
		p := fmt.Sprintf("encoder.layer.%d.", i)
		l := &m.layers[i]
		l.wq, l.bq = get(p+"attention.self.query.weight", Dim, Dim), get(p+"attention.self.query.bias", Dim)
		l.wk, l.bk = get(p+"attention.self.key.weight", Dim, Dim), get(p+"attention.self.key.bias", Dim)
		l.wv, l.bv = get(p+"attention.self.value.weight", Dim, Dim), get(p+"attention.self.value.bias", Dim)
		l.wo, l.bo = get(p+"attention.output.dense.weight", Dim, Dim), get(p+"attention.output.dense.bias", Dim)
		l.ln1g, l.ln1b = get(p+"attention.output.LayerNorm.weight", Dim), get(p+"attention.output.LayerNorm.bias", Dim)
		l.wi, l.bi = get(p+"intermediate.dense.weight", intermediate, Dim), get(p+"intermediate.dense.bias", intermediate)
		l.wo2, l.bo2 = get(p+"output.dense.weight", Dim, intermediate), get(p+"output.dense.bias", Dim)
		l.ln2g, l.ln2b = get(p+"output.LayerNorm.weight", Dim), get(p+"output.LayerNorm.bias", Dim)
	}
	if firstErr != nil {
		return nil, fmt.Errorf("loading %s: %w", dir, firstErr)
	}
	return m, nil
}

// Tokenize exposes the tokenizer for tests and diagnostics.
func (m *Model) Tokenize(text string) []int { return m.tok.encode(text) }

// Embed encodes texts concurrently and returns L2-normalised vectors.
func (m *Model) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	sem := make(chan struct{}, m.parallel)
	var wg sync.WaitGroup
	for i, t := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t string) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = m.encode(m.tok.encode(t))
		}(i, t)
	}
	wg.Wait()
	return out, ctx.Err()
}

// encode runs the transformer over one token sequence and mean-pools.
func (m *Model) encode(ids []int) []float32 {
	n := len(ids)
	x := make([]float32, n*Dim)
	for t, id := range ids {
		row := x[t*Dim : (t+1)*Dim]
		w := m.wordEmb[id*Dim : (id+1)*Dim]
		p := m.posEmb[t*Dim : (t+1)*Dim]
		for j := range row {
			row[j] = w[j] + p[j] + m.typeEmb[j]
		}
	}
	layerNorm(x, n, m.embLNg, m.embLNb)

	// Scratch buffers reused across layers.
	q, k, v := make([]float32, n*Dim), make([]float32, n*Dim), make([]float32, n*Dim)
	ctxb := make([]float32, n*Dim)
	attn := make([]float32, n*Dim)
	scores := make([]float32, n*n)
	inter := make([]float32, n*intermediate)
	ffn := make([]float32, n*Dim)
	scale := float32(1 / math.Sqrt(headDim))

	for li := range m.layers {
		l := &m.layers[li]
		linear(x, n, Dim, l.wq, l.bq, Dim, q)
		linear(x, n, Dim, l.wk, l.bk, Dim, k)
		linear(x, n, Dim, l.wv, l.bv, Dim, v)

		for h := range heads {
			off := h * headDim
			qh := blas32.General{Rows: n, Cols: headDim, Stride: Dim, Data: q[off:]}
			kh := blas32.General{Rows: n, Cols: headDim, Stride: Dim, Data: k[off:]}
			vh := blas32.General{Rows: n, Cols: headDim, Stride: Dim, Data: v[off:]}
			s := blas32.General{Rows: n, Cols: n, Stride: n, Data: scores}
			blas32.Gemm(blas.NoTrans, blas.Trans, scale, qh, kh, 0, s)
			softmaxRows(scores, n)
			c := blas32.General{Rows: n, Cols: headDim, Stride: Dim, Data: ctxb[off:]}
			blas32.Gemm(blas.NoTrans, blas.NoTrans, 1, s, vh, 0, c)
		}

		linear(ctxb, n, Dim, l.wo, l.bo, Dim, attn)
		addInPlace(x, attn)
		layerNorm(x, n, l.ln1g, l.ln1b)

		linear(x, n, Dim, l.wi, l.bi, intermediate, inter)
		gelu(inter)
		linear(inter, n, intermediate, l.wo2, l.bo2, Dim, ffn)
		addInPlace(x, ffn)
		layerNorm(x, n, l.ln2g, l.ln2b)
	}

	// Mean pooling over all tokens (no padding is ever present) and L2 norm.
	out := make([]float32, Dim)
	for t := range n {
		row := x[t*Dim : (t+1)*Dim]
		for j := range out {
			out[j] += row[j]
		}
	}
	var norm float64
	for j := range out {
		out[j] /= float32(n)
		norm += float64(out[j]) * float64(out[j])
	}
	if norm > 0 {
		inv := float32(1 / math.Sqrt(norm))
		for j := range out {
			out[j] *= inv
		}
	}
	return out
}

// linear computes out = in · Wᵀ + b for in [n×inDim], W [outDim×inDim].
func linear(in []float32, n, inDim int, w, b []float32, outDim int, out []float32) {
	a := blas32.General{Rows: n, Cols: inDim, Stride: inDim, Data: in}
	wm := blas32.General{Rows: outDim, Cols: inDim, Stride: inDim, Data: w}
	c := blas32.General{Rows: n, Cols: outDim, Stride: outDim, Data: out}
	blas32.Gemm(blas.NoTrans, blas.Trans, 1, a, wm, 0, c)
	for t := range n {
		row := out[t*outDim : (t+1)*outDim]
		for j := range row {
			row[j] += b[j]
		}
	}
}

func softmaxRows(s []float32, n int) {
	for i := range n {
		row := s[i*n : (i+1)*n]
		maxv := row[0]
		for _, v := range row[1:] {
			if v > maxv {
				maxv = v
			}
		}
		var sum float32
		for j, v := range row {
			e := float32(math.Exp(float64(v - maxv)))
			row[j] = e
			sum += e
		}
		inv := 1 / sum
		for j := range row {
			row[j] *= inv
		}
	}
}

func layerNorm(x []float32, n int, g, b []float32) {
	for t := range n {
		row := x[t*Dim : (t+1)*Dim]
		var mean float64
		for _, v := range row {
			mean += float64(v)
		}
		mean /= Dim
		var variance float64
		for _, v := range row {
			d := float64(v) - mean
			variance += d * d
		}
		variance /= Dim
		inv := float32(1 / math.Sqrt(variance+layerNormEps))
		for j, v := range row {
			row[j] = (v-float32(mean))*inv*g[j] + b[j]
		}
	}
}

func addInPlace(x, y []float32) {
	for i := range x {
		x[i] += y[i]
	}
}

// gelu applies the exact (erf) GELU used by BERT.
func gelu(x []float32) {
	for i, v := range x {
		x[i] = float32(0.5 * float64(v) * (1 + math.Erf(float64(v)/math.Sqrt2)))
	}
}
