from __future__ import annotations

import json
from typing import Any


def _normalize_skill_names(values: list[str] | None) -> list[str]:
    return list(dict.fromkeys(
        str(skill).strip() for skill in (values or []) if str(skill).strip()
    ))


def compose_prompt_skills(
    injected: list[str] | None,
    searchable: list[str] | None,
    excluded: tuple[str, ...] | list[str] | None = None,
) -> tuple[list[str], list[str]]:
    """Map Host policy onto LazyLLM SkillManager ``prompt_skills`` vs ``skills``.

    Prompt catalog is only the Host injection set. ``get_skill`` history must
    not grow that catalog; loading stays in tool results. Manager/loadable
    scope is the searchable authorized set passed as ``skills=``.
    """
    denied = {str(item).strip() for item in (excluded or []) if str(item).strip()}
    injected_names = [name for name in _normalize_skill_names(injected) if name not in denied]
    searchable_names = [name for name in _normalize_skill_names(searchable) if name not in denied]
    if not searchable_names:
        searchable_names = list(injected_names)
    prompt_skills = list(injected_names)
    manager_skills = list(dict.fromkeys([*searchable_names, *prompt_skills]))
    return prompt_skills, manager_skills


def core_skill_search(request: dict[str, Any]) -> dict[str, Any]:
    """Skill catalog search backend for LazyLLM SkillManager."""
    from lazymind.chat.engine.tools.infra.core_api_client import post_core_api

    try:
        payload = post_core_api('/internal/skills:search', request)
    except Exception as exc:
        return {'status': 'error', 'error': str(exc), 'skills': []}
    body = payload.get('response') or {}
    if isinstance(body, dict) and isinstance(body.get('data'), dict):
        body = body['data']
    skills = body.get('skills') if isinstance(body, dict) else None
    if not isinstance(skills, list):
        skills = []
    return {'status': 'ok', 'skills': skills}


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


__all__ = [
    'append_loaded_skill_invocations',
    'compose_prompt_skills',
    'core_skill_search',
]
