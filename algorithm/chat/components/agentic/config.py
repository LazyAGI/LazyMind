from __future__ import annotations

from typing import Any, Dict, Tuple

from chat.prompts.agentic import (
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    VISION_EXTRACTOR_GUIDANCE,
    MEMORY_GUIDANCE,
    SEARCH_GUIDANCE,
    SKILLS_GUIDANCE,
    TOOL_CALL_STATUS_GUIDANCE,
    VOCAB_GUIDANCE,
    _COMBINED_REVIEW_PROMPT,
    _MEMORY_REVIEW_PROMPT,
    _SKILL_REVIEW_PROMPT,
)
from chat.utils.load_config import normalize_skill_fs_url

DEFAULT_TOOLS = [
    'kb_search',
    'kb_get_parent_node',
    'kb_get_window_nodes',
    'kb_keyword_search',
    'calculator',
    'web_search',
    'url_fetch',
    'arxiv_search',
    'vision_extractor',
    'vocab_manage',
    'memory',
    'skill_manage',
]

BUILTIN_FILE_TOOLS = (
    'read_file',
    'list_dir',
    'search_in_files',
    'make_dir',
    'write_file',
    'delete_file',
    'move_file',
)

REVIEW_TOOLS: dict[str, list[str]] = {
    'memory': ['memory'],
    'skill': ['skill_manage'],
    'combined': ['memory', 'skill_manage', 'vocab_manage'],
}

REVIEW_PROMPTS: dict[str, str] = {
    'memory': _MEMORY_REVIEW_PROMPT,
    'skill': _SKILL_REVIEW_PROMPT,
    'combined': _COMBINED_REVIEW_PROMPT,
}


def _normalize_available_tools(tools: Any) -> list[str]:
    if tools is None:
        return list(DEFAULT_TOOLS)
    if isinstance(tools, str):
        tools = [tools]
    if not isinstance(tools, list):
        return list(DEFAULT_TOOLS)
    if any(isinstance(t, str) and t.lower() == 'all' for t in tools):
        return list(DEFAULT_TOOLS)
    normalized = [t for t in tools if isinstance(t, str) and t]
    if 'vocab_manage' not in normalized and any(t in normalized for t in ('memory', 'skill_manage')):
        normalized.append('vocab_manage')
    return normalized


def _merge_builtin_file_tools(tools: list[str]) -> list[str]:
    merged: list[str] = []
    seen_names: set[str] = set()

    for tool in tools:
        if not isinstance(tool, str) or not tool:
            continue
        tool_name = tool.rsplit('.', 1)[-1]
        if tool_name in seen_names:
            continue
        seen_names.add(tool_name)
        merged.append(tool)

    for tool_name in BUILTIN_FILE_TOOLS:
        if tool_name in seen_names:
            continue
        seen_names.add(tool_name)
        merged.append(tool_name)

    return merged


def _normalize_available_skills(skills: Any) -> list[str]:
    if skills is None:
        return []
    if isinstance(skills, str):
        skills = [skills]
    if not isinstance(skills, list):
        return []
    return [skill for skill in skills if isinstance(skill, str) and skill]


def _parse_dataset_url(dataset_url: str) -> Tuple[str, str]:
    parts = [p.strip() for p in str(dataset_url).split(',', 1)]
    kb_url = parts[0] if parts else ''
    kb_name = parts[1] if len(parts) > 1 else ''
    return kb_url, kb_name


def _normalize_environment_context(config: dict) -> None:
    env_ctx = config.get('environment_context')
    if not isinstance(env_ctx, dict):
        config['environment_context'] = {}
        return

    time_ctx = env_ctx.get('time')
    if not isinstance(time_ctx, dict):
        config['environment_context'] = {}
        return

    normalized_time = {}
    now = time_ctx.get('now')
    timezone = time_ctx.get('timezone')
    if isinstance(now, str) and now.strip():
        normalized_time['now'] = now.strip()
    if isinstance(timezone, str) and timezone.strip():
        normalized_time['timezone'] = timezone.strip()

    config['environment_context'] = {'time': normalized_time} if normalized_time else {}


def _sync_request_context(config: dict) -> None:
    filters = config.get('filters') if isinstance(config.get('filters'), dict) else {}
    raw_kb_id = filters.get('kb_id')
    if not raw_kb_id:
        raw_kb_id = config.get('kb_id')

    kb_id = ''
    if isinstance(raw_kb_id, str):
        kb_id = raw_kb_id.strip()
    elif isinstance(raw_kb_id, list):
        for item in raw_kb_id:
            if isinstance(item, str) and item.strip():
                kb_id = item.strip()
                break

    if kb_id:
        config['kb_id'] = kb_id
    else:
        config.pop('kb_id', None)

    files = config.get('files') or []
    config['temp_files'] = files if isinstance(files, list) else []

    kb_url, kb_name = _parse_dataset_url(config.get('document_url') or '')
    if kb_url:
        config['kb_url'] = kb_url
    if kb_name:
        if kb_name.startswith('ds_'):
            config.setdefault('kb_id', kb_name)
            from config import config as _cfg
            kb_name = _cfg['agentic_kb_name']
        config['kb_name'] = kb_name
    config['skill_fs_url'] = normalize_skill_fs_url(config.get('skill_fs_url'))
    _normalize_environment_context(config)


def _filter_tools_for_request(tools: list[str], config: dict) -> list[str]:
    if not config.get('use_memory', True):
        tools = [t for t in tools if t != 'memory']

    if config.get('kb_id'):
        return tools

    has_temp_files = bool(config.get('temp_files'))
    filtered = []
    for tool in tools:
        if not tool.startswith('kb_'):
            filtered.append(tool)
        elif has_temp_files and tool == 'kb_search':
            filtered.append(tool)
    return filtered


def _build_environment_context_prompt(config: dict) -> str:
    env_ctx = config.get('environment_context')
    if not isinstance(env_ctx, dict):
        return ''

    time_ctx = env_ctx.get('time')
    if not isinstance(time_ctx, dict):
        return ''

    lines = []
    now = time_ctx.get('now')
    timezone = time_ctx.get('timezone')
    if isinstance(now, str) and now.strip():
        lines.append(f'Current user time: {now.strip()}')
    if isinstance(timezone, str) and timezone.strip():
        lines.append(f'User timezone: {timezone.strip()}')
    if not lines:
        return ''

    return (
        '## Environment Context\n'
        + '\n'.join(lines)
        + '\n\n'
        + 'Use this context to interpret relative time expressions such as today, tomorrow, now, '
        + 'this morning, tonight, 本周, 今天, 明天, 现在. Do not assume the server timezone is the user timezone.'
    )


def _build_runtime_system_prompt(config: dict, available_tools: list[str]) -> str:
    prompt_parts = [DEFAULT_SYSTEM_PROMPT]

    environment_prompt = _build_environment_context_prompt(config)
    if environment_prompt:
        prompt_parts.append(environment_prompt)

    if config.get('use_memory', True):
        user_pref = config.get('user_preference')
        mem = config.get('memory')
        if (isinstance(user_pref, str) and user_pref.strip()) or (isinstance(mem, str) and mem.strip()):
            memory_block = []
            if isinstance(user_pref, str) and user_pref.strip():
                memory_block.append(f'## User Profile / Preferences\n{user_pref.strip()}')
            if isinstance(mem, str) and mem.strip():
                memory_block.append(f'## Agent Working Memory\n{mem.strip()}')
            prompt_parts.append('\n\n'.join(memory_block))

    tool_guidance: list[str] = []
    if 'vocab_manage' in available_tools:
        tool_guidance.append(VOCAB_GUIDANCE)
    if 'memory' in available_tools and config.get('use_memory', True):
        tool_guidance.append(MEMORY_GUIDANCE)
    if 'skill_manage' in available_tools:
        tool_guidance.append(SKILLS_GUIDANCE)
    if tool_guidance:
        prompt_parts.append(' '.join(tool_guidance))
    if available_tools:
        prompt_parts.append(TOOL_CALL_STATUS_GUIDANCE)
    if any(tool.startswith('kb_') for tool in available_tools):
        prompt_parts.append(SEARCH_GUIDANCE)
    if (
        any(tool.startswith('kb_') for tool in available_tools)
        or (config.get('image_files') or [])
    ):
        prompt_parts.append(IMAGE_REFERENCE_MARKDOWN_GUIDANCE)
    if 'vision_extractor' in available_tools:
        prompt_parts.append(VISION_EXTRACTOR_GUIDANCE)

    return '\n\n'.join(prompt_parts)


def _get_runtime_agent_defaults() -> Dict[str, Any]:
    from config import config as _cfg
    return {
        'kb_url': _cfg['agentic_kb_url'],
        'core_api_url': _cfg['core_api_url'],
        'kb_name': _cfg['agentic_kb_name'],
        'skill_fs_url': _cfg['skill_fs_url'],
        'es_url': _cfg['opensearch_uri'],
        'es_user': _cfg['opensearch_user'],
        'es_password': _cfg['opensearch_password'],
        'web_search_timeout': _cfg['web_search_timeout'],
        'web_search_auto_sources': _cfg['web_search_auto_sources'],
        'web_search_wikipedia_base_url': _cfg['web_search_wikipedia_base_url'],
        'web_search_google_api_key': _cfg['web_search_google_api_key'],
        'web_search_google_search_engine_id': _cfg['web_search_google_search_engine_id'],
        'web_search_bing_subscription_key': _cfg['web_search_bing_subscription_key'],
        'web_search_bing_endpoint': _cfg['web_search_bing_endpoint'],
        'web_search_bocha_api_key': _cfg['web_search_bocha_api_key'],
        'web_search_bocha_base_url': _cfg['web_search_bocha_base_url'],
        'arxiv_search_timeout': _cfg['arxiv_search_timeout'],
    }
