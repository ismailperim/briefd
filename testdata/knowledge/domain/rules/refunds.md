---
title: Refund rules
tags: [payments, refunds, compliance]
refs: ["services/ledger/refund/**", "apps/merchant-portal/src/refunds/**"]
---
# Refund rules

These rules apply to every surface that can initiate a refund: the merchant
portal, the public API, and support tooling.

## Refund window

A refund may be requested up to **180 days** after the capture date. Beyond
that the request must be rejected with `refund_window_expired`. Card networks
allow up to 180 days for disputes, so refunds after the window would leave us
unable to reconcile against a later chargeback.

Partial refunds are allowed; the sum of all refunds on a payment may never
exceed the captured amount. Attempting to over-refund returns
`refund_exceeds_capture`.

## Approval thresholds

| Amount (per refund) | Required approval |
|---|---|
| ≤ 500 EUR equivalent | none — merchant self-service |
| 500 – 5,000 EUR | second merchant user with the `refunds:approve` role |
| > 5,000 EUR | merchant approval **and** Kestrel Pay risk team |

Thresholds are evaluated in EUR using the daily FX rate snapshot taken at
00:00 UTC (see the multi-currency decision). The threshold check happens at
request time, not at execution time.

## Refunds on disputed payments

If a payment has an open chargeback, refunds are blocked with
`payment_disputed`. Refunding a disputed payment would return the funds twice
if the dispute is later lost. Once the dispute is closed in the merchant's
favor, refunds are allowed again within the normal window.

## Refunds and payouts

Refunds are debited from the merchant's available balance immediately. If the
balance is insufficient, the refund is still accepted but the merchant balance
goes negative and the next payout is reduced or skipped until the balance is
positive again. Merchants see this as a `pending_balance_recovery` flag in the
portal.

## Ledger representation

Every refund produces exactly one ledger posting from `merchant_payable` to
`cardholder_refunds`, referencing the original payment posting. A refund that
fails at the acquirer produces a reversing posting; it is never deleted.
