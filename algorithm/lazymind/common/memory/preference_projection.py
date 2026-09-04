from __future__ import annotations

from collections.abc import Iterable, Sequence
from dataclasses import dataclass

import json

from .validation.preference import PreferenceItem


@dataclass(frozen=True)
class PreferenceProjection:
    content: str
    stored_items: int
    full_projection_chars: int
    projected_items: int
    projected_chars: int
    projection_truncated: bool


def render_preference_projection(items: Iterable[PreferenceItem]) -> str:
    """Canonical YAML projection shared with Core, using JSON-quoted scalars.

    Fixed indentation, no line wrapping, and literal Unicode avoid dependence
    on the different scalar styles chosen by Go YAML and PyYAML.
    """
    def scalar(value: str) -> str:
        return json.dumps(value, ensure_ascii=False).replace('\u2028', r'\u2028').replace('\u2029', r'\u2029')

    rows = [f'- summary: {scalar(item.summary)}\n  ref: {scalar(item.ref)}\n' for item in items]
    return 'preferences:\n' + ''.join(rows) if rows else 'preferences: []\n'


def build_preference_projection(
    items: Sequence[PreferenceItem],
    *,
    max_chars: int,
) -> PreferenceProjection:
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')

    all_items = list(items)
    full = render_preference_projection(all_items)
    projected: list[PreferenceItem] = []
    for item in all_items:
        candidate = [*projected, item]
        if len(render_preference_projection(candidate)) > max_chars:
            break
        projected = candidate
    content = render_preference_projection(projected)
    # Even the empty YAML envelope may not fit an unusually small budget.
    if len(content) > max_chars:
        content = ''
    return PreferenceProjection(
        content=content,
        stored_items=len(all_items),
        full_projection_chars=len(full),
        projected_items=len(projected),
        projected_chars=len(content),
        projection_truncated=len(projected) < len(all_items),
    )


def projection_target_reached(
    projection_chars: int,
    *,
    max_chars: int,
    target_percent: int,
) -> bool:
    return projection_chars * 100 < max_chars * target_percent
