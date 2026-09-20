# Real-session benchmark

`run.py` measures what actually happens inside Claude Code, not what the
tokenizer estimates. Each task in `tasks.json` is run headlessly
(`claude -p --output-format stream-json`) in two throwaway projects:

- **A — everything in CLAUDE.md:** the whole sample knowledge repo
  (`testdata/knowledge`, 44 documents) concatenated into `CLAUDE.md`, the way
  teams do it today. No MCP server.
- **B — briefd over MCP:** a three-line `CLAUDE.md` telling the agent to call
  `compile_bundle` before answering; briefd attached as an MCP server.

Both get the same prompt and model, and the answer is checked against a few
expected facts (`expect`). The script reports, per scenario:

| Column | Meaning |
|---|---|
| Context per turn | tokens in the model's context on its largest API call — what a long session keeps paying for on every turn |
| Tokens processed / task | every token across all API calls of the task, cache reads included |
| Cost / task | `total_cost_usd` as reported by Claude Code |
| API calls | number of model calls; tool use adds round trips |

## Results (2026-09-20, Sonnet, 10 tasks)

| Scenario | Context per turn | Tokens processed / task | Output tokens | Cost / task | API calls | Correct |
|---|---:|---:|---:|---:|---:|---:|
| A · everything in CLAUDE.md | 51,739 | 51,739 | 81 | $0.120 | 1.0 | 10/10 |
| B · briefd over MCP | 33,614 | 138,224 | 459 | $0.072 | 4.4 | 10/10 |

**Context per turn −35%, cost per task −40%, same answers.** Claude Code's own
system prompt and tool schemas are ~33k tokens on this model; the knowledge
repo added ~18k on top in A and ~2k (a bundle plus briefd's tool schemas) in B.
The price of that is round trips: B makes 3–4 extra API calls per task
(`ToolSearch`, `compile_bundle`, `report_usage`), so the *cumulative* token
count is higher even though most of it is cheap cache reads. Claude Code loads
MCP tool schemas lazily through `ToolSearch`, so briefd's ~1.7k tokens of tool
definitions are not paid up front there; clients that send every schema with
every request pay them once per session. One-shot questions
are the worst case for briefd; the per-turn saving compounds over a long
session, and it grows with the size of the knowledge repo, which a static
`CLAUDE.md` cannot scale with at all.

Raw data: [`results-sonnet.json`](results-sonnet.json).

## Run it yourself

```sh
./bin/briefd serve --source testdata/knowledge --db /tmp/briefd.db --token dev-token &
python3 eval/session/run.py --token dev-token --model sonnet --out eval/session/results.json
```

Each run costs real API usage (~$2 for 10 tasks on Sonnet). Use `--only <id>`
and `--repeat N` to look at a single task.
