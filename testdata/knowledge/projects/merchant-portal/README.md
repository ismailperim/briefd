---
title: merchant-portal
tags: [portal, frontend, service]
---
# merchant-portal

The web application merchants use to view payments, issue refunds, manage
payouts, and configure their account. React front end with a Go BFF
(backend-for-frontend) that talks to internal services.

## Architecture

The BFF exposes a GraphQL API to the front end and calls `ledger-service`,
`payments-service`, and `identity` over HTTP. It holds no business rules of
its own: refund thresholds, payout minimums, and similar rules are enforced by
the owning services, and the portal only mirrors them for UX.

## Refund flow

The refund form pre-validates the amount against the captured amount and
shows the approval requirement from the refund rules, then submits with an
idempotency key generated per form submission. On `payment_disputed` the form
explains that refunds are blocked while a chargeback is open.

## Environments

`dev` uses mocked services; `staging` uses real services with test acquirers;
`prod` requires SSO with hardware keys for the support role.
