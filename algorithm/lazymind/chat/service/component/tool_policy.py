from __future__ import annotations

from copy import copy
from dataclasses import replace
from typing import Any

from lazymind.chat.engine.tools.local_file.workspace import grep, read_file
from lazymind.chat.service.chat_request import ChatRequest, ChatWorkflowOptions
from .tool_registry import (
    ArxivSearch,
    BingSearch,
    BochaSearch,
    GoogleSearch,
    KBToolkit,
    SciverseSearch,
    TavilySearch,
    ToolConfig,
    WikipediaToolkit,
    find_user_attachment,
    kb_tmp_search,
    read_user_attachment,
    url_fetch,
)

SIDECHAT_READONLY_CONFIGS = frozenset({
    'kb', 'temp_kb', 'wikipedia', 'web_search', 'academic_search', 'url_fetch',
    'read_user_attachment', 'find_user_attachment',
})
_SEARCH_METHODS = ('search', 'get_content', 'get_contents')
_READONLY_METHODS = {
    KBToolkit: (
        'list_knowledge_bases', 'list_knowledge_base_documents',
        'aggregate_knowledge_base_documents', 'kb_search',
        'kb_get_parent_node', 'kb_get_window_nodes', 'kb_keyword_search',
    ),
    WikipediaToolkit: _SEARCH_METHODS,
    GoogleSearch: _SEARCH_METHODS,
    BingSearch: _SEARCH_METHODS,
    BochaSearch: _SEARCH_METHODS,
    TavilySearch: _SEARCH_METHODS,
    ArxivSearch: _SEARCH_METHODS,
    SciverseSearch: (*_SEARCH_METHODS, 'meta_search', 'meta_catalog'),
}
_READONLY_FUNCTIONS = (
    kb_tmp_search, url_fetch, read_user_attachment, find_user_attachment, grep, read_file,
)
_ATTACHMENT_READONLY_APPENDIX = {
    'tool_policy': (
        'Read attachments only when relevant to the current question. '
        'Use find_user_attachment for an exact filename from the attachment list and '
        'read_user_attachment for its content. For document passages, prefer '
        'kb_tmp_search, grep, and read_file. Preserve source citation refs.'
    ),
}


def apply_tool_policy(request: ChatRequest) -> ChatRequest:
    """Apply the Host's restriction before task routing and tool/prompt assembly."""
    if request.runtime.tool_policy != 'sidechat_readonly':
        return request
    return request.model_copy(update={
        'runtime': request.runtime.model_copy(update={
            'mcp_config': [], 'mail_draft_confirm_id': None, 'mail_draft_confirm_revision': None,
        }),
        'personalization': request.personalization.model_copy(update={'use_memory': False}),
        'agent': request.agent.model_copy(update={
            'available_skills': [], 'has_subagents': False, 'enable_subagent': False,
        }),
        'workflow': ChatWorkflowOptions(enable_workflow=False),
        'explicit_resource_bindings': request.explicit_resource_bindings.model_copy(update={
            'skill_names': [], 'workflow_refs': [],
            'mentions': [item for item in request.explicit_resource_bindings.mentions
                         if item.get('type') not in {'skill', 'workflow'}],
        }),
    })


def _readonly_tool(tool: Any) -> Any:
    if isinstance(tool, tuple) and len(tool) == 2:
        inner = _readonly_tool(tool[0])
        return (inner, tool[1]) if inner is not None else None
    if isinstance(tool, dict):
        if tool.get('name') not in {'WebSearchToolkit', 'AcademicSearchToolkit'}:
            return None
        children = [_readonly_tool(child) for child in tool.get('tools', [])]
        return {**tool, 'tools': [child for child in children if child is not None]}
    methods = _READONLY_METHODS.get(type(tool))
    if methods is not None:
        # Toolkit gateways can activate every registered leaf, including leaves
        # absent from the initial schema. Freeze the actual callable surface too.
        restricted = copy(tool)
        restricted.__tool_public_apis__ = list(methods)
        return restricted
    return tool if any(tool is allowed for allowed in _READONLY_FUNCTIONS) else None


def sidechat_readonly_configs(configs: list[ToolConfig]) -> list[ToolConfig]:
    result = []
    for config in configs:
        if config.name not in SIDECHAT_READONLY_CONFIGS:
            continue
        tool = _readonly_tool(config.tool)
        if tool is not None:
            result.append(replace(
                config, tool=tool,
                appendix_system_prompt=(
                    _ATTACHMENT_READONLY_APPENDIX
                    if config.name in {'read_user_attachment', 'find_user_attachment'}
                    else config.appendix_system_prompt
                ),
            ))
    return result
