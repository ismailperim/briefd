---
title: Glossary
tags: [domain, terminology]
---
# Glossary

Shared vocabulary for every Kestrel Pay project. When a term here conflicts with
a term in a project README, this file wins; open a proposal to change it.

## Parties

### Merchant

A business that accepts payments through Kestrel Pay. Identified by a
`merchant_id` (ULID). Merchants belong to exactly one **organization** and may
have many **stores**. Turkish teams often say *üye işyeri*; use "merchant" in
code and documentation.

### Acquirer

The bank or financial institution that processes card transactions on behalf
of a merchant and receives funds from the card networks. We integrate with
several acquirers; each integration lives behind the `Acquirer` interface in
`ledger-service`.

### PSP (Payment Service Provider)

Kestrel Pay itself, from the merchant's point of view. Avoid using "PSP" for
third parties we integrate with — call those acquirers or gateways.

### Cardholder

The end customer paying with a card. We never store PAN (primary account
number) data; only the network token and the last four digits.

## Money movement

### Authorization

A hold placed on the cardholder's funds. An authorization expires after 7 days
if not captured (network default; some acquirers allow 30). Authorizations do
not move money and never appear in the ledger as a posting.

### Capture

Converting an authorization into an actual charge. Captures may be partial.
The first capture of an authorization creates the **payment** in the ledger.

### Settlement

The process by which the acquirer transfers captured funds to Kestrel Pay.
Settlement batches are cut daily at 00:00 UTC. Settlement is distinct from
**payout** (see below): settlement is money coming in, payout is money going
out to the merchant.

### Payout

A transfer from Kestrel Pay's merchant balance to the merchant's bank account.
Payouts run on the merchant's payout schedule (daily, weekly, or manual).
Turkish alias: *hakediş ödemesi*.

### Refund

Returning captured funds to the cardholder. Refunds are always tied to an
original payment and may be partial. See the refunds rule for the window and
approval requirements.

### Chargeback

A forced reversal initiated by the cardholder's bank (issuer) through the card
network. Turkish alias: *ters ibraz*. Chargebacks carry a reason code and a
dispute deadline; the merchant may submit evidence (representment).

## Identifiers

### Idempotency key

A client-supplied unique string sent with any request that creates a resource
or moves money. Replaying a request with the same key must return the original
result without side effects. See the idempotency rule for retention.

### Ledger posting

An immutable double-entry record in `ledger-service`: every posting debits one
account and credits another for the same amount and currency. Postings are
never updated or deleted; corrections are new postings.
