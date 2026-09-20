# 0004 — Hybrid fusion: AND-first BM25 and weighted RRF

- Status: accepted (refines [0001](0001-core-architecture.md) decision 3)
- Date: 2026-09-20

## Context

ADR-0001 fixed hybrid retrieval as BM25 top-50 + vector top-50 fused with
plain reciprocal rank fusion (k = 60). The first eval run on the golden set
(47 queries over 44 documents / 221 chunks, `briefd eval`) showed that plain
RRF made hybrid **worse than vector-only** on paraphrased questions:

| mode | Recall@5 | Recall@10 | MRR |
|---|---|---|---|
| bm25 | 0.681 | 0.723 | 0.570 |
| vector | 0.809 | 0.904 | 0.700 |
| hybrid, plain RRF | 0.755 | 0.851 | 0.679 |

The cause is structural: the BM25 query OR-ed every term, so a natural
language question like "a customer wants their money back for a purchase
from seven months ago" produced a BM25 list led by chunks that merely
contained "seven" or "purchase". RRF gives the top of every list the same
credit (1/61), so a confident vector hit was diluted by a meaningless BM25
hit half of the time.

## Decision

1. **Query-side stop words.** English and common Turkish function words are
   removed from queries before building the FTS5 expression (documents are
   still indexed in full).
2. **AND-first BM25.** The FTS5 query requiring every remaining term runs
   first; its hits lead the BM25 list. Only if fewer than 50 chunks match
   are any-term (OR) matches appended, de-duplicated, after them.
3. **Weighted RRF.** Fusion stays reciprocal rank fusion with k = 60, but
   the any-term tail of the BM25 list contributes with weight 0.25 (and its
   ranks continue after the all-term hits) while all-term BM25 hits and the
   vector list contribute with weight 1. The weight was chosen by a sweep
   over {0, 0.25, 0.5, 0.75}; 0.25 was the only value at which hybrid was at
   least as good as vector-only on every metric.

Result on the same golden set:

| mode | Recall@5 | Recall@10 | MRR |
|---|---|---|---|
| bm25 (AND-first, stop words) | 0.681 | 0.755 | 0.591 |
| vector | 0.809 | 0.904 | 0.700 |
| **hybrid (this ADR)** | **0.809** | **0.936** | **0.709** |

Per type, hybrid beats vector-only on keyword, typo and mixed-language
queries and matches it on paraphrases; it beats BM25 everywhere except pure
keyword queries, where both are perfect.

## Consequences

- `eval/thresholds.yaml` gates CI at hybrid R@5 ≥ 0.80, R@10 ≥ 0.90,
  MRR ≥ 0.70. SPEC §8's initial aspiration of R@5 ≥ 0.85 remains a target,
  not a gate, until retrieval improves.
- Remaining misses are paraphrases with no lexical overlap and Turkish
  questions whose English answer has no Turkish alias in the corpus.
  Candidate follow-ups, each to be measured with `briefd eval`: a
  multilingual embedding option (e.g. `paraphrase-multilingual-MiniLM`),
  glossary-alias query expansion, and embedding the document title with
  each chunk in a separate field.
- Any change to the weight, stop-word list, or fusion depth must report
  before/after numbers in the PR (CLAUDE.md testing rules).
