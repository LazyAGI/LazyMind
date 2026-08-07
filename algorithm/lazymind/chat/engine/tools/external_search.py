from __future__ import annotations

from functools import wraps
from typing import Any

import lazyllm

from lazymind.chat.service.utils.citations import (
    register_external_search_result,
    upsert_external_source,
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


def _wrap_search_method(base: type, method_name: str):
    original = getattr(base, method_name)

    @wraps(original)
    def wrapped(self, *args, **kwargs):
        return _register_items(original(self, *args, **kwargs))

    if not wrapped.__doc__:
        wrapped.__doc__ = 'Search external sources and return structured results with stable citation refs.'
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


def enable_search_result_citations(instance: Any) -> Any:
    """Add citation registration while preserving a search provider's public tool identity."""
    base = type(instance)
    if getattr(base, '_lazymind_search_result_citations', False):
        return instance
    methods = {
        'search': _wrap_search_method(base, 'search'),
        'get_content': _wrap_get_content(base),
        'get_contents': _wrap_get_contents(base),
    }
    if 'meta_search' in getattr(instance, '__public_apis__', ()):
        methods['meta_search'] = _wrap_search_method(base, 'meta_search')
    wrapped = type(base.__name__, (base,), {
        '__module__': base.__module__,
        '__doc__': base.__doc__,
        '_lazymind_search_result_citations': True,
        **methods,
    })
    instance.__class__ = wrapped
    return instance
