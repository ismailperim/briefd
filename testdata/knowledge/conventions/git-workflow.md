---
title: Git workflow and code review
tags: [git, process, code-review]
---
# Git workflow and code review

## Branching

`main` is always deployable. Work happens on short-lived branches named
`<type>/<ticket>-<slug>`, for example `feat/PAY-812-partial-capture`. Branches
older than two weeks are flagged in the weekly review; long-running work is
merged behind a feature flag instead.

## Commits

Conventional commits: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`,
`chore:`, with the ticket id in the footer. Squash-merge is the default so
the main history is one commit per pull request.

## Pull requests

Every PR needs one approving review from a code owner of the touched paths
and a green pipeline. PRs touching money movement (`ledger`, `payments`,
`payouts`) need two approvals, one from the domain owner. Reviews should be
picked up within one business day; a PR waiting longer is raised in the team
channel.

## What reviewers check

Correctness against the domain rules, test coverage for new behavior, error
handling and idempotency for anything that retries, migration backward
compatibility, and that the PR description explains *why*. Style comments go
to the linter, not the review.

## Release tagging

Services are released by tagging `main` with `v<major>.<minor>.<patch>`; the
pipeline builds, signs, and deploys the tag to staging automatically and to
production after the canary stage.
