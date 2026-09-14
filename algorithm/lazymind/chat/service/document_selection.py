from __future__ import annotations

import json
from typing import Any, Dict
from urllib.parse import quote

from lazyllm import LOG

from lazymind.chat.engine.tools.infra import get_core_api


def _text(value: Any, limit: int = 12000) -> str:
    return str(value or '').strip()[:limit]


def _segment_text(item: Any) -> str:
    if isinstance(item, dict):
        for key in ('display_content', 'content', 'text'):
            value = _text(item.get(key))
            if value:
                return value
        return ''
    return _text(getattr(item, 'text', ''))


def _metadata(item: Any) -> Dict[str, Any]:
    if isinstance(item, dict):
        value = item.get('metadata') or item.get('meta') or {}
    else:
        value = getattr(item, 'metadata', {}) or {}
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except (TypeError, ValueError):
            value = {}
    return value if isinstance(value, dict) else {}


def _page_matches(metadata: Dict[str, Any], page: Any) -> bool:
    try:
        expected = int(page)
        actual = int(metadata.get('page'))
    except (TypeError, ValueError):
        return False
    return actual in (expected, expected - 1)


def _bbox_overlap(metadata: Dict[str, Any], bbox: Any) -> float:
    candidate = metadata.get('bbox')
    if not (
        isinstance(candidate, (list, tuple)) and len(candidate) == 4
        and isinstance(bbox, (list, tuple)) and len(bbox) == 4
    ):
        return 0.0
    try:
        ax1, ay1, ax2, ay2 = map(float, candidate)
        bx1, by1, bx2, by2 = map(float, bbox)
    except (TypeError, ValueError):
        return 0.0
    intersection = max(0.0, min(ax2, bx2) - max(ax1, bx1)) * max(0.0, min(ay2, by2) - max(ay1, by1))
    selected_area = max(0.0, bx2 - bx1) * max(0.0, by2 - by1)
    return intersection / selected_area if selected_area else 0.0


def _find_positioned_segment(context: Dict[str, Any], selected_text: str) -> str:
    dataset_id = _text(context.get('dataset_id'), 256)
    document_id = _text(context.get('document_id'), 256)
    if not dataset_id or not document_id or not selected_text:
        return ''
    try:
        from lazymind.chat.engine.tools.algo import DOCUMENT

        candidates = DOCUMENT.keyword_search(
            group='block', keyword=selected_text, doc_id=document_id,
            kb_id=dataset_id, phrase=True, sort_by='score', size=20,
        )
    except Exception as exc:
        LOG.info(f'[DocumentSelection] positioned segment lookup unavailable: {exc}')
        return ''

    exact_candidates = []
    positioned_candidates = []
    for item in candidates or []:
        content = _segment_text(item)
        if selected_text not in content:
            continue
        exact_candidates.append(content)
        metadata = _metadata(item)
        if not _page_matches(metadata, context.get('page')):
            continue
        overlap = _bbox_overlap(metadata, context.get('bbox'))
        if overlap > 0:
            positioned_candidates.append((overlap, content))
    if positioned_candidates:
        return max(positioned_candidates, key=lambda candidate: candidate[0])[1]
    # A unique exact occurrence is safe even when older chunks have no location metadata.
    return exact_candidates[0] if len(exact_candidates) == 1 else ''


def resolve_document_selection_context(
    document_context: Dict[str, Any] | None,
    cited_text: str,
) -> str:
    context = document_context if isinstance(document_context, dict) else {}
    selected_text = _text(context.get('selected_text')) or _text(cited_text)
    paragraph_text = _text(context.get('paragraph_text'))
    dataset_id = _text(context.get('dataset_id'), 256)
    document_id = _text(context.get('document_id'), 256)
    segment_id = _text(context.get('segment_id'), 256)
    if dataset_id and document_id and segment_id:
        try:
            params = {}
            group = _text(context.get('segment_group'), 128)
            if group:
                params['group'] = group
            segment = get_core_api(
                f'/datasets/{quote(dataset_id, safe="")}/documents/'
                f'{quote(document_id, safe="")}/segments/{quote(segment_id, safe="")}',
                params=params,
            )
            content = _segment_text(segment)
            if content and (not selected_text or selected_text in content):
                return content
        except Exception as exc:
            LOG.info(f'[DocumentSelection] exact segment lookup unavailable: {exc}')

    positioned = _find_positioned_segment(context, selected_text)
    return positioned or paragraph_text or selected_text or _text(cited_text)


def render_document_selection(selected_text: str, context_text: str) -> str:
    selected = _text(selected_text)
    context = _text(context_text)
    if not selected:
        return context
    if not context or context == selected:
        return f'Selected text (the target of the user instruction):\n{selected}'
    return (
        f'Selected text (the target of the user instruction):\n{selected}\n\n'
        f'Surrounding passage (reference context only):\n{context}'
    )
