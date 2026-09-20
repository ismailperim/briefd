# 0001 — Core architecture

- Status: accepted
- Date: 2026-09-20

## Context

briefd is a self-hosted context compiler for AI coding teams: a git repository of
Markdown knowledge is indexed and served to coding agents over MCP as
token-budgeted context bundles (see [`SPEC.md`](../../SPEC.md)). The primary
deployment target is a single container on a small VPS or homelab box (2–4 GB
RAM), operated by a team that does not want to run a database cluster, a vector
store, or a message queue to get value out of the tool.

The decisions below were made after research and before any code was written.
They are deliberately conservative: boring, well-understood building blocks over
best-in-class components that would each add an operational dependency.

## Decision

1. **Language: Go (≥ 1.23).** One statically linked binary, trivial
   cross-compilation, mature HTTP/SQLite/git ecosystems, and an official MCP SDK
   (`modelcontextprotocol/go-sdk`, v1.x). Rejected: Python (packaging and
   memory footprint), Rust (slower iteration for a solo-started project).

2. **Storage: SQLite only, WAL mode.** A single `.db` file holds documents,
   chunks, embeddings, bundle cache, usage events and sync state. Rejected:
   Postgres/pgvector (second service), embedded KV stores (no full-text search).

3. **Search: hybrid FTS5 (BM25) + sqlite-vec (brute-force cosine), fused with
   Reciprocal Rank Fusion, k = 60.** Top-50 from each side. Brute-force vector
   search is adequate for the target corpus size (≤ 50K chunks × 384 dims) and
   removes any ANN index to build, tune or persist. BM25-only mode stays
   available (`embeddings.enabled=false`) so the service degrades gracefully
   without an embedding backend. Rejected: HNSW/ANN libraries (unnecessary at
   this scale, extra state to keep consistent).

4. **Embeddings: local ONNX by default (`all-MiniLM-L6-v2`, 384-dim), behind an
   `Embedder` interface** with `ollama` and `openai-compatible` adapters.
   Default works offline on CPU with no API key. The interface exists from day
   one because multiple implementations are specified, not as speculation.

5. **Git is the source of truth; the index is a disposable cache.** The service
   must rebuild the entire index from a fresh clone. Nothing that constitutes
   knowledge is stored only in SQLite.

6. **Agents never write to the index.** `propose_update` produces a git
   branch/commit (and optionally a PR). Merge → sync → reindex is the only write
   path, which keeps human review in the loop and makes every change auditable.

7. **Single container, zero external services.** No Redis, Postgres,
   Elasticsearch, Qdrant or queues — ever. Caching is in-process LRU plus SQLite
   tables.

8. **Token budget is a hard constraint.** Every context-returning API accepts
   `max_tokens` and never exceeds it (enforced with 5% headroom on top of an
   approximate tokenizer). Truncation policy lives in the bundle packer only.

9. **License: Apache-2.0.** No AGPL/GPL/SSPL dependencies; licenses are checked
   before any dependency is added.

Smaller decisions taken with the skeleton:

- **CLI uses the standard library `flag` package** with a hand-rolled
  subcommand dispatcher. Four subcommands do not justify a CLI framework; this
  can be revisited by a later ADR if flag handling becomes unwieldy.
- **Module path is `github.com/ismailperim/briefd`.** If the project moves to an
  organization, the path changes in one place before v1.0.

## Consequences

- CGO is required (SQLite, FTS5, sqlite-vec, ONNX runtime) and is confined to
  `internal/store` and `internal/embed/onnx`; everything else is pure Go.
  Cross-compiling CGO for the release matrix (linux/amd64, linux/arm64,
  darwin/arm64) is a known cost and will be addressed in the M5 release ADR.
- Bundling an ONNX runtime in a "single static binary" is in tension with
  decision 1; the concrete linking strategy (static link vs. runtime-loaded
  library with BM25-only fallback) is deferred to an ADR in M3.
- Retrieval quality is a product feature, so any change to chunking, embeddings
  or fusion must be accompanied by before/after numbers from `briefd eval`.
- Horizontal scaling and multi-node deployments are explicitly out of scope for
  v0.x; the design optimizes for one box.
