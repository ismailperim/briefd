---
title: "ADR-0018: Card tokenization vault"
tags: [adr, security, pci, cards]
---
# ADR-0018: Card tokenization vault

- Status: accepted
- Date: 2026-02-10

## Context

PCI DSS scope covered every service that touched card numbers, which made
audits expensive and slowed down feature work in services that only needed
to reference a card.

## Decision

A dedicated `vault` service receives card data directly from the browser or
mobile SDK, stores the PAN encrypted with an HSM-managed key, and returns an
opaque token. All other services store only the token, the BIN, the last four
digits, and the expiry. Acquirer requests are made by the vault on behalf of
`payments-service`, which passes the token. The vault is the only service in
PCI scope.

## Consequences

- Network tokens from card schemes are stored alongside our tokens and used
  when available for better authorization rates.
- Detokenization is not exposed to any service; even support tooling only
  sees masked cards.
- Key rotation re-encrypts the vault in the background; tokens are stable
  across rotations.
