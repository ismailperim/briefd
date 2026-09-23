# 0008 — Link knowledge to code through `refs`; do not index code

- Status: accepted
- Date: 2026-09-23

## Context

Agents spend most of their tokens on code, so "should briefd index the
source code too?" is the first feature request every knowledge tool gets.
SPEC §1 lists code indexing as a non-goal, and the reasons still hold:

- Agents already read code well. Claude Code has grep and LSP, Cursor has
  its own codebase index; a general vector search over code loses to exact
  match and symbol navigation for the questions agents actually ask.
- It changes what briefd is. "Reviewed team knowledge with a lifecycle"
  becomes "another code RAG", a crowded field, and the answer to "isn't
  this just RAG?" stops being honest.
- It does not fit the constraints: millions of lines through a CPU encoder,
  brute-force cosine in SQLite past ~1M chunks, and an index that churns
  with every commit.

What agents *do* lack is the link in the other direction: while editing
`services/payment/refund.go`, which rules apply? ADR-0007 already made
documents claim code through `refs` globs and follows code repositories
history-only. That is enough to answer the question without embedding a
line of code.

## Decision

1. **Path-aware retrieval.** `compile_bundle` and `search_context` accept
   `paths`: repository-relative code paths the task touches. Documents whose
   `refs` cover a path are *governing* documents. Their sections are fused
   into the ranking as a second list (RRF, k = 60) with weight 1.5 over the
   retrieval list, at most three sections per document and six in total.
   A governing section that retrieval also found scores highest; one that
   retrieval missed still enters above the general matches; without paths,
   or when nothing claims them, the ranking is unchanged. The bundle cache
   key includes the normalised path set.
2. **Coverage.** `GET /api/coverage` and a dashboard panel list, per followed
   code repository, the directories (two levels deep) that no document's
   refs reach — the knowledge base's blind spots. Computed on demand from
   the HEAD tree and the current refs; nothing is stored.
3. **No code indexing.** Revisiting that needs its own ADR with a use case
   the agent's native tools cannot serve.

## Consequences

- The golden sets carry no paths, so retrieval numbers are unchanged
  (EN hybrid R@5 0.830 / R@10 0.926 / MRR 0.746; TR 0.933 / 1.000 / 0.847).
  Path-aware ranking is covered by a deterministic test on the sample
  corpus instead.
- Quality of both features depends on teams writing `refs`. Coverage makes
  the missing ones visible, which is the intended pressure.
- Agents need to pass paths. The `skills/briefd` skill and the tool
  descriptions say so; editor hooks can do it automatically later.
