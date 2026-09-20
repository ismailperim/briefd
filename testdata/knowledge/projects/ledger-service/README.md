---
title: ledger-service
tags: [ledger, service]
---
# ledger-service

The double-entry ledger and balance projections for Kestrel Pay
(see ADR-0007). Written in Go, backed by Postgres, deployed as a single
Deployment with three replicas.

## Responsibilities

- Accept posting events from payments, refunds, payouts, and fees
- Maintain per-merchant, per-currency balance projections
- Serve balance queries to the merchant portal and payout scheduler
- Produce the daily settlement batch and reconciliation report

## Non-responsibilities

The ledger does not talk to acquirers, does not schedule payouts, and does not
know about merchant users or permissions. It trusts its callers' service
identities.

## Key interfaces

`POST /internal/postings` accepts a batch of postings with an idempotency key
per posting. `GET /internal/balances/{merchant_id}` returns available,
pending, and reserve balances per currency.

## Retries

Callers must retry posting submissions with the standard retry policy; the
ledger de-duplicates by posting id, so replays are safe. The ledger itself
retries projection refreshes internally and never retries acquirer calls
because it makes none.

## Local development

`make dev` starts Postgres in Docker and runs the service with hot reload.
Seed data lives in `testdata/seed.sql`.
