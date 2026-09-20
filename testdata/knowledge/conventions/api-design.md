---
title: API design conventions
tags: [api, http, conventions]
---
# API design conventions

## Resource naming

Public endpoints are versioned under `/v1/` and use plural nouns:
`/v1/payments`, `/v1/refunds`, `/v1/payouts`. Identifiers are ULIDs prefixed
with a resource tag (`pay_01J...`, `ref_01J...`) so they are recognizable in
logs and support tickets.

## Request and response shape

Bodies are JSON with `snake_case` keys. Amounts are objects
`{"amount": 1250, "currency": "EUR"}` in minor units. Timestamps are RFC 3339
in UTC. Every response includes a `request_id` header echoing or generating a
correlation id.

## Errors

Errors use a stable machine-readable `code` plus a human `message`:

```json
{"error": {"code": "refund_window_expired", "message": "...", "request_id": "req_..."}}
```

Codes are documented in the public error catalog. HTTP status mapping: 400 for
malformed input, 401/403 for auth, 404 for unknown resources, 409 for state
conflicts, 422 for business rule violations, 429 for rate limits, 5xx only for
our own failures.

## Pagination

List endpoints use cursor pagination: `?limit=50&cursor=...` with a
`next_cursor` in the response. Never offset pagination on money-related
resources.

## Versioning and deprecation

Breaking changes require a new major version path. Deprecated fields are
announced in the changelog and carry a `Deprecation` header for at least 6
months before removal.

## Webhooks

Webhook payloads contain the event type, the resource id, and a timestamp —
not the full resource. Consumers fetch the current state. Deliveries are
signed with HMAC-SHA256 over the raw body using the endpoint secret, retried
with exponential backoff for 3 days, and are idempotent by `event_id`.
