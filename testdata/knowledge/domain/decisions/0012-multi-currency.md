---
title: "ADR-0012: Multi-currency balances and FX snapshots"
tags: [adr, currency, treasury]
---
# ADR-0012: Multi-currency balances and FX snapshots

- Status: accepted
- Date: 2025-08-02

## Context

Merchants increasingly accept payments in several currencies but want payouts
in one. Converting at capture time exposed us to intraday FX movement and made
refund amounts unpredictable for merchants.

## Decision

Balances are kept **per currency**; no implicit conversion happens at capture
or refund. Conversion happens only at payout, when the merchant has opted into
a single payout currency, using the **daily FX snapshot** taken at 00:00 UTC
from our treasury provider. The snapshot rate is stored with the payout so
the amount can be explained later.

Rules and thresholds that are expressed in EUR (refund approvals, minimum
payouts) use the same daily snapshot so that a decision is stable for a whole
day.

## Consequences

- Amounts are always stored as integer minor units plus an ISO 4217 code.
  Never use floating point for money.
- The FX snapshot job is a tier-1 dependency: if it fails, payouts with
  conversion are paused and treasury is paged.
- Reports must state the snapshot date used for any converted figure.
