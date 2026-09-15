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
        'kb_tmp_search, grep, and read_file. Preserve source citation refs.'
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
