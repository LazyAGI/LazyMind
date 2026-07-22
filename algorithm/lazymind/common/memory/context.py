from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from lazyllm import LOG

from .validation.common import parse_yaml_frontmatter
from .validation.preference import (
    parse_preference_items,
    render_preference_item,
)
from .store import MemoryStore

# Cap preference index items when preparing prompt/runtime context.
MAX_PREFERENCE_CONTEXT_ITEMS = 100
MAX_PREFERENCE_CONTEXT_CHARS = 10_000


@dataclass(frozen=True)
class MemoryContext:
    soul: str
    profile: str
    preference: str


def load_memory_context(store: Optional[MemoryStore] = None) -> MemoryContext:
    """Load soul / profile / preference for prompt injection and tools.

    References are intentionally excluded; callers read them on demand.
    Missing or unreadable RemoteFS documents become empty strings so Chat
    startup does not depend on algorithm-side templates.
    """
    memory_store = store or MemoryStore()
    soul = _safe_read(memory_store.read_soul, label='soul')
    profile = _safe_read(memory_store.read_profile, label='profile')
    preference = _safe_read(memory_store.read_preference, label='preference')
    return MemoryContext(
        soul=soul,
        profile=profile,
        preference=truncate_preference_index(preference),
    )


def truncate_preference_index(
    content: str,
    *,
    max_items: int = MAX_PREFERENCE_CONTEXT_ITEMS,
    max_chars: int = MAX_PREFERENCE_CONTEXT_CHARS,
) -> str:
    """Keep preference frontmatter with bounded item count and total characters."""
    text = content if isinstance(content, str) else ''
    if max_items < 0:
        raise ValueError('max_items must be >= 0')
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')
    if not text.strip():
        return text
    items = parse_preference_items(text)
    if len(items) <= max_items and len(text) <= max_chars:
        return text

    frontmatter, _body = parse_yaml_frontmatter(text)
    kept = items[:max_items]
    header_lines = ['---']
    schema_version = frontmatter.get('schema_version', 1)
    header_lines.append(f'schema_version: {schema_version}')
    updated_at = frontmatter.get('updated_at')
    if updated_at is not None:
        header_lines.append(f'updated_at: {updated_at}')
    header_lines.extend(['---', '# Preference Index'])
    body_lines = list(header_lines)
    base_text = '\n'.join(body_lines) + '\n'
    if len(base_text) > max_chars:
        return base_text[:max_chars]

    blocks = [render_preference_item(item).rstrip('\n') for item in kept]
    result_lines = list(body_lines)
    current_text = base_text
    for block in blocks:
        candidate = '\n'.join(result_lines + [block]) + '\n'
        if len(candidate) > max_chars:
            break
        result_lines.append(block)
        current_text = candidate
    return current_text


def profile_languages(profile: str) -> list[str]:
    frontmatter, _ = parse_yaml_frontmatter(profile or '')
    locale = frontmatter.get('locale') if isinstance(frontmatter, dict) else None
    if not isinstance(locale, dict):
        return []
    languages = locale.get('languages')
    if not isinstance(languages, list):
        return []
    return [str(item).strip() for item in languages if str(item).strip()]


def _safe_read(loader, *, label: str) -> str:
    try:
        value = loader()
        return value if isinstance(value, str) else ''
    except Exception as exc:
        LOG.warning(f'[MemoryContext] failed to load {label}: {exc}')
        return ''
