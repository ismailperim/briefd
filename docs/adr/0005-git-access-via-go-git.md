# 0005 — Git access via go-git; proposals as in-memory commits

- Status: accepted
- Date: 2026-09-20

## Context

M5 needs briefd to clone and follow a knowledge repository and to create
proposal branches (SPEC §3.1.5, §4.1). Two options: shell out to a `git`
binary, or use `go-git` (pure Go, Apache-2.0).

## Decision

1. **`github.com/go-git/go-git/v5`** is used for clone, fetch, reset and
   push. The container image therefore stays `distroless/static` (33 MB)
   with no git binary, and the release binaries work on hosts without git.
2. **Sync is fetch + hard reset** to `origin/<branch>`, never a merge: the
   checkout is a read-only mirror, so nothing local can drift into the index.
3. **Proposals are built as git objects directly** (blob → trees → commit)
   on top of the followed branch's current commit and pushed as
   `briefd/proposal-<id>`. The worktree is never checked out to another
   branch, so a concurrent sync or index run can never observe a proposal.
   A GitHub pull request is opened when `forge.type: github` is configured.
4. A local directory that is itself a git checkout (a developer's clone) is
   opened in place: sync is a no-op (the developer owns the checkout) and
   proposals become local branches, pushed only when `origin` exists.

## Consequences

- Authentication: HTTPS remotes use a token (`git.token`, any username);
  SSH remotes use `git.ssh_key` or the agent. SSH host key verification
  relies on the runtime user's `known_hosts`, so HTTPS + token is the
  recommended path inside containers.
- `sync_state.last_commit` carries the real commit hash for git sources;
  the index fingerprint (ADR/M4) covers both git and plain directories.
- Proposal status (`open` → `merged`/`closed`) is refreshed from GitHub during
  source sync when a forge is configured. A status lookup error leaves the proposal
  open and does not fail document indexing.
