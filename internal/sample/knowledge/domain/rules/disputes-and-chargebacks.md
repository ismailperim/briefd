---
title: Disputes and chargebacks
tags: [payments, disputes, chargebacks, compliance]
---
# Disputes and chargebacks

## Lifecycle

A dispute starts when the issuer sends a chargeback through the card network.
We record it as `dispute.opened` with the network reason code, the disputed
amount (which may be less than the payment), and the **evidence deadline**.
From there it moves to `under_review` once evidence is submitted, and ends in
`won`, `lost`, or `withdrawn` when the network rules.

## Evidence deadlines

Evidence must be submitted before the network deadline, which is typically
**7 days** for Visa and **10 days** for Mastercard after the chargeback is
received; the exact date is always taken from the network message, never
computed locally. Merchants see the deadline in the portal and receive a
reminder 48 hours before it. Evidence uploaded after the deadline is stored
but not forwarded.

## Funds handling

When a dispute opens, the disputed amount is moved from the merchant's
available balance to a `disputed` hold and the chargeback fee is charged
immediately. If the dispute is won, the hold is released; if lost, the hold is
debited permanently. The fee is never refunded, even on a win — this mirrors
the network's own fee handling.

## Reason codes

Reason codes are normalized into five families: `fraud`, `authorization`,
`processing_error`, `consumer_dispute`, and `other`. The raw network code is
kept alongside for evidence templates. Fraud-family disputes on payments that
used 3-D Secure are auto-flagged as `liability_shifted` and usually win
without merchant action.

## Pre-arbitration and arbitration

If the merchant wins and the issuer escalates to pre-arbitration, the risk
team owns the case. Arbitration filing requires director approval because the
network charges a non-refundable filing fee of 500 USD.
