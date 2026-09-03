#!/usr/bin/env python3
"""Export the first accessible inline SVG from a generated diagram HTML file.

The source HTML is never modified. The output keeps the authored SVG markup,
adds the SVG namespace when needed, injects the approved font stylesheet, and
validates that the result is well-formed XML.

Usage:
    python3 export_svg.py <diagram.html> [--out diagram.svg] [--overwrite]

Exit codes: 0 success, 2 invalid input, unsafe/non-diagram markup, or write error.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from pathlib import Path
from typing import NoReturn
from xml.etree import ElementTree as ET


MAX_INPUT_BYTES = 8 * 1024 * 1024
FONT_STYLE = (
    "<style>"
    "@import url('https://fonts.googleapis.com/css2?"
    "family=Instrument+Serif:ital@0;1&amp;"
    "family=Geist:wght@400;500;600&amp;"
    "family=Geist+Mono:wght@400;500;600&amp;display=swap');"
    "[data-motion-decorative]{display:none!important;}"
    "[data-motion-item]{opacity:1!important;transform:none!important;"
    "animation:none!important;transition:none!important;}"
    "</style>"
)


def fail(message: str) -> NoReturn:
    print(f"export_svg: {message}", file=sys.stderr)
    raise SystemExit(2)


def read_source(path: Path) -> str:
    try:
        size = path.stat().st_size
    except OSError as error:
        fail(f"cannot read {path}: {error}")
    if size > MAX_INPUT_BYTES:
        fail(
            f"{path.name}: input is {size} bytes; maximum is "
            f"{MAX_INPUT_BYTES // (1024 * 1024)} MiB"
        )
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        fail(f"cannot read UTF-8 HTML from {path}: {error}")


def extract_first_svg(source: str) -> str:
    match = re.search(r"<svg\b[^>]*>.*?</svg\s*>", source, re.IGNORECASE | re.DOTALL)
    if not match:
        fail("source HTML does not contain an inline <svg> diagram")
    svg = match.group(0)
    opening_match = re.match(r"<svg\b[^>]*>", svg, re.IGNORECASE | re.DOTALL)
    if not opening_match:
        fail("could not parse the opening <svg> tag")
    opening = opening_match.group(0)

    if not re.search(r"\bviewBox\s*=\s*(['\"]).+?\1", opening, re.IGNORECASE | re.DOTALL):
        fail("diagram SVG needs an explicit viewBox; refusing to guess its canvas")
    if re.search(r"<script\b", svg, re.IGNORECASE):
        fail("diagram SVG contains a script tag; run scripts/self_check.py first")
    if re.search(r"\son[a-z0-9_-]+\s*=", svg, re.IGNORECASE):
        fail("diagram SVG contains an executable event attribute")
    if not re.search(r"<title\b[^>]*>\s*[^<\s]", svg, re.IGNORECASE | re.DOTALL):
        fail("diagram SVG needs a non-empty <title>")
    if not re.search(r"<desc\b[^>]*>\s*[^<\s]", svg, re.IGNORECASE | re.DOTALL):
        fail("diagram SVG needs a non-empty <desc>")

    if not re.search(r"\bxmlns\s*=", opening, re.IGNORECASE):
        replacement = opening[:-1].rstrip() + ' xmlns="http://www.w3.org/2000/svg">'
        svg = replacement + svg[len(opening) :]

    # Motion markup uses HTML boolean-style data attributes. Standalone SVG is
    # strict XML, so normalize only the two approved valueless attributes.
    for attribute in ("data-motion-item", "data-motion-decorative"):
        svg = re.sub(
            rf"(\s{attribute})(?=\s|/?>)",
            rf'\1=""',
            svg,
            flags=re.IGNORECASE,
        )

    if "fonts.googleapis.com/css2" not in svg:
        defs_match = re.search(r"<defs\b[^>]*>", svg, re.IGNORECASE | re.DOTALL)
        if defs_match:
            insert_at = defs_match.end()
        else:
            desc_match = re.search(r"</desc\s*>", svg, re.IGNORECASE)
            if not desc_match:
                fail("diagram SVG needs <desc> before standalone export")
            insert_at = desc_match.end()
            FONT_DEFS = f"<defs>{FONT_STYLE}</defs>"
            svg = svg[:insert_at] + FONT_DEFS + svg[insert_at:]
            insert_at = -1
        if insert_at >= 0:
            svg = svg[:insert_at] + FONT_STYLE + svg[insert_at:]

    result = '<?xml version="1.0" encoding="UTF-8"?>\n' + svg.strip() + "\n"
    try:
        ET.fromstring(result.encode("utf-8"))
    except ET.ParseError as error:
        fail(f"extracted SVG is not well-formed XML: {error}")
    return result


def write_output(path: Path, content: str, overwrite: bool) -> None:
    if path.suffix.lower() != ".svg":
        fail("output path must end in .svg")
    if path.exists() and not overwrite:
        fail(f"{path}: already exists; choose another path or explicitly allow overwrite")
    if not path.parent.is_dir():
        fail(f"output directory does not exist: {path.parent}")
    try:
        if overwrite:
            temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
            temporary.write_text(content, encoding="utf-8", newline="\n")
            os.replace(temporary, path)
        else:
            with path.open("x", encoding="utf-8", newline="\n") as handle:
                handle.write(content)
    except OSError as error:
        fail(f"cannot write {path}: {error}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("file", help="generated diagram HTML file")
    parser.add_argument("--out", help="output .svg path (default: next to source)")
    parser.add_argument(
        "--overwrite",
        action="store_true",
        help="replace an existing output file; use only when explicitly authorized",
    )
    args = parser.parse_args(argv)

    source_path = Path(args.file).expanduser().resolve()
    if not source_path.is_file():
        fail(f"{source_path}: no such file")
    output_path = (
        Path(args.out).expanduser().resolve()
        if args.out
        else source_path.with_suffix(".svg")
    )
    if output_path == source_path:
        fail("source and output paths must differ")

    svg = extract_first_svg(read_source(source_path))
    write_output(output_path, svg, args.overwrite)
    print(
        json.dumps(
            {
                "status": "ok",
                "source": str(source_path),
                "output": str(output_path),
                "bytes": len(svg.encode("utf-8")),
            },
            ensure_ascii=False,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
