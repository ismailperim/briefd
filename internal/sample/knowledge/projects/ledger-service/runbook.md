---
title: ledger-service runbook
tags: [ledger, runbook, oncall]
---
# ledger-service runbook

## Projection drift alert

**Alert:** `LedgerProjectionDrift` — the nightly rebuild found balances that
differ from the live projection.

1. Check `ledger_projection_drift_total` for the affected merchants.
2. Look for postings written directly to projection tables (there should be
   none; ADR-0007 forbids it).
3. Run `ledger rebuild --merchant <id>` to rebuild the projection from events.
4. If drift recurs, page the ledger team lead; do not silence the alert.

## Settlement batch late

**Alert:** `SettlementBatchLate` — the 00:00 UTC batch has not completed by
02:00 UTC.

1. Check whether acquirer files arrived (`settlement_files_received_total`).
2. If a file is missing, contact the acquirer on-call via the treasury
   channel; the batch will complete with `late_settlement` markers when it
   arrives.
3. If files arrived but reconciliation is stuck, inspect the exceptions queue
   for a poison item and move it aside.

## Posting backlog

If `ledger_postings_queue_depth` grows past 10,000, projection refresh is
falling behind. Scale replicas to 5 and check Postgres for lock waits on the
`balances` table.
