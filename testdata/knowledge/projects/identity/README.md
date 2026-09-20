---
title: identity
tags: [identity, auth, service]
---
# identity

Authentication and authorization for merchant users, support staff, API
keys, and service-to-service calls.

## Responsibilities

- Merchant user login with password and mandatory TOTP; SSO for staff
- API key issuance, hashing, scoping, rotation with overlap
- Short-lived JWTs for service-to-service calls (10 minutes, audience per
  service)
- Role assignments per merchant (viewer, operator, refunds:approve, admin)
- Storage of KYC identity documents (encrypted, access-logged)

## Sessions

Merchant sessions last 12 hours and renew on activity; they are revoked on
password change, role removal, or by an admin. Staff assuming a merchant
context creates an audited impersonation session limited to 1 hour.

## Rate limits

Login attempts are limited to 5 per 15 minutes per account and per IP;
further attempts return `too_many_attempts` without revealing whether the
account exists.

## Dependencies

No dependency on payments or ledger. The portal, the gateway, and every
service depend on identity, so it runs with the highest availability target
(99.99%).
