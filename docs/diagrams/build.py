#!/usr/bin/env python3
"""Generate the architecture diagrams as editable Excalidraw scenes and PNGs.

    python3 docs/diagrams/build.py            # writes docs/diagrams/*.excalidraw
    python3 docs/diagrams/build.py --render   # also renders docs/assets/diagram-*.png
                                               # (needs a headless Chromium; see render())

The scenes are plain Excalidraw JSON: open them at https://excalidraw.com to
edit by hand. Re-running this script overwrites them, so keep the source of
truth here and port manual tweaks back.
"""
import json, os, pathlib, random, subprocess, sys, tempfile, time, glob, shutil

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT_SCENES = ROOT / "docs" / "diagrams"
OUT_ASSETS = ROOT / "docs" / "assets"

# Excalidraw palette
BLUE, GREEN, YELLOW, RED, GRAY, VIOLET, ORANGE = "#a5d8ff", "#b2f2bb", "#ffec99", "#ffc9c9", "#e9ecef", "#d0bfff", "#ffd8a8"
INK, MUTED = "#1e1e1e", "#868e96"
FONT = 5  # Excalifont (hand-drawn)


class Scene:
    def __init__(self):
        self.elements = []
        self._seed = random.Random(7)

    def _base(self, **kw):
        e = {
            "id": kw.pop("id", f"e{len(self.elements)}"), "strokeColor": INK, "backgroundColor": "transparent",
            "fillStyle": "solid", "strokeWidth": 2, "strokeStyle": "solid", "roughness": 1, "opacity": 100,
            "angle": 0, "seed": self._seed.randint(1, 2**31), "version": 1, "versionNonce": 1, "isDeleted": False,
            "groupIds": [], "boundElements": [], "updated": 1, "link": None, "locked": False, "frameId": None,
        }
        e.update(kw)
        self.elements.append(e)
        return e

    def rect(self, x, y, w, h, label="", fill=GRAY, size=18, dashed=False, id=None, round=True):
        r = self._base(id=id, type="rectangle", x=x, y=y, width=w, height=h, backgroundColor=fill,
                       roundness={"type": 3} if round else None, strokeStyle="dashed" if dashed else "solid")
        if label:
            t = self.text(x + 10, y + 10, label, size=size, w=w - 20, h=h - 20, align="center", valign="middle")
            t["containerId"] = r["id"]
            r["boundElements"].append({"id": t["id"], "type": "text"})
        return r

    def text(self, x, y, s, size=18, w=None, h=None, align="left", valign="top", color=INK, id=None):
        lines = s.split("\n")
        est_w = w or max(len(l) for l in lines) * size * 0.55 + 10
        est_h = h or len(lines) * size * 1.25
        return self._base(id=id, type="text", x=x, y=y, width=est_w, height=est_h, text=s, originalText=s,
                          fontSize=size, fontFamily=FONT, textAlign=align, verticalAlign=valign,
                          strokeColor=color, lineHeight=1.25, autoResize=True, containerId=None)

    def arrow(self, x1, y1, x2, y2, label="", color=INK, dashed=False, via=None, label_dy=-24):
        pts = [[0, 0]]
        if via:
            for vx, vy in via:
                pts.append([vx - x1, vy - y1])
        pts.append([x2 - x1, y2 - y1])
        a = self._base(type="arrow", x=x1, y=y1, width=abs(x2 - x1), height=abs(y2 - y1), points=pts,
                       strokeColor=color, strokeStyle="dashed" if dashed else "solid",
                       startBinding=None, endBinding=None, startArrowhead=None, endArrowhead="arrow",
                       lastCommittedPoint=None, elbowed=False, roundness=None if via else {"type": 2})
        if label:
            mx, my = (x1 + x2) / 2, (y1 + y2) / 2
            if via:
                mx, my = via[0]
            self.text(mx - len(label) * 4, my + label_dy, label, size=15, color=MUTED, align="center")
        return a

    def doc_stack(self, x, y, w, h, label, fill, n=4):
        for i in range(n - 1, -1, -1):
            self.rect(x + i * 6, y + i * 6, w, h, "" if i else label, fill=fill, size=16)

    def dump(self, path):
        scene = {"type": "excalidraw", "version": 2, "source": "briefd docs/diagrams/build.py",
                 "elements": self.elements, "appState": {"viewBackgroundColor": "#ffffff", "gridSize": None}, "files": {}}
        path.write_text(json.dumps(scene, indent=1))


def comparison():
    s = Scene()
    # ---- left: today
    s.text(0, 0, "Today: knowledge in CLAUDE.md", size=26)
    s.text(0, 40, "loaded into every session, every turn", size=17, color=MUTED)
    s.doc_stack(0, 100, 260, 190, "CLAUDE.md\n\nglossary, rules, ADRs,\nconventions, runbooks…\n\n12,869 tokens", BLUE)
    s.arrow(285, 195, 430, 195, label="all of it, always")
    s.rect(440, 150, 200, 90, "coding agent", fill=GRAY, size=20)
    s.text(440, 260, "needs one rule for this task,\ngets the whole library", size=16, color=MUTED)
    s.rect(0, 330, 640, 70, "✗ whatever doesn't fit is silently left out\n✗ pay for it on every turn of every session", fill=RED, size=16)

    # ---- right: briefd
    X = 760
    s.text(X, 0, "With briefd: context on demand", size=26)
    s.text(X, 40, "one question per task, budgeted answer", size=17, color=MUTED)
    s.doc_stack(X, 100, 200, 120, "knowledge repo\n(git)", GREEN)
    s.arrow(X + 205, 160, X + 285, 160, label="sync")
    s.rect(X + 290, 110, 190, 100, "briefd\nindex · retrieve · pack", fill=YELLOW, size=17)
    s.rect(X + 640, 110, 200, 100, "coding agent", fill=GRAY, size=20)
    s.arrow(X + 636, 135, X + 485, 135, label='compile_bundle(task)', label_dy=-26)
    s.arrow(X + 485, 185, X + 636, 185, label="bundle · ~1,800 tokens", label_dy=8)
    s.text(X + 290, 230, "only the sections that matter,\nordered domain → conventions → project,\nnever above max_tokens", size=16, color=MUTED)
    s.rect(X, 330, 840, 70, "✓ 86% fewer knowledge tokens per task · answer present 96% of the time\n✓ scales with the repo; agents propose changes as reviewable PRs", fill=GREEN, size=16)
    return s


def pipeline():
    s = Scene()
    s.text(0, 0, "How briefd works", size=26)
    y = 70
    boxes = [
        ("knowledge repo\n(git or directory)\ndomain/ · conventions/ · projects/", GREEN, 290),
        ("sync\npull every 60 s\nor webhook", GRAY, 170),
        ("chunker\nsplit on H2/H3 headings\n200–800 tokens, stable ids", BLUE, 260),
        ("SQLite — one file\nFTS5 (BM25) · vectors\nbundle cache · usage", YELLOW, 250),
    ]
    x = 0
    coords = []
    for label, fill, w in boxes:
        s.rect(x, y, w, 110, label, fill=fill, size=15)
        coords.append((x, w))
        x += w + 50
    for i in range(len(coords) - 1):
        x0, w0 = coords[i]
        s.arrow(x0 + w0 + 4, y + 55, coords[i + 1][0] - 4, y + 55)
    sq_x, sq_w = coords[3]
    s.text(sq_x + sq_w + 20, y + 20, "embeddings: all-MiniLM-L6-v2\ncomputed in pure Go —\nno ONNX runtime, no CGO", size=14, color=MUTED)

    # second row: the query path
    y2 = 300
    s.rect(0, y2, 290, 110, "coding agent\nClaude Code · Cursor · Codex", fill=GRAY, size=15)
    s.rect(350, y2, 200, 110, "MCP / REST\nbearer token", fill=GRAY, size=15)
    s.rect(610, y2, 280, 110, "hybrid retrieval\nBM25 top-50 + cosine top-50\nfused with RRF (k = 60)", fill=BLUE, size=15)
    s.rect(950, y2, 290, 110, "budget packer\ndedupe · order by scope\n≤ max_tokens (5% headroom)", fill=VIOLET, size=15)
    s.arrow(292, y2 + 40, 348, y2 + 40, label="task", label_dy=-26)
    s.arrow(348, y2 + 75, 292, y2 + 75, label="bundle", label_dy=8)
    s.arrow(552, y2 + 55, 608, y2 + 55)
    s.arrow(892, y2 + 55, 948, y2 + 55, label="ranked", label_dy=8)
    # retrieval reads the index; the packer reads/writes the bundle cache
    s.arrow(750, y2 - 4, sq_x + 60, y + 116, dashed=True, via=[(750, y2 - 60), (sq_x + 60, y2 - 60)])
    s.text(sq_x + 70, y2 - 92, "reads index", size=14, color=MUTED)
    s.arrow(1095, y2 - 4, sq_x + 190, y + 116, dashed=True, via=[(1095, y2 - 30), (sq_x + 190, y2 - 30)])
    s.text(1110, y2 - 60, "bundle cache: byte-identical\nfor the same task + repo state", size=14, color=MUTED)

    # third row: observability
    y3 = 470
    s.rect(350, y3, 540, 70, "dashboard  ·  /metrics (Prometheus)  ·  /api/stats\nrequests, tokens served, latency, cache hits, sync state", fill=ORANGE, size=15)
    s.text(0, y3 + 15, "one binary · one SQLite file\nno Postgres, no vector DB", size=16, color=MUTED)
    return s


def write_path():
    s = Scene()
    s.text(0, 0, "Agents never write to the index", size=26)
    s.text(0, 40, "the only way knowledge changes: propose → review → merge → sync", size=17, color=MUTED)
    y = 110
    steps = [
        ("coding agent\nfinds a gap or an error", GRAY),
        ("propose_update\ndoc_path + new content\n+ why", YELLOW),
        ("briefd\ncommits on top of HEAD\nbranch briefd/proposal-<id>", YELLOW),
        ("pull request\n(GitHub, when configured)", BLUE),
        ("human review\nmerge or close", GREEN),
    ]
    x = 0
    for i, (label, fill) in enumerate(steps):
        s.rect(x, y, 210, 110, label, fill=fill, size=15)
        if i < len(steps) - 1:
            s.arrow(x + 214, y + 55, x + 256, y + 55)
        x += 260
    # loop back
    s.arrow(1100, y + 114, 1100, y + 200, via=[(1100, y + 200)])
    s.text(1120, y + 190, "merge triggers sync\n(pull or webhook)", size=15, color=MUTED)
    s.arrow(1100, y + 240, 640, y + 290, via=[(1100, y + 290)])
    s.rect(300, y + 260, 340, 70, "reindex only the changed files\n→ every agent sees the new rule", fill=GREEN, size=15)
    s.text(0, y + 270, "the served knowledge changed\nbecause a human said so,\nwith a diff and a name on it", size=15, color=MUTED)
    return s


def render(scene_path, png_path, width=1600, height=700):
    """Render with the official Excalidraw library in headless Chromium.

    Set BRIEFD_CHROME to a Chrome/Chromium headless binary. The scene is
    served over a temporary local HTTP server because the library is loaded
    from esm.sh and needs an http origin.
    """
    chrome = os.environ.get("BRIEFD_CHROME") or shutil.which("chromium") or shutil.which("google-chrome")
    if not chrome:
        found = glob.glob(os.path.expanduser("~/Library/Caches/ms-playwright/chromium_headless_shell-*/*/chrome-headless-shell"))
        chrome = found[0] if found else None
    if not chrome:
        sys.exit("no headless Chromium found; set BRIEFD_CHROME")
    html = """<!doctype html><html><head><meta charset="utf-8">
<script>window.EXCALIDRAW_ASSET_PATH = "https://esm.sh/@excalidraw/excalidraw@0.18.0/dist/prod/";</script>
<style>body{margin:0;background:#fff}</style></head><body><div id="out"></div>
<script type="module">
import { exportToSvg } from "https://esm.sh/@excalidraw/excalidraw@0.18.0?deps=react@19.0.0,react-dom@19.0.0";
const scene = await (await fetch(new URLSearchParams(location.search).get("scene"))).json();
const svg = await exportToSvg({ elements: scene.elements, appState: { ...scene.appState, exportBackground: true, exportPadding: 24 }, files: {} });
svg.style.width = "100%"; svg.style.height = "auto"; svg.setAttribute("preserveAspectRatio", "xMinYMin meet"); document.getElementById("out").appendChild(svg);
document.body.style.height = (svg.getBoundingClientRect().height) + "px";
</script></body></html>"""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = pathlib.Path(tmp)
        (tmp / "render.html").write_text(html)
        shutil.copy(scene_path, tmp / "scene.json")
        srv = subprocess.Popen([sys.executable, "-m", "http.server", "8766", "--bind", "127.0.0.1"], cwd=tmp,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            time.sleep(0.8)
            # The library is fetched from a CDN; if it is slow the screenshot
            # fires before anything is drawn, so retry while the PNG is blank.
            for attempt in range(4):
                subprocess.run([chrome, "--headless", "--disable-gpu", "--hide-scrollbars", f"--window-size={width},{height}",
                                f"--virtual-time-budget={20000 * (attempt + 1)}", f"--screenshot={png_path}",
                                "http://127.0.0.1:8766/render.html?scene=scene.json"], check=True,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                if pathlib.Path(png_path).stat().st_size > 40_000:
                    break
            else:
                sys.exit(f"render of {scene_path} stayed blank; check network access to esm.sh")
        finally:
            srv.terminate()


DIAGRAMS = {
    "comparison": (comparison, 1660, 440),
    "pipeline": (pipeline, 1300, 560),
    "write-path": (write_path, 1360, 460),
}

if __name__ == "__main__":
    OUT_SCENES.mkdir(parents=True, exist_ok=True)
    for name, (fn, w, h) in DIAGRAMS.items():
        path = OUT_SCENES / f"{name}.excalidraw"
        fn().dump(path)
        print("wrote", path.relative_to(ROOT))
        if "--render" in sys.argv:
            png = OUT_ASSETS / f"diagram-{name}.png"
            render(path, png, 2000, int(2000 * (h + 60) / (w + 60)) + 8)
            print("rendered", png.relative_to(ROOT))
