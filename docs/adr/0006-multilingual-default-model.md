# 0006 — Multilingual embeddings by default (multilingual-e5-small)

- Status: accepted (refines [0003](0003-embeddings-pure-go-minilm.md))
- Date: 2026-09-20

## Context

ADR-0003 made `all-MiniLM-L6-v2` the default local model. It is trained almost
exclusively on English, and the first golden set showed it: Turkish queries
scored Recall@5 0.69 in hybrid mode against 1.00 for English keyword queries.
briefd's own author works in Turkish, and most engineering teams outside the
Anglosphere write knowledge in their language with English terms mixed in, so
"works in your language out of the box" is a core promise, not an extra.

Options: a remote multilingual model via the Ollama/OpenAI adapters (already
supported, but an external service), or a multilingual model in the same
pure-Go encoder. `intfloat/multilingual-e5-small` (118M parameters, 384-dim,
12 layers, XLM-R vocabulary, trained on 100+ languages) shares MiniLM's BERT
architecture; only the tokenizer differs (SentencePiece Unigram instead of
WordPiece), and E5 expects `query: ` / `passage: ` prefixes.

## Decision

1. The pure-Go encoder becomes **spec-driven** (`internal/embed/minilm/spec.go`)
   and gains a SentencePiece Unigram tokenizer (Viterbi over the vocabulary in
   `tokenizer.json`, NFKC + whitespace collapsing + Metaspace). Both models
   reproduce onnxruntime's output to cosine 1.000000 on English, Turkish,
   Japanese, Russian and emoji inputs, including whitespace edge cases.
2. The `Embedder` interface gains `EmbedQuery`, so asymmetric models can
   apply the query prefix while documents get the passage prefix.
3. **`multilingual-e5-small` is the default** (`embeddings.model`);
   `all-MiniLM-L6-v2` stays available for English-only deployments that want
   the smaller download and ~2.5× faster indexing.
4. The 384 MB vocabulary matrix is memory-mapped and read row by row, so the
   larger model does not blow the idle-memory target.
5. A Turkish corpus (`testdata/knowledge-tr`, 12 documents) and golden set
   (`eval/golden/queries-tr.yaml`, 30 queries) join the eval, gated by
   `eval/thresholds-tr.yaml` in CI.

## Evidence

English golden set (47 queries, hybrid):

| model | R@5 | R@10 | MRR | Turkish queries R@5 | paraphrase R@5 |
|---|---|---|---|---|---|
| all-MiniLM-L6-v2 | 0.809 | 0.936 | 0.709 | 0.688 | 0.711 |
| **multilingual-e5-small** | **0.830** | 0.926 | **0.746** | **0.938** | 0.605 |

Turkish golden set (30 queries, hybrid):

| model | R@5 | R@10 | MRR | paraphrase R@5 |
|---|---|---|---|---|
| all-MiniLM-L6-v2 | 0.850 | 0.850 | 0.669 | 0.625 |
| **multilingual-e5-small** | **0.933** | **1.000** | **0.847** | **0.833** |

## Consequences

- First start downloads 470 MB instead of 87 MB; indexing costs ~130 ms per
  section on a laptop instead of ~55 ms (the sample corpus: 29 s vs 12 s).
  Queries stay well under 100 ms.
- English paraphrase retrieval regresses (R@5 0.71 → 0.61 in hybrid; R@10
  0.92 → 0.84). Candidate fixes to measure: re-tuning the RRF weights per
  model, and embedding the document title alongside the section.
- Existing indexes re-embed automatically: vectors are keyed by model name.
- Because the multilingual vocabulary contains no Turkish-specific stemming
  on the BM25 side, a light Turkish suffix stripper for FTS5 remains a
  follow-up; the vector side now carries Turkish semantics.
