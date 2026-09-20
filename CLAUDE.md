# CLAUDE.md — Development Instructions

> Name: **briefd** — your agents, briefed. Not flooded. A self-hosted "context compiler"
> for AI coding teams: git-backed domain knowledge, served to agents via MCP as
> token-budgeted context bundles.

## What this project is

One sentence: *Stop loading your team's knowledge into every prompt. Compile only what
the task needs.*

Teams working on many projects in one domain keep shared knowledge (conventions, ADRs,
specs, decisions) as Markdown in a **knowledge git repo**. This service indexes it and
serves task-relevant, token-budgeted context bundles to AI coding agents (Claude Code,
Cursor, Codex) over **MCP** and REST.

## Locked architecture decisions — do NOT revisit without an ADR

These were decided after research. Do not "improve" them mid-task. If you believe one is
wrong, stop and propose an ADR instead of changing code.

1. **Language: Go** (>= 1.23). Single static binary. Official MCP Go SDK
   (`modelcontextprotocol/go-sdk`, v1.x).
2. **Storage: SQLite only** (WAL mode). One `.db` file holds documents, chunks,
   embeddings, usage events, bundle cache, sync state.
3. **Search: hybrid** — SQLite **FTS5 (BM25)** + **sqlite-vec** (brute-force vector) fused
   with **RRF, k=60**. No ANN libraries. BM25-only mode must remain available via config
   (`embeddings.enabled=false`).
4. **Embeddings: local ONNX by default** (`all-MiniLM-L6-v2`, 384-dim), pluggable
   adapters: `ollama`, `openai-compatible`. Adapter interface first, implementations
   behind it.
5. **Git is the source of truth. The index is a disposable cache.** The service must be
   able to rebuild the entire index from a fresh clone. Never store knowledge that exists
   only in SQLite.
6. **Agents never write to the index directly.** `propose_update` creates a git
   branch/commit (PR-based review). Merge → sync → reindex is the only write path.
7. **Deployment: one container, zero external services.** Never add Redis, Postgres,
   Elasticsearch, Qdrant, or any message queue. Caching is in-process LRU + SQLite tables.
8. **Token budget is a hard constraint.** Any API that returns context accepts
   `max_tokens` and must never exceed it. Truncation strategy lives in the bundle
   packer, nowhere else.
9. **License: Apache-2.0.** No AGPL/GPL/SSPL dependencies. Check licenses before adding
   any dependency.

## Repository layout

```
briefd/
├── cmd/briefd/            # main: serve | index | eval | version
├── internal/
│   ├── config/          # YAML config + env overrides
│   ├── gitsync/         # clone/pull, commit-hash diff, poll + webhook
│   ├── ingest/          # markdown parsing, chunking, front-matter
│   ├── store/           # SQLite: schema, migrations, queries (sqlc or hand-written)
│   ├── search/          # fts5 query, vec query, rrf fusion
│   ├── embed/           # Embedder interface + onnx/ollama/openai adapters
│   ├── bundle/          # compile_bundle: selection + token packing + cache
│   ├── mcpserver/       # MCP tools (streamable HTTP)
│   ├── httpapi/         # REST + health + /metrics + embedded dashboard
│   ├── metrics/         # in-process counters/histograms (Prometheus text exposition)
│   └── tokenizer/       # token counting (tiktoken-compatible approximation)
├── eval/                # golden dataset + eval harness (see Testing)
├── docs/adr/            # ADRs — one file per decision, NNNN-title.md
├── deploy/              # docker-compose.yml, Dockerfile
└── SPEC.md              # product/technical spec — read before any feature work
```

## Workflow rules

- **Read `SPEC.md` before implementing any feature.** If the spec is silent or
  ambiguous, ask; do not invent behavior.
- Work in **small vertical slices** that end in something runnable (see PROMPTS.md
  milestones). Do not scaffold all packages upfront.
- Every non-obvious technical decision gets an ADR in `docs/adr/` in the same PR.
- Conventional commits (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
- Update `SPEC.md` when implemented behavior legitimately diverges from it — spec and
  code must not drift.

## Coding conventions

- Standard Go style; `gofmt` + `golangci-lint` clean.
- Errors: wrap with context (`fmt.Errorf("indexing %s: %w", path, err)`); no panics
  outside `main`.
- No premature abstraction: interfaces only where a second implementation exists or is
  specced (Embedder is the canonical example).
- CGO is acceptable (sqlite, fts5, sqlite-vec, ONNX runtime) but must be confined to
  `store/` and `embed/onnx/`. Everything else stays pure Go.
- All SQL lives in `internal/store`. No SQL strings elsewhere.
- Config precedence: flags > env (`BRIEFD_*`) > yaml > defaults.

## Testing rules

Two mandatory layers — a feature is not done without both where applicable:

1. **Deterministic tests** (`go test ./...`):
   - Chunking: heading hierarchy preserved, size limits respected, stable chunk IDs.
   - Git sync: changed/deleted files trigger incremental reindex; full rebuild from
     clean clone produces identical index.
   - RRF: pure-function table-driven tests.
   - Budget packer: output never exceeds `max_tokens` (property-style test with random
     inputs).
2. **Retrieval quality eval** (`briefd eval`):
   - Golden dataset in `eval/` (corpus + queries + expected chunk IDs).
   - Metrics: **Recall@5, Recall@10, MRR**.
   - CI gate: fail if Recall@5 drops below the threshold in `eval/thresholds.yaml`.
   - Any change to chunking, embeddings, or fusion MUST run the eval and report
     before/after numbers in the PR description.

## Off limits

- Do not add external service dependencies (see locked decision #7).
- Do not modify `eval/golden/*` to make a failing eval pass.
- Do not bypass the git write path with direct index mutations.
- Do not add telemetry/phone-home of any kind.
