---
title: Idempotency
tags: [api, reliability, payments]
---
# Idempotency

## Which requests require an idempotency key

Every request that creates a resource or moves money must carry an
`Idempotency-Key` header: creating payments, captures, refunds, payouts, and
merchant onboarding. GET requests never use one. Requests without a key on a
mutating endpoint are rejected with `idempotency_key_required`.

## Key format and scope

Keys are client-generated strings of 16–64 characters. They are scoped to the
API key that sent them, so two merchants can use the same value without
conflict. We recommend UUIDv4 or ULID.

## Replay semantics

When a request is replayed with the same key and the same request body, the
original response (status code, headers, body) is returned unchanged. If the
body differs, the request is rejected with `idempotency_key_reused` and HTTP
422, because silently returning the old result would hide a client bug.

While the original request is still in flight, a replay returns HTTP 409
`idempotency_in_progress`; clients should retry after the `Retry-After`
interval.

## Retention

Idempotency records are kept for **24 hours**. After that a key may be reused
and will be treated as a new request. Clients that need longer protection
should rely on their own de-duplication (for example, by storing our
`payment_id`).

## Storage

Records live in the `idempotency` table of the service that owns the endpoint,
keyed by `(api_key_id, idempotency_key)`, storing a hash of the request body
and the serialized response. They are not replicated across services.
