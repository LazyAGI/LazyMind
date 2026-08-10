"""Request-thread citation processing for completed tool results."""

from __future__ import annotations

import copy
import json
from functools import wraps
from typing import Any

import lazyllm
from lazyllm.tools.agent.toolsManager import SkipMixin, ToolGroup
from lazyllm.tools.tools.search import SearchBase

from lazymind.chat.engine.tools.lazy_kb import KBToolkit
from lazymind.chat.service.utils.citations import (
    annotate_citations,
    register_external_search_result,
    supplement_external_search_result,
    upsert_external_source,
)


_KNOWLEDGE_SEARCH_METHODS = {
    'kb_search',
    'kb_get_parent_node',
    'kb_get_window_nodes',
    'kb_keyword_search',
}
_KNOWLEDGE_FUNCTIONS = {'kb_tmp_search'}
_PAGE_FUNCTIONS = {'url_fetch'}
_SEARCH_METHOD_DOCS = {
    'search': 'Search the provider and return ranked structured results.',
    'meta_search': 'Search provider metadata and return ranked structured results.',
    'meta_catalog': 'Return the provider metadata catalog.',
    'get_content': 'Return content for one unchanged provider search result item.',
    'get_contents': 'Return content for provider search result items in input order.',
}


class SearchProviderTools(SkipMixin, ToolGroup):
    """Expose an unchanged SearchBase provider through documented ToolManager callables."""

    def __init__(self, provider: SearchBase):
        self._instance = provider
        SkipMixin.__init__(self, getattr(type(provider), '__key_source__', None))
        callables = []
        methods = []
        for method_name in getattr(provider, '__public_apis__', ()):
            bound = getattr(provider, method_name)

            @wraps(bound)
            def execute(*args, __bound=bound, **kwargs):
                return __bound(*args, **kwargs)

            if not execute.__doc__:
                execute.__doc__ = _SEARCH_METHOD_DOCS.get(
                    method_name, 'Call the provider tool method.',
                )
            execute.__name__ = method_name
            callables.append(execute)
            methods.append(method_name)
        ToolGroup.__init__(
            self,
            tools=callables,
            name=type(provider).__name__,
            desc=type(provider).__doc__ or '',
            lazy=False,
            prefix=True,
        )
        for tool, method_name in zip(self._children, methods):
            tool._citation_provider = provider
            tool._citation_method = method_name

    @property
    def provider(self) -> SearchBase:
        return self._instance


def _citation_state() -> dict[str, Any]:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    state = agentic_config.get('citation_state')
    return state if isinstance(state, dict) else {}


def _tool_arguments(tool_call: dict[str, Any]) -> dict[str, Any]:
    function = tool_call.get('function') if isinstance(tool_call, dict) else None
    arguments = function.get('arguments', {}) if isinstance(function, dict) else {}
    if isinstance(arguments, str):
        try:
            arguments = json.loads(arguments)
        except (TypeError, json.JSONDecodeError):
            return {}
    return arguments if isinstance(arguments, dict) else {}


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
    return value


def _annotate_page_results(value: Any, state: dict[str, Any]) -> Any:
    annotated = copy.deepcopy(value)

    def visit(item: Any) -> None:
        if not isinstance(item, dict) or item.get('success') is False:
            return
        if isinstance(item.get('result'), dict):
            visit(item['result'])
        for result in item.get('results') or []:
            visit(result)
        upsert_external_source(item, state, roles={'searched'})

    visit(annotated)
    return annotated


class CitationResultMiddleware:
    """Register citations after ToolManager returns ordered results."""

    def __init__(self, manager: Any):
        self._manager = manager

    def __getattr__(self, name: str) -> Any:
        return getattr(self._manager, name)

    def _tool_kind(self, name: str) -> tuple[str, str] | None:
        tool = (getattr(self._manager, 'tools_info', None) or {}).get(name)
        instance = getattr(tool, '_citation_provider', None) or getattr(tool, '_instance', None)
        method = str(
            getattr(tool, '_citation_method', '')
            or getattr(tool, '_method_name', '')
            or ''
        )
        if isinstance(instance, SearchBase) and method in {
            'search', 'meta_search', 'get_content', 'get_contents',
        }:
            return 'external_search', method
        if isinstance(instance, KBToolkit) and method in _KNOWLEDGE_SEARCH_METHODS:
            return 'knowledge_base', method
        if name in _KNOWLEDGE_FUNCTIONS:
            return 'knowledge_base', name
        if name in _PAGE_FUNCTIONS:
            return 'external_page', name
        return None

    @staticmethod
    def _process_external_content(
        method: str,
        arguments: dict[str, Any],
        value: Any,
        state: dict[str, Any],
    ) -> Any:
        if method == 'get_content':
            item = arguments.get('item')
            if isinstance(item, dict):
                supplement_external_search_result(item, value, state, roles={'searched'})
            return value
        if method == 'get_contents':
            items = arguments.get('items')
            if isinstance(items, list) and isinstance(value, list):
                for item, content in zip(items, value):
                    if isinstance(item, dict):
                        supplement_external_search_result(
                            item, content, state, roles={'searched'},
                        )
            return value
        return _annotate_external_results(value, state)

    def _process_result(
        self,
        tool_call: dict[str, Any],
        result: Any,
        state: dict[str, Any],
    ) -> Any:
        if not isinstance(result, dict) or result.get('ok') is not True:
            return result
        function = tool_call.get('function') or {}
        name = str(function.get('name') or '')
        kind = self._tool_kind(name)
        if kind is None:
            return result
        source_type, method = kind
        value = result.get('value')
        if source_type == 'external_search':
            processed = self._process_external_content(
                method, _tool_arguments(tool_call), value, state,
            )
        elif source_type == 'knowledge_base':
            processed = copy.deepcopy(value)
            payload = (
                processed.get('result')
                if isinstance(processed, dict) and processed.get('success') is True
                else processed
            )
            annotate_citations(payload, state)
        else:
            processed = _annotate_page_results(value, state)
        return {**result, 'value': processed}

    def __call__(self, tools: Any, verbose: bool = False) -> Any:
        tool_calls = [tools] if isinstance(tools, dict) else list(tools or [])
        results = list(self._manager(tools, verbose=verbose))
        state = _citation_state()
        if not state:
            return results
        return [
            self._process_result(tool_call, result, state)
            for tool_call, result in zip(tool_calls, results)
        ]
