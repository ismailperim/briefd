---
title: Documentation style
tags: [docs, conventions, writing]
---
# Documentation style

## Where things live

Domain rules and decisions live in this knowledge repository. Service
specifics live in the service's `projects/<name>/` folder here, with a short
README in the code repository pointing to it. Runbooks live next to the
service.

## Writing rules

Lead with the rule, then the reason. Use concrete numbers, not "reasonable"
or "soon". Prefer tables for thresholds. One idea per section so sections
can be quoted on their own.

## ADRs

One decision per file, numbered, with Context / Decision / Consequences.
Superseded ADRs stay in place with a link to the replacement.

## Terminology

Use the glossary terms exactly; do not introduce synonyms in documentation
(for example, say "payout", not "disbursement" or "settlement to merchant").
When a Turkish term is common in the team, mention it once in the glossary
and use the English term everywhere else.

## Keeping docs current

A pull request that changes behavior described here updates the document in
the same change. Documents not touched for 12 months are reviewed by their
owner and either confirmed or archived.
