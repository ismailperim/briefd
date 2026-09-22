---
title: Webhook delivery policy
tags: [webhooks, integration, reliability]
---
# Webhook delivery policy

## Events

Merchants subscribe endpoints to event types such as `payment.captured`,
`refund.succeeded`, `payout.paid`, and `dispute.opened`. Each event is
delivered at least once; consumers must de-duplicate by `event_id`.

## Delivery and retries

A delivery is successful when the endpoint returns any 2xx within 10 seconds.
Failed deliveries are retried with exponential backoff starting at 1 minute
and doubling up to 12 hours, for a maximum of **3 days**. After that the event
is marked `undeliverable` and the merchant is emailed. Endpoints failing for
7 consecutive days are disabled automatically.

## Ordering

Events are delivered in order per resource on a best-effort basis; consumers
must not rely on ordering across resources. The payload carries the resource
id and a `version` so consumers can fetch the current state and discard stale
deliveries.

## Signing

Every delivery carries an `X-Kestrel-Signature` header: an HMAC-SHA256 over
the raw body with the endpoint secret, plus a timestamp. Consumers should
reject deliveries older than 5 minutes to prevent replay. Secrets can be
rotated with a 24-hour overlap during which both signatures are sent.

## Delivery logs

The portal shows the last 30 days of deliveries per endpoint with status,
attempts, and response code, and allows manual redelivery of any event.
