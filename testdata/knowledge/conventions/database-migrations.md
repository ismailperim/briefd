---
title: Database migrations
tags: [database, migrations, postgres]
---
# Database migrations

## Tooling

Migrations are plain SQL files under `migrations/` numbered
`NNNN_description.sql`, applied by the service at startup (pre-install hook
in Kubernetes) with an advisory lock so replicas do not race.

## Backward compatibility

Every migration must work with the previous release still running: add
columns as nullable or with defaults, never rename or drop a column in the
same release that stops using it. Removing a column takes three releases:
stop writing, stop reading, drop.

## Large tables

Adding an index to a table with more than a million rows uses
`CREATE INDEX CONCURRENTLY` outside a transaction. Backfills run in batches
of 10,000 rows with a sleep between batches and are resumable.

## Data migrations

Data changes that encode business meaning (recomputing balances, fixing fee
postings) are not migrations; they are compensating ledger events produced
by a reviewed script, so the audit trail stays intact.

## Rollback

Migrations are not rolled back; a bad migration is fixed forward with a new
migration. Deployments can still roll back the application because of the
compatibility rule above.
