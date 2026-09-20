---
title: payments-service runbook
tags: [payments, runbook, oncall]
---
# payments-service runbook

## Authorization approval rate drop

**Alert:** `AuthApprovalRateDrop` — approval rate for an acquirer route is 20
points below baseline for 15 minutes.

1. Check the acquirer status page and our egress connectivity.
2. If the acquirer is degraded, flip the ops flag
   `route.<acquirer>.disabled`; traffic fails over to the next route.
3. Announce in the merchant status channel if failover affects approval
   rates for more than 30 minutes.

## Stuck authorizations

Authorizations older than 6 days without capture are listed by
`payments admin stale-auths`. Contact the merchant; captures after expiry
fail with `authorization_expired` and need a new payment.

## Duplicate payment reports

If a merchant reports duplicate charges, check the idempotency table for the
key; two payments with different keys are a merchant integration bug, one key
with two payments is a P1 — freeze the merchant's captures and page the
payments lead.

## Risk engine unavailable

When `risk-engine` is down, authorizations fall back to `medium` band
handling (3-D Secure where supported). Do not disable the risk check
entirely; that requires the risk lead.
