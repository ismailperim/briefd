# 0002 — SQLite driver and vector search strategy

- Status: accepted (amends [0001](0001-core-architecture.md) decisions 1 and 3)
- Date: 2026-09-20

## Context

ADR-0001 fixed SQLite (FTS5 + WAL) as the only storage engine and named
`sqlite-vec` as the vector search component, with CGO confined to `store/` and
`embed/onnx/`. Before writing the first line of `internal/store` we evaluated
the available Go drivers:

| Driver | CGO | FTS5 | sqlite-vec | Cross-compile |
|---|---|---|---|---|
| `mattn/go-sqlite3` | yes | build tag | via `sqlite-vec-go-bindings/cgo` | needs a C toolchain per target |
| `modernc.org/sqlite` | no | yes | no | trivial |
| `ncruces/go-sqlite3` v0.35 | no | yes (`ext/fts5`) | no (bindings target a 2024 ABI) | trivial |

`ncruces/go-sqlite3` ships SQLite 3.53 translated to Go with `wasm2go`, has a
`database/sql` driver, supports WAL, custom functions and virtual tables, and
depends only on `x/sys`. A smoke test confirmed FTS5 with
`porter unicode61 remove_diacritics 2` and `bm25()` ranking work out of the
box. It requires Go ≥ 1.26.

`sqlite-vec` is a C extension. Loading it into a cgo-free driver is not
possible today, and pulling `mattn` in only for vector search would reintroduce
the per-target C toolchain problem that makes the M5 release matrix
(linux/amd64, linux/arm64, darwin/arm64) painful.

## Decision

1. **SQLite driver: `github.com/ncruces/go-sqlite3`** (MIT). The store opens
   connections through `driver.Open` with `fts5.Register`. As a consequence
   the **minimum Go version becomes 1.26** (the current `oldstable`).
2. **Vector search moves from `sqlite-vec` to a brute-force scan in Go.**
   Embeddings are stored in SQLite (`chunk_vectors(chunk_id, model, dim,
   embedding BLOB)`, L2-normalized float32 little-endian), loaded into an
   in-memory matrix on startup and after each reindex, and scored by dot
   product. Top-k selection uses a bounded heap. This preserves every property
   ADR-0001 wanted from `sqlite-vec` — SQLite as the single store, brute force,
   no ANN index — while removing CGO from the search path entirely.
3. CGO is now confined to `internal/embed/onnx` alone. `internal/store` is
   pure Go.

## Consequences

- `go build` and `goreleaser` cross-compile without a C toolchain for every
  target that does not include the ONNX adapter; the ONNX linking question
  remains open for M3 (see ADR-0001 consequences).
- Memory: 50K chunks × 384 dims × 4 bytes ≈ 77 MB resident when the vector
  cache is fully loaded; typical team corpora (2–5K chunks) need < 10 MB.
  Int8 quantization is an available follow-up if the 300 MB idle budget is
  threatened.
- Latency: 50K × 384 dot products is ~20M multiply-adds, comfortably under the
  100 ms p95 target on 2 vCPUs without SIMD.
- The wasm2go-translated SQLite is somewhat slower than a native build for
  heavy write workloads; indexing is batched in transactions so this is not
  expected to matter. Revisit with numbers if `briefd index` on a large corpus
  becomes a complaint.
- `CLAUDE.md` locked decisions 1 and 3 and `SPEC.md` §5–§7 are updated in the
  same commit to match.
