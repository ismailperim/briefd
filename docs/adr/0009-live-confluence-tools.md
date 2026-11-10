# 0009 — Live Confluence Cloud search as read-only, budgeted MCP tools

- Status: accepted; ADR-0010 adds the shared `X-Atlassian-Email` /
  `X-Atlassian-Token` headers (the `X-Confluence-*` pair still works)
- Date: 2026-11-09

## Context

Many teams keep much of their knowledge in Confluence, not in a git repo.
Their agents need it today, and "move everything to Markdown first" is not
an answer they can act on. Atlassian's own MCP server already gives agents
raw Confluence access; what it does not give is briefd's contract: compact
output that never exceeds an explicit token budget, next to the reviewed
knowledge base, behind one MCP endpoint.

Two ways to bring Confluence in:

1. **Index it** — sync selected spaces into the SQLite index so
   `compile_bundle` ranks Confluence and git sections together. This makes
   Confluence a second source of truth (locked decision 5 says git is the
   only one), needs incremental sync, deletion tracking and per-space
   permissions, and serves every caller whatever the service account sees.
2. **Search it live** — call Confluence's own search when the agent asks,
   convert the answer, cut it to the budget, store nothing.

## Decision

Ship (2) now, as two tools that exist only when `confluence.base_url` is
set:

- `confluence_search(query, spaces?, limit?, max_tokens?)` — CQL
  `type = page AND siteSearch ~ "<query>"` (Confluence's ranking), returning
  title, space, last-updated date, id, link and excerpt per hit. Hits are
  kept in rank order while the rendered text fits `max_tokens` (default
  1000); an excerpt is dropped before its hit is.
- `confluence_page(id, max_tokens?)` — the page's rendered (`view`) HTML
  converted to Markdown by a small converter on `golang.org/x/net/html`
  (headings, paragraphs, lists, tables, code, links, emphasis; images,
  scripts and styling dropped), cut at a line boundary by the bundle
  packer's `Fit` with the same truncation marker (default 3000 tokens).

Rules:

- **Confluence Cloud only** (REST v1 search, v2 pages, Basic auth with an
  Atlassian email + API token). Data Center / Server are out of scope for
  now.
- **Nothing is stored.** Results are not indexed, cached or logged as
  queries; git remains the only source the index is built from and the
  only write path. Turning a Confluence page into a team rule goes through
  `propose_update`.
- **Credentials per caller.** With `confluence.user_credentials` (default
  on), an MCP client may send `X-Confluence-Email` and `X-Confluence-Token`
  headers; briefd uses them for that request only, so each person sees what
  Confluence lets them see. Otherwise the optional service account is used.
  Secrets come from the environment or headers and are never written to the
  database, logs or the dashboard.
- **Spaces allow-list.** `confluence.spaces` limits both tools; a caller's
  `spaces` argument can narrow it, never widen it. Space keys are validated
  and every user value enters CQL as a quoted literal.
- No new dependency: `golang.org/x/net` (BSD-3-Clause) was already in the
  module graph.

## Consequences

- Confluence content does not take part in `compile_bundle` ranking, drift,
  links, gaps or the graph. Agents decide when to look there; the tool
  descriptions say the knowledge base comes first.
- Every call costs one or two round trips to Confluence (page reads resolve
  the space key to enforce the allow-list) and inherits its latency, rate
  limits and availability; errors are reported, never retried silently.
- The tool list grows by two definitions (~800 tokens per session, 2.8k → 3.6k) only for
  instances that configure Confluence.
- Indexing selected spaces, option (1), stays possible later as its own
  ADR; the converter and client are reusable for it.
