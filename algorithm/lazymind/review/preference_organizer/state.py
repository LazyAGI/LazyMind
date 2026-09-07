from __future__ import annotations

import hashlib
from dataclasses import dataclass
from typing import Any

from lazymind.common.memory import (
    MemoryStore,
    parse_preference_items,
    validate_preference_index,
)
from lazymind.common.memory.preference_projection import (
    build_preference_projection,
    projection_target_reached,
)
from lazymind.config import config as _cfg

from .schemas import PreferenceStateData


@dataclass(frozen=True)
class PreferenceStateSnapshot:
    content: str
    items: tuple[Any, ...]
    data: PreferenceStateData


def load_preference_state(store: MemoryStore | None = None) -> PreferenceStateSnapshot:
    memory_store = store or MemoryStore()
    content = memory_store.read_preference()
    error = validate_preference_index(content)
    if error:
        raise ValueError(error)
    items = tuple(parse_preference_items(content))
    max_chars = int(_cfg['preference_context_max_chars'])
    projection = build_preference_projection(
        items,
        max_chars=max_chars,
    )
    data = PreferenceStateData(
        stored_items=len(items),
        full_projection_chars=projection.full_projection_chars,
        projected_items=projection.projected_items,
        projected_chars=projection.projected_chars,
        projection_truncated=projection.projection_truncated,
        etag='sha256:' + hashlib.sha256(content.encode('utf-8')).hexdigest(),
    )
    return PreferenceStateSnapshot(content=content, items=items, data=data)


TARGET_PROMPT_PERCENT = 40
MAX_PASSES = 2
MAX_ROUNDS_PER_PASS = 60


def target_reached(state: PreferenceStateData) -> bool:
    return (
        not state.projection_truncated
        and projection_target_reached(
            state.full_projection_chars,
            max_chars=int(_cfg['preference_context_max_chars']),
            target_percent=TARGET_PROMPT_PERCENT,
        )
    )
