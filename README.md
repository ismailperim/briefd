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

Pre-alpha, built milestone by milestone. Today you can index a directory of
Markdown and search it from the CLI (BM25). The MCP server, hybrid retrieval,
bundle compilation and git sync are in progress. See [`SPEC.md`](SPEC.md) for
the full specification.

## Try it

```sh
make build
./bin/briefd index --source testdata/knowledge --db /tmp/briefd.db
./bin/briefd search --db /tmp/briefd.db "retry policy"
./bin/briefd search --db /tmp/briefd.db --scopes projects/ledger-service "projection drift"
./bin/briefd search --db /tmp/briefd.db --json "refund approval threshold"
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
