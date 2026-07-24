from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any

from evo.artifact_flow import FlowSnapshot
from evo.message_intent import MessageHistoryResponse, MessageTurnResult


_SECRET_KEYS = frozenset({
    'api_key', 'apikey', 'authorization', 'password', 'secret',
    'access_token', 'refresh_token', 'bearer_token',
})
_PRIVATE_URL_KEYS = frozenset({
    'base_url', 'router_chat_url', 'router_admin_url', 'service_url',
})
_QUOTED_SECRET = re.compile(
    r"(?i)(['\"]?(?:api[_-]?key|authorization|password|secret|access_token)"
    r"['\"]?\s*[:=]\s*)['\"][^'\"]*['\"]"
)
_BEARER = re.compile(r'(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+')


def public_value(value: object, *, key: str = '') -> object:
    normalized = key.lower()
    if normalized in _SECRET_KEYS:
        return '<redacted>'
    if normalized in _PRIVATE_URL_KEYS:
        return '<redacted-url>'
    if isinstance(value, Mapping):
        return {
            str(name): public_value(item, key=str(name))
            for name, item in value.items()
        }
    if isinstance(value, (list, tuple)):
        return [public_value(item) for item in value]
    if isinstance(value, str):
        if value.startswith(('file://', '/')):
            return '<redacted-path>'
        return public_text(value)
    return value


def public_text(value: str) -> str:
    text = _QUOTED_SECRET.sub(lambda match: f"{match.group(1)}'<redacted>'", value)
    return _BEARER.sub('<redacted>', text)


def public_thread_state(snapshot: FlowSnapshot) -> dict[str, Any]:
    pending = snapshot.pending_approval
    first_incomplete = next(
        (stage for stage in snapshot.stages if stage.status != 'completed'),
        None,
    )
    released = [
        stage.stage
        for stage in snapshot.stages
        if stage.approval_key is not None and stage.status == 'completed'
    ]
    error = snapshot.runtime.error
    result = {
        'status': {
            'awaiting_approval': 'paused',
            'pausing': 'running',
            'cancelling': 'running',
            'completed': 'ended',
        }.get(snapshot.status, snapshot.status),
        'current_step': snapshot.current_stage,
        'checkpoint_state': (
            'pending' if pending is not None else 'valid' if released else 'none'
        ),
        'first_missing_step': '' if first_incomplete is None else first_incomplete.stage,
        'last_released_step': released[-1] if released else '',
        'retry_from_step': '' if first_incomplete is None else first_incomplete.stage,
        'last_error': '' if error is None else public_text(error.message),
    }
    if pending is None or pending.result_ref is None:
        return result

    index = snapshot.stages.index(pending)
    ref = pending.result_ref
    result['pending_checkpoint'] = {
        'checkpoint_id': f'{pending.stage}:v{ref.version}',
        'completed_stage': pending.stage,
        'next_stage': (
            snapshot.stages[index + 1].stage
            if index + 1 < len(snapshot.stages)
            else ''
        ),
        'step': pending.stage,
        'artifact_id': ref.key.artifact_id,
        'version': ref.version,
        'ref': f'{ref.key.artifact_id}@v{ref.version}',
    }
    return result


def public_message_result(result: MessageTurnResult | Mapping[str, Any]
                          ) -> dict[str, Any]:
    data = result.model_dump() if isinstance(result, MessageTurnResult) else dict(result)
    data['turn_decision'] = {
        'needs_confirmation': 'needs_approval',
        'action_executed': 'action_submitted',
    }.get(data.get('turn_decision'), data.get('turn_decision'))
    data['pending_approval_ref'] = data.pop('pending_confirmation_ref', None)
    return public_value(data)


def public_message_history(
    history: MessageHistoryResponse | Mapping[str, Any],
) -> dict[str, Any]:
    data = history.model_dump() if isinstance(history, MessageHistoryResponse) else dict(history)
    data['items'] = [public_message_result(item) for item in data.get('items', ())]
    return public_value(data)


__all__ = [
    'public_message_history', 'public_message_result', 'public_text',
    'public_thread_state', 'public_value',
]
