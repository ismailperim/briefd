---
title: payments-service
tags: [payments, service]
---
# payments-service

Owns the payment lifecycle: authorization, capture, refund initiation, and
acquirer routing. Go, Postgres, deployed with five replicas in each region.

## Responsibilities

- Expose `/v1/payments`, `/v1/captures`, `/v1/refunds` through the gateway
- Run the risk check with `risk-engine` before authorization
- Route authorizations to acquirers and handle their responses
- Emit posting events to `ledger-service` at capture and refund time
- Store idempotency records for its own endpoints

## Acquirer routing

Each merchant has an ordered list of acquirer routes per card brand. The
first healthy route is used; a route is unhealthy when its circuit breaker is
open or its authorization approval rate over 15 minutes drops 20 points below
its 7-day baseline. Failover to the next route happens only for
retry-safe declines (`do_not_honor`, network errors), never for
`insufficient_funds` or fraud declines.

## Retries

Acquirer calls use the strict retry policy (3 attempts, 10 second budget)
with the payment's idempotency key forwarded as the acquirer reference.
Ledger posting submissions use the standard policy.

## Local development

`make dev` starts Postgres and the acquirer simulator; sandbox cards from
`testkit` produce deterministic approve/decline outcomes by amount.
