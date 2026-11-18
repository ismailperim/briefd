# 0011 — Proposal lifecycle without a forge, and closing from the dashboard

- Status: accepted
- Date: 2026-11-18

## Context

`propose_update` pushes a branch `briefd/proposal-<id>` (ADR-0005). With
`forge.type` `github` or `gitlab`, briefd also opens a pull request and
later reads its state, so a proposal ends as `merged` or `closed`.

Many teams host the knowledge repository where briefd has no forge adapter:
Azure DevOps, Bitbucket, Gitea, or a plain git server. There a reviewer
opens the pull request by hand, and briefd sees only git. Two problems
followed:

- The store only listed open proposals that had a pull request URL, so the
  existing "the change landed on the followed branch → merged" check never
  ran for these proposals. They stayed `open` after a merge.
- Git alone cannot tell "declined" from "not reviewed yet". A declined
  proposal stayed `open` on the dashboard indefinitely, and nobody could
  close it.

## Decision

1. **Merged.** Every open proposal without a forge pull request is checked
   on each sync. It is `merged` when its commit is an ancestor of the
   followed branch, or when the branch holds exactly the proposed content of
   the document, which covers squash and rebase merges.
2. **Closed by deleting the branch.** A proposal is `closed` when its branch
   is gone and its change has not landed. The reviewer declines it and
   deletes the branch, which Azure DevOps and Bitbucket offer when a pull
   request is abandoned. Details:
   - Branches are listed in one round trip (the equivalent of
     `git ls-remote`), at most once per `StatusSyncInterval` (10 minutes),
     and only while open proposals exist.
   - A branch that was never pushed is looked up in the local clone. A new
     `pushed` column records which case applies; rows from before it are
     assumed pushed.
   - The proposal commit must be present in the clone. If the clone was made
     again since, a missing branch could equally mean "merged, branch
     deleted", so the proposal stays open.
3. **Closed by hand.** `POST /api/proposals/{id}/close` (bearer token,
   JSON body `{"delete_branch": bool}`) and two dashboard buttons, *Close*
   and *Close & delete branch*, mark an open proposal closed.
   - With `delete_branch`, the branch is deleted on the remote and locally
     first. If that fails, nothing changes.
   - Only `briefd/proposal-*` branches can be deleted.
   - A forge pull request is not touched. GitHub closes it when its branch
     is deleted; otherwise it is closed on the forge.

Agents get no close tool: deciding on a proposal is the reviewer's job.

## Consequences

- Without a forge, the proposal list reflects reality after one sync (for
  merges) or one lookup interval (for deleted branches).
- The dashboard's read-only rule (SPEC §3.4) gains a third exception, after
  sync/rebuild and resetting counters. The action changes only a proposal
  record and a branch briefd created; the index is untouched.
- A reviewer who closes a pull request but keeps the branch must also close
  the proposal in briefd, or delete the branch.
