#!/usr/bin/env python3
"""Render docs/social/preview.html to docs/assets/social-preview.png (1280×640 @2x).

Upload the PNG under Settings → Social preview. Needs a headless Chromium
(BRIEFD_CHROME, or the Playwright cache like docs/diagrams/build.py).
"""
import glob, os, pathlib, shutil, subprocess, sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
SRC = ROOT / "docs" / "social" / "preview.html"
OUT = ROOT / "docs" / "assets" / "social-preview.png"

chrome = os.environ.get("BRIEFD_CHROME") or shutil.which("chromium") or shutil.which("google-chrome")
if not chrome:
    found = glob.glob(os.path.expanduser("~/Library/Caches/ms-playwright/chromium_headless_shell-*/*/chrome-headless-shell"))
    chrome = found[0] if found else None
if not chrome:
    sys.exit("no headless Chromium found; set BRIEFD_CHROME")
subprocess.run([chrome, "--headless", "--disable-gpu", "--hide-scrollbars", "--allow-file-access-from-files", "--virtual-time-budget=4000", "--window-size=1280,640",
                "--force-device-scale-factor=2", f"--screenshot={OUT}", SRC.as_uri()], check=True,
               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
print("rendered", OUT.relative_to(ROOT), OUT.stat().st_size, "bytes")
