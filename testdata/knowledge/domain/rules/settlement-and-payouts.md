---
title: Settlement and payouts
tags: [payments, settlement, payouts, treasury]
---
# Settlement and payouts

## Settlement cycle

Acquirers send settlement files once per day. We cut our internal settlement
batch at **00:00 UTC** and reconcile every acquirer file against the captures
in that batch. A batch is `reconciled` when every capture is matched, or
`reconciled_with_exceptions` when unmatched items were routed to the treasury
queue. Batches are never re-opened; late items are attached to the next batch
with a `late_settlement` marker.

## Merchant balance

Each merchant has one balance per currency, made of:

- **available** — captured and settled funds minus refunds, fees, and reserves
- **pending** — captured but not yet settled by the acquirer
- **reserve** — a rolling percentage (default 5%, risk-adjustable) held for
  90 days to cover chargebacks

Only the available balance can be paid out.

## Payout schedules

Merchants choose one of:

- `daily` — every business day at 06:00 in the merchant's configured timezone
- `weekly` — a chosen weekday
- `manual` — merchant triggers payouts from the portal or API

A payout is only created when the available balance exceeds the merchant's
minimum payout amount (default 50 EUR equivalent). Payouts below the minimum
roll over to the next run.

## Payout failures

If a bank rejects a payout (closed account, wrong IBAN), the funds return to
the available balance and the merchant is notified. After three consecutive
failures the schedule is switched to `manual` and the account is flagged for
KYC review.

## Fees

Fees are deducted at capture time as a separate ledger posting from
`merchant_payable` to `fee_revenue`. Fee schedules are per merchant and
versioned; a capture always uses the schedule version active at capture time.
