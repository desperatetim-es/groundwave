# SPDX-License-Identifier: GPL-3.0-only
# Copyright (c) 2026 KD9PSM

"""Builds the user guide PDF that ships inside the program.

    py tools/docshots/buildguide.py

Reads docs/user-guide/user-guide.md and its pictures (img/), and writes
internal/guide/groundwave-user-guide.pdf, which the program embeds and puts in the
Library on first launch. Needs Chrome or Chromium and the `markdown-it-py` package.
Run tools/docshots/shoot.py first if the screens have changed.
"""
import re, subprocess, sys, tempfile
from pathlib import Path
from markdown_it import MarkdownIt

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "docs" / "user-guide"
OUT = ROOT / "internal" / "guide" / "groundwave-user-guide.pdf"
CHROME = next((p for p in [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
] if Path(p).exists()), None)

CSS = """
@page { size: Letter; margin: 0.7in 0.75in; }
body { font: 11pt/1.5 'Segoe UI', 'Helvetica Neue', Arial, sans-serif; color: #1b2430; }
h1 { font-size: 26pt; margin: 0 0 .3em; }
h2 { font-size: 17pt; margin: 1.4em 0 .4em; border-bottom: 1px solid #d0d7de; padding-bottom: .15em; break-after: avoid; }
h3 { font-size: 13pt; margin: 1.1em 0 .3em; break-after: avoid; }
img { display: block; max-width: 100%; max-height: 8.6in; width: auto; height: auto; margin: .8em auto;
      border: 1px solid #c9d1d9; border-radius: 6px; break-inside: avoid; }
table { border-collapse: collapse; width: 100%; margin: .8em 0; font-size: 10pt; break-inside: avoid; }
th, td { border: 1px solid #d0d7de; padding: 4px 8px; text-align: left; vertical-align: top; }
th { background: #f0f3f6; }
code { font: 9.5pt Consolas, 'Courier New', monospace; background: #f0f3f6; padding: 1px 4px; border-radius: 3px; }
pre { background: #f0f3f6; padding: 8px 12px; border-radius: 6px; break-inside: avoid; }
pre code { background: none; padding: 0; }
blockquote { margin: .8em 0; padding: .1em 1em; border-left: 4px solid #0969da; background: #f4f8fd; }
a { color: #0969da; text-decoration: none; }
li { margin: .2em 0; }
"""


def slug(text):  # the same heading anchors GitHub makes, so the contents links work in both
    return re.sub(r"[^\w\- ]", "", text.lower()).replace(" ", "-")


def main():
    if not CHROME:
        sys.exit("Chrome or Chromium was not found.")
    md = MarkdownIt("commonmark").enable("table")
    html = md.render((SRC / "user-guide.md").read_text(encoding="utf-8"))
    html = re.sub(r"<h([23])>(.*?)</h\1>",
                  lambda m: f'<h{m[1]} id="{slug(re.sub("<.*?>", "", m[2]).replace("&amp;", "&"))}">{m[2]}</h{m[1]}>', html)
    page = (f'<!doctype html><html lang="en"><head><meta charset="utf-8"><title>groundwλve User Guide</title>'
            f'<base href="{SRC.as_uri()}/"><style>{CSS}</style></head><body>{html}</body></html>')
    with tempfile.TemporaryDirectory() as tmp:
        f = Path(tmp) / "guide.html"
        f.write_text(page, encoding="utf-8")
        OUT.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run([CHROME, "--headless=new", "--disable-gpu", "--no-pdf-header-footer", f"--user-data-dir={Path(tmp) / 'p'}",
                        f"--print-to-pdf={OUT}", f.as_uri()], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    print("wrote", OUT, OUT.stat().st_size // 1024, "KB")


if __name__ == "__main__":
    main()
