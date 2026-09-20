#!/usr/bin/env bash
# Cut a release: scripts/release.sh 0.3.0
#
# Version is derived from the git tag everywhere else (ldflags in goreleaser
# and the Dockerfile, the container tags, the MCP Registry entry). This script
# only makes sure the human-maintained files agree before the tag is pushed:
#   - CHANGELOG.md gets a "## [X.Y.Z] — date" section from "Unreleased"
#   - deploy/server.json version + image tag are set to X.Y.Z
# then commits, tags and pushes. The release workflow does the rest.
set -euo pipefail
v="${1:?usage: scripts/release.sh X.Y.Z}"
[[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version must be X.Y.Z"; exit 1; }
cd "$(dirname "$0")/.."
[[ -z "$(git status --porcelain)" ]] || { echo "working tree not clean"; exit 1; }
[[ "$(git branch --show-current)" == main ]] || { echo "release from main"; exit 1; }
git fetch -q origin main && [[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || { echo "main is not up to date with origin"; exit 1; }
grep -q "^## \[Unreleased\]" CHANGELOG.md || { echo "CHANGELOG.md has no Unreleased section"; exit 1; }
awk '/^## \[Unreleased\]/{f=1;next} /^## \[/{exit} f&&NF' CHANGELOG.md | grep -q . || { echo "Unreleased section is empty; write the changelog first"; exit 1; }
today=$(date -u +%Y-%m-%d)
python3 - "$v" "$today" <<'PY'
import re, sys, json, pathlib
v, today = sys.argv[1], sys.argv[2]
p = pathlib.Path("CHANGELOG.md"); s = p.read_text()
s = s.replace("## [Unreleased]\n", f"## [Unreleased]\n\n## [{v}] — {today}\n", 1)
m = re.search(r"^\[Unreleased\]: (https://github.com/[^/]+/[^/]+)/compare/v([^.]+\.[^.]+\.[^.]+)\.\.\.HEAD$", s, re.M)
if not m: sys.exit("CHANGELOG.md: missing [Unreleased] compare link")
repo, prev = m.group(1), m.group(2)
s = s.replace(m.group(0), f"[Unreleased]: {repo}/compare/v{v}...HEAD\n[{v}]: {repo}/compare/v{prev}...v{v}")
p.write_text(s)
sj = pathlib.Path("deploy/server.json"); d = json.loads(sj.read_text())
d["version"] = v
for pkg in d.get("packages", []):
    if pkg.get("registryType") == "oci":
        pkg["identifier"] = re.sub(r":[^:]+$", f":{v}", pkg["identifier"])
sj.write_text(json.dumps(d, indent=2, ensure_ascii=False) + "\n")
PY
git add CHANGELOG.md deploy/server.json
git commit -q -m "chore(release): v$v"
git tag -a "v$v" -m "briefd v$v"
git push -q origin main "v$v"
echo "v$v pushed — watch: gh run list --workflow Release"
