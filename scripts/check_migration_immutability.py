#!/usr/bin/env python3
"""Reject changes to dev migrations that already exist on the target branch."""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Sequence


DEV_MIGRATION_PREFIX = "backend/core/migrations/dev_mode/"


@dataclass(frozen=True)
class MigrationChange:
    status: str
    path: str
    old_path: str | None = None


def _git(root: Path, args: Sequence[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=root,
        check=check,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def _rev_exists(root: Path, rev: str) -> bool:
    return _git(root, ["rev-parse", "--verify", "--quiet", rev], check=False).returncode == 0


def resolve_base(root: Path, explicit_base: str | None = None) -> str | None:
    if explicit_base:
        return explicit_base
    if _rev_exists(root, "HEAD^2"):
        return "HEAD^1"
    env_base = os.environ.get("GITHUB_BASE_REF")
    if env_base:
        for candidate in (f"origin/{env_base}", env_base):
            if _rev_exists(root, candidate):
                return candidate
    for candidate in ("origin/main", "origin/master", "main", "master"):
        if _rev_exists(root, candidate):
            return candidate
    return None


def parse_name_status(output: str) -> list[MigrationChange]:
    fields = output.split("\0")
    changes: list[MigrationChange] = []
    index = 0
    while index < len(fields) and fields[index]:
        status = fields[index]
        index += 1
        if status.startswith(("R", "C")):
            old_path = fields[index]
            path = fields[index + 1]
            index += 2
            changes.append(MigrationChange(status=status, path=path, old_path=old_path))
        else:
            path = fields[index]
            index += 1
            changes.append(MigrationChange(status=status, path=path))
    return changes


def changed_dev_migrations(root: Path, base: str) -> list[MigrationChange]:
    result = _git(
        root,
        [
            "diff",
            "--name-status",
            "--find-renames",
            "-z",
            base,
            "--",
            DEV_MIGRATION_PREFIX,
        ],
    )
    return [
        change
        for change in parse_name_status(result.stdout)
        if change.path.endswith(".sql") or (change.old_path or "").endswith(".sql")
    ]


def immutable_violations(changes: Sequence[MigrationChange]) -> list[MigrationChange]:
    return [
        change
        for change in changes
        if not change.status.startswith("A")
        and ((change.old_path or change.path).startswith(DEV_MIGRATION_PREFIX))
    ]


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="repository root")
    parser.add_argument("--base", help="target/base revision to compare against")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    root = args.root.resolve()
    base = resolve_base(root, args.base)
    if not base:
        print("migration immutability check skipped: no target branch revision found")
        return 0
    violations = immutable_violations(changed_dev_migrations(root, base))
    for change in violations:
        target = change.old_path or change.path
        print(f"{target}: existing dev migration changed relative to {base} ({change.status})")
    if violations:
        print(
            "migration immutability check failed: create a new dev migration instead of editing target history",
            file=sys.stderr,
        )
        return 1
    print("migration immutability check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
