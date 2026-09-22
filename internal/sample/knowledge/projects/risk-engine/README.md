---
title: risk-engine
tags: [risk, fraud, service]
---
# risk-engine

Computes the risk score for payment attempts and manages velocity counters,
allow-lists, and the manual review queue.

## Scoring pipeline

1. Velocity counters are checked first (per card, IP, merchant); exceeding a
   hard limit short-circuits with `velocity_exceeded`.
2. Features are assembled: BIN country, billing country, device reputation,
   merchant chargeback ratio, amount relative to merchant average.
3. The model produces a score 0–100; merchant thresholds map it to a band.

Scoring must complete in under 150 ms p99; on timeout the caller uses the
`medium` band fallback.

## Model updates

Models are trained offline weekly on labeled disputes and refunds, validated
against the previous week's holdout, and rolled out with a shadow period of
24 hours during which both models score and only the old one decides.

## Counters

Velocity counters live in the service's Postgres with a 24-hour TTL; they
are the one place where an in-memory cache is used in front of the database,
because the counters are hot and tolerate a few seconds of staleness.

## Review queue

The manual review queue exposes `GET /internal/reviews` to the admin tooling
with a 4-hour SLA tracked as a metric.
