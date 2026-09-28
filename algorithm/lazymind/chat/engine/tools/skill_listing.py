from __future__ import annotations

import ast
import json
import uuid
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


def _loaded_skill_bodies(history: list[dict[str, Any]]) -> set[tuple[str, str, str]]:
    """Only a paired successful tool result proves that L2 reached the model."""
    calls: dict[str, str] = {}
    loaded: set[tuple[str, str, str]] = set()
    for message in history:
        for tool_call in (message.get('tool_calls') or []) if message.get('role') == 'assistant' else []:
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
                if name and tool_call.get('id'):
                    calls[tool_call['id']] = name
        if message.get('role') != 'tool' or message.get('tool_call_id') not in calls:
            continue
        payload = message.get('content')
        if isinstance(payload, str):
            try:
                payload = json.loads(payload)
            except (ValueError, TypeError):
                try:
                    payload = ast.literal_eval(payload)
                except (ValueError, SyntaxError):
                    continue
        if (isinstance(payload, dict) and payload.get('ok') is True
                and isinstance(payload.get('value'), dict)):
            payload = payload['value']
        if not isinstance(payload, dict) or payload.get('status') != 'ok':
            continue
        content = payload.get('content')
        if not isinstance(content, str) or not content.strip():
            continue
        requested_name = calls[message['tool_call_id']]
        result_name = payload.get('name')
        if result_name is not None and (
                not isinstance(result_name, str) or result_name.strip() != requested_name):
            continue
        name = requested_name
        loaded.add((name, str(payload.get('revision_id') or ''), content))
    return loaded


def append_loaded_skill_invocations(
    history: list[dict[str, Any]] | None,
    loaded: list[dict[str, Any]] | None,
    *,
    excluded: list[str] | None = None,
) -> list[dict[str, Any]]:
    """Append first-load @Skill bodies as get_skill tool history, not prompt L1."""
    messages = list(history or [])
    denied = {str(item).strip() for item in (excluded or []) if str(item).strip()}
    present = _loaded_skill_bodies(messages)
    for item in loaded or []:
        key = str(item.get('skill_key') or '').strip()
        content = str(item.get('content') or '')
        if not key or key in denied or not content.strip():
            continue
        revision = str(item.get('revision_id') or '')
        identity = (key, revision, content)
        if identity in present:
            continue
        call_id = f'skill-invoke-{uuid.uuid4().hex}'
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
                'revision_id': revision,
                'content': content,
            }, ensure_ascii=False),
        })
        present.add(identity)
    return messages


def restore_loaded_skill_runtime(manager: Any, history: list[dict[str, Any]] | None) -> None:
    """Rehydrate resource declarations, not just synthetic model-facing L2 history.

    Use the public loader so visibility, exclusions, size limits and resource
    discovery remain authoritative; never seed the private cache from history.
    This only reads authorized Skills and does not run their scripts.
    """
    if manager is None:
        return
    for name in sorted({item[0] for item in _loaded_skill_bodies(history or [])}):
        try:
            manager.get_skill(name)
        except Exception as exc:
            # A stale/deleted Skill must not prevent the conversation from starting.
            # The normal get_skill tool retains the detailed error for a retry.
            import lazyllm
            lazyllm.LOG.warning(f'[SkillRuntime] history reload failed: {type(exc).__name__}')


__all__ = [
    'append_loaded_skill_invocations',
    'compose_prompt_skills',
    'core_skill_search',
    'restore_loaded_skill_runtime',
]
