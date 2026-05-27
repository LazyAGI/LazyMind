from __future__ import annotations

from typing import Any


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


def _normalize_kb_id_value(kb_id: Any) -> Any:
    if isinstance(kb_id, str):
        normalized = kb_id.strip()
        return normalized or None
    if isinstance(kb_id, list):
        normalized = [
            item.strip()
            for item in kb_id
            if isinstance(item, str) and item.strip()
        ]
        if normalized:
            return normalized[0] if len(normalized) == 1 else normalized
    return None


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


def _normalize_available_skills(skills: Any) -> list[str]:
    if skills is None:
        return []
    if isinstance(skills, str):
        skills = [skills]
    if not isinstance(skills, list):
        return []
    return [skill for skill in skills if isinstance(skill, str) and skill]


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
    kb_id = _normalize_kb_id_value(filters.get('kb_id'))
    if kb_id is not None:
        config['kb_id'] = kb_id
    else:
        config.pop('kb_id', None)

    _normalize_environment_context(config)


def _filter_tools_for_request(tools: list[str], config: dict) -> list[str]:
    if not config.get('use_memory', True):
        tools = [t for t in tools if t != 'memory']

    if config.get('kb_id'):
        return tools

    has_files = bool(config.get('files'))
    filtered = []
    for tool in tools:
        if not tool.startswith('kb_'):
            filtered.append(tool)
        elif has_files and tool == 'kb_search':
            filtered.append(tool)
    return filtered
