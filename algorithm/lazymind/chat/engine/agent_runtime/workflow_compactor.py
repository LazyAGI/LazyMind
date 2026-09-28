"""Overflow-only model-history projection for Workflow steps.

Workflow state and artifacts are durable outside the model transcript.  This
module therefore leaves ordinary Workflow calls untouched and only projects
older, completed tool rounds when the *complete* next request would overflow.
It never asks an LLM to summarize execution history.
"""
from __future__ import annotations

from typing import Any, Callable, Optional

from lazyllm.tools.agent import describe_tool_turns

from .budget import build_context_budget
from .compactors import (
    format_spilled_tool_notice,
    spill_tool_result_to_workspace,
    tool_result_utf8_size,
)
from .context_estimator import estimate_non_history_tokens
from .pruner import estimate_history_tokens


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
    except Exception:
        return None
    if not rel_path:
        return None
    notice = format_spilled_tool_notice(
        tool_name,
        content,
        rel_path,
        tool_result_utf8_size(content),
        workspace=workspace,
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
        # LazyLLM owns transcript pairing; LazyMind owns the overflow policy.
        layout = kwargs.get('tool_turns')
        if layout is None:
            layout = describe_tool_turns(prior, current)
        turns = [turn for turn in layout if not turn.current]
        projected = prior + current
        split = len(prior)
        dropped: set[int] = set()

        def parts():
            return (
                [message for index, message in enumerate(projected[:split]) if index not in dropped],
                projected[split:],
            )

        def fits():
            candidate_prior, candidate_current = parts()
            return _next_request_tokens(
                candidate_prior + candidate_current,
                prefix=prefix,
                current_input=current_input,
                reserved_runtime_context_tokens=reserved,
            ) <= budget.effective_input_budget

        # Keep recent turn structure, but allow every result body to be replaced
        # with a recoverable reference before removing any older turns.
        for turn in turns:
            for index in turn.result_indexes:
                replacement = _spill_old_tool_result(projected[index], workspace)
                if replacement is not None:
                    projected[index] = replacement
                    if fits():
                        return parts()

        # Only completed, unprotected prior turns may be removed.
        removable = turns[:-effective_keep] if effective_keep else turns
        for turn in removable:
            dropped.update(range(turn.start, turn.stop))
            if fits():
                return parts()

        # The current turn may span both lists. Keep its structure and project
        # only result bodies as the final fallback; never delete its messages.
        for turn in layout:
            if not turn.current:
                continue
            for index in turn.result_indexes:
                replacement = _spill_old_tool_result(projected[index], workspace)
                if replacement is not None:
                    projected[index] = replacement
                    if fits():
                        return parts()
        return parts()

    return _compact
