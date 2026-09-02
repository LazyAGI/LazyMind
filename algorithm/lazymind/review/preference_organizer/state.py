from __future__ import annotations

import hashlib
from dataclasses import dataclass
from typing import Any

import yaml

from lazymind.common.memory import MemoryStore, parse_preference_items
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
    items = tuple(parse_preference_items(content))
    max_items = int(_cfg['preference_index_max_items'])
    max_chars = int(_cfg['preference_context_max_chars'])
    full = _render_projection(items)
    projected: list[Any] = []
    for item in items:
        if len(projected) >= max_items:
            break
        candidate = [*projected, item]
        if len(_render_projection(candidate)) > max_chars:
            break
        projected = candidate
    projection = _render_projection(projected)
    data = PreferenceStateData(
        stored_items=len(items),
        full_projection_chars=len(full),
        projected_items=len(projected),
        projected_chars=len(projection),
        projection_truncated=len(projected) < len(items),
        etag='sha256:' + hashlib.sha256(content.encode('utf-8')).hexdigest(),
    )
    return PreferenceStateSnapshot(content=content, items=items, data=data)


def target_reached(
    state: PreferenceStateData,
    *,
    min_items: int,
    max_items: int,
    target_prompt_percent: int,
) -> bool:
    max_chars = int(_cfg['preference_context_max_chars'])
    return (
        min_items <= state.stored_items <= max_items
        and not state.projection_truncated
        and state.full_projection_chars * 100 <= max_chars * target_prompt_percent
    )


def _render_projection(items: list[Any] | tuple[Any, ...]) -> str:
    payload = [
        {
            'name': item.name,
            'summary': item.summary,
            'ref': item.ref,
            'updated_at': item.updated_at,
        }
        for item in items
    ]
    return yaml.safe_dump(
        {'preferences': payload},
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )
