---
title: Reconciliation
tags: [treasury, reconciliation, settlement]
---
# Reconciliation

## What is reconciled

Three sources are compared every day: our ledger (captures, refunds, fees),
acquirer settlement files, and bank statements for the settlement accounts.
Every capture in a settlement batch must match an acquirer line, and the sum
of acquirer lines must match the bank credit for that batch.

## Matching rules

Lines match on acquirer reference first, then on (amount, currency, card last
four, date ± 1 day) as a fallback. A fallback match is flagged `weak_match`
and reviewed weekly. Amounts differing by less than 0.01 in the settlement
currency are treated as rounding and auto-adjusted with a `rounding` posting.

## Exceptions

Unmatched items go to the treasury exceptions queue with a category:
`missing_at_acquirer`, `missing_in_ledger`, `amount_mismatch`, or
`duplicate`. Exceptions older than 5 business days page the treasury lead.
Exceptions are resolved by a compensating ledger posting, never by editing
either source.

## Late settlements

An acquirer line arriving after its batch closed is attached to the current
batch with a `late_settlement` marker and matched against the original
capture. Late items do not reopen the closed batch.

## Reports

The daily reconciliation report lists batch status, matched totals, exception
counts by category, and aging of open exceptions. It is produced by the
`reconciliation-tool` and archived for 10 years alongside the ledger.
