---
title: Fees and pricing
tags: [fees, pricing, billing]
---
# Fees and pricing

## Fee schedule structure

A fee schedule is a versioned document attached to a merchant. It defines a
percentage plus a fixed amount per transaction, optionally varying by card
brand, card region (domestic, intra-EEA, international), and payment method.
Schedules are immutable once activated; a change creates a new version with
an effective date.

## When fees are charged

Transaction fees are charged at **capture time**, using the schedule version
active at that moment, as a separate ledger posting. Refund fees, if any, are
charged when the refund is executed. Chargeback fees are charged when the
dispute opens. Monthly fees (account, portal seats) are charged on the first
day of the month in the merchant's payout currency.

## Rounding

Fees are computed in minor units with banker's rounding (round half to even)
to avoid systematic bias in either direction. The percentage part is
calculated before the fixed part is added.

## Interchange plus

Enterprise merchants may opt into interchange-plus pricing, where the fee is
the actual interchange and scheme fee passed through plus our margin.
Interchange is known only at settlement, so the fee posting at capture uses an
estimate and a correcting posting is made when the settlement file arrives.

## Fee invoices

A monthly fee invoice is generated on the first business day and lists every
fee posting grouped by type. It is available in the portal and via
`GET /v1/invoices`. Disputing an invoice line opens a support ticket; fees are
never silently reversed.
