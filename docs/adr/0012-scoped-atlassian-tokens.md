# 0012 — Per-product scoped Atlassian API tokens, and Atlassian health

- Status: accepted
- Date: 2026-11-18

## Context

ADR-0009 and ADR-0010 authenticate Confluence and Jira with one Atlassian
account. The caller sends `X-Atlassian-Email` and `X-Atlassian-Token` (or the
older `X-Confluence-*` pair), and briefd calls the site URL
(`https://<site>.atlassian.net/rest/api/3/...`, `/wiki/rest/api/...`,
`/wiki/api/v2/...`) with Basic auth. This works with classic API tokens,
which can do everything the account can do, in every product.

Atlassian now offers API tokens with scopes and recommends them as the more
secure option. Unattended callers, such as monitoring agents, should use
them with read-only scopes. Three properties of scoped tokens break the
current model:

1. **One product per token.** A scoped token is created for exactly one app.
   Read-only access to both Jira and Confluence needs two tokens; the
   account email is the same for both.
2. **A different endpoint.** Requests with a scoped token must go through
   the Atlassian API gateway:
   `https://api.atlassian.com/ex/jira/{cloudId}/...` or
   `https://api.atlassian.com/ex/confluence/{cloudId}/...`. In a live test
   against an Atlassian Cloud site (2026-11-18), Confluence on the site URL
   answered 401 to a scoped token; Jira accepted it there too, but that is
   not documented. The gateway accepted both.
3. **Scopes per endpoint.** A call fails when the token lacks that
   endpoint's scope, even if it has other read scopes.

Scoped tokens expire after 1 to 365 days. Authentication is still Basic
auth with `email:token`.

The existing `X-Confluence-Email` / `X-Confluence-Token` pair cannot become
"Confluence only". Since ADR-0010 it means "the shared account", and Jira
accepts it too; changing that would break callers that use it for Jira.

Operators also had no view of the Atlassian connections. Expired tokens,
missing scopes and an unreachable site showed up only as tool errors in
someone's agent session.

## Decision

### Headers

Two product-specific token headers are added. The email header stays
shared.

| Header | Meaning |
|---|---|
| `X-Atlassian-Email` | Account email, shared by all tokens (unchanged) |
| `X-Atlassian-Token` | Classic token for all products (unchanged) |
| `X-Atlassian-Jira-Token` | **New.** Scoped token for Jira only |
| `X-Atlassian-Confluence-Token` | **New.** Scoped token for Confluence only |
| `X-Confluence-Email` / `X-Confluence-Token` | Legacy classic pair (unchanged, both products) |

Each product resolves credentials in this order. The first match wins, and
a half-filled pair is ignored:

1. `X-Atlassian-Email` + the product's own scoped-token header → **scoped**.
2. `X-Atlassian-Email` + `X-Atlassian-Token` → **classic**.
3. `X-Confluence-Email` + `X-Confluence-Token` → **classic** (legacy).
4. The product's service account from the configuration.

A caller may send both scoped headers and a classic token at the same time.
Each product picks its own scoped token first, and a scoped token for one
product is never used for the other.

`jira_issue` checks whether linked Confluence pages lie in
`confluence.spaces` (ADR-0010). That lookup is a Confluence request, so it
uses the credentials resolved for Confluence. A caller with only a Jira
scoped token therefore has no Confluence credentials of its own. Unless a
Confluence service account exists, linked pages are hidden when
`confluence.spaces` is set, as before.

### Routing

- **Classic** credentials keep using the site URL.
- **Scoped** credentials use the gateway. The request path stays the same
  and is appended to `https://api.atlassian.com/ex/{jira|confluence}/{cloudId}`,
  for example `/ex/jira/{cloudId}/rest/api/3/search/jql` and
  `/ex/confluence/{cloudId}/wiki/api/v2/pages/{id}`.
- Links shown to users (issue and page URLs) always use the site URL, never
  the gateway.

### Cloud ID

- Setting `atlassian.cloud_id` (env `BRIEFD_ATLASSIAN_CLOUD_ID`), shared by
  Jira and Confluence.
  - It must be a UUID, because it becomes part of gateway paths.
  - It is only valid when `jira.base_url` and `confluence.base_url` name
    the same site. Otherwise startup fails, and the operator leaves it empty
    so that each product looks up its own.
- Empty: on the first scoped request, each client reads the cloud ID from
  its site at `https://<site>/_edge/tenant_info` (unauthenticated, field
  `cloudId`) and keeps it for the process lifetime.
  - A failed lookup is remembered for 30 seconds, then retried. It is never
    cached permanently.
  - The error asks for `BRIEFD_ATLASSIAN_CLOUD_ID`.
  - Classic requests never look the cloud ID up.

### Service accounts

Atlassian service accounts must use scoped tokens. A per-product flag,
`jira.scoped_token` / `confluence.scoped_token` (env
`BRIEFD_JIRA_SCOPED_TOKEN` / `BRIEFD_CONFLUENCE_SCOPED_TOKEN`, default
false), marks the configured service-account token as scoped and routes it
through the gateway.

Jira's existing fallback to the Confluence service account applies only
when the Confluence token is classic; a scoped Confluence token is never
used for Jira. If Jira then has no service account and `user_credentials`
is off, startup fails with "nobody could authenticate".

### Scopes

The read-only scopes briefd needs, per endpoint, verified live with tokens
that had only these scopes:

| Product | Endpoint used by briefd | Scope |
|---|---|---|
| Jira | `GET /rest/api/3/issue/{key}` (with `renderedFields`, `names`) | `read:jira-work` |
| Jira | `GET /rest/api/3/issue/{key}/remotelink` | `read:jira-work` |
| Jira | `GET /rest/api/3/search/jql` | `read:jira-work` |
| Confluence | `GET /wiki/rest/api/search` (CQL) | `search:confluence` |
| Confluence | `GET /wiki/api/v2/pages/{id}` (with body) | `read:page:confluence` |
| Confluence | `GET /wiki/api/v2/spaces/{id}` | `read:space:confluence` |

### Errors

A 401 or 403 on a scoped request says: check that the token was created for
the product, has the scope for this endpoint (the path, without the query),
and has not expired. The messages for classic tokens are unchanged.

### Health

`GET /api/atlassian/health` (bearer token) and an *Atlassian* panel on the
dashboard's Instance page report each configured product:

- Requests since the server started, split into classic and scoped, with
  failures.
- Time of the last success, and the last error with its time.
- Whether the site answers, from an unauthenticated GET of
  `/_edge/tenant_info`. It runs at most once a minute, and only while
  someone requests the health view; briefd does no background polling.
- Whether the cloud ID is configured, resolved or unresolved.

Status is `ok`, `idle` (no requests yet), `degraded` (the site answers but
the last request failed) or `down` (the site does not answer). A degraded
or down product appears under *Needs attention*.

A 404 counts as an answer, not a failure: a missing issue is not a broken
connection. Error texts are built from the status code and the token kind
only. They never echo response bodies, URLs, queries or credentials.

### Unchanged

- Read-only GET requests only; Cloud only.
- Nothing from Jira or Confluence is stored. Tokens are never logged or
  cached.
- The allow-lists (`jira.projects`, `confluence.spaces`) apply the same way.

## Consequences

- Unattended callers can run with two read-only, expiring tokens instead of
  a classic token with full account rights.
- Existing callers are unaffected: classic headers and the legacy pair route
  exactly as before.
- One more setting (`atlassian.cloud_id`) and two optional flags. Without
  the cloud ID, the first scoped request costs one extra unauthenticated
  request.
- Credentials now carry a kind (classic or scoped) next to the email and
  token, and the Atlassian client picks its base URL from it.
- Scope errors surface per endpoint, so a missing scope shows up only when
  that tool is used. The scope table above is the reference for creating
  tokens.
- Operators see expired tokens, missing scopes and outages on the
  dashboard. They no longer have to collect them from agent sessions.
- Recent application logs on the dashboard were considered and deferred:
  they need a bounded in-memory log buffer and a redaction policy of their
  own.
