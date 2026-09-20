# 0003 — Default embeddings: all-MiniLM-L6-v2 in pure Go

- Status: accepted (refines [0001](0001-core-architecture.md) decision 4)
- Date: 2026-09-20

## Context

ADR-0001 chose `all-MiniLM-L6-v2` (384-dim) as the default local embedding
model and left the runtime question open: "bundled ONNX" implies ONNX Runtime,
a large C++ library that must be linked (CGO) or loaded at runtime per
platform, which conflicts with the single-static-binary goal and makes the
release matrix expensive.

We evaluated three routes:

| Route | CGO | Deploy | Speed (256-token chunk) |
|---|---|---|---|
| ONNX Runtime via `yalue/onnxruntime_go` | yes | needs `libonnxruntime` per platform | ~5–10 ms |
| Pure Go ONNX interpreters (`gonnx`, `onnx-go`) | no | single binary | unproven op coverage for BERT |
| **Pure Go BERT encoder + gonum SGEMM** | no | single binary | 315 ms on 12 cores, 890 ms on 2 |

The third route is possible because MiniLM-L6 is a small, fixed architecture:
word/position/type embeddings, six identical layers (multi-head attention,
GELU feed-forward, two LayerNorms), mean pooling, L2 normalisation. A
prototype (`internal/embed/minilm`) reproduces onnxruntime's output with
cosine similarity 1.000000 on mixed English/Turkish/Markdown inputs, and the
WordPiece tokenizer matches the Hugging Face tokenizer id-for-id.

## Decision

1. The default embedding adapter is **`local`**: `all-MiniLM-L6-v2` executed
   by our own pure-Go encoder, with matrix products from `gonum` (BSD-3).
   No ONNX Runtime, no CGO.
2. Weights are the model's `model.safetensors` (fp32, 87 MB) and `vocab.txt`
   from the Hugging Face repository `sentence-transformers/all-MiniLM-L6-v2`
   (Apache-2.0). They are **not embedded in the binary**; on first use
   briefd downloads them into `embeddings.model_dir` and verifies pinned
   SHA-256 digests. `briefd model pull` pre-fetches them for offline or
   image-build use, and any directory containing the two files works
   without network access. This is the one outbound connection SPEC §9
   allows for "the configured embedding adapter".
3. `ollama` and `openai-compatible` adapters remain available and are the
   recommended choice for corpora above ~20K chunks or for GPU hosts.
   `none` (or `embeddings.enabled: false`) keeps BM25-only mode.
4. Vectors are stored per chunk with the model name and content hash;
   changing the model or a chunk's content triggers re-embedding of exactly
   those chunks (SPEC §7).

## Consequences

- Indexing cost: ~50–80 ms per typical 70-token chunk on a laptop, 150–250 ms
  on a 2-vCPU VPS; a 5K-chunk corpus takes minutes, 50K takes hours. The
  work is incremental after the first run and runs in the background of
  `briefd serve`, with BM25 results available immediately.
- Query cost: one short-sentence embedding ≈ 20–30 ms, dominated by the
  first-layer matmuls; well within the 100 ms p95 target together with the
  brute-force scan.
- Correctness is guarded by a reference test that compares against
  onnxruntime output (`BRIEFD_TEST_MODEL_DIR`/`BRIEFD_TEST_REF`), so future
  optimisation (fp16/int8 weights, fused kernels, SIMD) can be validated.
- The `onnx` adapter name from ADR-0001/SPEC §7 is replaced by `local`.
