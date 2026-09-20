---
title: Testing strategy
tags: [testing, quality, ci]
---
# Testing strategy

## Pyramid

Unit tests cover business rules and pure functions and must run in under a
minute per service. Integration tests exercise a service against a real
Postgres and the message bus in Docker, tagged `//go:build integration`, and
run on every PR for the touched service. End-to-end tests run against
staging nightly and cover the money paths: authorize, capture, refund,
payout, and dispute.

## Money paths need golden tests

Any code that computes an amount (fees, FX, splits, refunds) has table-driven
golden tests with inputs and expected minor-unit outputs reviewed by the
domain owner. Changing a golden value requires a justification in the PR.

## Test data

Never use production data in tests. Synthetic merchants and cards come from
the `testkit` package; test card numbers are the acquirer sandbox numbers.
Fixtures that mimic acquirer settlement files live under `testdata/` and are
anonymized.

## Flaky tests

A test that fails without a code change is quarantined the same day with a
ticket, not retried in CI. Quarantined tests older than two weeks are
deleted.

## Coverage

Coverage is reported, not gated, except in `platform/money` and the ledger
posting code, where line coverage below 90% fails the pipeline.
