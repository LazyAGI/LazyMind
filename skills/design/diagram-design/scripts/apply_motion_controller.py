#!/usr/bin/env python3
"""Install the bundled canonical motion controller into generated HTML."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile


MAX_HTML_BYTES = 2 * 1024 * 1024
SCRIPT_RE = re.compile(
    r"<script\b(?P<attrs>[^>]*)>(?P<body>.*?)</script\s*>",
    re.IGNORECASE | re.DOTALL,
)
SCRIPT_OPEN_RE = re.compile(r"<script\b", re.IGNORECASE)
CONTROLLER_ATTR_RE = re.compile(
    r"\s*data-diagram-controls(?:\s*=\s*(?:\"\"|'\'))?\s*",
    re.IGNORECASE,
)
BODY_CLOSE_RE = re.compile(r"</body\s*>", re.IGNORECASE)


class InstallError(ValueError):
    """Raised when the target cannot be repaired safely."""


def canonical_controller() -> str:
    template = Path(__file__).resolve().parents[1] / "assets" / "template-motion.html"
    source = template.read_text(encoding="utf-8")
    matches = list(SCRIPT_RE.finditer(source))
    if len(matches) != 1 or len(SCRIPT_OPEN_RE.findall(source)) != 1:
        raise InstallError("template-motion.html must contain exactly one controller script")
    if CONTROLLER_ATTR_RE.fullmatch(matches[0].group("attrs")) is None:
        raise InstallError("template-motion.html controller attributes are not canonical")
    return matches[0].group(0)


def install_controller(source: str, controller: str) -> tuple[str, str]:
    matches = list(SCRIPT_RE.finditer(source))
    openings = len(SCRIPT_OPEN_RE.findall(source))
    if openings != len(matches):
        raise InstallError("target contains an unclosed or malformed script tag")

    if not matches:
        closings = list(BODY_CLOSE_RE.finditer(source))
        if len(closings) != 1:
            raise InstallError("scriptless target must contain exactly one closing body tag")
        marker = closings[0].start()
        separator = "" if source[:marker].endswith("\n") else "\n"
        updated = source[:marker] + separator + controller + "\n" + source[marker:]
        return updated, "inserted"

    if len(matches) != 1:
        raise InstallError("target must not contain multiple scripts")
    match = matches[0]
    if CONTROLLER_ATTR_RE.fullmatch(match.group("attrs")) is None:
        raise InstallError(
            "target script must carry only the data-diagram-controls attribute"
        )
    updated = source[: match.start()] + controller + source[match.end() :]
    action = "unchanged" if updated == source else "replaced"
    return updated, action


def atomic_write(path: Path, content: str, mode: int) -> None:
    temporary_name: str | None = None
    try:
        with tempfile.NamedTemporaryFile(
            "w",
            encoding="utf-8",
            newline="",
            dir=path.parent,
            prefix=f".{path.name}.",
            suffix=".tmp",
            delete=False,
        ) as temporary:
            temporary.write(content)
            temporary.flush()
            os.fsync(temporary.fileno())
            temporary_name = temporary.name
        os.chmod(temporary_name, stat.S_IMODE(mode))
        os.replace(temporary_name, path)
    finally:
        if temporary_name is not None:
            try:
                os.unlink(temporary_name)
            except FileNotFoundError:
                pass


def target_path(raw_path: str) -> tuple[Path, os.stat_result]:
    path = Path(raw_path).expanduser()
    if path.suffix.casefold() != ".html":
        raise InstallError("target must use the .html extension")
    if path.is_symlink():
        raise InstallError("target must not be a symbolic link")
    try:
        metadata = path.stat()
    except OSError as exc:
        raise InstallError(f"cannot stat target: {exc}") from exc
    if not stat.S_ISREG(metadata.st_mode):
        raise InstallError("target must be a regular file")
    if metadata.st_size > MAX_HTML_BYTES:
        raise InstallError(f"target exceeds {MAX_HTML_BYTES} bytes")
    return path.resolve(), metadata


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Install diagram-design's exact canonical motion controller."
    )
    parser.add_argument("html", help="Absolute or relative path to generated HTML")
    args = parser.parse_args()

    try:
        path, metadata = target_path(args.html)
        source = path.read_text(encoding="utf-8")
        updated, action = install_controller(source, canonical_controller())
        if action != "unchanged":
            atomic_write(path, updated, metadata.st_mode)
        result = {
            "status": "ok",
            "path": str(path),
            "action": action,
            "bytes": path.stat().st_size,
        }
        print(json.dumps(result, ensure_ascii=False, sort_keys=True))
        return 0
    except (InstallError, OSError, UnicodeError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
