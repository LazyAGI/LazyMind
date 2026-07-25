from __future__ import annotations

import re

AGENTS_ROOT = 'memory/agents'
USERS_ROOT = 'memory/users'
REFERENCE_ROOT = 'memory/users/references'

SOUL_PATH = 'memory/agents/soul.yaml'
PROFILE_PATH = 'memory/users/profile.yaml'
PREFERENCE_PATH = 'memory/users/preference.yaml'

_REFERENCE_NAME_RE = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$')


def normalize_memory_path(path: str) -> str:
    raw = str(path or '').strip()
    if '://' in raw:
        # Allow remote://memory/... style inputs from FS tooling.
        raw = raw.split('://', 1)[1]
    return raw.strip('/')


def is_reference_path(path: str) -> bool:
    normalized = normalize_memory_path(path)
    if not normalized.startswith(f'{REFERENCE_ROOT}/'):
        return False
    name = normalized[len(REFERENCE_ROOT) + 1:]
    if '/' in name or not name.endswith('.md'):
        return False
    return bool(_REFERENCE_NAME_RE.fullmatch(name[:-3]))


def is_fixed_memory_file(path: str) -> bool:
    return normalize_memory_path(path) in {SOUL_PATH, PROFILE_PATH, PREFERENCE_PATH}


def reference_filename(path: str) -> str:
    normalized = normalize_memory_path(path)
    if not is_reference_path(normalized):
        raise ValueError(f'invalid reference path: {path!r}')
    return normalized.rsplit('/', 1)[-1]


def build_reference_path(name: str) -> str:
    raw = str(name or '').strip()
    if raw.endswith('.md'):
        raw = raw[:-3]
    if not _REFERENCE_NAME_RE.fullmatch(raw):
        raise ValueError(
            f'invalid reference name {name!r}; expected [A-Za-z0-9][A-Za-z0-9_-]{{0,63}}'
        )
    return f'{REFERENCE_ROOT}/{raw}.md'


def split_reference_ref(ref: str) -> tuple[str, str]:
    """Split references/foo.md#anchor into (path, anchor)."""
    raw = str(ref or '').strip()
    if not raw:
        raise ValueError('reference ref is required')
    path_part, _, anchor = raw.partition('#')
    normalized = normalize_memory_path(path_part)
    if normalized.startswith('references/'):
        normalized = f'{USERS_ROOT}/{normalized}'
    if not is_reference_path(normalized):
        raise ValueError(f'invalid reference ref: {ref!r}')
    return normalized, anchor.strip()
