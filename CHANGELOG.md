# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] — 2026-09-21

### Added

- Proposal status sync: open proposals with a GitHub pull request are refreshed
  during source sync and marked `merged` / `closed`; counts appear in
  `/api/stats` and on the dashboard (#13, thanks @Voyagerroc-Lab).
- `deploy/README.md`: deployment guide (Compose, systemd, forges, offline model,
  proxies/CAs, security, operations).
- BEIR runner (`eval/beir/run.py`) with SciFact and NFCorpus results: vector-only
  matches the published model quality, hybrid beats BM25 and vector-only.
- `PORT` environment variable (when `BRIEFD_LISTEN` is unset) and `GET /healthz`
  alias, for PaaS and directory runners.
- `briefd init [DIR]`: scaffolds a knowledge repository with the expected layout
  and example documents.
- `deploy/local/`: localhost-only config, macOS launchd service, `.mcp.json` and
  `CLAUDE.md` templates for private, on-machine use.

### Changed

- Security workflow: govulncheck, CodeQL (Go + JavaScript), dependency review on
  PRs, Trivy image scan; releases include a CycloneDX SBOM per archive.
- Releases are cut with `make release VERSION=X.Y.Z`; the release workflow
  verifies changelog/server.json consistency and publishes to the MCP Registry
  automatically (GitHub OIDC).

## [0.2.1] — 2026-09-20

### Changed

- Container image carries OCI labels/annotations (`io.modelcontextprotocol.server.name`)
  so it can be listed in the MCP Registry.

## [0.2.0] — 2026-09-20

### Added

- Multilingual embeddings: `multilingual-e5-small` (100+ languages) runs in the
  same pure-Go encoder via a new SentencePiece Unigram tokenizer and is now the
  default model; `embeddings.model: all-MiniLM-L6-v2` keeps the faster
  English-only model. Model weights are memory-mapped. `briefd model list`,
  `briefd model pull --model`, `--model` on every command.
- Turkish evaluation corpus (`testdata/knowledge-tr`, 12 docs) and golden set
  (30 queries) with its own CI threshold file. Hybrid retrieval on it: R@5 0.93,
  R@10 1.00, MRR 0.85 (was 0.85 / 0.85 / 0.67 with MiniLM).

### Changed

- `Embedder` gained `EmbedQuery` so asymmetric models apply query/passage prefixes.
- Docker image caches models under `/data/cache` (`XDG_CACHE_HOME`).

## [0.1.0] — 2026-09-20

First release: everything below.

### Added

- `eval/session/run.py`: real Claude Code session benchmark (CLAUDE.md vs
  briefd over MCP) with results.
- `briefd bench`: measures knowledge tokens per task and answer coverage for
  "everything in CLAUDE.md", a curated CLAUDE.md, and `compile_bundle` at
  several budgets, over the golden tasks.
- Token estimator calibrated against o200k/cl100k on the corpus, code and
  Turkish prose: +4.6% overall (was +22%), so budgets are used, not wasted.
- Git sources: `source` may be a git URL; briefd clones with go-git (no git
  binary needed), polls with fetch + hard reset, and accepts GitHub-style
  signed pushes on `POST /webhook/git`. A local git checkout is read in place.
- `propose_update` (MCP) and `POST /api/proposals`: commits the proposed
  content to `briefd/proposal-<id>` on top of the current head, pushes it and
  opens a GitHub pull request when `forge` is configured; `GET /api/proposals`
  lists them. Paths outside `domain/`, `conventions/`, `projects/<name>/` are
  rejected.
- Deployment: distroless container image (`deploy/Dockerfile`, ~34 MB,
  multi-arch), `deploy/docker-compose.yml`, goreleaser binaries for
  linux/darwin/windows, release workflow publishing to GitHub Releases and
  ghcr.io.
- `compile_bundle` (MCP) and `POST /api/bundle` (REST): compiles a task into one
  context block — near-duplicate sections dropped, greedy packing within the
  budget with 5% headroom, domain → conventions → project ordering, source
  line per section, truncation marker when nothing fits. Byte-identical for
  identical inputs and cached in SQLite + an in-process LRU keyed on task,
  scopes, budget, index fingerprint and embedding model.
- `report_usage` (MCP) and `POST /api/usage`: stores which sections helped.
- Bundle cache hit rate on `/metrics`, `/api/stats` and the dashboard.
- Hybrid retrieval: local `all-MiniLM-L6-v2` embeddings computed by a pure-Go
  encoder (no CGO, no ONNX runtime; weights downloaded once or via
  `briefd model pull`), Ollama and OpenAI-compatible adapters, vectors stored
  in SQLite and scanned in memory, fused with BM25 by weighted RRF. Chunks are
  re-embedded only when their content or the model changes.
- `briefd eval`: Recall@5/10 and MRR for bm25, vector and hybrid against a
  47-query golden set over the sample corpus (now 44 documents); CI gate via
  `eval/thresholds.yaml`.
- `briefd search --mode bm25|vector|hybrid`, `--embeddings` provider override,
  `embeddings.*` configuration, vector counts on the dashboard and `/metrics`.
- Dashboard: sparklines for requests/tokens/latency, requests-per-minute,
  budget-pressure ratio, sortable per-tool table, index share bars, click a
  recent request to re-run it, onboarding snippet when idle, theme toggle
  (`?theme=dark|light`), keyboard focus states, reduced-motion support.
- Observability: in-process metrics with a Prometheus `/metrics` endpoint,
  `/api/stats` JSON snapshot (per-tool counts, p50/p95 latency, tokens served,
  index size per scope, sync state, recent requests) and an embedded read-only
  dashboard at `/` with a search box.
- Default listen address is `:7788` (8080 is too often taken by something else).
- `briefd serve`: MCP server (streamable HTTP, official Go SDK) with
  `search_context`, `get_document` and `list_scopes`; REST endpoints
  `/api/search`, `/api/docs/{path}`, `/api/scopes`, `/api/health`; single
  bearer token; periodic re-scan of the source directory; YAML + `BRIEFD_*`
  configuration.
- Token budgets: `search_context` and `/api/search` never return more than
  `max_tokens` (5% headroom over the estimate).
- `briefd index`: walks a knowledge directory, parses front matter, chunks
  Markdown on H2/H3 headings (200–800 tokens, stable chunk IDs) and stores
  documents + chunks in SQLite with an FTS5 index. Incremental by content hash;
  deleted files are removed from the index.
- `briefd search`: BM25 search over the index with scope filtering, JSON output,
  and diacritics-insensitive, stemmed matching.
- Sample knowledge repository under `testdata/knowledge/`.
- Project skeleton: Go module, `briefd version` command, Makefile, lint config,
  CI, ADR-0001 (core architecture) and ADR-0002 (SQLite driver, vector search).

[Unreleased]: https://github.com/ismailperim/briefd/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/ismailperim/briefd/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/ismailperim/briefd/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/ismailperim/briefd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ismailperim/briefd/releases/tag/v0.1.0
