#!/usr/bin/env python3
"""Run `briefd eval` on a public BEIR dataset for numbers anyone can reproduce.

    python3 eval/beir/run.py scifact            # ~5k docs, 300 test queries
    python3 eval/beir/run.py nfcorpus           # ~3.6k docs, 323 test queries
    python3 eval/beir/run.py scifact --model all-MiniLM-L6-v2

Downloads the dataset (BEIR mirror), writes each document as one Markdown file
under a temporary knowledge dir (domain/<id>.md, title as H1, text as body),
turns the test qrels into a briefd golden set (relevant docs → expected
sections), and runs `briefd eval` in bm25, vector and hybrid modes. BEIR's
headline metric is nDCG@10; briefd reports it alongside Recall@5/10 and MRR.

Published BM25 nDCG@10 for reference (BEIR paper / leaderboard):
scifact 0.665, nfcorpus 0.325. Note: briefd chunks long documents into
several sections and treats any matching section as a hit, so numbers are
comparable in spirit, not a leaderboard submission.
"""
import argparse, io, json, os, pathlib, shutil, subprocess, sys, tempfile, urllib.request, zipfile, csv

ROOT = pathlib.Path(__file__).resolve().parents[2]
MIRROR = "https://public.ukp.informatik.tu-darmstadt.de/thakur/BEIR/datasets/{}.zip"


def yaml_str(s):
    return json.dumps(s, ensure_ascii=False)  # JSON strings are valid YAML scalars


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("dataset", choices=["scifact", "nfcorpus"])
    ap.add_argument("--model", default="", help="embedding model (default: config default)")
    ap.add_argument("--split", default="test")
    ap.add_argument("--max-queries", type=int, default=0)
    ap.add_argument("--work", default=os.environ.get("BRIEFD_BEIR_WORK", ""), help="cache dir for datasets and the index")
    ap.add_argument("--briefd", default=str(ROOT / "bin" / "briefd"))
    args = ap.parse_args()

    work = pathlib.Path(args.work) if args.work else pathlib.Path(tempfile.gettempdir()) / "briefd-beir"
    work.mkdir(parents=True, exist_ok=True)
    ds = work / args.dataset
    if not (ds / "corpus.jsonl").exists():
        print(f"downloading {args.dataset} …", file=sys.stderr)
        data = urllib.request.urlopen(MIRROR.format(args.dataset), timeout=600).read()
        zipfile.ZipFile(io.BytesIO(data)).extractall(work)

    kb = work / f"{args.dataset}-knowledge"
    if not kb.exists():
        (kb / "domain").mkdir(parents=True)
        for line in open(ds / "corpus.jsonl", encoding="utf-8"):
            d = json.loads(line)
            title = (d.get("title") or "").strip() or f"Document {d['_id']}"
            body = (d.get("text") or "").strip()
            (kb / "domain" / f"{d['_id']}.md").write_text(f"# {title}\n\n{body}\n", encoding="utf-8")
    titles = {}
    for line in open(ds / "corpus.jsonl", encoding="utf-8"):
        d = json.loads(line)
        titles[d["_id"]] = (d.get("title") or "").strip() or f"Document {d['_id']}"
    queries = {json.loads(l)["_id"]: json.loads(l)["text"] for l in open(ds / "queries.jsonl", encoding="utf-8")}

    rel = {}
    with open(ds / "qrels" / f"{args.split}.tsv", encoding="utf-8") as f:
        for row in csv.DictReader(f, delimiter="\t"):
            if int(row["score"]) > 0:
                rel.setdefault(row["query-id"], []).append(row["corpus-id"])
    golden = work / f"{args.dataset}-{args.split}.yaml"
    with open(golden, "w", encoding="utf-8") as out:
        out.write("queries:\n")
        n = 0
        for qid, docs in rel.items():
            if qid not in queries:
                continue
            out.write(f"  - id: {yaml_str('q' + qid)}\n    type: {args.dataset}\n    query: {yaml_str(queries[qid])}\n    expected:\n")
            for did in docs:
                out.write(f"      - {{ path: {yaml_str('domain/' + did + '.md')}, heading: {yaml_str(titles[did])} }}\n")
            n += 1
            if args.max_queries and n >= args.max_queries:
                break
    print(f"{args.dataset}: {len(titles)} docs, {n} queries → {golden}", file=sys.stderr)

    db = work / f"{args.dataset}{'-' + args.model if args.model else ''}.db"
    cmd = [args.briefd, "eval", "--config", "/dev/null", "--source", str(kb), "--golden", str(golden), "--thresholds", "", "--db", str(db)]
    if args.model:
        cmd += ["--model", args.model]
    subprocess.run(cmd, check=True)


if __name__ == "__main__":
    main()
