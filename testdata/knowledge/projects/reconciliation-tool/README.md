---
title: reconciliation-tool
tags: [treasury, reconciliation, tooling]
---
# reconciliation-tool

A batch tool and small web UI used by treasury to run and inspect daily
reconciliation between the ledger, acquirer settlement files, and bank
statements.

## Batch run

Runs at 01:00 UTC after the settlement batch is cut: downloads acquirer
files over SFTP, parses them into a common line format, matches against
ledger captures using the reconciliation rules, and writes exceptions to the
queue. A run is idempotent per batch date; re-running re-matches only
unmatched lines.

## File formats

Each acquirer has a parser under `parsers/<acquirer>/` with golden test
files. New acquirer formats require a parser plus at least three anonymized
sample files.

## Exceptions UI

Treasury resolves exceptions by attaching a compensating posting request,
which is submitted to the ledger through the normal API with an idempotency
key derived from the exception id. The UI never writes to the ledger
database.

## Reports

Produces the daily reconciliation report as PDF and CSV, archived to the
10-year store.
