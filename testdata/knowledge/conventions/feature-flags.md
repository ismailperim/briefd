---
title: Feature flags
tags: [feature-flags, release, conventions]
---
# Feature flags

## When to use a flag

Any change that alters money movement, a merchant-visible behavior, or an
integration with an acquirer ships behind a flag. Pure refactors do not.

## Flag types

- **Release flags** gate unfinished work; removed within 30 days of full
  rollout.
- **Ops flags** (kill switches) let on-call disable a feature or an acquirer
  route instantly; they are permanent and documented in the runbook.
- **Merchant flags** enable a capability for specific merchants, managed
  from the admin tooling.

## Rollout

Release flags roll out by percentage of merchants, not of requests, so a
merchant sees consistent behavior. Start at 1%, then 10%, 50%, 100%, with at
least one business day between steps for money-moving features.

## Defaults and failure

A flag evaluation that fails (service unavailable) returns the flag's
declared default, which must be the safe behavior. Flags are cached locally
for 60 seconds.

## Hygiene

The flag service reports flags fully on or off for more than 30 days; owners
remove them or convert them to ops flags.
