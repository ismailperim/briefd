---
title: Fraud and risk scoring
tags: [risk, fraud, payments]
---
# Fraud and risk scoring

## Scoring model

Every payment attempt receives a risk score from 0 to 100 computed by the
`risk-engine` before authorization. Inputs include velocity (attempts per
card, per IP, per merchant in the last hour and day), BIN country versus
billing country mismatch, device fingerprint reputation, and merchant-level
chargeback ratio. The score is stored on the payment and is visible to the
merchant as a band: `low`, `medium`, `high`.

## Actions by score

- **0–39 (low):** authorize normally.
- **40–74 (medium):** request 3-D Secure if the issuer supports it; otherwise
  authorize.
- **75–100 (high):** decline with `risk_declined`, unless the merchant has an
  approved allow-list rule for the customer.

Thresholds are per merchant and can be tightened, never loosened, by the
merchant. Loosening requires the risk team.

## Velocity limits

Hard limits apply independently of the score: a single card may attempt at
most 5 payments per minute and 20 per day across all merchants. Exceeding a
limit returns `velocity_exceeded` and the attempt is not scored.

## Merchant monitoring

Merchants whose chargeback ratio exceeds **0.9%** over a rolling 30 days are
placed in a monitoring program: reserve rises to 10% and the risk team reviews
the account weekly. Above 1.5% for two consecutive months, the account is
suspended pending review, in line with network monitoring programs.

## Manual review queue

Payments in the medium band above 2,000 EUR go to a manual review queue with
a 4-hour SLA. Reviewers can approve, decline, or request documents; every
decision is logged with the reviewer id for audit.
