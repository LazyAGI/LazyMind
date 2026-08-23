from __future__ import annotations

import copy
import hashlib
import json
from typing import Any, Optional

from .context_estimator import estimate_tokens
from .message_fields import model_facing_message


PROJECTION_STATE_VERSION = 1


def fingerprint_message(message: dict[str, Any]) -> str:
    payload = json.dumps(
        model_facing_message(message),
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
        default=str,
    )
    return hashlib.sha256(payload.encode('utf-8', errors='replace')).hexdigest()


def message_tokens(message: dict[str, Any]) -> int:
    return estimate_tokens(json.dumps(model_facing_message(message), ensure_ascii=False, default=str))


def projection_tokens(entries: list[dict[str, Any]]) -> int:
    return sum(message_tokens(entry['message']) for entry in entries)


def _full_entry(
    message: dict[str, Any],
    source_index: int,
    *,
    model_visible: bool = False,
) -> dict[str, Any]:
    return {
        'source_start': source_index,
        'source_end': source_index + 1,
        'message': copy.deepcopy(message),
        'kind': 'full',
        'model_visible': model_visible,
    }


def _rebuild_state(
    history: list[dict[str, Any]],
    fingerprints: list[str],
    state: dict[str, Any],
    reason: str,
) -> None:
    state.clear()
    state.update({
        'version': PROJECTION_STATE_VERSION,
        'source_fingerprints': fingerprints,
        'entries': [
            _full_entry(message, index, model_visible=True)
            for index, message in enumerate(history)
        ],
        'last_sent_fingerprint': '',
        'next_pressure_tokens': 0,
        'last_reconcile_reason': reason,
    })


def reconcile_projection(
    history: list[dict[str, Any]],
    runtime_state: Optional[dict[str, Any]],
) -> tuple[dict[str, Any], bool]:
    state = runtime_state if runtime_state is not None else {}
    fingerprints = [fingerprint_message(message) for message in history]
    recorded = state.get('source_fingerprints')
    valid = (
        state.get('version') == PROJECTION_STATE_VERSION
        and isinstance(recorded, list)
        and isinstance(state.get('entries'), list)
        and len(fingerprints) >= len(recorded)
        and fingerprints[:len(recorded)] == recorded
    )
    if not valid:
        reason = 'initialized' if not state else (
            'source_shortened' if isinstance(recorded, list) and len(fingerprints) < len(recorded)
            else 'source_prefix_mismatch'
        )
        _rebuild_state(history, fingerprints, state, reason)
        return state, True

    start = len(recorded)
    for index in range(start, len(history)):
        state['entries'].append(_full_entry(history[index], index))
    state['source_fingerprints'] = fingerprints
    state['last_reconcile_reason'] = 'appended' if start < len(history) else 'unchanged'
    return state, False


def clone_entries(entries: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return copy.deepcopy(entries)


def render_projection(entries: list[dict[str, Any]]) -> list[dict[str, Any]]:
    rendered: list[dict[str, Any]] = []
    for entry in entries:
        message = entry['message']
        model_message = model_facing_message(message)
        if len(model_message) == len(message):
            rendered.append(copy.deepcopy(message))
        else:
            rendered.append(copy.deepcopy(model_message))
    return rendered


def projection_fingerprint(entries: list[dict[str, Any]]) -> str:
    payload = json.dumps(
        render_projection(entries),
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
        default=str,
    )
    return hashlib.sha256(payload.encode('utf-8', errors='replace')).hexdigest()


def transition_metrics(
    previous: list[dict[str, Any]],
    candidate: list[dict[str, Any]],
    *,
    before_total: int,
    after_total: int,
) -> dict[str, Any]:
    limit = min(len(previous), len(candidate))
    first_changed: Optional[int] = None
    for index in range(limit):
        old = previous[index]
        new = candidate[index]
        if (
            old.get('source_start') != new.get('source_start')
            or old.get('source_end') != new.get('source_end')
            or old.get('kind') != new.get('kind')
            or old.get('message') != new.get('message')
        ):
            first_changed = index
            break
    if first_changed is None and len(previous) != len(candidate):
        first_changed = limit

    changed_visible: list[bool] = []
    cache_disruption = 0
    if first_changed is not None:
        changed_visible = [
            bool(entry.get('model_visible'))
            for entry in previous[first_changed:]
        ]
        cache_disruption = sum(
            message_tokens(entry['message'])
            for entry in previous[first_changed:]
            if entry.get('model_visible')
        )
    return {
        'reclaimed_tokens': max(0, before_total - after_total),
        'first_changed_projection_index': first_changed,
        'cache_disruption_tokens': cache_disruption,
        'changed_messages': max(
            len(previous) - (first_changed or 0),
            len(candidate) - (first_changed or 0),
        ) if first_changed is not None else 0,
        'changed_model_visible': changed_visible,
        'estimated_before': before_total,
        'estimated_after': after_total,
    }


def commit_entries(
    state: dict[str, Any],
    entries: list[dict[str, Any]],
    *,
    next_pressure_tokens: Optional[int] = None,
) -> None:
    state['entries'] = entries
    if next_pressure_tokens is not None:
        state['next_pressure_tokens'] = max(0, int(next_pressure_tokens))


def mark_projection_sent(state: dict[str, Any]) -> None:
    entries = state.get('entries') or []
    for entry in entries:
        entry['model_visible'] = True
    state['last_sent_fingerprint'] = projection_fingerprint(entries)
