# Contributing to briefd

Thanks for your interest! This document explains how to get set up and what we
expect from contributions.

## Before you start

- Read [`SPEC.md`](SPEC.md) — it is the product and technical specification.
  If the spec is silent or ambiguous about something, open an issue before
  implementing; do not invent behavior.
- Read [`docs/adr/`](docs/adr/). Some architectural decisions are **locked**
  (see [ADR-0001](docs/adr/0001-core-architecture.md)). Proposals to change
  them are welcome, but they go through a new ADR, not a code change.
- For anything beyond a small fix, open an issue first so we can agree on the
  approach.

## Development setup

- Go ≥ 1.23
- [golangci-lint](https://golangci-lint.run) v2
- `make` (GNU)

```sh
make build   # bin/briefd
make test    # go test -race -cover ./...
make lint    # golangci-lint run
make fmt     # apply formatters
```

All three of `make build`, `make test` and `make lint` must pass before you
open a pull request.

## Coding conventions

- Standard Go style; `gofmt` + `golangci-lint` clean.
- Wrap errors with context: `fmt.Errorf("indexing %s: %w", path, err)`. No
  panics outside `main`.
- No premature abstraction: add an interface only when a second implementation
  exists or is specified.
- CGO is confined to `internal/store` and `internal/embed/onnx`.
- All SQL lives in `internal/store`.
- Config precedence: flags > `BRIEFD_*` env > YAML > defaults.

## Testing

Two layers, both mandatory where applicable:

1. **Deterministic tests** (`go test ./...`) for chunking, sync, fusion and
   budget packing.
2. **Retrieval quality eval** (`briefd eval`). Any change to chunking,
   embeddings or fusion must include before/after Recall@5, Recall@10 and MRR
   in the PR description. Never edit `eval/golden/*` to make a failing eval
   pass.

## Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org):
  `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`.
- Keep PRs focused; one logical change per PR.
- Non-obvious technical decisions get an ADR in `docs/adr/` in the same PR.
- If implemented behavior legitimately diverges from `SPEC.md`, update the spec
  in the same PR — spec and code must not drift.
- Add a line to `CHANGELOG.md` under *Unreleased* for user-visible changes.

## Dependencies

- Apache-2.0-compatible licenses only. No AGPL/GPL/SSPL. Check before adding.
- No external services (databases, queues, vector stores). Ever.
- No telemetry or phone-home of any kind.

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
