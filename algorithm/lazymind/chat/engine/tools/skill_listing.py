from __future__ import annotations

import json
from typing import Any

from lazyllm.tools import fc_register


DEFAULT_SEARCH_LIMIT = 4


def _normalize_skill_names(values: list[str] | None) -> list[str]:
    return list(dict.fromkeys(
        str(skill).strip() for skill in (values or []) if str(skill).strip()
    ))


def apply_prompt_skill_catalog(manager: Any, names: list[str] | None) -> None:
    """Limit SkillManager L1 catalog without shrinking get_skill / scripts.

    LazyLLM's SkillManager uses one skill list for both prompt injection and
    loading. Host chat needs a short catalog while still loading searchable
    skills. This wraps the instance prompt methods. ``None`` leaves the
    default catalog. An empty list injects no individual L1 entries.
    """
    if manager is None:
        return
    catalog = None if names is None else _normalize_skill_names(names)

    originals = getattr(manager, '_lazymind_prompt_catalog_orig', None)
    if originals is None:
        originals = {
            'visible': manager._visible_skill_keys,
            'format': manager._format_skills_list,
            'build': manager.build_prompt,
            'describe': manager.describe_prompt,
        }
        manager._lazymind_prompt_catalog_orig = originals

    def catalog_keys() -> list[str]:
        visible = originals['visible']()
        if catalog is None:
            return visible
        if not catalog:
            return []
        resolved: list[str] = []
        seen: set[str] = set()
        for ref in catalog:
            key, _error = manager._resolve_skill_ref(ref, visible)
            if key and key not in seen:
                seen.add(key)
                resolved.append(key)
        return resolved

    def compact_format(skill_names: list[str]) -> str:
        if catalog is None:
            return originals['format'](skill_names)
        lines = []
        for name in skill_names:
            info = manager._skills_index.get(name)
            if not info:
                continue
            desc = (info.get('description', '') or '')[:1024]
            lines.append(f'- {name}: {desc}')
        return '\n'.join(lines)

    def _with_catalog(fn):
        def wrapped(*args, **kwargs):
            manager._visible_skill_keys = catalog_keys
            manager._format_skills_list = compact_format
            try:
                return fn(*args, **kwargs)
            finally:
                manager._visible_skill_keys = originals['visible']
                manager._format_skills_list = originals['format']
        return wrapped

    manager.build_prompt = _with_catalog(originals['build'])
    manager.describe_prompt = _with_catalog(originals['describe'])


def compose_prompt_skills(
    injected: list[str] | None,
    searchable: list[str] | None,
    excluded: tuple[str, ...] | list[str] | None = None,
) -> tuple[list[str], list[str]]:
    """Split discovery catalog from loadable SkillManager scope.

    Prompt catalog is only the Host injection set. ``get_skill`` history must
    not grow that catalog; loading stays in tool results. Manager/loadable
    scope is the searchable authorized set.
    """
    denied = {str(item).strip() for item in (excluded or []) if str(item).strip()}
    injected_names = [name for name in _normalize_skill_names(injected) if name not in denied]
    searchable_names = [name for name in _normalize_skill_names(searchable) if name not in denied]
    if not searchable_names:
        searchable_names = list(injected_names)
    prompt_skills = list(injected_names)
    manager_skills = list(dict.fromkeys([*searchable_names, *prompt_skills]))
    return prompt_skills, manager_skills


def _active_skill_names_from_history(history: list[dict[str, Any]]) -> list[str]:
    activated: list[str] = []
    seen: set[str] = set()
    for message in history:
        for tool_call in message.get('tool_calls') or []:
            if not isinstance(tool_call, dict):
                continue
            function = tool_call.get('function')
            function = function if isinstance(function, dict) else tool_call
            if function.get('name') != 'get_skill':
                continue
            arguments = function.get('arguments', {})
            if isinstance(arguments, str):
                try:
                    arguments = json.loads(arguments)
                except json.JSONDecodeError:
                    continue
            if isinstance(arguments, dict) and isinstance(arguments.get('name'), str):
                name = arguments['name'].strip()
                if name and name not in seen:
                    seen.add(name)
                    activated.append(name)
    return activated


def build_list_skills_tool(available_skills: list[str] | None) -> Any:
    """Build a conversation-scoped tool that lists injected skill names."""
    skills = _normalize_skill_names(available_skills)

    @fc_register(host_file='NONE')
    def list_skills() -> dict[str, Any]:
        """List skills currently injected into this turn's catalog.

        This is the short injected set, not the full library. Use search_skill
        when the user rejects a listed skill or none of the listed skills match.
        Use get_skill when full instructions are needed.
        """
        return {'status': 'ok', 'count': len(skills), 'skills': skills}

    return list_skills


def _search_skill_catalog(
    request: dict[str, Any], searchable: set[str], excluded: set[str], limit: int,
) -> dict[str, Any]:
    from lazymind.chat.engine.tools.infra.core_api_client import post_core_api

    allowed = searchable - excluded
    if not allowed:
        return {'status': 'ok', 'count': 0, 'skills': []}
    request.update({'limit': limit, 'exclude': sorted(excluded), 'allowed_skill_keys': sorted(allowed)})
    try:
        payload = post_core_api('/internal/skills:search', request)
    except Exception as exc:
        return {'status': 'error', 'error': str(exc), 'skills': []}
    body = payload.get('response') or {}
    if isinstance(body, dict) and isinstance(body.get('data'), dict):
        body = body['data']
    raw_hits = body.get('skills') if isinstance(body, dict) else None
    hits, seen = [], set()
    for item in raw_hits or []:
        if not isinstance(item, dict):
            continue
        key = str(item.get('skill_key') or '').strip()
        if key not in allowed or key in seen:
            continue
        seen.add(key)
        hits.append({
            'skill_key': key,
            'name': str(item.get('name') or key.rsplit('/', 1)[-1]),
            'description': str(item.get('description') or '')[:1024],
        })
        if len(hits) >= limit:
            break
    return {'status': 'ok', 'count': len(hits), 'skills': hits}


def _search_limit(value: Any) -> int:
    try:
        return max(1, min(20, int(value)))
    except (TypeError, ValueError):
        return DEFAULT_SEARCH_LIMIT


def build_search_skills_tool(
    *,
    searchable_skills: list[str] | None,
    injected_skills: list[str] | None = None,
    excluded_skills: tuple[str, ...] | list[str] | None = None,
) -> Any:
    searchable = set(_normalize_skill_names(searchable_skills))
    excluded = set(_normalize_skill_names(excluded_skills))

    @fc_register(host_file='NONE')
    def search_skill(query: str, limit: int = DEFAULT_SEARCH_LIMIT) -> dict[str, Any]:
        """Search all skills available in this conversation by task or exact name.

        Includes skills already listed in the prompt. Returns candidate names and
        descriptions; call get_skill to load the chosen skill's full instructions.
        """
        cleaned = str(query or '').strip()
        if not cleaned:
            return {'status': 'error', 'error': 'query is required', 'skills': []}
        return _search_skill_catalog({'query': cleaned}, searchable, excluded, _search_limit(limit))

    return search_skill


def build_discover_skill_by_field_tool(
    *, searchable_skills: list[str] | None,
    excluded_skills: tuple[str, ...] | list[str] | None = None,
) -> Any:
    searchable = set(_normalize_skill_names(searchable_skills))
    excluded = set(_normalize_skill_names(excluded_skills))

    @fc_register(host_file='NONE')
    def discover_skill_by_field(field: str, value: str | list[str], limit: int = DEFAULT_SEARCH_LIMIT) -> dict[str, Any]:
        """Find available skills by an exact structured field and value.

        Fields: field (domain such as writing), tags, aliases, keywords, name.
        Use a string or an array of strings. Results contain candidate names and
        descriptions; call get_skill for the chosen skill's full instructions.
        """
        if field not in {'field', 'tags', 'aliases', 'keywords', 'name'}:
            return {'status': 'error', 'error': 'unsupported discovery field', 'skills': []}
        if not isinstance(value, (str, list)) or (
            isinstance(value, list) and any(not isinstance(item, str) for item in value)
        ):
            return {'status': 'error', 'error': 'value must be a string or string array', 'skills': []}
        values = [value] if isinstance(value, str) else value
        values = [item.strip() for item in values if item.strip()]
        if not values:
            return {'status': 'error', 'error': 'value is required', 'skills': []}
        return _search_skill_catalog({'field': field, 'value': values}, searchable, excluded, _search_limit(limit))

    return discover_skill_by_field


def append_loaded_skill_invocations(
    history: list[dict[str, Any]] | None,
    loaded: list[dict[str, Any]] | None,
    *,
    excluded: list[str] | None = None,
) -> list[dict[str, Any]]:
    """Append first-load @Skill bodies as get_skill tool history, not prompt L1."""
    messages = list(history or [])
    denied = {str(item).strip() for item in (excluded or []) if str(item).strip()}
    present = set(_active_skill_names_from_history(messages))
    for item in loaded or []:
        key = str(item.get('skill_key') or '').strip()
        content = str(item.get('content') or '')
        if not key or key in denied or not content.strip():
            continue
        basename = key.rsplit('/', 1)[-1]
        if key in present or basename in present:
            continue
        call_id = f'skill-invoke-{key}'
        messages.append({
            'role': 'assistant',
            'content': '',
            'tool_calls': [{
                'id': call_id,
                'type': 'function',
                'function': {
                    'name': 'get_skill',
                    'arguments': json.dumps({'name': key}, ensure_ascii=False),
                },
            }],
        })
        messages.append({
            'role': 'tool',
            'tool_call_id': call_id,
            'name': 'get_skill',
            'content': json.dumps({
                'status': 'ok',
                'name': key,
                'revision_id': str(item.get('revision_id') or ''),
                'content': content,
            }, ensure_ascii=False),
        })
        present.add(key)
        present.add(basename)
    return messages
