"""Overflow-only model-history projection for Workflow steps.

Workflow state and artifacts are durable outside the model transcript.  This
module therefore leaves ordinary Workflow calls untouched and only projects
older, completed tool rounds when the *complete* next request would overflow.
It never asks an LLM to summarize execution history.
"""
from __future__ import annotations

from typing import Any, Callable, Optional

from .budget import build_context_budget
from .compactors import (
    format_spilled_tool_notice,
    spill_tool_result_to_workspace,
    tool_result_utf8_size,
)
from .context_estimator import estimate_non_history_tokens
from .pruner import estimate_history_tokens


def _completed_tool_turns(history: list[dict[str, Any]]) -> list[tuple[int, int, set[int]]]:
    """Return complete assistant-tool-call ranges and their result indexes.

    An incomplete or malformed turn is intentionally not considered removable:
    preserving its messages is safer than creating an orphan provider payload.
    """
    turns: list[tuple[int, int, set[int]]] = []
    start: Optional[int] = None
    pending_ids: set[str] = set()
    result_indexes: set[int] = set()

    for index, message in enumerate(history):
        role = str(message.get('role') or '')
        if role == 'assistant':
            if start is not None:
                # Another assistant message before all results means the prior
                # turn is not safe to remove as a paired unit.
                start = None
                pending_ids = set()
                result_indexes = set()
            call_ids = {
                str(call.get('id') or '')
                for call in (message.get('tool_calls') or [])
                if isinstance(call, dict) and str(call.get('id') or '')
            }
            if call_ids:
                start = index
                pending_ids = call_ids
                result_indexes = set()
            continue

        if role != 'tool' or start is None:
            continue
        call_id = str(message.get('tool_call_id') or '')
        if call_id not in pending_ids:
            continue
        result_indexes.add(index)
        pending_ids.remove(call_id)
        if not pending_ids:
            turns.append((start, index, result_indexes))
            start = None
            result_indexes = set()

    return turns


def _next_request_tokens(
    history: list[dict[str, Any]],
    *,
    prefix: dict[str, Any],
    current_input: Any,
    reserved_runtime_context_tokens: Any,
) -> int:
    return (
        estimate_non_history_tokens(prefix, current_input)
        + max(0, int(reserved_runtime_context_tokens or 0))
        + estimate_history_tokens(history)
    )


def _spill_old_tool_result(message: dict[str, Any], workspace: Optional[str]) -> Optional[dict[str, Any]]:
    """Return a smaller file-reference message, or None when no safe projection exists."""
    if not workspace:
        return None
    content = message.get('content')
    tool_name = str(message.get('name') or '')
    try:
        rel_path = spill_tool_result_to_workspace(workspace, tool_name, content)
    except OSError:
        return None
    if not rel_path:
        return None
    notice = format_spilled_tool_notice(
        tool_name,
        content,
        rel_path,
        tool_result_utf8_size(content),
    )
    projected = dict(message, content=notice)
    return projected if estimate_history_tokens([projected]) < estimate_history_tokens([message]) else None


def make_workflow_history_compactor(
    *,
    max_input_tokens: Any = None,
    llm_config: Optional[dict[str, Any]] = None,
    keep_recent: int = 2,
    workspace: Optional[str] = None,
) -> Callable[..., tuple[list[dict[str, Any]], list[dict[str, Any]]]]:
    """Build an overflow-only, deterministic history compactor for Workflow steps."""
    budget = build_context_budget(max_input_tokens, llm_config=llm_config)
    default_keep = max(0, int(keep_recent))

    def _compact(
        history: list[dict[str, Any]],
        keep_full_turns: Optional[int] = None,
        **kwargs: Any,
    ) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
        prior = list(history)
        current = list(kwargs.get('current_round_messages') or [])
        prefix = kwargs.get('prefix') or {}
        current_input = kwargs.get('current_input')
        reserved = kwargs.get('reserved_runtime_context_tokens')
        total = _next_request_tokens(
            prior + current,
            prefix=prefix,
            current_input=current_input,
            reserved_runtime_context_tokens=reserved,
        )
        if total <= budget.effective_input_budget:
            return prior, current

        effective_keep = max(
            0,
            int(default_keep if keep_full_turns is None else keep_full_turns),
        )
        turns = _completed_tool_turns(prior)
        protected_turns = turns[-effective_keep:] if effective_keep else []
        protected_results = {
            result_index
            for _start, _end, result_indexes in protected_turns
            for result_index in result_indexes
        }

        # First, preserve every old call/result pair and replace only tool-result
        # bodies with workspace references, stopping as soon as the request fits.
        projected = list(prior)
        for _start, _end, result_indexes in turns:
            for result_index in sorted(result_indexes):
                if result_index in protected_results:
                    continue
                replacement = _spill_old_tool_result(projected[result_index], workspace)
                if replacement is None:
                    continue
                projected[result_index] = replacement
                total = _next_request_tokens(
                    projected + current,
                    prefix=prefix,
                    current_input=current_input,
                    reserved_runtime_context_tokens=reserved,
                )
                if total <= budget.effective_input_budget:
                    return projected, current

        # If references cannot recover enough budget, drop only the oldest
        # complete rounds. Current-round messages and the protected tail remain.
        removable = turns[:-effective_keep] if effective_keep else turns
        dropped: set[int] = set()
        for start, end, _result_indexes in removable:
            dropped.update(range(start, end + 1))
            candidate = [message for index, message in enumerate(projected) if index not in dropped]
            total = _next_request_tokens(
                candidate + current,
                prefix=prefix,
                current_input=current_input,
                reserved_runtime_context_tokens=reserved,
            )
            if total <= budget.effective_input_budget:
                return candidate, current

        return [message for index, message in enumerate(projected) if index not in dropped], current

    return _compact
