# SPEC — briefd

Self-hosted context compiler for AI coding teams. Git-backed domain knowledge, served to
coding agents as token-budgeted context bundles via MCP.

Status: draft v0.1 · License: Apache-2.0 · Deployment target: single container, 2–4 GB VPS

---

## 1. Problem & positioning

- `CLAUDE.md` / `AGENTS.md` files load 5–10K tokens of static context into every session;
  the task usually needs a fraction of it, and the files are trapped in one repo.
- Teams building **many projects in one domain** have no home for cross-repo knowledge:
  terminology, business rules, ADRs, integration conventions.
- briefd inverts the model: **context on demand, not up front.** Agents query; the service
  compiles only what the task needs, within an explicit token budget.

Non-goals (v0.x): per-user/agent personal memory (Mem0 territory), code indexing
(Cursor/Continue territory), multi-node clustering, SaaS hosting.

## 2. Core concepts

| Term | Definition |
|---|---|
| Knowledge repo | A git repo of Markdown files. Single source of truth. |
| Scope | A visibility unit derived from folder layout (`domain`, `conventions`, `projects/<name>`). |
| Chunk | A retrievable unit (~heading-bounded section) with stable ID, metadata, embedding. |
| Bundle | A compiled, token-budgeted package of chunks answering a task description. |
| Proposal | An agent-suggested knowledge change, materialized as a git branch/PR. |

### Knowledge repo layout (convention over configuration)

```
knowledge-repo/
├── domain/          # shared: terminology, business rules, cross-project decisions
├── conventions/     # shared: coding standards, infra patterns
└── projects/
    ├── <project-a>/ # only visible to scope=projects/<project-a>
    └── <project-b>/
```

Scope resolution: a client requesting `scopes=["domain","conventions","projects/a"]`
sees those folders only. Default when unspecified: `domain` + `conventions`.

Optional front-matter per file:

```yaml
---
title: Retry policy            # defaults to first H1
tags: [payments, resilience]
refs: ["services/payment/**"]  # code paths this doc governs (staleness input, v0.2)
---
```

## 3. Interfaces

### 3.1 MCP tools (primary interface, streamable HTTP)

1. `search_context(query, max_tokens?=2000, scopes?, top_k?=8)`
   → ranked chunks: `[{chunk_id, doc_path, heading, content, score, tokens}]`,
   total ≤ `max_tokens`.
2. `compile_bundle(task_description, max_tokens?=2000, scopes?)`
   → single assembled context block: deduplicated, ordered (domain → conventions →
   project), with per-chunk source attribution and `bundle_id`. Deterministic: same
   task + same knowledge state (repo commit hash) ⇒ byte-identical bundle (cacheable,
   prompt-cache friendly).
3. `get_document(doc_path)` → full document (subject to scope).
4. `list_scopes()` → available scopes + doc counts.
5. `propose_update(doc_path, change_description, new_content, scopes?)`
   → creates branch `briefd/proposal-<id>`, commits, returns branch name + (if forge API
   configured) PR URL. Never touches the index.
6. `report_usage(bundle_id, useful_chunk_ids)` — optional feedback signal (v0.2 uses it
   for relevance tuning; v0.1 only stores it).

### 3.2 REST (secondary)

`GET /api/search`, `POST /api/bundle` (`{task, max_tokens?, scopes?}`), `POST /api/usage`
(`{bundle_id, useful_chunk_ids?, client?}`), `GET /api/docs/{path}`, `GET /api/scopes`,
`POST /api/proposals` (`{doc_path, change_description, new_content, client?}`),
`GET /api/proposals`, `GET /api/health`, `GET /api/stats` (chunk counts, index freshness,
cache hit rate, token-served counters, and proposal counts grouped by open, merged, and
closed status). `POST /webhook/git` (GitHub-style
`X-Hub-Signature-256` HMAC over the body with `sync.webhook_secret`) triggers a sync.
Auth: single bearer token (`BRIEFD_API_TOKEN`); MCP uses the same.

`GET /metrics` — Prometheus text format, no auth by default (configurable):
request counts and latency histograms per tool/endpoint, tokens served, cache hits/misses,
index size, last sync time/status, embedding calls.

### 3.4 Web dashboard (read-only)

`GET /` serves a single-page status dashboard embedded in the binary (no build step,
no JS framework): request volume and latency per tool, tokens served, cache hit rate,
index contents per scope, sync status, proposal counts by forge status, and a search box
that calls `/api/search` for manual inspection. Read-only; it never mutates state. Same
bearer token as the API.

### 3.3 CLI

`briefd serve` · `briefd index [--source DIR] [--rebuild]` · `briefd search <query>` (CLI
inspection of the index, same ranking as the API) · `briefd model pull` · `briefd eval` ·
`briefd version`

## 4. Ingestion & indexing

1. **Startup:** clone (or open) the knowledge repo (`source` may be a git URL or a
   directory; a directory that is a git checkout is read in place) → incremental reindex
   by content hash of changed/deleted files only. Empty DB ⇒ full build. The DB is
   disposable; `--rebuild` recreates it from git alone. Sync is fetch + hard reset to the
   remote branch (ADR-0005).
2. **Runtime freshness:** polling every `sync.interval` (default 60s) and/or
   `POST /webhook/git` (HMAC-verified). Both supported; polling is the default because
   homelab setups often can't receive webhooks. Each sync also refreshes stored open pull
   request statuses when a GitHub forge is configured. A forge error leaves the stored
   status unchanged and does not fail document indexing.
3. **Chunking:** split on headings (H2/H3), keep heading breadcrumb in chunk metadata,
   target 200–500 tokens per chunk, hard max 800 (split on paragraph boundary). Chunk ID =
   `hash(doc_path + heading_path)` — stable across re-indexing unless content moves.
4. **Embedding:** compute per chunk on index; content-hash skip for unchanged chunks.

## 5. Search & bundle compilation

- **Hybrid retrieval:** FTS5 BM25 top-50 + brute-force cosine top-50 (vectors stored in
  SQLite, scanned in memory; ADR-0002) → **RRF (k=60)**. The BM25 list is built AND-first
  (chunks containing every query term lead; any-term matches follow with RRF weight 0.25)
  and query stop words are dropped (ADR-0004). Modes `bm25`, `vector`, `hybrid` are
  selectable per request for evaluation.
- **Budget packer (`compile_bundle`):** greedy fill in fused-rank order → dedupe
  near-identical chunks → order by scope priority (domain > conventions > project) then
  rank → stop before exceeding budget; if the top chunk alone exceeds budget, return its
  head with a truncation marker. Token counting via tiktoken-compatible approximation
  (documented margin of error; budget enforced with 5% safety headroom).
- **Bundle cache:** key = `hash(task_description + sorted scopes + max_tokens +
  index_fingerprint + embedding_model)`, stored in SQLite, in-process LRU (256 entries) in
  front. `index_fingerprint` hashes every indexed (path, content_hash) — it changes with
  the repo commit but also for plain-directory sources — so invalidation is automatic;
  stale rows are pruned after each index run. `bundle_id` is the first 16 hex chars of
  the key, so identical requests share an id.
- **Dedupe:** chunks with identical content or a term-set Jaccard ≥ 0.85 to an already
  selected chunk are dropped. Rendered sections omit the chunk's own heading line (the
  attribution line carries the breadcrumb).

## 6. Data model (SQLite, WAL)

```sql
documents(id, path, scope, title, tags, front_matter, content_hash, updated_commit, indexed_at)
chunks(rowid, id, doc_id, title, heading_path, content, tokens, content_hash, position)
                  -- title is denormalized from documents so the FTS index can weight it
chunks_fts        -- FTS5 external-content table over chunks(title, heading_path, content),
                  -- kept in sync by triggers; tokenizer: porter unicode61 remove_diacritics 2
chunk_vectors     -- chunk_id, model, dim, embedding BLOB (L2-normalized float32 LE)
bundles(id, cache_key, task, scopes, max_tokens, index_fingerprint, model, content, tokens,
        sections, truncated, created_at, hits)
usage_events(id, bundle_id, chunk_id, useful, client, created_at)
proposals(id, branch, doc_path, description, commit_hash, pr_url, status, client, created_at)
sync_state(source, last_commit, last_sync_at, last_error, index_fingerprint)
```

## 7. Embeddings

- Default: `multilingual-e5-small` (384-dim, 100+ languages, ADR-0006), alternative
  `all-MiniLM-L6-v2` (English only, ~2.5× faster), both executed in-process by a pure-Go
  encoder (ADR-0003), CPU only. Weights are fetched once into
  `embeddings.model_dir` (default `<user cache>/briefd/models/<model>`) with pinned
  checksums, or pre-fetched with `briefd model pull [--model NAME]`.
- Adapters (config-selected): `local` (default) | `ollama` | `openai-compatible` | `none`
  (BM25-only mode; also `embeddings.enabled: false`).
- Changing the embedding model invalidates `chunk_vectors` (model name stored alongside;
  mismatch triggers re-embed).

## 8. Evaluation (part of the product, not an afterthought)

- Corpora: `testdata/knowledge/` (fictional payments platform, 44 English docs) with
  `eval/golden/queries.yaml` (47 queries), and `testdata/knowledge-tr/` (12 Turkish docs)
  with `eval/golden/queries-tr.yaml` (30 queries). Entries are
  `{id, query, type: keyword|paraphrase|typo|mixed-lang, expected: [{path, heading}]}`,
  written in task language rather than document wording.
- `briefd eval` outputs Recall@5, Recall@10, MRR — overall and per query type — and
  compares BM25-only vs hybrid.
- CI gate: `eval/thresholds.yaml`. Target: Recall@5 ≥ 0.85 hybrid; the gate is set at the
  measured baseline (0.80 as of 2026-09-20, see ADR-0004) and raised as retrieval improves.
  Chunking/embedding/fusion changes must include before/after eval numbers.

## 9. Non-functional requirements

- Single container; `docker compose up` with one service. Also distributed as a single
  binary (goreleaser: linux/amd64, linux/arm64, darwin/arm64).
- Resource target: idle < 300 MB RAM; 50K chunks searchable < 100 ms p95 on 2 vCPU.
- No telemetry. No outbound network except git remote and configured embedding adapter.
- Config: `briefd.yaml` + `BRIEFD_*` env overrides.

## 10. v0.1 scope

**In:** git sync (clone/pull/poll/webhook) · chunking · FTS5+vec+RRF hybrid ·
`search_context`, `compile_bundle`, `get_document`, `list_scopes`, `propose_update`
(branch+commit; PR URL if forge token given) · `report_usage` (store only) · ONNX default
+ Ollama adapter · bundle cache · REST + bearer auth · Prometheus `/metrics` + embedded
read-only web dashboard · eval harness + golden set · docker compose + binary release.

**Out (v0.2+):** usage-based relevance tuning · staleness scoring via `refs` globs ·
contradiction detection for proposals · multi-repo knowledge sources ·
`report_usage`-driven ranking · dashboard write actions (trigger sync, manage proposals).

## 11. Milestones

| M | Deliverable | Demo |
|---|---|---|
| M1 | ingest + FTS5, CLI search | `briefd index && briefd search "retry policy"` |
| M2 | MCP server with `search_context` (BM25) | Claude Code queries it live |
| M2b | Metrics + embedded web dashboard | open `/`, see live request/token counters |
| M3 | Local embeddings + vector scan + RRF | eval shows hybrid > BM25 |
| M4 | `compile_bundle` + budget packer + cache | deterministic bundle, budget respected |
| M5 | git sync loop + `propose_update` + docker/goreleaser | end-to-end team flow |
