from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

import yaml

from .validation.common import parse_yaml_mapping
from .validation.preference import parse_preference_items, validate_preference_index
from .store import MemoryStore

# Cap preference index items when preparing prompt/runtime context.
MAX_PREFERENCE_CONTEXT_ITEMS = 100
MAX_PREFERENCE_CONTEXT_CHARS = 50_000


@dataclass(frozen=True)
class MemoryContext:
    soul: str
    profile: str
    preference: str


def load_memory_context(
    store: Optional[MemoryStore] = None,
    *,
    project_preference: bool = True,
) -> MemoryContext:
    """Load soul / profile / preference for prompt injection and tools.

    References are intentionally excluded; callers read them on demand.
    The three fixed files are required. Missing, unreadable, or invalid files
    raise instead of silently disabling persistent memory.
    """
    memory_store = store or MemoryStore()
    soul = memory_store.read_soul()
    profile = memory_store.read_profile()
    preference = memory_store.read_preference()
    preference_context = (
        truncate_preference_index(preference)
        if project_preference
        else preference
    )
    return MemoryContext(
        soul=soul,
        profile=profile,
        preference=preference_context,
    )


def truncate_preference_index(
    content: str,
    *,
    max_items: int = MAX_PREFERENCE_CONTEXT_ITEMS,
    max_chars: int = MAX_PREFERENCE_CONTEXT_CHARS,
) -> str:
    """Render the first preferences in stored order for prompt injection.

    ``created_at`` is intentionally omitted from the resident prompt projection;
    only ``updated_at`` is exposed.
    """
    text = content if isinstance(content, str) else ''
    if max_items < 0:
        raise ValueError('max_items must be >= 0')
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')
    if not text.strip():
        return text
    error = validate_preference_index(text)
    if error:
        raise ValueError(error)

    items = parse_preference_items(text)[:max_items]
    projected: list[dict[str, str]] = []
    for item in items:
        candidate = {
            'name': item.name,
            'summary': item.summary,
            'ref': item.ref,
            'updated_at': item.updated_at,
        }
        rendered = _render_preference_context([*projected, candidate])
        if len(rendered) > max_chars:
            break
        projected.append(candidate)
    return _render_preference_context(projected)


def profile_languages(profile: str) -> list[str]:
    document = parse_yaml_mapping(profile or '')
    locale = document.get('locale') if isinstance(document, dict) else None
    if not isinstance(locale, dict):
        return []
    languages = locale.get('languages')
    if not isinstance(languages, list):
        return []
    return [str(item).strip() for item in languages if str(item).strip()]


def _render_preference_context(items: list[dict[str, str]]) -> str:
    return yaml.safe_dump(
        {'preferences': items},
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )
