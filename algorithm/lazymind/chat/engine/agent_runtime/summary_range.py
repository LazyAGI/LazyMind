from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, Optional

from .context_estimator import estimate_tokens

RUNTIME_SUMMARY_KIND = 'runtime_summary'
RUNTIME_SUMMARY_DISCLAIMER_PREFIX = (
    'The following is a runtime-generated summary of earlier conversation history.'
)


def _message_tokens(message: dict[str, Any]) -> int:
    return estimate_tokens(json.dumps(message, ensure_ascii=False, default=str))


def is_runtime_summary_message(message: dict[str, Any]) -> bool:
    """Detect a projected runtime summary message via meta or content prefix."""
    meta = message.get('_lazymind_meta')
    if isinstance(meta, dict) and meta.get('kind') == RUNTIME_SUMMARY_KIND:
        return True
    if message.get('role') != 'user':
        return False
    content = message.get('content')
    if isinstance(content, str):
        return content.startswith(RUNTIME_SUMMARY_DISCLAIMER_PREFIX)
    return False


def extract_summary_markdown(message: dict[str, Any]) -> str:
    """Return summary body without the disclaimer prefix when present."""
    content = message.get('content')
    if not isinstance(content, str):
        return ''
    if not content.startswith(RUNTIME_SUMMARY_DISCLAIMER_PREFIX):
        return content.strip()
    parts = content.split('\n\n', 1)
    if len(parts) == 2:
        return parts[1].strip()
    return content.strip()


def _tool_call_ids(message: dict[str, Any]) -> list[str]:
    tool_calls = message.get('tool_calls')
    if not isinstance(tool_calls, list):
        return []
    ids: list[str] = []
    for call in tool_calls:
        if not isinstance(call, dict):
            continue
        call_id = call.get('id')
        if call_id:
            ids.append(str(call_id))
    return ids


def validate_tool_pairing(history: list[dict[str, Any]]) -> tuple[bool, str]:
    """Return whether assistant tool_calls and tool results are paired legally.

    Unresolved tool_calls at the very end are allowed (in-flight mid-turn).
    """
    pending: set[str] = set()
    for index, message in enumerate(history):
        role = message.get('role')
        if role == 'assistant':
            call_ids = _tool_call_ids(message)
            if call_ids and pending:
                return False, f'unresolved_tool_calls_before_assistant_at_{index}'
            pending = set(call_ids)
            continue
        if role == 'tool':
            tool_call_id = message.get('tool_call_id')
            if not tool_call_id:
                return False, f'missing_tool_call_id_at_{index}'
            tool_call_id = str(tool_call_id)
            if tool_call_id not in pending:
                return False, f'orphan_tool_result_at_{index}'
            pending.discard(tool_call_id)
            continue
        if pending:
            return False, f'unresolved_tool_calls_before_role_{role}_at_{index}'
    return True, 'ok'


@dataclass(frozen=True)
class SummaryRange:
    """Indices into history for summary / tail projection.

    - summary_messages: messages to feed the summarizer (may include prior summary)
    - replace_start / replace_end: half-open slice replaced by the new summary message
    - tail: messages kept verbatim after the summary
    """

    summary_messages: list[dict[str, Any]]
    replace_start: int
    replace_end: int
    tail: list[dict[str, Any]]
    prior_summary_index: Optional[int]
    tail_tokens: int
    summary_input_tokens: int


def _user_turn_starts(history: list[dict[str, Any]]) -> list[int]:
    starts: list[int] = []
    for index, message in enumerate(history):
        if message.get('role') != 'user':
            continue
        if is_runtime_summary_message(message):
            continue
        starts.append(index)
    return starts


def _select_tail_start_by_turns(
    history: list[dict[str, Any]],
    *,
    token_cap: int,
    min_recent_user_turns: int,
) -> Optional[int]:
    """Choose Tail start index on a user-turn boundary.

    Always keeps the last ``min_recent_user_turns`` user turns. Then expands
    Tail leftward one full turn at a time while under ``token_cap``.
    """
    starts = _user_turn_starts(history)
    if not starts:
        # No user turns: keep trailing messages by token budget only.
        tail_start = len(history)
        tokens = 0
        for index in range(len(history) - 1, -1, -1):
            next_tokens = tokens + _message_tokens(history[index])
            if tokens > 0 and next_tokens > token_cap:
                break
            tokens = next_tokens
            tail_start = index
        return None if tail_start <= 0 else tail_start

    min_turns = max(1, min(3, int(min_recent_user_turns)))
    protected_idx = max(0, len(starts) - min_turns)
    tail_start = starts[protected_idx]
    tokens = sum(_message_tokens(message) for message in history[tail_start:])

    for turn_idx in range(protected_idx - 1, -1, -1):
        candidate = starts[turn_idx]
        turn_tokens = sum(
            _message_tokens(message) for message in history[candidate:tail_start]
        )
        if tokens + turn_tokens > token_cap:
            break
        tokens += turn_tokens
        tail_start = candidate

    return None if tail_start <= 0 else tail_start


def select_summary_range(
    history: list[dict[str, Any]],
    *,
    effective_input_budget: int,
    keep_recent_ratio: float,
    min_recent_user_turns: int,
) -> Optional[SummaryRange]:
    """Choose old-history summary span and recent Tail.

    Returns None when there is nothing useful to summarize (empty head, or
    Tail already covers the entire history).
    """
    if not history:
        return None

    ratio = max(0.0, float(keep_recent_ratio))
    budget = max(0, int(effective_input_budget))
    token_cap = max(1, int(budget * ratio)) if budget and ratio > 0 else 1

    tail_start = _select_tail_start_by_turns(
        history,
        token_cap=token_cap,
        min_recent_user_turns=min_recent_user_turns,
    )
    if tail_start is None or tail_start <= 0 or tail_start >= len(history):
        return None

    prior_summary_index: Optional[int] = None
    for index, message in enumerate(history[:tail_start]):
        if is_runtime_summary_message(message):
            prior_summary_index = index

    if prior_summary_index is not None:
        replace_start = prior_summary_index
        summary_messages = list(history[prior_summary_index:tail_start])
    else:
        replace_start = 0
        summary_messages = list(history[:tail_start])

    if not summary_messages:
        return None

    # Nothing new beyond an existing summary — skip.
    if (
        prior_summary_index is not None
        and len(summary_messages) == 1
        and is_runtime_summary_message(summary_messages[0])
    ):
        return None

    tail = list(history[tail_start:])
    return SummaryRange(
        summary_messages=summary_messages,
        replace_start=replace_start,
        replace_end=tail_start,
        tail=tail,
        prior_summary_index=prior_summary_index,
        tail_tokens=sum(_message_tokens(m) for m in tail),
        summary_input_tokens=sum(_message_tokens(m) for m in summary_messages),
    )
