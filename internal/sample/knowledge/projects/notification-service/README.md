---
title: notification-service
tags: [notifications, webhooks, email, service]
---
# notification-service

Delivers webhooks to merchant endpoints and transactional emails to merchant
users. Consumes events from the bus (via the outbox relay) and owns delivery
logs.

## Webhook delivery

Implements the webhook delivery policy: 10-second timeout, exponential
backoff from 1 minute to 12 hours for 3 days, automatic disabling after 7
days of failure, HMAC-SHA256 signatures with rotation overlap. Deliveries per
endpoint are serialized per resource to keep best-effort ordering.

## Email

Templates are versioned in the repository and rendered with the merchant's
locale. Emails about money (payout paid, dispute opened) are never batched;
digest emails are sent daily at 08:00 local time.

## Delivery logs

Webhook attempts are kept for 30 days and exposed in the portal with manual
redelivery. Email delivery status comes from the provider's callbacks.

## Failure modes

If the bus is unavailable the service simply falls behind; no events are
lost because the outbox holds them. If a merchant endpoint is slow, only that
endpoint's queue grows; per-endpoint concurrency is capped at 4.
