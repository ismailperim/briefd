# Architecture Decision Records

Every non-obvious technical decision is recorded here as one file per decision,
named `NNNN-short-title.md`, numbered sequentially. ADRs are immutable once
accepted: to change a decision, write a new ADR that supersedes the old one and
link both ways.

Template:

```markdown
# NNNN — Title

- Status: proposed | accepted | superseded by [NNNN](NNNN-title.md)
- Date: YYYY-MM-DD

## Context
## Decision
## Consequences
```

| ADR | Title | Status |
|---|---|---|
| [0001](0001-core-architecture.md) | Core architecture | accepted (amended by 0002) |
| [0002](0002-sqlite-driver-and-vector-search.md) | SQLite driver and vector search strategy | accepted |
