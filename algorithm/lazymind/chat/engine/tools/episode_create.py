from __future__ import annotations

from typing import Any

import lazyllm
from pydantic import ValidationError

from lazymind.chat.engine.tools.infra import tool_error, tool_success
from lazymind.memory import EpisodeCreateInput, get_episode_store


def episode_create(items: list[dict[str, Any]]) -> dict[str, Any]:
    """Persist one or more immutable historical Episode snapshots.

    ChatAgent may call this only when the user explicitly asks to record,
    remember, or save a historical event.  It is also used by Memory Review
    for durable decisions, progress, results, blockers, and events.

    Args:
        items: Episode objects. Each item includes summary, episode_type,
            thread_key, occurred_at_ms, and source. episode_type must be one of
            decision, progress, result, blocker, or event.
    """
    config = lazyllm.globals.get('agentic_config', {}) or {}
    user_id = str(config.get('user_id') or '').strip()
    if not user_id:
        return tool_error('episode_create', 'user_id is required')
    if not isinstance(items, list) or not items:
        return tool_error('episode_create', 'items must be a non-empty list')
    parsed, preflight = [], []
    task_id = str(config.get('task_id') or config.get('session_id') or '')
    kind = 'memory_review' if task_id.startswith('memory_review_') else 'chat_explicit'
    for index, raw in enumerate(items):
        try:
            value = dict(raw)
            value.setdefault('thread_key', config.get('conversation_id') or task_id or 'chat')
            source = dict(value.get('source') or {})
            source.setdefault('kind', kind)
            source.setdefault('task_id', task_id or None)
            source.setdefault('conversation_id', config.get('conversation_id'))
            value['source'] = source
            parsed.append(EpisodeCreateInput.model_validate(value))
            preflight.append(None)
        except (TypeError, ValidationError, ValueError) as exc:
            parsed.append(None)
            preflight.append({'status': 'rejected', 'id': None, 'reason': f'item {index}: {exc}'})
    valid = [item for item in parsed if item is not None]
    created = iter(get_episode_store().create_many(user_id, valid))
    results = [next(created).model_dump() if error is None else error for error in preflight]
    return tool_success('episode_create', {'items': results})
