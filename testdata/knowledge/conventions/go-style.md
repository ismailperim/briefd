---
title: Go style guide
tags: [go, conventions]
---
# Go style guide

## Formatting and linting

All Go code is `gofmt`-formatted and passes `golangci-lint` with the shared
configuration in `platform/lint/.golangci.yml`. CI fails on any lint finding;
do not add `//nolint` without a reason comment.

## Errors

Wrap errors with context using `fmt.Errorf("doing x for %s: %w", id, err)`.
Sentinel errors are exported variables named `ErrXxx`; typed errors are used
only when callers need fields. Never log and return the same error — pick
one. Panics are reserved for programmer errors at startup (bad config,
missing migrations).

## Context and cancellation

Every function that performs I/O takes `ctx context.Context` as its first
parameter. Do not store contexts in structs. Respect cancellation in loops
that run longer than a few hundred milliseconds.

## Money

Money is represented as `money.Amount{Minor int64, Currency string}` from
`platform/money`. Never use `float64` for amounts, and never construct an
`Amount` from a float. Formatting for display is done at the edge only.

## Package layout

Services follow `cmd/<service>/main.go` for wiring, `internal/<domain>/` for
business logic, and `internal/store/` for persistence. No package may import
`cmd/`. Interfaces are declared by the consumer, not the implementer, and only
when there is more than one implementation or a test double is needed.

## Testing

Table-driven tests with `t.Run`. Use `testing/synctest` or injected clocks for
time-dependent code; never `time.Sleep` in tests. Integration tests that need
Postgres are tagged `//go:build integration` and run in the nightly pipeline.
