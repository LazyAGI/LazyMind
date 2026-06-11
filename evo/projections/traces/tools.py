from __future__ import annotations

from functools import lru_cache
from typing import Any

from lazyllm.tracing.datamodel.structured import ExecutionStep

from evo.projections.traces.values import drop_empty

_FALLBACK_CHAT_TOOL_NAMES = frozenset({
    'ArxivSearch',
    'BingSearch',
    'BochaSearch',
    'GoogleSearch',
    'SciverseSearch',
    'TavilySearch',
    'WikipediaSearch',
    'calculator',
    'get_skill',
    'kb_get_parent_node',
    'kb_get_window_nodes',
    'kb_keyword_search',
    'kb_search',
    'memory_editor',
    'read_reference',
    'run_script',
    'skill_editor',
    'url_fetch',
    'vision_extractor',
    'vocab_learn',
    'vocab_manage',
})


def is_tool_node(node: ExecutionStep) -> bool:
    return node.name in registered_chat_tool_names()


@lru_cache(maxsize=1)
def registered_chat_tool_names() -> frozenset[str]:
    try:
        from lazymind.chat.service.component import get_all_tool_groups
    except Exception:
        return _FALLBACK_CHAT_TOOL_NAMES

    names: set[str] = set()
    for group in get_all_tool_groups():
        if not isinstance(group, dict):
            continue
        for method in group.get('methods') or []:
            if isinstance(method, dict) and (name := str(method.get('name') or '').strip()):
                names.add(name)
    return frozenset(names) | _FALLBACK_CHAT_TOOL_NAMES


def tool_metadata(value: Any, *, fallback_tool: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        return {'tool_name': fallback_tool}
    result = value.get('result') if isinstance(value.get('result'), dict) else value
    items = result.get('items') if isinstance(result.get('items'), list) else None
    return drop_empty({
        'tool_name': value.get('tool') or value.get('name') or fallback_tool,
        'success': value.get('success'),
        'status': value.get('status') or result.get('status'),
        'error': value.get('error') or result.get('error'),
        'total': result.get('total'),
        'item_count': len(items) if items is not None else None,
    })
