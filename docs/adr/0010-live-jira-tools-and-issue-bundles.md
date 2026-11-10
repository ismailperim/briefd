# 0010 — Live Jira Cloud tools and issue-driven bundles

- Status: accepted
- Date: 2026-11-10

## Context

Coding agents usually start from a ticket: "implement PAY-123". The
acceptance criteria, the discussion in the comments and the linked design
page live in Jira and Confluence. The team rules the agent must follow live
in briefd. Until now the agent could not read the ticket, and briefd could
not use the ticket to choose which rules to serve.

ADR-0009 added live Confluence Cloud tools. Jira runs on the same Atlassian
Cloud site, with the same accounts and API tokens. Atlassian's own MCP server
already gives agents raw Jira access. It does not give them briefd's
contract: output within an explicit token budget, and the issue packed
together with the rules that apply to it.

## Decision

Add three things. They exist only when `jira.base_url` is set.

- **`jira_search(text?, projects?, status?, type?, assignee?, open_only?,
  limit?, max_tokens?)`** returns key, summary, type, status, priority,
  assignee and last-updated date for each issue, newest first. Results stay
  within `max_tokens` (default 800). Agents pass filters, never raw JQL.
  briefd builds the JQL itself, every user value enters it as a quoted
  literal, and Lucene operators in the text are dropped. This keeps the
  project allow-list from being bypassed. Jira rejects unbounded searches,
  so at least one condition is required.
- **`jira_issue(key, comments?, max_tokens?)`** returns one issue as compact
  Markdown, in this order:
  1. Header and metadata.
  2. Parent and related issues.
  3. Linked Confluence pages, with their ids for `confluence_page`.
  4. Description.
  5. Configured extra fields, such as acceptance criteria (`jira.fields`).
  6. Comments, newest first.

  The bundle packer's `Fit` cuts the text from the end, so the oldest
  comments are lost first (default budget 2000).

  Rich text comes from Jira's `renderedFields` HTML and goes through the
  existing HTML→Markdown converter. Other field values are rendered as plain
  text. No converter for Atlassian Document Format (ADF, Jira's JSON
  rich-text format) is needed.
- **`compile_bundle(..., issue?)`** takes a Jira issue key. The input schema
  carries `issue` only on instances that configure Jira.
  - The issue's summary, description and configured fields (at most 1500
    characters) are added to `task_description` to form the retrieval query.
    `task_description` becomes optional.
  - The issue, cut to at most 35% of the effective budget, heads the result.
    The knowledge bundle fills the rest.
  - The whole result never exceeds the effective budget a plain bundle has.

Rules shared with ADR-0009, now in `internal/atlassian`:

- **One Atlassian account for both products.**
  - Callers may send `X-Atlassian-Email` and `X-Atlassian-Token`. Both
    Confluence and Jira accept them.
  - The v0.7 headers `X-Confluence-Email` and `X-Confluence-Token` keep
    working, and Jira accepts them too, because they carry the same account.
  - If `jira.email` and `jira.token` are both empty, Jira falls back to the
    Confluence service account.
  - `user_credentials` (default on) can be set separately per product.
- **Cloud only, read-only GET requests.** No comments, transitions or other
  writes. Changing a ticket is the job of Jira's own tools.
- **Allow-list.** `jira.projects` limits both tools and `compile_bundle`. An
  issue key is checked before the request is made, and the issue's actual
  project is checked again after the response, because issues can move.
- **Nothing from Jira is stored.**
  - The bundle cache and the query log record only the issue key and the
    caller's own `task_description`.
  - The issue text reaches the cache key only as a SHA-256 hash, through the
    new `bundle.Request.Query` field, which separates the retrieval text from
    the stored label.
  - The issue block is added to the response, not to the cached bundle. A
    cached bundle therefore always comes back with a freshly fetched issue.

## Consequences

- An issue key is enough to start a task with both the ticket and the rules
  that apply to it, in one call and one budget.
- Every issue call costs two round trips: the issue, then its remote links.
  A failed remote-link request is ignored. Latency, rate limits and outages
  are Jira's.
- Tool definitions grow by about 1k tokens per session (2.8k → 3.8k; 4.6k
  with Confluence too), only on instances that configure Jira.
- `compile_bundle` with an issue is cached per issue text. When the issue
  is edited, a new bundle is compiled. Old entries are pruned with the index
  like any others.
- The REST `POST /api/bundle` does not take `issue` yet. Per-person
  credentials travel in MCP headers, and the REST API has a single shared
  token.
- Data Center / Server and Jira Service Management queues remain out of
  scope.
