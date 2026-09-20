---
title: "ADR-0015: API versioning by URL path"
tags: [adr, api, versioning]
---
# ADR-0015: API versioning by URL path

- Status: accepted
- Date: 2025-09-03

## Context

We needed a versioning strategy for the public API before the first breaking
change (moving amounts from decimal strings to minor-unit integers). Options
considered: header-based versioning, date-based versions like Stripe, and a
major version in the path.

## Decision

Breaking changes ship under a new major version path (`/v2/`). Additive
changes are made in place. Old major versions are supported for at least 18
months after the new one is announced, with a `Sunset` header during the last
6 months. Date-based versioning was rejected because our merchants
integrate once and rarely revisit; a path is easier to reason about than a
pinned date.

## Consequences

- At most two major versions are live at any time.
- SDKs are generated per major version from the OpenAPI document.
- Webhook payload versions follow the API version the endpoint was
  registered with.
