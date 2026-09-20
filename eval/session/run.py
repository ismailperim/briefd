#!/usr/bin/env python3
"""Measure real Claude Code sessions: everything-in-CLAUDE.md vs briefd over MCP.

For each task the same prompt is run headlessly (`claude -p --output-format json`)
in two throwaway project directories:

  A) CLAUDE.md contains the whole knowledge corpus (what teams do today)
  B) CLAUDE.md is three lines pointing at briefd; briefd is attached as an MCP
     server and the model calls compile_bundle / search_context as needed

Reported per scenario: prompt tokens processed (input + cache reads + cache
writes, summed over the session's API calls), output tokens, cost, turns, and
whether the answer contained the expected facts.

Usage: python3 eval/session/run.py --briefd-url http://localhost:7788/mcp --token dev-token [--model sonnet] [--tasks N]
Requires the `claude` CLI and a running `briefd serve --source testdata/knowledge`.
"""
import argparse, json, os, pathlib, shutil, statistics, subprocess, sys, tempfile, time

ROOT = pathlib.Path(__file__).resolve().parents[2]
CORPUS = ROOT / "testdata" / "knowledge"

BRIEFD_CLAUDE_MD = """# Project notes
Team knowledge (domain rules, conventions, decisions) is served by the `briefd` MCP server.
Before answering questions about rules or conventions, call `compile_bundle` with the task
(scopes: domain, conventions) and answer from it. Do not guess.
"""

def build_corpus_md():
    parts = ["# Team knowledge\n"]
    for p in sorted(CORPUS.rglob("*.md")):
        if p.name == "README.md" and p.parent == CORPUS:
            continue
        parts.append(f"\n<!-- {p.relative_to(CORPUS)} -->\n" + p.read_text())
    return "\n".join(parts)

def run_claude(cwd, prompt, model, mcp_config=None, allowed=None, max_turns=6):
    cmd = ["claude", "-p", prompt, "--output-format", "stream-json", "--verbose", "--max-turns", str(max_turns), "--model", model]
    if mcp_config:
        cmd += ["--mcp-config", str(mcp_config), "--strict-mcp-config"]
    if allowed:
        cmd += ["--allowedTools", ",".join(allowed)]
    env = {k: v for k, v in os.environ.items() if k != "CLAUDECODE"}
    t0 = time.time()
    out = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, env=env, timeout=600)
    if out.returncode != 0:
        raise RuntimeError(f"claude failed ({out.returncode}): {out.stderr[-500:]}")
    # stream-json: one event per line; the answer may be spread over several
    # assistant messages (e.g. answer, then a report_usage call, then a stub),
    # so collect every text block rather than only the final "result".
    data, texts, tool_calls = None, [], 0
    for line in out.stdout.splitlines():
        try:
            e = json.loads(line)
        except json.JSONDecodeError:
            continue
        if e.get("type") == "assistant":
            for c in e["message"].get("content", []):
                if c.get("type") == "text":
                    texts.append(c["text"])
                elif c.get("type") == "tool_use":
                    tool_calls += 1
        elif e.get("type") == "result":
            data = e
    if data is None:
        raise RuntimeError("no result event in claude output")
    data["result"] = "\n".join(texts)
    data["tool_calls"] = tool_calls
    u = data["usage"]
    calls = u.get("iterations") or []
    per_call = [c["input_tokens"] + c.get("cache_read_input_tokens", 0) + c.get("cache_creation_input_tokens", 0) for c in calls]
    total = u["input_tokens"] + u.get("cache_read_input_tokens", 0) + u.get("cache_creation_input_tokens", 0)
    turns = data.get("num_turns", max(1, len(calls)))
    return {
        # tokens the model had in context on its largest single call: what a
        # long session keeps paying for on every turn
        "context_tokens": max(per_call) if per_call else round(total / max(1, turns)),
        # every token processed across all calls of the session (cache reads included)
        "prompt_tokens": total,
        "output_tokens": u["output_tokens"],
        "cost_usd": data.get("total_cost_usd", 0.0),
        "turns": turns,
        "tool_calls": data["tool_calls"],
        "seconds": round(time.time() - t0, 1),
        "result": data.get("result", ""),
    }

def correct(answer, expect):
    a = answer.lower()
    return all(e.lower() in a for e in expect)

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--briefd-url", default="http://localhost:7788/mcp")
    ap.add_argument("--token", default="")
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--tasks", type=int, default=0, help="run only the first N tasks")
    ap.add_argument("--only", default="", help="comma-separated task ids to run")
    ap.add_argument("--repeat", type=int, default=1, help="run each task this many times")
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    tasks = json.loads((ROOT / "eval" / "session" / "tasks.json").read_text())
    if args.tasks:
        tasks = tasks[: args.tasks]
    if args.only:
        wanted = set(args.only.split(","))
        tasks = [t for t in tasks if t["id"] in wanted]
    tasks = [t for t in tasks for _ in range(args.repeat)]

    work = pathlib.Path(tempfile.mkdtemp(prefix="briefd-session-"))
    a_dir, b_dir = work / "a-claudemd", work / "b-briefd"
    a_dir.mkdir(); b_dir.mkdir()
    (a_dir / "CLAUDE.md").write_text(build_corpus_md())
    (b_dir / "CLAUDE.md").write_text(BRIEFD_CLAUDE_MD)
    mcp = b_dir / "mcp.json"
    headers = {"Authorization": "Bearer " + args.token} if args.token else {}
    mcp.write_text(json.dumps({"mcpServers": {"briefd": {"type": "http", "url": args.briefd_url, "headers": headers}}}))
    allowed = ["mcp__briefd__compile_bundle", "mcp__briefd__search_context", "mcp__briefd__get_document", "mcp__briefd__list_scopes", "mcp__briefd__report_usage"]

    rows = []
    for t in tasks:
        print(f"[{t['id']}] A: CLAUDE.md …", file=sys.stderr, end=" ", flush=True)
        a = run_claude(a_dir, t["task"], args.model)
        print(f"{a['prompt_tokens']} tok  B: briefd …", file=sys.stderr, end=" ", flush=True)
        b = run_claude(b_dir, t["task"], args.model, mcp_config=mcp, allowed=allowed)
        print(f"{b['prompt_tokens']} tok", file=sys.stderr)
        rows.append({"id": t["id"], "expect": t["expect"],
                     "a": {**a, "correct": correct(a["result"], t["expect"])},
                     "b": {**b, "correct": correct(b["result"], t["expect"])}})

    def agg(key):
        return {
            "context_tokens_mean": statistics.mean(r[key]["context_tokens"] for r in rows),
            "prompt_tokens_mean": statistics.mean(r[key]["prompt_tokens"] for r in rows),
            "output_tokens_mean": statistics.mean(r[key]["output_tokens"] for r in rows),
            "cost_usd_mean": statistics.mean(r[key]["cost_usd"] for r in rows),
            "turns_mean": statistics.mean(r[key]["turns"] for r in rows),
            "correct": sum(1 for r in rows if r[key]["correct"]),
        }
    summary = {"model": args.model, "tasks": len(rows), "a_claudemd": agg("a"), "b_briefd": agg("b"), "rows": rows}
    if args.out:
        pathlib.Path(args.out).write_text(json.dumps(summary, indent=2))

    A, B = summary["a_claudemd"], summary["b_briefd"]
    print(f"\nmodel {args.model} · {len(rows)} tasks · corpus {len((a_dir / 'CLAUDE.md').read_text())} chars in CLAUDE.md\n")
    print("| Scenario | Context per turn | Tokens processed / task | Output tokens | Cost / task | API calls | Correct |")
    print("|---|---:|---:|---:|---:|---:|---:|")
    for name, s in (("A · everything in CLAUDE.md", A), ("B · briefd over MCP", B)):
        print(f"| {name} | {s['context_tokens_mean']:,.0f} | {s['prompt_tokens_mean']:,.0f} | {s['output_tokens_mean']:,.0f} | ${s['cost_usd_mean']:.3f} | {s['turns_mean']:.1f} | {s['correct']}/{len(rows)} |")
    ctx = B["context_tokens_mean"] / A["context_tokens_mean"] - 1
    cost = B["cost_usd_mean"] / A["cost_usd_mean"] - 1
    proc = B["prompt_tokens_mean"] / A["prompt_tokens_mean"] - 1
    print(f"\nbriefd vs CLAUDE.md: context per turn {ctx:+.0%} · cost per task {cost:+.0%} · tokens processed {proc:+.0%} (extra tool-call round trips, mostly cache reads)")
    print("\nper task (context per turn A → B, correct A/B):")
    for r in rows:
        print(f"  {r['id']:<20} {r['a']['context_tokens']:>8,} → {r['b']['context_tokens']:>8,}   {'✓' if r['a']['correct'] else '✗'}/{'✓' if r['b']['correct'] else '✗'}")
    shutil.rmtree(work, ignore_errors=True)

if __name__ == "__main__":
    main()
