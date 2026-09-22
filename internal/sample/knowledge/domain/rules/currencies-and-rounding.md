---
title: Currencies and rounding
tags: [currency, money, rounding]
---
# Currencies and rounding

## Representation

Amounts are integers in the currency's minor unit with an ISO 4217 code:
1,250 for EUR 12.50, 1250 for JPY 1250 (zero decimals), 12500 for KWD 12.500
(three decimals). The minor-unit exponent comes from the currency table in
`platform/money`; never hard-code 100.

## Supported currencies

Payments can be accepted in 25 currencies; payouts are made in EUR, USD, GBP,
TRY, and PLN. A merchant's payout currency must be one of the payout
currencies; balances in other currencies are converted at payout using the
daily FX snapshot (ADR-0012).

## Rounding rules

Percent-based calculations (fees, partial refunds by percentage, FX
conversion) round half to even at the final step only; intermediate values
keep full precision. Splitting an amount (for example a marketplace split
between sellers) distributes the remainder one minor unit at a time to the
largest shares so the parts always sum to the original.

## Display

Formatting for display happens at the edge (portal, invoices, emails) using
the merchant's locale. Services and APIs never exchange formatted strings.
