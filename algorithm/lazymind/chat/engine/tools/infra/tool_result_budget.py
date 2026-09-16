"""Character budget for external search results, after citation registration."""
from __future__ import annotations

import copy
import json
from typing import Any


SEARCH_RESULT_CHARS = 16384
PREVIEW_CHARS = 700
_PREVIEW_KEYS = {'title', 'snippet', 'raw_content', 'content', 'answer'}
_SOURCE_KEYS = {'url', 'source', 'ref', 'citation_index', 'doc_id', 'document_id',
                'doi', 'paperId', 'pageid', 'provider', 'target_url', 'link', 'id'}
_EXTRA_KEYS = _SOURCE_KEYS | _PREVIEW_KEYS | {'truncated'}
_PAGE_KEYS = {'items', 'total_count', 'total_pages', 'page', 'page_size', 'next_cursor',
              'returned_count', 'truncated'}


def _size(value: Any) -> int:
    return len(json.dumps(value, ensure_ascii=False, default=str))


def _preview(item: Any) -> Any:
    if not isinstance(item, dict):
        return item
    extra = item.get('extra')
    changed = False
    for fields in (item, extra):
        if not isinstance(fields, dict):
            continue
        for key in _PREVIEW_KEYS:
            value = fields.get(key)
            if isinstance(value, str) and len(value) > PREVIEW_CHARS:
                fields[key] = value[:PREVIEW_CHARS]
                changed = True
    if changed:
        item.setdefault('extra', {})['truncated'] = True
    return item


def bound_external_search_result(result: dict[str, Any]) -> dict[str, Any]:
    """Keep source identities intact and count the complete tool envelope."""
    bounded = copy.deepcopy(result)
    value = bounded.get('value')
    if isinstance(value, list):
        items = value
    elif isinstance(value, dict) and isinstance(value.get('items'), list):
        items = value['items']
    else:
        return result
    for item in items:
        _preview(item)
    if isinstance(value, dict):
        value['returned_count'] = len(items)
    if _size(bounded) <= SEARCH_RESULT_CHARS:
        return bounded

    bounded['truncated'] = True
    bounded['returned_count'] = len(items)
    if isinstance(value, dict):
        for key in list(value):
            if key not in _PAGE_KEYS:
                del value[key]
        value['truncated'] = True
    for item in items:
        if not isinstance(item, dict):
            continue
        for key in list(item):
            if key not in _SOURCE_KEYS | _PREVIEW_KEYS | {'extra'}:
                del item[key]
        if isinstance(item.get('extra'), dict):
            item['extra'] = {k: v for k, v in item['extra'].items() if k in _EXTRA_KEYS}
    while items and _size(bounded) > SEARCH_RESULT_CHARS:
        items.pop()
        bounded['returned_count'] = len(items)
        if isinstance(value, dict):
            value['returned_count'] = len(items)
    if not items:
        bounded['notice'] = 'Search results were omitted to fit the character budget.'
    if _size(bounded) <= SEARCH_RESULT_CHARS:
        return bounded
    # Even metadata such as an enormous provider cursor can exceed the budget.
    return {'ok': True, 'value': {'items': [], 'returned_count': 0, 'truncated': True}
            if isinstance(value, dict) else [], 'returned_count': 0, 'truncated': True,
            'notice': 'Search result metadata exceeds the character budget; results were omitted.'}
