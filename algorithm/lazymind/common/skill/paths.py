from __future__ import annotations

import posixpath


def normalize_skill_package_path(path: str | None) -> str:
    raw = str(path or '').strip()
    if not raw or raw.startswith('/') or '\\' in raw:
        raise ValueError('operation path must be a non-empty relative POSIX path.')
    parts = raw.split('/')
    if any(part in ('', '.', '..') for part in parts):
        raise ValueError("operation path must not contain empty, '.', or '..' segments.")
    normalized = posixpath.normpath(raw)
    if normalized in ('', '.') or normalized == '..' or normalized.startswith('../'):
        raise ValueError('operation path must stay inside the skill package.')
    return normalized


def relative_to_package(package_dir: str, path: str) -> str:
    package_parts = _normalized_parts(package_dir)
    path_parts = _normalized_parts(path)
    if len(path_parts) > len(package_parts) and path_parts[:len(package_parts)] == package_parts:
        rel_parts = path_parts[len(package_parts):]
    else:
        rel_parts = path_parts[-1:]
    return normalize_skill_package_path('/'.join(rel_parts))


def _normalized_parts(path: str) -> list[str]:
    raw = str(path or '').strip().replace('\\', '/').rstrip('/')
    if '://' in raw:
        raw = raw.split('://', 1)[1]
    return [part for part in raw.split('/') if part]
