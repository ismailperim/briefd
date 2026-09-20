---
title: Kubernetes deployment conventions
tags: [infra, kubernetes, deployment]
---
# Kubernetes deployment conventions

## Manifests

Every service ships a Helm chart under `deploy/chart/` generated from the
shared `kestrel-service` library chart. Do not hand-write Deployments; set
values instead. Charts are linted and rendered in CI.

## Resources

Requests and limits are mandatory. Start from 250m CPU / 256Mi memory and
adjust from production utilization; a service must not exceed 80% of its
memory limit at p99 over a week.

## Rollouts

Deployments use rolling updates with `maxUnavailable: 0` and readiness probes
that check dependencies. Money-moving services additionally use a canary
stage at 5% traffic for 15 minutes with automatic rollback on error-rate
increase.

## Configuration and secrets

Configuration is environment variables rendered from values files per
environment. Secrets come from the external secrets operator; never commit
secrets or put them in values files. Rotating a secret must not require a
code change.

## Databases

Each service owns its own Postgres database; cross-service access goes
through APIs or events, never shared tables. Migrations run as a pre-install
hook and must be backward compatible with the previous release.
