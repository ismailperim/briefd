# PROMPTS.md — Claude Code Session Plan

How to run this project with Claude Code: one milestone ≈ 1–3 sessions. Each session is a
vertical slice ending in something runnable. Start every session by letting Claude read
`CLAUDE.md` + `SPEC.md` (they load automatically / via reference), then paste the
session prompt. Keep sessions scoped — don't let a session bleed into the next milestone.

---

## Session 0 — Skeleton

> Read CLAUDE.md and SPEC.md fully. Initialize the Go module and the repo layout from
> CLAUDE.md (only the packages needed for M1 — do not scaffold empty packages). Set up:
> golangci-lint config, Makefile (build/test/lint), Apache-2.0 LICENSE, minimal README
> with the one-line pitch, docs/adr/0001-core-architecture.md recording the locked
> decisions (Go, SQLite+FTS5+sqlite-vec, RRF k=60, ONNX default, git-as-truth,
> single-container). Acceptance: `make build && make test && make lint` all pass.

## Session 1 — M1: Ingest + FTS5 + CLI search

> Implement SPEC §4 (ingestion) and the FTS5 half of §5 for a LOCAL directory first (git
> sync comes in M5; take a `--source ./path` flag). Deliver: markdown walker +
> front-matter parsing, heading-based chunker per SPEC §4.3 with table-driven tests,
> SQLite schema from SPEC §6 (documents, chunks, chunks_fts, sync_state), `briefd index`
> and a temporary `briefd search "<query>"` CLI command printing BM25 results.
> Acceptance: index a sample knowledge repo (create `testdata/knowledge/` with realistic
> domain/conventions/projects content), search returns sensible ranked chunks, all
> chunking tests pass.

## Session 2 — M2: MCP server (first wow)

> Implement `internal/mcpserver` with the official MCP Go SDK, streamable HTTP transport,
> bearer auth. Tools: `search_context` (BM25-only for now, but with max_tokens
> enforcement via the tokenizer package), `get_document`, `list_scopes`. Wire `briefd
> serve`. Write an integration test that starts the server and calls tools over HTTP.
> Acceptance: from a real Claude Code session, `/mcp` connects and `search_context`
> returns chunks from the sample repo within the token budget. Document the Claude Code
> connection snippet in README.

## Session 3 — M3: Embeddings + hybrid + eval

> Implement the Embedder interface with the ONNX all-MiniLM-L6-v2 adapter (CGO confined
> per CLAUDE.md) and the Ollama adapter. Add chunk_vectors (sqlite-vec), content-hash
> skip, model-name invalidation (SPEC §7). Implement RRF fusion (pure function,
> table-driven tests). Build `briefd eval` + `eval/golden/` with 30+ queries against
> testdata (SPEC §8), reporting Recall@5/10 + MRR for BM25-only vs hybrid.
> Acceptance: eval runs in CI, hybrid beats BM25-only on paraphrase queries, thresholds
> file in place.

## Session 4 — M4: Bundle compiler

> Implement `compile_bundle` per SPEC §5: greedy budget packer with 5% headroom
> (property-based test: never exceeds max_tokens), near-duplicate dedupe, scope-priority
> ordering, truncation marker, deterministic output keyed on
> (task, scopes, max_tokens, repo_commit), SQLite bundle cache + in-process LRU.
> Expose as MCP tool + REST. Acceptance: same inputs ⇒ byte-identical bundle; cache hit
> visible in /api/stats; determinism test in CI.

## Session 5 — M5: Git sync + proposals + ship

> Implement `internal/gitsync` (SPEC §4.1–4.2): clone/open, HEAD-vs-sync_state
> incremental reindex, poll loop, HMAC webhook. Implement `propose_update` (SPEC §3.1.5):
> branch `briefd/proposal-<id>`, commit, optional PR via forge API (GitHub first). Add
> Dockerfile (distroless or alpine, CGO build), deploy/docker-compose.yml (single
> service), goreleaser config. Acceptance: end-to-end demo — edit knowledge repo, push,
> service reindexes within poll interval, agent sees new content; `docker compose up`
> works from clean checkout; README quickstart takes < 5 minutes.

## After M5 (pre-launch checklist, not a coding session)

- Migrate one real project's CLAUDE.md content into a knowledge repo; measure
  tokens-per-session before/after → launch post chart #1.
- Run eval, capture hybrid-vs-BM25 chart → launch post chart #2.
- Record 60–90s demo GIF (Claude Code asking, bundle coming back).
- Rename from working name, squat GitHub org / domain, tag v0.1.0.

## Standing rules for every session

- If Claude proposes deviating from a locked decision in CLAUDE.md → stop, write an ADR
  first.
- End of session: run `make lint test`, run `briefd eval` if retrieval was touched, update
  SPEC.md if behavior diverged, conventional-commit everything.
