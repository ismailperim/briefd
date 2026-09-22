---
title: merchant-portal authentication and roles
tags: [portal, auth, security]
---
# merchant-portal authentication and roles

## Login

Merchant users authenticate through the `identity` service with email and
password plus mandatory TOTP. Sessions are 12 hours, renewed on activity, and
revoked on password change. Support staff log in through company SSO and
assume a merchant context explicitly, which is audit-logged.

## Roles

| Role | Can |
|---|---|
| `viewer` | read payments, balances, reports |
| `operator` | viewer + create refunds under the self-service threshold |
| `refunds:approve` | approve refunds in the 500–5,000 EUR band |
| `admin` | everything, including user management and payout settings |

Roles are per merchant; a user may hold different roles in different
merchants.

## API keys

Admins create API keys with a scope list. Keys are shown once, stored hashed,
and can be rotated with an overlap window of up to 7 days. Every API request
is attributed to a key for idempotency scoping and rate limits.
