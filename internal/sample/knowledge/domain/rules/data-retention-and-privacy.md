---
title: Data retention and privacy
tags: [compliance, privacy, gdpr, retention]
---
# Data retention and privacy

## Retention periods

| Data | Retained for | Basis |
|---|---|---|
| Transaction records and ledger events | 10 years | financial regulation |
| Identity documents (KYC) | 5 years after relationship ends | AML |
| Cardholder personal data (name, email, address) | 13 months after last transaction | dispute window plus margin |
| Idempotency records | 24 hours | operational |
| Application logs | 90 days | operational |
| Webhook delivery logs | 30 days | operational |

## Cardholder data

We never store the full PAN, the CVV, or magnetic stripe data. Cards are
tokenized by the vault (ADR-0018) and only the token, the last four digits,
the expiry month/year, and the BIN are available to services. Screenshots,
logs, and support tickets containing a PAN are a security incident.

## Right to erasure

A cardholder erasure request removes personal data that is not required by
financial regulation: name, email, address, IP, device fingerprint. Transaction
amounts and tokens are retained but no longer linkable to a person. Requests
are executed within 30 days and confirmed to the requester.

## Data residency

Merchant and cardholder data for EU merchants is stored in the EU region;
US merchants in the US region. Cross-region replication is allowed only for
encrypted backups. Analytics exports are pseudonymized before leaving the
region.

## Access to production data

Engineers access production data through the support tooling with a ticket
reference; direct database access is limited to the on-call engineer during
an incident and is logged. Exports of personal data for debugging are
prohibited.
