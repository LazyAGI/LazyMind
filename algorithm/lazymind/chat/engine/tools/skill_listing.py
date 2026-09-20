from __future__ import annotations

from lazyllm.tools import fc_register

from typing import Any


DEFAULT_SEARCH_LIMIT = 4


def _normalize_skill_names(values: list[str] | None) -> list[str]:
    return list(dict.fromkeys(
        str(skill).strip() for skill in (values or []) if str(skill).strip()
    ))


def compose_prompt_skills(
    injected: list[str] | None,
    searchable: list[str] | None,
    history: list[dict[str, Any]] | None,
    excluded: tuple[str, ...] | list[str] | None = None,
) -> tuple[list[str], list[str]]:
    denied = {str(item).strip() for item in (excluded or []) if str(item).strip()}
    injected_names = [name for name in _normalize_skill_names(injected) if name not in denied]
    searchable_names = [name for name in _normalize_skill_names(searchable) if name not in denied]
    if not searchable_names:
        searchable_names = list(injected_names)
    allowed = set(searchable_names)
    from_history = []
    for name in _active_skill_names_from_history(history or []):
        if name in denied:
            continue
        if name in allowed:
            from_history.append(name)
            continue
        # Older tool records may contain a bare name. Resolve it only when the
        # current authorized catalog has exactly one matching identity.
        matches = [key for key in searchable_names if key.rsplit('/', 1)[-1] == name]
        if len(matches) == 1:
            from_history.append(matches[0])
    prompt_skills = list(dict.fromkeys([*from_history, *injected_names]))
    manager_skills = list(dict.fromkeys([*searchable_names, *prompt_skills]))
    return prompt_skills, manager_skills


def _active_skill_names_from_history(history: list[dict[str, Any]]) -> list[str]:
    import json

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


def render_loaded_skills(
    loaded: list[dict[str, Any]], available: list[str], *, excluded: list[str] | None = None,
) -> str:
    allowed = set(available) - set(excluded or [])
    sections, seen = [], set()
    for item in loaded:
        key = str(item.get('skill_key') or '')
        content = str(item.get('content') or '')
        if key not in allowed or key in seen or not content.strip():
            continue
        seen.add(key)
        revision = str(item.get('revision_id') or '')
        sections.append(f'### Loaded Skill: {key} (revision: {revision})\n\n{content}')
    if not sections:
        return ''
    return (
        'The user explicitly selected these skills. Their complete L2 instructions are already loaded. '
        'Follow the relevant instructions; get_skill is not required again before reading references '
        'or running scripts named in this content.\n\n' + '\n\n'.join(sections)
    )
