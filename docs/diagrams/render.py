#!/usr/bin/env python3
"""Render the architecture diagrams (docs/diagrams/*.html) to docs/assets/diagram-*.png at 2x.

Needs a headless Chromium (BRIEFD_CHROME, or the Playwright cache).
"""
import glob, os, pathlib, shutil, subprocess, sys

HERE = pathlib.Path(__file__).resolve().parent
OUT = HERE.parent / "assets"
SIZES = {"comparison": (1600, 520), "pipeline": (1600, 760), "write-path": (1600, 470)}

chrome = os.environ.get("BRIEFD_CHROME") or shutil.which("chromium") or shutil.which("google-chrome")
if not chrome:
    found = glob.glob(os.path.expanduser("~/Library/Caches/ms-playwright/chromium_headless_shell-*/*/chrome-headless-shell"))
    chrome = found[0] if found else None
if not chrome:
    sys.exit("no headless Chromium found; set BRIEFD_CHROME")
for name, (w, h) in SIZES.items():
    if len(sys.argv) > 1 and name not in sys.argv[1:]:
        continue
    out = OUT / f"diagram-{name}.png"
    subprocess.run([chrome, "--headless", "--disable-gpu", "--hide-scrollbars", "--allow-file-access-from-files",
                    "--virtual-time-budget=3000", f"--window-size={w},{h}", "--force-device-scale-factor=2",
                    f"--screenshot={out}", (HERE / f"{name}.html").as_uri()], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    print("rendered", out.relative_to(HERE.parent.parent))
