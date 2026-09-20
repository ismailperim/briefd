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

Pre-alpha. The project is being built milestone by milestone; nothing is usable
yet. See [`SPEC.md`](SPEC.md) for the product and technical specification.

## Building from source

Requires Go ≥ 1.23 and [golangci-lint](https://golangci-lint.run) v2.

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
