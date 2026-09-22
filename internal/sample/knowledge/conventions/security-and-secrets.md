---
title: Security and secrets handling
tags: [security, secrets, conventions]
---
# Security and secrets handling

## Secrets

Secrets live in the secrets manager and reach services as environment
variables injected at deploy time. Never commit secrets, never log them,
never pass them as command-line arguments. Rotation must not require a
deploy: services re-read secrets on SIGHUP or within 5 minutes.

## API keys and tokens

Merchant API keys are random 32-byte values shown once and stored as
SHA-256 hashes. Internal service-to-service calls use short-lived JWTs
issued by `identity` with a 10-minute lifetime and an audience claim per
service.

## Dependencies

Dependencies are pinned and updated weekly by the bot; a critical CVE is
patched within 48 hours. New dependencies need a license check (no
AGPL/SSPL) and a maintainer review of the package's activity.

## Input handling

All external input is validated at the edge with explicit schemas; amounts
are range-checked against the merchant's limits. SQL is parameterized
without exception. Uploaded documents are scanned and re-encoded before
storage.

## Incident classification

A suspected leak of PAN, credentials, or identity documents is a P1
incident: page security, freeze affected credentials, and start the
timeline document within 15 minutes.
