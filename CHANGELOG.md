# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
