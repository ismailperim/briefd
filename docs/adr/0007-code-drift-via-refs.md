# 0007 — Code drift: knowledge that lags the code it governs

- Status: accepted
- Date: 2026-09-21

## Context

A knowledge base rots quietly. The refund rules say 180 days; the code has
said 90 for three months; the agent that reads the rules cannot know. SPEC §2
reserved a `refs` front-matter field ("code paths this doc governs, staleness
input") without defining the mechanism. Two facts are now available to build
one on: each document's `updated_at` (ADR-0006 follow-up work: the last commit
that touched it) and go-git access to any repository's history (ADR-0005).

Options considered:

1. **Index the code repositories** and compare content. Far too expensive, and
   code indexing is an explicit non-goal (SPEC §1).
2. **Watch commit history only**: for each document with `refs`, count the
   commits in the configured code repositories that touched a matching path
   *after* the document's last change. Cheap (tree-hash diffs, bounded walk),
   needs no working files, and the signal is exactly what a maintainer asks:
   "did the code move after the doc?"
3. Diff-content heuristics (which symbols changed, how much) on top of 2 —
   more precise, much more complex; deferred until 2 proves useful.

## Decision

1. `code.repos` in the config lists repositories to follow. A git URL is
   cloned **bare** next to the database (`<db dir>/code-repos/<name>`) with the
   same `git.*` credentials as the knowledge repo; a local path is opened in
   place. briefd never reads their working files.
2. On every sync, after indexing, each code repository is fetched. When a code
   head moved, the index changed, or nothing has been computed yet, history is
   walked newest-first (`ChangesSince`) from the **oldest `updated_at` among
   documents with refs**, capped at `code.max_commits` (5000). Each commit's
   changed paths — those that differ from every parent, as `git log` counts
   merges — are matched against every document's globs.
3. Globs are matched by a small in-tree matcher (`internal/staleness`): `*`
   and `?` stay within a segment, `**` spans segments, a plain path is a
   prefix. No new dependency.
4. The result is one `code_drift` row per document: repo, commits since,
   last change time and path. When the rows change, the bundle cache is
   invalidated, because drift is **rendered into the attribution line**:
   `## path — heading (updated 2026-03-01; code changed since: 3 commits,
   last 2026-06-01)`. That costs roughly ten tokens per affected section and
   only when there is something to say; the agent can weigh the section and
   is often the right party to fix it with `propose_update`.
5. Search results and bundle sections carry `code_changes` /
   `code_changed_at`; `/api/stats` lists the eight most-behind documents; the
   dashboard shows them as "Behind the code"; Prometheus exposes
   `briefd_documents_behind_code`.

## Consequences

- Zero cost without `code.repos`; with them, one fetch per repository per
  sync and a bounded history walk that usually stops within a few commits.
- Drift is a hint, not a verdict: a formatting-only commit counts, a
  behavioural change outside the globs does not. Precision improvements
  (option 3) can be layered on the same table.
- A document without a known age (plain directory, first run before ages are
  resolved) is skipped rather than guessed.
- Merge commits are attributed to their merge time, not to the branch's
  commits — same as `git log --first-parent` would show a maintainer.
