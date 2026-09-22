---
name: briefd
description: Use the team's briefd knowledge server before implementing anything that domain rules, conventions or past decisions could govern; compile a task bundle first, propose fixes for stale knowledge, report what helped.
---

# briefd — team knowledge, on demand

The `briefd` MCP server holds the team's shared knowledge: domain rules,
glossary, architecture decisions, conventions and per-project notes, kept
as Markdown in a git repository and reviewed like code. Nothing in it is
loaded into your context automatically — you ask for what the task needs.

## When to use it

Call `compile_bundle` **once, at the start of a task**, whenever the work
could be governed by a team rule: changing business logic, touching money,
dates, identifiers or external providers, adding an endpoint, choosing a
library, writing a migration, naming things. If you are unsure whether a rule
exists, that is a reason to ask, not to skip.

Do not call it for purely mechanical edits (formatting, a typo, a rename the
user spelled out).

## How to use it

1. `compile_bundle(task_description, max_tokens?, scopes?)` — describe the
   task in one or two sentences ("add partial refunds to the merchant
   portal"), not keywords. Default scopes are `domain` and `conventions`;
   add `projects/<name>` when working inside that project (`list_scopes`
   shows the names). The bundle never exceeds the budget; 2000 tokens is a
   good default, 1000 for a small task.
2. Read the bundle before writing code. Each section starts with
   `## path — heading (updated YYYY-MM-DD)`. Treat a section that says
   `code changed since: N commits` with suspicion: the code it governs moved
   after the rule was written — check the code, and if the rule is out of
   date, fix it (step 4).
3. `search_context(query)` when you need to look one thing up, and
   `get_document(doc_path)` when a section is not enough.
4. `propose_update(doc_path, change_description, new_content)` when knowledge
   is wrong, stale or missing. Fetch the current document first, send the
   complete new content, and explain the evidence for the reviewer. It opens
   a branch / pull request; it never changes what is served until a human
   merges. Never edit the knowledge repository directly.
5. `report_usage(bundle_id, useful_chunk_ids)` when the task is done: the
   chunk ids that actually helped, or an empty list if nothing did — the
   empty list is how maintainers learn what the knowledge base is missing.

## Rules of thumb

- A bundle is evidence, not gospel: quote the section path when you apply a
  rule, so the user can check it.
- If the bundle contradicts the code, say so explicitly and let the user
  decide; do not silently follow either.
- Do not paste whole bundles into your answers; apply them.
