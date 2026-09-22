---
title: "ADR-0003: One database per service"
tags: [adr, architecture, database]
---
# ADR-0003: One database per service

- Status: accepted
- Date: 2024-11-20

## Context

The original monolith shared one Postgres schema across payments, ledger, and
merchant management. Schema changes required coordinating every team, and a
slow report query could lock payment writes.

## Decision

Each service owns a private Postgres database. No other service may read or
write it directly; data crosses service boundaries only through APIs or
published events. Reporting reads from a replica fed by events, never from
service databases.

## Consequences

- Joins across services are impossible; services keep local copies of the
  fields they need, updated from events.
- Transactions spanning services are replaced by the outbox pattern
  (ADR-0009) and idempotent consumers.
- Each service runs its own migrations as part of deployment.
