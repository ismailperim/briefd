---
title: "ADR-0007: Event sourcing for the ledger"
tags: [adr, ledger, architecture]
---
# ADR-0007: Event sourcing for the ledger

- Status: accepted
- Date: 2025-03-11

## Context

The ledger is the system of record for every balance in Kestrel Pay.
Regulators and auditors require us to explain any balance at any point in
time, and treasury regularly needs to replay a day's activity to investigate
reconciliation exceptions. A mutable balances table with an audit log bolted
on made both of these hard and error-prone.

## Decision

The ledger stores an append-only stream of **posting events**. Balances are
projections rebuilt from the stream. Events are immutable; corrections are
compensating events. Projections may be rebuilt from scratch at any time and
must produce identical balances.

Each event carries: event id (ULID), posting id, debit account, credit
account, amount (minor units), currency, effective timestamp, and a reference
to the business object (payment, refund, payout, fee).

## Consequences

- Balance reads go through a projection table refreshed synchronously on
  write; a daily job rebuilds projections from events and alerts on drift.
- Storage grows without bound. Events older than 7 years are archived to cold
  storage after the retention review.
- Every consumer of balances must tolerate eventual rebuilds; nothing may
  write to projection tables directly.
