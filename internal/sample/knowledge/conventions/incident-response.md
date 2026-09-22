---
title: Incident response
tags: [oncall, incidents, process]
---
# Incident response

## Severity

- **P1:** money is moving wrongly, cardholder data may be exposed, or the
  API is down for more than 5% of merchants. Page immediately, 24/7.
- **P2:** a feature is degraded for many merchants with a workaround.
  Business-hours response within 1 hour.
- **P3:** single-merchant or cosmetic issues. Ticket.

## Roles

The on-call engineer is incident commander until handing over explicitly.
The commander does not debug; they coordinate, keep the timeline, and decide
on mitigations. A communications lead posts merchant updates every 30
minutes for P1.

## First actions

Stop the bleeding before finding the cause: flip the ops flag, roll back the
last deploy, or pause payouts. Pausing payouts is always safe; pausing
captures is not (authorizations expire).

## Postmortems

Every P1 and P2 gets a blameless postmortem within 5 business days with a
timeline, contributing factors, and actions with owners and due dates.
Actions are tracked until closed; the postmortem is shared company-wide.

## Money incidents

If postings may be wrong, freeze payouts for the affected merchants first,
then reconcile. Corrections are compensating postings reviewed by treasury;
never edit ledger data directly.
