---
title: "ADR-0009: Transactional outbox for events"
tags: [adr, events, reliability]
---
# ADR-0009: Transactional outbox for events

- Status: accepted
- Date: 2025-01-15

## Context

Services publish domain events (payment captured, refund succeeded) that
other services and webhooks depend on. Publishing directly to the message bus
inside a request handler lost events when the bus was unavailable, and
publishing after commit produced events for rolled-back transactions.

## Decision

Events are written to an `outbox` table in the same database transaction as
the state change. A relay process polls the outbox, publishes to the bus, and
marks rows as sent. Consumers are idempotent by `event_id` because the relay
guarantees at-least-once delivery.

## Consequences

- Event publication is delayed by the relay interval (default 500 ms).
- The outbox table needs pruning; rows older than 7 days are deleted.
- Every consumer must store processed event ids or be naturally idempotent.
