# briefd

> **Your agents, briefed. Not flooded.**

Stop loading your team's knowledge into every prompt. Compile only what the task needs.

briefd is a self-hosted **context compiler** for AI coding teams. Keep your shared
domain knowledge — terminology, business rules, ADRs, conventions — as Markdown in
a git repository, and briefd serves task-relevant, **token-budgeted context
bundles** to coding agents (Claude Code, Cursor, Codex) over
[MCP](https://modelcontextprotocol.io) and REST.

- **Context on demand, not up front.** Agents query; briefd compiles only what the
  task needs, within an explicit token budget.
- **Git is the source of truth.** The index is a disposable cache. Agents propose
  changes as branches/PRs — humans stay in the loop.
- **One binary, one container, zero external services.** SQLite with FTS5 +
  sqlite-vec, local ONNX embeddings, no telemetry.

## Status

Pre-alpha, built milestone by milestone. Today briefd indexes a directory of
Markdown, serves it to Claude Code (or any MCP client) over streamable HTTP
with BM25 ranking and a hard token budget, and exposes the same over REST.
Hybrid (vector + BM25) retrieval, bundle compilation and git sync are in
progress. See [`SPEC.md`](SPEC.md) for the full specification.

## Quickstart

```sh
make build
./bin/briefd serve --source testdata/knowledge --db /tmp/briefd.db --token dev-token
```

Then connect Claude Code to it:

```sh
claude mcp add --transport http briefd http://localhost:7788/mcp \
  --header "Authorization: Bearer dev-token"
```

or add it to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "briefd": {
      "type": "http",
      "url": "http://localhost:7788/mcp",
      "headers": { "Authorization": "Bearer dev-token" }
    }
  }
}
```

Ask Claude Code something like *"what is our retry policy for acquirer calls?"*
and it will call `search_context`; check `/mcp` inside Claude Code to see the
connection status and the three tools:

| Tool | Purpose |
|---|---|
| `search_context(query, max_tokens?, scopes?, top_k?)` | ranked sections that fit the budget |
| `get_document(doc_path, scopes?)` | one document in full |
| `list_scopes()` | scopes with document/section counts |

Edits to files under `--source` are picked up within `sync.interval`
(default 60 s). Configuration lives in `briefd.yaml`
(see [`deploy/briefd.example.yaml`](deploy/briefd.example.yaml)) or `BRIEFD_*`
environment variables.

### Dashboard and metrics

Open <http://localhost:7788/> for a read-only status page: requests, tokens
served, latency percentiles per tool, index size per scope, sync state, the
last 100 requests, and a search box for manual inspection. It asks for the
bearer token once and keeps it in your browser.

`GET /metrics` exposes the same counters in Prometheus text format
(`briefd_requests_total`, `briefd_tokens_served_total`,
`briefd_request_duration_seconds`, `briefd_index_chunks`, `briefd_sync_runs_total`, …).
It is unauthenticated by default; set `metrics.require_auth: true` to change that.
`GET /api/stats` returns a JSON snapshot for your own tooling.

### REST

```sh
curl -H "Authorization: Bearer dev-token" \
  "localhost:7788/api/search?q=refund+approval&max_tokens=500&scopes=domain"
curl -H "Authorization: Bearer dev-token" localhost:7788/api/scopes
curl -H "Authorization: Bearer dev-token" localhost:7788/api/docs/domain/glossary.md
curl localhost:7788/api/health
```

### CLI

```sh
./bin/briefd index --source testdata/knowledge --db /tmp/briefd.db
./bin/briefd search --db /tmp/briefd.db "retry policy"
./bin/briefd search --db /tmp/briefd.db --scopes projects/ledger-service "projection drift"
./bin/briefd search --db /tmp/briefd.db --json --max-tokens 500 "refund approval threshold"
```

`testdata/knowledge/` is a small, fictional payments-domain knowledge repo that
follows the expected layout:

```
knowledge-repo/
├── domain/          # shared: terminology, business rules, ADRs
├── conventions/     # shared: coding standards, infra patterns
└── projects/<name>/ # visible only when scope projects/<name> is requested
```

Re-running `index` only re-parses files whose content changed and removes
documents that disappeared. `--rebuild` drops the database first.

## Building from source

Requires Go ≥ 1.26 and [golangci-lint](https://golangci-lint.run) v2.

```sh
make build   # → bin/briefd
make test
make lint
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Architecture decisions are recorded in
[`docs/adr/`](docs/adr/).

## License

[Apache-2.0](LICENSE)
