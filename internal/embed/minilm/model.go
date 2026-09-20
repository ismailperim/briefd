// Package minilm is a dependency-free (no CGO, no runtime) implementation
// of BERT-family sentence encoders: all-MiniLM-L6-v2 (English) and
// multilingual-e5-small (100+ languages), producing 384-dim mean-pooled,
// L2-normalized embeddings from safetensors weights (ADR-0003, ADR-0006).
package minilm

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas32"
)

// Dim is the embedding size shared by all supported models.
const Dim = 384

type layer struct {
	wq, bq, wk, bk, wv, bv []float32
	wo, bo                 []float32
	ln1g, ln1b             []float32
	wi, bi                 []float32
	wo2, bo2               []float32
	ln2g, ln2b             []float32
}

type tokenizer interface {
	encode(text string) []int
}

// Model holds the weights and tokenizer. It is safe for concurrent use.
type Model struct {
	spec     Spec
	tok      tokenizer
	wordEmb  []byte // mmapped [vocab×Dim] float32 LE, read row by row
	posEmb   []float32
	typeEmb  []float32
	embLNg   []float32
	embLNb   []float32
	layers   []layer
	parallel int
	closeFn  func() error
}

// Load reads the model named by spec from dir.
func Load(spec Spec, dir string) (*Model, error) {
	st, err := openSafetensors(filepath.Join(dir, "model.safetensors"))
	if err != nil {
		return nil, err
	}
	m := &Model{spec: spec, parallel: runtime.GOMAXPROCS(0), closeFn: st.close}

	switch spec.Tokenizer {
	case WordPiece:
		vocab, err := loadVocab(filepath.Join(dir, "vocab.txt"))
		if err != nil {
			return nil, err
		}
		if len(vocab) != spec.VocabSize {
			return nil, fmt.Errorf("vocab.txt has %d entries, want %d", len(vocab), spec.VocabSize)
		}
		if m.tok, err = newTokenizer(vocab, spec.MaxSeq); err != nil {
			return nil, err
		}
	case Unigram:
		if m.tok, err = loadUnigram(filepath.Join(dir, "tokenizer.json"), spec.MaxSeq); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported tokenizer %q", spec.Tokenizer)
	}

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
	if m.wordEmb, err = st.raw("embeddings.word_embeddings.weight", spec.VocabSize, Dim); err != nil {
		return nil, fmt.Errorf("loading %s: %w", dir, err)
	}
	m.posEmb = get("embeddings.position_embeddings.weight", spec.MaxPositions, Dim)
	m.typeEmb = get("embeddings.token_type_embeddings.weight", 2, Dim)
	m.embLNg = get("embeddings.LayerNorm.weight", Dim)
	m.embLNb = get("embeddings.LayerNorm.bias", Dim)
	m.layers = make([]layer, spec.Layers)
	for i := range spec.Layers {
		p := fmt.Sprintf("encoder.layer.%d.", i)
		l := &m.layers[i]
		l.wq, l.bq = get(p+"attention.self.query.weight", Dim, Dim), get(p+"attention.self.query.bias", Dim)
		l.wk, l.bk = get(p+"attention.self.key.weight", Dim, Dim), get(p+"attention.self.key.bias", Dim)
		l.wv, l.bv = get(p+"attention.self.value.weight", Dim, Dim), get(p+"attention.self.value.bias", Dim)
		l.wo, l.bo = get(p+"attention.output.dense.weight", Dim, Dim), get(p+"attention.output.dense.bias", Dim)
		l.ln1g, l.ln1b = get(p+"attention.output.LayerNorm.weight", Dim), get(p+"attention.output.LayerNorm.bias", Dim)
		l.wi, l.bi = get(p+"intermediate.dense.weight", spec.Intermediate, Dim), get(p+"intermediate.dense.bias", spec.Intermediate)
		l.wo2, l.bo2 = get(p+"output.dense.weight", Dim, spec.Intermediate), get(p+"output.dense.bias", Dim)
		l.ln2g, l.ln2b = get(p+"output.LayerNorm.weight", Dim), get(p+"output.LayerNorm.bias", Dim)
	}
	if firstErr != nil {
		return nil, fmt.Errorf("loading %s: %w", dir, firstErr)
	}
	return m, nil
}

// Spec returns the model's specification.
func (m *Model) Spec() Spec { return m.spec }

// Close releases the mapped weights.
func (m *Model) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

// Tokenize exposes the tokenizer for tests and diagnostics (no prefix added).
func (m *Model) Tokenize(text string) []int { return m.tok.encode(text) }

// Embed encodes documents (passages) concurrently and returns L2-normalized
// vectors.
func (m *Model) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return m.embedAll(ctx, texts, m.spec.PassagePrefix)
}

// EmbedQuery encodes a search query, applying the model's query prefix.
func (m *Model) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	out, err := m.embedAll(ctx, []string{text}, m.spec.QueryPrefix)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

func (m *Model) embedAll(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
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
			out[i] = m.encode(m.tok.encode(prefix + t))
		}(i, t)
	}
	wg.Wait()
	return out, ctx.Err()
}

func (m *Model) wordRow(id int) []float32 {
	row := make([]float32, Dim)
	b := m.wordEmb[id*Dim*4 : (id+1)*Dim*4]
	for j := range row {
		row[j] = math.Float32frombits(binary.LittleEndian.Uint32(b[j*4:]))
	}
	return row
}

// encode runs the transformer over one token sequence and mean-pools.
func (m *Model) encode(ids []int) []float32 {
	n := len(ids)
	heads := m.spec.Heads
	headDim := Dim / heads
	inter := m.spec.Intermediate
	eps := m.spec.LayerNormEps

	x := make([]float32, n*Dim)
	for t, id := range ids {
		row := x[t*Dim : (t+1)*Dim]
		w := m.wordRow(id)
		p := m.posEmb[t*Dim : (t+1)*Dim]
		for j := range row {
			row[j] = w[j] + p[j] + m.typeEmb[j]
		}
	}
	layerNorm(x, n, m.embLNg, m.embLNb, eps)

	q, k, v := make([]float32, n*Dim), make([]float32, n*Dim), make([]float32, n*Dim)
	ctxb := make([]float32, n*Dim)
	attn := make([]float32, n*Dim)
	scores := make([]float32, n*n)
	interBuf := make([]float32, n*inter)
	ffn := make([]float32, n*Dim)
	scale := float32(1 / math.Sqrt(float64(headDim)))

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
		layerNorm(x, n, l.ln1g, l.ln1b, eps)

		linear(x, n, Dim, l.wi, l.bi, inter, interBuf)
		gelu(interBuf)
		linear(interBuf, n, inter, l.wo2, l.bo2, Dim, ffn)
		addInPlace(x, ffn)
		layerNorm(x, n, l.ln2g, l.ln2b, eps)
	}

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

func layerNorm(x []float32, n int, g, b []float32, eps float64) {
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
		inv := float32(1 / math.Sqrt(variance+eps))
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
