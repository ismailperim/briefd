#!/usr/bin/env bash
# CI guard on tag pushes: the tag must match CHANGELOG.md and deploy/server.json.
set -euo pipefail
tag="${1:?tag}"; v="${tag#v}"
grep -q "^## \[$v\] " CHANGELOG.md || { echo "CHANGELOG.md has no section for $v"; exit 1; }
python3 - "$v" <<'PY'
import json, sys
v = sys.argv[1]
d = json.load(open("deploy/server.json"))
assert d["version"] == v, f"deploy/server.json version {d['version']} != {v}"
for p in d.get("packages", []):
    if p.get("registryType") == "oci":
        assert p["identifier"].endswith(":" + v), f"image tag in server.json is not :{v}"
print("release metadata consistent for", v)
PY
