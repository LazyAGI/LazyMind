"""Request-thread citation processing for completed tool results."""

from __future__ import annotations

import copy
from typing import Any

import lazyllm
from lazyllm.tools.tools.search import SearchBase

from lazymind.chat.engine.tools.lazy_kb import KBToolkit
from lazymind.chat.service.utils.citations import (
    annotate_citations,
    register_external_search_result,
    upsert_external_source,
)


_KNOWLEDGE_SEARCH_METHODS = {
    'kb_search',
    'kb_get_parent_node',
    'kb_get_window_nodes',
    'kb_keyword_search',
}
_KNOWLEDGE_FUNCTIONS: set[str] = set()
_PAGE_FUNCTIONS = {'url_fetch'}


def _citation_state() -> dict[str, Any]:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    state = agentic_config.get('citation_state')
    return state if isinstance(state, dict) else {}


def _annotate_external_item(item: Any, state: dict[str, Any]) -> Any:
    if not isinstance(item, dict):
        return item
    annotated = dict(item)
    register_external_search_result(annotated, state, roles={'searched'})
    return annotated


def _annotate_external_results(value: Any, state: dict[str, Any]) -> Any:
    if isinstance(value, list):
        return [_annotate_external_item(item, state) for item in value]
    if isinstance(value, dict) and isinstance(value.get('items'), list):
        annotated = dict(value)
        annotated['items'] = [
            _annotate_external_item(item, state)
            for item in value['items']
        ]
        return annotated
    if isinstance(value, dict) and any(
        key in value
        for key in ('url', 'doi', 'doc_id', 'document_id', 'source', 'provider')
    ):
        return _annotate_external_item(value, state)
    return value


def _annotate_page_results(value: Any, state: dict[str, Any]) -> Any:
    annotated = copy.deepcopy(value)
    if isinstance(annotated, dict):
        upsert_external_source(annotated, state, roles={'searched'})
    return annotated


class CitationResultMiddleware:
    """Register citations after ToolManager returns ordered results."""

    def __init__(self, manager: Any):
        self._manager = manager

    def __getattr__(self, name: str) -> Any:
        return getattr(self._manager, name)

    def _tool_kind(self, name: str) -> str | None:
        tool = (getattr(self._manager, 'tools_info', None) or {}).get(name)
        instance = getattr(tool, '_instance', None)
        method = str(getattr(tool, '_method_name', '') or '')
        if isinstance(instance, SearchBase) and method in {
            'search', 'meta_search', 'get_content', 'get_contents',
        }:
            return 'external_search'
        if isinstance(instance, KBToolkit) and method in _KNOWLEDGE_SEARCH_METHODS:
            return 'knowledge_base'
        if name in _KNOWLEDGE_FUNCTIONS:
            return 'knowledge_base'
        if name in _PAGE_FUNCTIONS:
            return 'external_page'
        return None

    def _process_result(
        self,
        tool_call: dict[str, Any],
        result: Any,
        state: dict[str, Any],
        *,
        collect_only: bool = False,
    ) -> Any:
        if not isinstance(result, dict) or result.get('ok') is not True:
            return result
        function = tool_call.get('function') or {}
        name = str(function.get('name') or '')
        kind = self._tool_kind(name)
        if kind is None:
            return result
        value = result.get('value')
        if kind == 'external_search':
            processed = _annotate_external_results(value, state)
        elif kind == 'knowledge_base':
            processed = copy.deepcopy(value)
            annotate_citations(processed, state, roles={'searched'})
        else:
            processed = _annotate_page_results(value, state)
        if collect_only:
            return result
        return {**result, 'value': processed}

    def __call__(self, tools: Any, verbose: bool = False,
                 allowed_tool_names: set[str] | None = None) -> Any:
        tool_calls = [tools] if isinstance(tools, dict) else list(tools or [])
        results = list(self._manager(
            tools,
            verbose=verbose,
            allowed_tool_names=allowed_tool_names,
        ))
        state = _citation_state()
        if not state:
            return results
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        collect_only = agentic_config.get('citation_mode') == 'collect_only'
        return [
            self._process_result(
                tool_call, result, state, collect_only=collect_only,
            )
            for tool_call, result in zip(tool_calls, results)
        ]
