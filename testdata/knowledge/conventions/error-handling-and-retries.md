---
title: Error handling and retry policy
tags: [resilience, retries, conventions]
refs: ["platform/retry/**"]
---
# Error handling and retry policy

## Classifying failures

Before retrying anything, classify the failure:

- **Transient** — timeouts, connection resets, HTTP 429/502/503/504, database
  serialization failures. Safe to retry.
- **Permanent** — validation errors, 4xx other than 429, business rule
  rejections. Never retry; surface to the caller.
- **Ambiguous** — the request may or may not have been applied (timeout after
  sending). Retry only through an idempotent path.

## Retry policy

Use `platform/retry` rather than hand-rolled loops. The default policy is:

- exponential backoff starting at 200 ms, multiplier 2, **full jitter**
- maximum 5 attempts
- total budget capped at 30 seconds
- honor `Retry-After` when present

Money-moving calls to acquirers use a stricter policy: 3 attempts, 10 second
budget, and always with an idempotency key. Never retry an acquirer call
without one.

## Circuit breaking

Clients to external dependencies wrap calls in a circuit breaker that opens
after 50% failures over a 30-second window (minimum 20 requests) and
half-opens after 15 seconds. When a breaker is open, fail fast with
`dependency_unavailable`; do not queue.

## Timeouts

Every outbound call has an explicit timeout. Defaults: 2 s for internal
services, 10 s for acquirers, 30 s for batch file transfers. Timeouts are
propagated through `context.Context`; never rely on library defaults.

## Dead letters

Asynchronous jobs that exhaust their retry budget go to a dead-letter queue
with the last error attached. Dead letters are reviewed daily by the owning
team; nothing is silently dropped.
