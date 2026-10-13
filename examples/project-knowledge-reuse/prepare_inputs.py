#!/usr/bin/env python3
"""Extract the pinned showcase HTML articles using only the Python standard library."""

import argparse
import hashlib
from html.parser import HTMLParser
import json
from pathlib import Path


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]


class ArticleText(HTMLParser):
    """Retain article text, table cells and diagram labels; discard page chrome."""

    BLOCKS = {"article", "section", "div", "p", "li", "tr", "figure", "figcaption",
              "h1", "h2", "h3", "h4", "footer", "text", "title", "small"}

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.in_article = False
        self.articles = 0
        self.skipped = 0
        self.parts = []
        self.link = None

    def handle_starttag(self, tag, attrs):
        if tag == "article":
            self.in_article = True
            self.articles += 1
        if not self.in_article:
            return
        if tag in {"script", "style"}:
            self.skipped += 1
        if self.skipped:
            return
        if tag in self.BLOCKS or tag == "br":
            self.parts.append("\n")
        attrs = dict(attrs)
        if tag == "h2" and attrs.get("id"):
            self.parts.append(f"[{attrs['id']}] ")
        if tag == "a":
            self.link = attrs.get("href")
        if tag in {"td", "th"}:
            self.parts.append("\t")

    def handle_endtag(self, tag):
        if not self.in_article:
            return
        if tag in {"script", "style"}:
            self.skipped = max(0, self.skipped - 1)
        elif not self.skipped and tag == "a" and self.link:
            self.parts.append(f" ({self.link})")
            self.link = None
        elif not self.skipped and tag in self.BLOCKS:
            self.parts.append("\n")
        if tag == "article":
            self.in_article = False

    def handle_data(self, data):
        if self.in_article and not self.skipped:
            self.parts.append(data)

    def render(self):
        if self.articles != 1 or self.in_article:
            raise ValueError("Expected one complete article in the source HTML")
        lines = []
        for line in "".join(self.parts).splitlines():
            cells = [" ".join(cell.split()) for cell in line.split("\t")]
            text = "\t".join(cells).strip()
            if text:
                lines.append(text)
        if not lines:
            raise ValueError("Source article is empty")
        return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Verify without writing files")
    args = parser.parse_args()
    manifest = json.loads((HERE / "sources.json").read_text(encoding="utf-8"))
    outputs = []
    for source in manifest["documents"]:
        raw = (ROOT / source["path"]).read_bytes()
        if hashlib.sha256(raw).hexdigest() != source["sha256"]:
            raise SystemExit(f"Source changed: {source['path']}. Review and repin explicitly.")
        article = ArticleText()
        article.feed(raw.decode("utf-8"))
        article.close()
        header = (
            "知策：仓库展示案例 / Zhice repository demonstration fixture\n"
            "性质：概念与示例材料，不是已验证、已批准或已实现的产品事实。\n"
            "Status: illustrative concept material, not verified, approved or implemented product facts.\n"
            f"Source: {source['path']}\n"
            f"Repository revision: {manifest['repository_revision']}\n"
            f"Source SHA-256: {source['sha256']}\n"
            "Extraction: article text, section anchors, links, table cells (tab-separated), and diagram labels; visual layout omitted.\n\n"
        )
        outputs.append((HERE / source["input"], header + article.render()))
    for path, content in outputs:
        if args.check:
            if not path.is_file() or path.read_text(encoding="utf-8") != content:
                raise SystemExit(f"Input snapshot differs: {path.relative_to(HERE)}")
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8")
    print(f"{'Verified' if args.check else 'Prepared'} {len(outputs)} pinned input documents.")


if __name__ == "__main__":
    main()
