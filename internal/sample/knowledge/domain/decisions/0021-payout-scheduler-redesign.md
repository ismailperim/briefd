---
title: "ADR-0021: Payout scheduler as a single-writer job"
tags: [adr, payouts, scheduling]
---
# ADR-0021: Payout scheduler as a single-writer job

- Status: accepted
- Date: 2026-05-28

## Context

Payouts were created by whichever `payments-service` replica noticed a
merchant's schedule was due, guarded by row locks. Under load two replicas
occasionally created duplicate payouts for the same merchant and day, which
the bank then rejected as duplicates or, worse, paid twice.

## Decision

Payout creation moves to a single scheduler job with leader election. The job
runs every minute, selects merchants whose schedule is due, and creates one
payout per (merchant, currency, day) with a deterministic idempotency key
`payout:<merchant_id>:<currency>:<date>`. The ledger rejects a second payout
with the same key.

## Consequences

- Payout creation is delayed by up to one minute; acceptable for daily and
  weekly schedules.
- Manual payouts from the portal still go through the API but use the same
  key format, so a manual and a scheduled payout cannot both succeed.
- Leader failover is handled by the job framework; at most one leader exists.
