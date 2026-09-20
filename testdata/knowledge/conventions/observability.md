---
title: Observability conventions
tags: [observability, logging, metrics, tracing]
---
# Observability conventions

## Logging

Structured JSON logs via `log/slog`. Required fields on every line: `service`,
`env`, `request_id`, and `merchant_id` when known. Never log PAN, CVV, full
IBAN, or bearer tokens; the log pipeline rejects lines matching the PAN regex
and pages the owning team.

Log levels: `debug` for developer detail (off in production), `info` for
business events, `warn` for handled anomalies, `error` for failures that
need a human.

## Metrics

Prometheus metrics exposed on `/metrics`. Naming follows
`<service>_<subsystem>_<name>_<unit>`, e.g. `ledger_postings_total`,
`ledger_projection_lag_seconds`. Every request handler records a latency
histogram with buckets `5ms 10ms 25ms 50ms 100ms 250ms 500ms 1s 2.5s 5s`.

## Tracing

OpenTelemetry traces with W3C trace context propagated over HTTP and the
message bus. Sample 10% of requests in production, 100% in staging. Spans on
external calls must carry the peer service name and the acquirer id.

## Dashboards and alerts

Each service owns one overview dashboard: request rate, error rate, latency
p50/p95/p99, saturation, and its tier-1 dependencies. Alerts are defined as
code next to the service and page only on user-visible impact; everything
else goes to a ticket queue.

## SLOs

Public API availability 99.95% monthly; payment creation latency p95 under
400 ms; settlement batch completed by 02:00 UTC 99% of days.
