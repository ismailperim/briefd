---
title: Example business rule
tags: [rules]
refs: ["services/example/**"]   # code paths this rule governs (optional)
---
# Example business rule

## When it applies

State the situation precisely: which operation, which actor, which limits.

## The rule

The rule itself, with concrete numbers and the error code or behavior when it
is violated (for example: requests after 180 days are rejected with
`window_expired`).

## Why

One or two sentences on the reason — agents use this to judge edge cases.
