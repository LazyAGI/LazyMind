"""Unified citation adapters for search tools."""

from __future__ import annotations

from functools import wraps
from typing import Any, get_type_hints

import lazyllm

from lazymind.chat.service.utils.citations import (
    annotate_citations,
    register_external_search_result,
    upsert_external_source,
)


_KNOWLEDGE_SEARCH_METHODS = (
    'kb_search',
    'kb_get_parent_node',
    'kb_get_window_nodes',
    'kb_keyword_search',
)


def _citation_state() -> dict[str, Any]:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    state = agentic_config.get('citation_state')
    return state if isinstance(state, dict) else {}


def _register_items(value: Any) -> Any:
    state = _citation_state()
    if not state:
        return value
    if isinstance(value, list):
        unique = []
        seen = set()
        for item in value:
            if isinstance(item, dict):
                register_external_search_result(item, state)
                for image in item.get('images') or []:
                    image_url = str(image.get('url') if isinstance(image, dict) else image).strip()
                    if image_url:
                        register_external_search_result({
                            'title': str(image.get('description') if isinstance(image, dict) else '').strip()
                            or item.get('title') or 'Image',
                            'url': image_url,
                            'image_urls': [image_url],
                        }, state)
                index = item.get('citation_index')
                if index and index in seen:
                    continue
                if index:
                    seen.add(index)
            unique.append(item)
        value[:] = unique
    elif isinstance(value, dict) and isinstance(value.get('items'), list):
        _register_items(value['items'])
    return value


def _annotate_knowledge_result(value: Any) -> Any:
    state = _citation_state()
    if state:
        result = value.get('result') if isinstance(value, dict) and value.get('success') is True else value
        annotate_citations(result, state)
    return value


def _register_page_results(value: Any) -> Any:
    state = _citation_state()

    def visit(item: Any) -> None:
        if not state or not isinstance(item, dict) or item.get('success') is False:
            return
        if isinstance(item.get('result'), dict):
            visit(item['result'])
        for result in item.get('results') or []:
            visit(result)
        if item.get('source_status') in (None, 'ok'):
            upsert_external_source(item, state)

    visit(value)
    return value


def _content_result(item: dict[str, Any], content: Any) -> dict[str, Any]:
    result = {'content': str(content or '')}
    state = _citation_state()
    if not state:
        return result
    page = {
        'url': item.get('url'),
        'title': item.get('title'),
        'content': result['content'],
    }
    upsert_external_source(page, state)
    for key in ('citation_index', 'ref'):
        if page.get(key):
            result[key] = page[key]
    return result


def _wrap_result_method(base: type, method_name: str, handler):
    original = getattr(base, method_name)

    @wraps(original)
    def wrapped(self, *args, **kwargs):
        return handler(original(self, *args, **kwargs))

    if not wrapped.__doc__:
        wrapped.__doc__ = 'Search external sources and return structured results with stable citation refs.'
    return wrapped


def _wrap_function(func, handler):
    @wraps(func)
    def wrapped(*args, **kwargs):
        return handler(func(*args, **kwargs))

    wrapped.__annotations__ = get_type_hints(func)
    return wrapped


def _wrap_get_content(base: type):
    original = base.get_content

    @wraps(original)
    def wrapped(self, item, *args, **kwargs):
        return _content_result(item, original(self, item, *args, **kwargs))

    if not wrapped.__doc__:
        wrapped.__doc__ = 'Read one unchanged search result item and return its content with the existing ref.'
    return wrapped


def _wrap_get_contents(base: type):
    @wraps(base.get_contents)
    def wrapped(self, items):
        return [_content_result(item, base.get_content(self, item)) for item in items]

    if not wrapped.__doc__:
        wrapped.__doc__ = 'Read search result items in order and return content with each existing ref.'
    return wrapped


def enable_search_result_citations(tool: Any, source_type: str = 'external') -> Any:
    """Register citations outside the underlying search implementation."""
    if source_type not in {'external', 'knowledge_base'}:
        raise ValueError(f'unsupported citation source type: {source_type}')
    if not hasattr(tool, '__public_apis__'):
        handler = _annotate_knowledge_result if source_type == 'knowledge_base' else _register_page_results
        return _wrap_function(tool, handler)

    base = type(tool)
    if getattr(base, '_lazymind_search_result_citations', None) == source_type:
        return tool
    if source_type == 'knowledge_base':
        methods = {
            name: _wrap_result_method(base, name, _annotate_knowledge_result)
            for name in _KNOWLEDGE_SEARCH_METHODS
            if name in getattr(tool, '__public_apis__', ())
        }
    else:
        methods = {
            'search': _wrap_result_method(base, 'search', _register_items),
            'get_content': _wrap_get_content(base),
            'get_contents': _wrap_get_contents(base),
        }
        if 'meta_search' in getattr(tool, '__public_apis__', ()):
            methods['meta_search'] = _wrap_result_method(base, 'meta_search', _register_items)
    wrapped = type(base.__name__, (base,), {
        '__module__': base.__module__,
        '__doc__': base.__doc__,
        '_lazymind_search_result_citations': source_type,
        **methods,
    })
    tool.__class__ = wrapped
    return tool
