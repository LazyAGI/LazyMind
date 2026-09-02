from __future__ import annotations

from collections.abc import Iterable, Sequence
from dataclasses import dataclass

import yaml

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
    """Render the compact Preference prompt projection used by Chat."""
    payload = [
        {
            'summary': item.summary,
            'ref': item.ref,
        }
        for item in items
    ]
    return yaml.safe_dump(
        {'preferences': payload},
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )


def build_preference_projection(
    items: Sequence[PreferenceItem],
    *,
    max_items: int,
    max_chars: int,
) -> PreferenceProjection:
    if max_items < 0:
        raise ValueError('max_items must be >= 0')
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')

    all_items = list(items)
    full = render_preference_projection(all_items)
    projected: list[PreferenceItem] = []
    for item in all_items:
        if len(projected) >= max_items:
            break
        candidate = [*projected, item]
        if len(render_preference_projection(candidate)) > max_chars:
            break
        projected = candidate
    content = render_preference_projection(projected)
    return PreferenceProjection(
        content=content,
        stored_items=len(all_items),
        full_projection_chars=len(full),
        projected_items=len(projected),
        projected_chars=len(content),
        projection_truncated=len(projected) < len(all_items),
    )


def projection_safe_item_count(
    items: Sequence[PreferenceItem],
    *,
    max_chars: int,
    target_percent: int,
) -> int:
    """Return the largest count whose worst current subset stays below target.

    The longest current prompt entries form the worst retained subset. The
    Organizer receives only the returned count; character budgets remain a
    deterministic controller concern.
    """
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')
    if not 1 <= target_percent <= 100:
        raise ValueError('target_percent must be between 1 and 100')

    ranked = sorted(
        items,
        key=lambda item: len(render_preference_projection([item])),
        reverse=True,
    )
    safe_count = 0
    for count in range(1, len(ranked) + 1):
        candidate = render_preference_projection(ranked[:count])
        if len(candidate) * 100 >= max_chars * target_percent:
            break
        safe_count = count
    return safe_count


def projection_target_reached(
    projection_chars: int,
    *,
    max_chars: int,
    target_percent: int,
) -> bool:
    return projection_chars * 100 < max_chars * target_percent
