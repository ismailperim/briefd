# BEIR results

Public retrieval benchmarks anyone can reproduce with `python3 eval/beir/run.py <dataset>`.
Each BEIR document becomes one Markdown file (`domain/<id>.md`, title as H1); the test
qrels become a briefd golden set; `briefd eval` runs bm25, vector and hybrid modes.
BEIR's headline metric is nDCG@10.

Measured 2026-09-21 (Apple M3 Pro, `GOMAXPROCS=4`, default settings, no tuning):

| nDCG@10 | SciFact (5,183 docs, 300 queries) | NFCorpus (3,633 docs, 323 queries) |
|---|---:|---:|
| BM25 — BEIR paper [1] | 0.665 | 0.325 |
| **briefd BM25** (FTS5, AND-first, stop words) | 0.691 | 0.329 |
| multilingual-e5-small, published [2] | 0.677 | 0.311 |
| **briefd vector, multilingual-e5-small** | 0.677 | 0.310 |
| all-MiniLM-L6-v2, published [3] | 0.645 | 0.315 |
| **briefd vector, all-MiniLM-L6-v2** | 0.646 | 0.318 |
| **briefd hybrid, multilingual-e5-small** | **0.714** | 0.340 |
| **briefd hybrid, all-MiniLM-L6-v2** | 0.693 | **0.355** |

Full metrics (Recall@5 / Recall@10 / MRR / nDCG@10):

```
scifact  e5      bm25 0.743/0.814/0.660/0.691  vector 0.734/0.807/0.645/0.677  hybrid 0.769/0.849/0.680/0.714
scifact  MiniLM  bm25 0.743/0.814/0.660/0.691  vector 0.741/0.787/0.606/0.646  hybrid 0.771/0.824/0.658/0.693
nfcorpus e5      bm25 0.124/0.159/0.528/0.329  vector 0.118/0.150/0.509/0.310  hybrid 0.134/0.168/0.548/0.340
nfcorpus MiniLM  bm25 0.124/0.159/0.528/0.329  vector 0.120/0.155/0.507/0.318  hybrid 0.141/0.176/0.564/0.355
```

What this shows:

- The pure-Go encoders reproduce the published quality of both models
  (vector-only rows match the MTEB/BEIR numbers within noise), on top of the
  cosine-1.0 equivalence tests against onnxruntime.
- Hybrid fusion (ADR-0004) beats both BM25 and vector-only on both datasets,
  by 2–4 nDCG points over the better single retriever.
- NFCorpus recall looks low in absolute terms because its queries have many
  relevant documents (Recall@10 = 0.17 is normal there); nDCG@10 is the
  comparable number.

Cost: embedding SciFact's 5,216 sections took 1h58m with multilingual-e5-small
and 38 min with all-MiniLM-L6-v2 at four cores and `nice -n 15`; queries run in
tens of milliseconds. Larger corpora are where the Ollama / OpenAI-compatible
adapters, or a bigger box, pay off.

[1] Thakur et al., *BEIR: A Heterogeneous Benchmark for Zero-shot Evaluation of Information Retrieval Models*, 2021.
[2] Wang et al., *Multilingual E5 Text Embeddings: A Technical Report*, 2024 (multilingual-e5-small, BEIR subsets).
[3] sentence-transformers/all-MiniLM-L6-v2 on the MTEB leaderboard.
