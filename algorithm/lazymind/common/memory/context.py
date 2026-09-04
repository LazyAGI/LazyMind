from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from lazymind.config import config as _cfg

from .preference_projection import build_preference_projection
from .store import MemoryStore
from .validation.preference import parse_preference_items, validate_preference_index


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
    max_chars: Optional[int] = None,
) -> str:
    """Render the compact summary/ref Preference projection for Chat."""
    if max_chars is None:
        max_chars = int(_cfg['preference_context_max_chars'])
    text = content if isinstance(content, str) else ''
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')
    if not text.strip():
        return ''
    error = validate_preference_index(text)
    if error:
        raise ValueError(error)

    projection = build_preference_projection(
        parse_preference_items(text),
        max_chars=max_chars,
    )
    return projection.content
