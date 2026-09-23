# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Links between documents: Obsidian-style `[[wikilinks]]` (in the body or in
  front-matter properties such as `related:`) and relative
  Markdown links are extracted at index time and resolved like Obsidian
  (by path or by file name). `GET /api/graph` returns the graph with orphans
  and broken links; the dashboard draws it and lists the issues;
  `get_document` (MCP and REST) returns `links` and `backlinks`. Documents
  indexed by earlier versions are re-parsed once.
- Dashboard counters survive restarts: requests, tokens served, latency,
  the request log, cache counters and circulation are saved to the database
  every 30 seconds and on shutdown, and restored on start ("Counting since"
  on the Activity view). "Reset statistics" (`POST /api/stats/reset`) zeroes
  them; "Sync now" and "Rebuild index" on the Instance view
  (`POST /api/sync`, `{"rebuild": true}` re-parses every document) start a
  sync without waiting for the interval. Actions require a JSON body type so
  another site cannot trigger them from a browser.
- Proposals on the dashboard: Maintenance lists open proposals first (then
  recent merged/closed) with the document, the change, the branch and a link
  to the pull request — or, without a forge, a link that opens one on
  GitHub, GitLab or Azure DevOps. They count in Needs attention.
- Proposal status without a forge: each sync marks a proposal merged once
  its change is on the followed branch (the commit is an ancestor of HEAD,
  or HEAD holds exactly the proposed file content, which also covers squash
  merges).
- Link suggestions: documents that mention another document by name (its
  title, the part before a colon, or its file name) without linking to it
  in either direction. `GET /api/links/suggestions`, computed once per index
  state; listed under Maintenance → "Links to fix" with the wikilink to add.
- Dashboard: "Circulation" — documents retrieval handed to agents in the
  last 24 hours (in memory), stamped on the graph and faded by age; an
  Instance view with the running configuration (source, branch and head,
  git auth method, embeddings, access, proposals, code repositories, build,
  storage — secrets shown only as set/unset; `GET /api/instance`); a new
  layout with a white navigation header, Overview / Graph / Maintenance /
  Activity / Instance views, figure cards and a Needs attention summary. The
  Hanken Grotesk font (OFL) and Lucide icons (ISC) are embedded in the
  binary; see `THIRD_PARTY_NOTICES.md`.
- Path-aware retrieval (ADR-0008): `compile_bundle` and `search_context`
  take `paths` — the code files the task touches. Documents whose `refs`
  cover them are fused into the ranking ahead of general matches (at most
  three sections per document, six in total); the bundle cache key includes
  the path set. REST: `paths` in `POST /api/bundle` and `?paths=` on
  `/api/search`.
- Coverage: `GET /api/coverage` and a dashboard panel list, per followed
  code repository, the directories no document's `refs` claim.

### Added

- `briefd demo`: serves the built-in sample knowledge base (the fictional
  payments platform from `testdata/knowledge`, embedded in the binary) with
  the dashboard and MCP tools — try briefd without a repository.
- `INSTALL_FOR_AGENTS.md`: instructions a coding agent can follow to install
  and wire briefd itself (`Retrieve and follow the instructions at: …`).
- `skills/briefd/SKILL.md`: an agent skill that says when to compile a
  bundle, how to treat a stale section and when to propose an update
  (`npx skills add ismailperim/briefd`).
- GitLab merge requests for proposals: `forge.type: gitlab` with a token that
  has the `api` scope; the project path and `https://<host>/api/v4` are
  derived from the git URL. Status sync maps opened/locked → open (#9).
- Release pipeline: the container image is also pushed to Docker Hub when
  `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN` secrets exist (#10), and a
  Homebrew cask is published to `ismailperim/homebrew-tap` when
  `HOMEBREW_TAP_TOKEN` exists (#11). Forks without the secrets are unaffected.
- README: "Not another agent memory" — how briefd differs from agent-memory
  tools and why they run side by side.

## [0.5.0] — 2026-09-22

### Added

- Code drift (ADR-0007): `code.repos` lists code repositories (git URL,
  cloned bare next to the database, or a local checkout) whose history is
  compared against documents' `refs` globs. Commits that touched governed
  code after a document's last change are counted per document; bundle
  attribution lines say `code changed since: N commits, last YYYY-MM-DD`,
  search results and bundle sections carry `code_changes` /
  `code_changed_at`, `/api/stats` lists the documents most behind, the
  dashboard shows them as "Behind the code", and Prometheus exposes
  `briefd_documents_behind_code`. Cached bundles are invalidated when drift
  changes.

- `briefd mcp`: the MCP tools over stdio (JSON-RPC on stdin/stdout, logs on
  stderr) for Claude Desktop, Cursor's stdio config and directory inspectors
  such as Glama and Smithery. Same index, sync and tools as `serve`; no port,
  no token. `serve` and `mcp` now share one process setup.

- `glama.json` (maintainer metadata for the Glama directory).

### Changed

- Tool and parameter descriptions rewritten for agents: when to use each
  tool versus its neighbour, side effects (all read-only except
  `propose_update`), what comes back, budget and error behaviour, and that an
  empty `report_usage` marks a knowledge gap. The tool schemas now cost
  ~2,300 tokens once per session (was ~1,750).

### Fixed

- Cloning from Azure DevOps (cloud and Server) failed with "object not
  found": go-git does not advertise `multi_ack` by default and Azure DevOps
  serves no pack without it. briefd now enables it for Azure DevOps URLs,
  as go-git's own Azure DevOps example does.
- Container image: `BRIEFD_LISTEN` is no longer baked into the image, so a
  platform-provided `PORT` (Glama, Render, Fly, …) is honored as documented.
- `briefd serve` starts BM25-only, with an error in the log, when the local
  embedding model cannot be loaded or downloaded, instead of exiting; remote
  providers still fail hard.
- Knowledge gaps: the same question asked over `search_context` and
  `compile_bundle`, or with scopes listed in a different order, is one row.
- Dashboard: the gap lists show eight rows each; long document paths no
  longer push the "Behind the code" columns out of the panel.

## [0.4.0] — 2026-09-21

### Added

- Knowledge-gap report: every `search_context` / `compile_bundle` call is
  logged with its retrieval confidence (`query_log`, default 30-day
  retention). `GET /api/gaps` and the dashboard's "Knowledge gaps" panel list
  the questions that returned nothing or whose bundle the agent reported as
  not useful, grouped and counted, plus a "Low confidence" list of answered
  questions whose top result barely stood out. Config: `query_log.enabled`,
  `query_log.retention_days`.
- `search.Result` and bundles carry `top_score` and `margin`.
- Document age: each document records when its content last changed (last
  commit that touched it, via a newest-first go-git history walk; file mtime
  for plain directories). Bundle attribution lines now read
  `## path — heading (updated YYYY-MM-DD)`, search results and bundle
  sections carry `updated_at`, `/api/docs/{path}` returns it, and the
  dashboard shows the oldest documents.

### Changed

- README benchmark figures re-measured with the default multilingual model
  and the dated attribution lines: 889 tokens / 96% at `max_tokens=1000`,
  1,800 tokens / 96% at 2000 (was 885 / 96% and 1,791 / 98%, measured with
  `all-MiniLM-L6-v2`). Savings versus a full `CLAUDE.md` are unchanged
  (−93% / −86%).

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

[Unreleased]: https://github.com/ismailperim/briefd/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/ismailperim/briefd/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/ismailperim/briefd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/ismailperim/briefd/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/ismailperim/briefd/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/ismailperim/briefd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ismailperim/briefd/releases/tag/v0.1.0
