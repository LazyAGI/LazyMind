from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

import yaml

from lazymind.config import config as _cfg

from .validation.preference import parse_preference_items, validate_preference_index
from .store import MemoryStore


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
    max_items: Optional[int] = None,
    max_chars: Optional[int] = None,
) -> str:
    """Render the first preferences in stored order for prompt injection.

    ``created_at`` is intentionally omitted from the resident prompt projection;
    only ``updated_at`` is exposed.
    """
    if max_items is None:
        max_items = int(_cfg['preference_index_max_items'])
    if max_chars is None:
        max_chars = int(_cfg['preference_context_max_chars'])
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


def _render_preference_context(items: list[dict[str, str]]) -> str:
    return yaml.safe_dump(
        {'preferences': items},
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )
