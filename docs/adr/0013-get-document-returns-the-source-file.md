# 0013 — get_document returns the complete source file, and proposals check their base

- Status: accepted
- Date: 2026-11-18

## Context

`propose_update` replaces a whole knowledge file. Its input said `new_content`
is "the complete new content of the file as Markdown, including front matter",
and told agents to "fetch the current content with get_document first when
editing".

`get_document` could not supply that. `Store.GetDocument` rebuilds `content`
by joining the document's chunks in order, so the result:

- has **no front matter**: `title` and `tags` come back as separate fields, but
  `refs`, `related` and any other property are gone;
- has **no H1**: the first chunk starts after the title heading;
- may differ in whitespace from the file, because chunks are joined with a
  blank line.

For example, `domain/rules/refunds.md` in the sample knowledge base starts with
a front-matter block (`title`, `tags`) and `# Refund rules`; the `content` from
`get_document` starts with the first section under that heading.

An agent that follows the documented path (call `get_document`, edit, call
`propose_update`) therefore produces a proposal that also deletes the front
matter and the title: code `refs`, wiki links in `related` and any other
property disappear, and the reviewer only notices if they read the diff
carefully. This happens with any MCP client. Unattended callers, such as
monitoring agents that propose knowledge changes, cannot safely edit existing
documents at all and are limited to creating new ones.

A second, smaller gap: `propose_update` did not know which version of the file
the agent based its edit on. If the document changes between `get_document`
and `propose_update` (another proposal is merged, a sync runs), the new
proposal silently undoes those changes.

The indexer already has most of what is needed: `ingest.Document.ContentHash`
is the SHA-256 of the file as read from disk, and the raw front matter is
stored in `documents.front_matter`.

## Decision

1. **Store the source file.** At index time the file is stored exactly as read
   (front matter and body) in a new nullable `documents.source` column
   (migration `0012_document_source.sql`). Documents indexed before the
   migration have `source IS NULL`; the indexer re-parses them once even though
   their content hash is unchanged, through the same mechanism that re-parses
   documents scanned by older link rules (`Store.DocumentsToReparse`).

2. **Return it from `get_document`**, over MCP and `GET /api/docs/{path}`,
   alongside the existing fields:

   | Field | Meaning |
   |---|---|
   | `source` | the complete file as stored in the repository, byte for byte: the text to edit for `propose_update`. Omitted (MCP) or `null` (REST) until the document has been re-indexed. |
   | `content_hash` | SHA-256 of `source` (the existing `content_hash`): the `base_content_hash` for `propose_update` |
   | `front_matter` | the raw YAML header without delimiters, or `""` |

   `content` stays as it is: the readable body that search and bundles use, and
   `tokens` keeps counting it. The MCP text form, which some clients show to the
   model instead of the structured result, carries `source` and `content_hash`
   instead of `content`: the source holds everything the content does, plus the
   front matter and title, so the text form does not grow and text-only clients
   can edit too. The tool descriptions say: to edit a document, change `source`
   and pass the full result to `propose_update` with
   `base_content_hash = content_hash`.

   Callers that do not need `source` pay for it in the structured result.
   Knowledge documents are small (most are under 3k tokens); an
   `include_source` flag can be added if that becomes a problem.

3. **`propose_update` checks its base.** It takes an optional
   `base_content_hash` (MCP, and `POST /api/proposals`). The check runs against
   **the file in the commit the proposal branch is built from** (the followed
   branch), not against the index: the branch can be ahead of the index between
   a pull and the re-index that follows it, and the base the proposal will
   overwrite is the branch's file. Both are SHA-256 hashes of the raw file
   bytes, so they compare directly.

   | `base_content_hash` | File on the branch | Result |
   |---|---|---|
   | absent (or `null`) | any | no check (earlier behavior); the proposal description ends with a note that no base hash was given |
   | `""` | missing | created: a new file |
   | `""` | exists | refused: the file already exists |
   | a hash | missing | refused: the file does not exist |
   | a hash | different hash | refused: `document changed since it was read (have <x>, now <y>); call get_document again` |
   | a hash | same hash | created |

   "Absent" and `""` must stay distinguishable, so the field is a pointer in Go
   (`*string`). Hashes are compared case-insensitively; anything that is not 64
   hex characters is rejected. A refused proposal creates no branch. When the
   caller's hash equals the indexed hash but not the branch's, the index is
   lagging, and the error says to retry after the next sync rather than to call
   `get_document` again (which would return the same stale hash). Over REST a
   stale base is `409 stale_base`.

4. **Guard against losing the front matter.** When the file on the branch has a
   front-matter block and `new_content` does not, the proposal is refused:
   `new_content has no front matter but the current file has one; edit the
   source returned by get_document`. A caller removes front matter on purpose
   by sending an empty `---`/`---` block. "Has a block" is decided by the
   opening delimiter line (`ingest.HasFrontMatter`), not by `front_matter`
   being non-empty, because an empty block parses to `""`. Over REST this is
   `422 front_matter_dropped`.

## Consequences

- Agents can edit existing documents without losing metadata or the title, and
  the reviewer sees a minimal diff.
- Concurrent edits are detected instead of overwritten when clients send
  `base_content_hash`; clients that do not keep working, and their proposals
  say so to the reviewer.
- `get_document` structured results get larger, roughly twice the document
  size; the text form does not.
- Schema change: one nullable column, and one re-parse of every existing
  document on the first index run after the upgrade (no re-embedding: chunk
  hashes are unchanged).
- A body that begins with a `---` horizontal rule counts as front matter for
  the guard. That is rare in knowledge documents and errs on the side of
  refusing.

## Tests

- A file with front matter, an H1 and `related` links is stored byte for byte;
  `content_hash` is its SHA-256; a document whose `source` is `NULL` is
  re-parsed once and then skipped again (`internal/indexer`).
- `propose_update` with a matching base is created; with a stale base it is
  refused without a branch; `""` creates a new file and is refused for an
  existing one; a hash for a missing file and a malformed hash are refused;
  without a base the description carries the note (`internal/proposal`).
- New content without front matter for a file that has one is refused; an
  explicit empty block is accepted; files without front matter are unaffected.
- An index that lags the branch produces the "retry after the next sync" hint.
- MCP and REST `get_document` return the same three fields; an MCP round trip
  edits `source` with its `content_hash` as the base, and REST answers a stale
  base with 409 (`internal/httpapi`).
