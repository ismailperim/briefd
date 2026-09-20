---
title: KYC and merchant onboarding
tags: [compliance, kyc, onboarding]
---
# KYC and merchant onboarding

## Tiers

Merchants are onboarded into one of three tiers that determine limits before
full verification:

| Tier | Requirements | Monthly volume cap | Payouts |
|---|---|---|---|
| `starter` | email, phone, business name | 5,000 EUR | held |
| `verified` | + registration document, representative ID | 100,000 EUR | daily |
| `enterprise` | + beneficial owners, financial statements | none | any schedule |

A merchant may accept payments at `starter` tier, but payouts stay on hold
until they reach `verified`.

## Document review

Documents are reviewed by the compliance team within two business days.
Automated checks run first: document expiry, name matching against the
registration, and sanctions screening of the company and every beneficial
owner holding 25% or more. A screening hit blocks onboarding until a human
clears it; the merchant is told only that verification is pending.

## Re-verification

Verification expires after **24 months**, or immediately when a representative
changes, the registered address changes, or monthly volume exceeds ten times
the trailing average. Re-verification reuses documents that are still valid.

## Prohibited businesses

Gambling, adult content, weapons, and cryptocurrency exchanges are prohibited
regardless of tier. A merchant category code from the prohibited list rejects
the application automatically with `mcc_prohibited`.

## Data handling

Identity documents are stored encrypted in the `identity` service only, with
access logged. They are deleted 5 years after the merchant relationship ends,
in line with the data retention rule.
