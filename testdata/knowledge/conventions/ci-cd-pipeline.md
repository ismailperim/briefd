---
title: CI/CD pipeline
tags: [ci, cd, deployment, pipeline]
---
# CI/CD pipeline

## Stages

1. **Lint and unit tests** on every push, under 5 minutes.
2. **Integration tests** for touched services against Docker dependencies.
3. **Build and sign** container images; the image digest is recorded in the
   deployment manifest.
4. **Deploy to staging** automatically on merge to `main`.
5. **Production** deploys are triggered by a release tag, run the canary
   stage from the Kubernetes conventions, and require a passing end-to-end
   smoke test.

## Artifacts

Images are built once and promoted; never rebuilt per environment. SBOMs are
attached to every image and scanned for vulnerabilities before promotion.

## Deployment windows

Production deploys of money-moving services happen Monday to Thursday,
09:00–16:00 in the team's timezone, never during the 00:00 UTC settlement
window or the payout run at 06:00 local time. Emergency fixes are exempt with
incident commander approval.

## Rollback

Every deploy keeps the previous image available; rollback is a single
command and is exercised in staging weekly. Database migrations follow the
compatibility rule so rollback never needs a schema change.

## Secrets in CI

CI uses short-lived credentials from the identity provider; no long-lived
tokens are stored in the CI system.
