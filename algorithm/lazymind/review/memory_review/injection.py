from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from lazyllm import LOG

from .defaults import default_preference_md, default_profile_md, default_soul_md
from .schema.common import parse_yaml_frontmatter
from .schema.preference import parse_preference_items, render_preference_item
from .store import MemoryStore

# Phase-1 injection cap from the memory design: at most 100 preference index items.
MAX_PREFERENCE_INJECT_ITEMS = 100


@dataclass(frozen=True)
class ChatMemoryContext:
    soul: str
    profile: str
    preference: str


def load_chat_memory_context(store: Optional[MemoryStore] = None) -> ChatMemoryContext:
    """Load soul / profile / preference for chat system-prompt injection.

    References are intentionally excluded; the model reads them on demand.
    RemoteFS failures fall back to defaults so chat startup does not break
    while the backend memory tree is still rolling out.
    """
    memory_store = store or MemoryStore()
    soul = _safe_read(memory_store.read_soul, default_soul_md(), label='soul')
    profile = _safe_read(memory_store.read_profile, default_profile_md(), label='profile')
    preference = _safe_read(
        memory_store.read_preference,
        default_preference_md(),
        label='preference',
    )
    return ChatMemoryContext(
        soul=soul,
        profile=profile,
        preference=truncate_preference_index(preference),
    )


def truncate_preference_index(
    content: str,
    *,
    max_items: int = MAX_PREFERENCE_INJECT_ITEMS,
) -> str:
    """Keep preference frontmatter and at most ``max_items`` index entries."""
    text = content if isinstance(content, str) else ''
    if max_items < 0:
        raise ValueError('max_items must be >= 0')
    items = parse_preference_items(text)
    if len(items) <= max_items:
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
    blocks = [render_preference_item(item).rstrip('\n') for item in kept]
    return '\n'.join(header_lines + blocks) + '\n'


def profile_languages(profile: str) -> list[str]:
    frontmatter, _ = parse_yaml_frontmatter(profile or '')
    locale = frontmatter.get('locale') if isinstance(frontmatter, dict) else None
    if not isinstance(locale, dict):
        return []
    languages = locale.get('languages')
    if not isinstance(languages, list):
        return []
    return [str(item).strip() for item in languages if str(item).strip()]


def _safe_read(loader, default: str, *, label: str) -> str:
    try:
        value = loader()
        return value if isinstance(value, str) else default
    except Exception as exc:
        LOG.warning(f'[MemoryInjection] failed to load {label}: {exc}')
        return default
