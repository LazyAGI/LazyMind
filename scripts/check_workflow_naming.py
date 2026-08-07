#!/usr/bin/env python3
"""Reject legacy Plugin domain names outside persistence compatibility syntax.  # noqa: Q000

The workflow migration deliberately keeps physical ``plugin_*`` database names.
Those names are allowed only where they are unambiguously used as SQL identifiers,
GORM column/table mappings, or row-mapper aliases. Published v0.1/v0.2 migration
history is immutable and excluded; current migration releases are scanned normally.
"""  # noqa: Q000

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Iterator, Sequence


DEFAULT_ROOTS = (
    "api",  # noqa: Q000
    "algorithm/lazymind",  # noqa: Q000
    "backend/channel-gateway/channel_gateway",  # noqa: Q000
    "backend/core",  # noqa: Q000
    "frontend/src",  # noqa: Q000
    "frontend/scripts/openapi/specs/core.yaml",  # noqa: Q000
    "i18n",  # noqa: Q000
    "skills",  # noqa: Q000
    "workflows",  # noqa: Q000
    "docker-compose.yml",  # noqa: Q000
)

SCANNED_SUFFIXES = {
    ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".json", ".yaml", ".yml", ".md",  # noqa: Q000
}

IGNORED_PARTS = {
    ".git", "node_modules", "vendor", "__pycache__", "dist", "build",  # noqa: Q000
}

IMMUTABLE_MIGRATION_RELEASES = {'v0_1', 'v0_2'}

# Database identifiers are lower snake case with at least one underscore.  A
# bare "plugin" is never a physical identifier and therefore is never allowed.  # noqa: Q000
PHYSICAL_NAME = re.compile(r"\bplugin_[a-z][a-z0-9_]*\b")  # noqa: Q000
PERSISTENCE_FILES = {
    'backend/core/common/orm/all_models.go',
    'backend/core/common/orm/plugin_models.go',
    'backend/core/common/orm/plugin_models_test.go',
    'backend/core/common/orm/taskcenter_models.go',
    'backend/core/common/orm/user_config_models_test.go',
    'backend/core/resourceupdate/stats.go',
    'backend/core/migrate/mode_test.go',
    'backend/core/migrate/repository_sqlite_test.go',
    'backend/core/workflow/domain/capabilities.go',
    'backend/core/workflow/domain/capabilities_test.go',
    'backend/core/workflow/domain/models.go',
    'backend/core/workflow/domain/models_test.go',
    'backend/core/workflow/domain/session_repository.go',
    'backend/core/workflow/domain/session_repository_test.go',
    'backend/core/workflow/draft_handlers_test.go',
    'backend/core/workflow/migration_contract_test.go',
    'backend/core/workflow/store.go',
    'backend/core/workflow/store_test.go',
    'backend/core/workflow/facade/handler_test.go',
    'backend/core/workflow/store/models.go',
    'backend/core/workflow/store/repository.go',
}
LEGACY_DOMAIN = re.compile(
    r"(?<![A-Za-z0-9])(?:Plugin[A-Za-z0-9_]*|plugin[A-Z][A-Za-z0-9_]*|"  # noqa: Q000
    r"plugin[-_][A-Za-z0-9_-]+|/api/plugins(?:/|\b))"  # noqa: Q000
)

GO_PERSISTENCE = re.compile(
    r"(?:gorm:\"[^\"]*(?:column|index|uniqueIndex):[^\"]*plugin_|"  # noqa: Q000
    r"TableName\(\).*return\s+\"plugin_|"  # noqa: Q000
    r"(?:Table|Column|Index|Constraint)\([^\n]*\"plugin_)"  # noqa: Q000
)
PYTHON_SQL = re.compile(
    r"(?i)\b(?:from|join|into|update|table|column|index|constraint)\s+plugin_"  # noqa: Q000
)
SQL_IDENTIFIER = re.compile(
    r"(?i)\b(?:create|alter|drop)\s+(?:table|index)\s+(?:if\s+(?:not\s+)?exists\s+)?plugin_"  # noqa: Q000
    r"|\b(?:from|join|into|update|references)\s+plugin_"  # noqa: Q000
    r"|\bplugin_[a-z0-9_]*\s+(?:varchar|text|uuid|jsonb?|integer|bigint|boolean|timestamp)\b"  # noqa: Q000
)


@dataclass(frozen=True)
class Violation:
    path: Path
    line: int
    column: int
    token: str

    def render(self, root: Path) -> str:
        try:
            path = self.path.relative_to(root)
        except ValueError:
            path = self.path
        return f"{path}:{self.line}:{self.column}: legacy Workflow domain name {self.token!r}"  # noqa: Q000


def _is_physical_name_allowed(path: Path, line: str, token: str, start: int) -> bool:
    if not PHYSICAL_NAME.fullmatch(token):
        return False
    if 'workflow-naming: persistence' in line:
        return True
    normalized = path.as_posix()
    if any(normalized.endswith(item) for item in PERSISTENCE_FILES):
        return True
    if path.suffix == ".sql":  # noqa: Q000
        # Historical migrations are immutable persistence records. Only their
        # lower-snake physical identifiers are exempt; routes and prose are not.
        if 'migrations' in path.parts:
            return True
        return bool(SQL_IDENTIFIER.search(line)) and not re.search(
            r"(?i)\bAS\s+$", line[:start]  # noqa: Q000
        )
    if path.suffix == ".go":  # noqa: Q000
        for match in re.finditer(r'gorm:\"[^\"]*\"', line):  # noqa: Q000
            if match.start() <= start < match.end():
                return True
        table_return = re.search(r'TableName\(\).*return\s+\"([^\"]+)\"', line)  # noqa: Q000
        if table_return and table_return.start(1) <= start < table_return.end(1):
            return True
        in_literal = any(
            match.start() <= start < match.end()
            for match in re.finditer(r'\"[^\"]*\"|`[^`]*`', line)
        )
        persistence_call = re.search(
            r'\.(?:Where|Joins|Table|Select|Order|Update|Updates|Exec)\(|'
            r'clause\.(?:Column|OnConflict)|(?i:\b(?:FROM|JOIN|UPDATE|INTO|TABLE)\b)',
            line,
        )
        return in_literal and bool(persistence_call)
    if path.suffix == ".py":  # noqa: Q000
        return bool(PYTHON_SQL.search(line)) and not re.search(
            r"(?i)\bAS\s+$", line[:start]  # noqa: Q000
        )
    return False


def scan_file(path: Path) -> Iterator[Violation]:
    parts = set(path.parts)
    if 'migrations' in parts and parts.intersection(IMMUTABLE_MIGRATION_RELEASES):
        return
    try:
        contents = path.read_text(encoding="utf-8")  # noqa: Q000
    except UnicodeDecodeError:
        return
    sql_literals = [
        (match.start(), match.end())
        for match in re.finditer(r'`[^`]*`', contents, re.DOTALL)
        if re.search(r'(?i)\b(?:SELECT|FROM|JOIN|UPDATE|INSERT|DELETE|CREATE|ALTER)\b', match.group())
    ]
    offset = 0
    for line_number, line in enumerate(contents.splitlines(keepends=True), 1):
        for match in LEGACY_DOMAIN.finditer(line):
            token = match.group(0)
            absolute = offset + match.start()
            if PHYSICAL_NAME.fullmatch(token) and any(
                start <= absolute < end for start, end in sql_literals
            ):
                continue
            if _is_physical_name_allowed(path, line, token, match.start()):
                continue
            yield Violation(path, line_number, match.start() + 1, token)
        offset += len(line)


def iter_files(paths: Iterable[Path]) -> Iterator[Path]:
    for path in paths:
        if path.is_file():
            if path.suffix in SCANNED_SUFFIXES or path.suffix == ".sql":  # noqa: Q000
                yield path
            continue
        if not path.exists():
            continue
        for candidate in path.rglob("*"):  # noqa: Q000
            if any(part in IGNORED_PARTS for part in candidate.parts):
                continue
            if candidate.is_file() and (
                candidate.suffix in SCANNED_SUFFIXES or candidate.suffix == ".sql"  # noqa: Q000
            ):
                yield candidate


def scan_paths(paths: Iterable[Path]) -> list[Violation]:
    return [violation for path in iter_files(paths) for violation in scan_file(path)]


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", nargs="*", help="files/directories; defaults to public product roots")  # noqa: Q000
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="repository root")  # noqa: Q000
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    root = args.root.resolve()
    paths = [Path(item) for item in args.paths] if args.paths else [root / item for item in DEFAULT_ROOTS]
    paths = [path if path.is_absolute() else root / path for path in paths]
    violations = scan_paths(paths)
    for violation in violations:
        print(violation.render(root))
    if violations:
        print(f"workflow naming check failed: {len(violations)} violation(s)", file=sys.stderr)  # noqa: Q000
        return 1
    print("workflow naming check passed")  # noqa: Q000
    return 0


if __name__ == "__main__":  # noqa: Q000
    raise SystemExit(main())
