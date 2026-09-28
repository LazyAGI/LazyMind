from __future__ import annotations

from dataclasses import replace

from .tool_registry import KBToolkit, ToolConfig, filter_tools

SIDECHAT_TOOL_GROUPS = (
    'kb', 'temp_kb', 'wikipedia', 'web_search', 'academic_search', 'url_fetch',
    'read_user_attachment', 'find_user_attachment',
)
_ATTACHMENT_READONLY_APPENDIX = {
    'tool_policy': (
        'Read attachments only when relevant to the current question. '
        'Use find_user_attachment for an exact filename from the attachment list and '
        'read_user_attachment for its content. For document passages, prefer '
        'kb_tmp_search, search_file_resource, and read_file_resource. Preserve source citation refs.'
    ),
}


def build_sidechat_tool_configs(
    configs: list[ToolConfig],
    *,
    user_query: str,
    kb_ids: str | list[str] | None,
) -> list[ToolConfig]:
    """Build the Host-identified Sidechat's read-only capability surface."""
    inherited_kbs = [kb_ids] if isinstance(kb_ids, str) else list(kb_ids or [])
    result = []
    for config in filter_tools(configs, available_tools=SIDECHAT_TOOL_GROUPS, user_query=user_query):
        if config.name == 'kb':
            if not inherited_kbs:
                continue
            config = replace(config, tool=KBToolkit(kb_scope=inherited_kbs))
        elif config.name in {'read_user_attachment', 'find_user_attachment'}:
            config = replace(config, appendix_system_prompt=_ATTACHMENT_READONLY_APPENDIX)
        result.append(config)
    return result


def select_search_provider(config, query):
    """Keep explicit provider selection in product tool policy."""
    import re
    definition = {**config.tool, 'discoverable': True, 'prefix': config.tool.get('prefix')}
    service = config.name
    for provider in config.tool['tools']:
        source = provider.source_name
        if re.search(r'(?:使用|通过|用|using\s+|with\s+|via\s+)\s*' + re.escape(source)
                     + r'(?![a-zA-Z0-9_])', query, re.IGNORECASE):
            definition['tools'] = [provider]
            service = f'{service}/{source}'
            break
    return service, definition
