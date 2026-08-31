#!/usr/bin/env python3
"""Verify semantic structure required by an OAuth sequence diagram.

This checker is coordinate-independent so it can validate a newly generated
diagram, not only the bundled example. It checks the combined fragment, guards,
Bearer call, asynchronous open arrow, dashed return paths, and activation bar.

Usage:
    python3 scripts/verify-sequence-oauth.py oauth-sequence.html
    python3 scripts/verify-sequence-oauth.py --all

Exit codes: 0 clean, 1 findings, 2 unreadable input or usage.
"""

from __future__ import annotations

import argparse
import re
import sys
from html.parser import HTMLParser
from pathlib import Path


SKILL_ROOT = Path(__file__).resolve().parent.parent
ASSET_DIR = SKILL_ROOT / "assets"

DASHED_CONNECTOR_RE = re.compile(
    r"<(?:line|path)\b[^>]*\bstroke-dasharray\s*=\s*(['\"])[^'\"]+\1[^>]*>",
    re.IGNORECASE,
)
ACTIVATION_RE = re.compile(
    r"<rect\b(?=[^>]*\bwidth\s*=\s*(['\"])(?P<width>[\d.]+)\1)"
    r"(?=[^>]*\bheight\s*=\s*(['\"])(?P<height>[\d.]+)\3)[^>]*>",
    re.IGNORECASE,
)


class VisibleTextParser(HTMLParser):
    """Collect rendered labels while ignoring metadata, comments, and CSS."""

    HIDDEN_TAGS = {"script", "style", "title", "desc"}

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.hidden_depth = 0
        self.parts: list[str] = []

    def handle_starttag(
        self, tag: str, attrs: list[tuple[str, str | None]]
    ) -> None:
        if tag.casefold() in self.HIDDEN_TAGS:
            self.hidden_depth += 1

    def handle_startendtag(
        self, tag: str, attrs: list[tuple[str, str | None]]
    ) -> None:
        return

    def handle_endtag(self, tag: str) -> None:
        if tag.casefold() in self.HIDDEN_TAGS and self.hidden_depth:
            self.hidden_depth -= 1

    def handle_data(self, data: str) -> None:
        if not self.hidden_depth:
            self.parts.append(data)


def visible_text(source: str) -> str:
    parser = VisibleTextParser()
    parser.feed(source)
    parser.close()
    return " ".join(" ".join(parser.parts).split())


def check(path: Path) -> list[str]:
    try:
        source = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        return [f"{path}: cannot read UTF-8 HTML: {error}"]

    text = visible_text(source)
    lowered = text.casefold()
    findings: list[str] = []

    if not re.search(r"(?:^|\s)ALT(?:\s|$)", text):
        findings.append("missing ALT combined-fragment operator")
    if "[token valid]" not in lowered and "token valid" not in lowered:
        findings.append("missing token-valid guard")
    if "[else" not in lowered and "else" not in lowered:
        findings.append("missing else/failure guard")
    if "bearer" not in lowered:
        findings.append("missing Bearer-authenticated request")

    marker_defined = re.search(
        r"<marker\b[^>]*\bid\s*=\s*(['\"])[^'\"]*arrow-open[^'\"]*\1",
        source,
        re.IGNORECASE,
    )
    marker_used = re.search(r"url\(\s*#(?:[^)]*arrow-open[^)]*)\)", source, re.IGNORECASE)
    if not marker_defined or not marker_used:
        findings.append("missing defined-and-used arrow-open marker for async semantics")

    dashed_returns = len(DASHED_CONNECTOR_RE.findall(source))
    if dashed_returns < 2:
        findings.append(
            f"needs at least two dashed return connectors; found {dashed_returns}"
        )

    activation_found = False
    for match in ACTIVATION_RE.finditer(source):
        width = float(match.group("width"))
        height = float(match.group("height"))
        if width <= 12 and height >= 80:
            activation_found = True
            break
    if not activation_found:
        findings.append("missing activation bar with width <=12px and height >=80px")

    if re.search(r"<script\b", source, re.IGNORECASE):
        findings.append("OAuth sequence output must remain static and script-free")

    return [f"{path.name}: {finding}" for finding in findings]


def targets(args: argparse.Namespace) -> list[Path]:
    if args.all:
        return sorted(ASSET_DIR.glob("example-sequence-oauth*.html"))
    return [Path(value) for value in args.paths]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("paths", nargs="*", help="OAuth sequence HTML files")
    parser.add_argument(
        "--all", action="store_true", help="check every bundled OAuth example"
    )
    args = parser.parse_args(argv)
    if not args.all and not args.paths:
        parser.print_help()
        return 2

    paths = targets(args)
    if not paths:
        print("verify-sequence-oauth: no files selected", file=sys.stderr)
        return 2

    findings: list[str] = []
    for path in paths:
        if not path.is_file():
            print(f"verify-sequence-oauth: {path}: no such file", file=sys.stderr)
            return 2
        findings.extend(check(path))

    for finding in findings:
        print(finding)
    if findings:
        print(f"Summary: {len(paths)} file(s), {len(findings)} finding(s).")
        return 1
    print(
        "OK OAuth sequence: "
        f"{len(paths)} file(s), ALT guards, async marker, dashed returns, and activation present"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
