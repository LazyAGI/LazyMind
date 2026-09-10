from __future__ import annotations

from typing import Any
from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.summary_range import (
    is_runtime_summary_message,
    validate_tool_pairing,
)
from lazymind.chat.engine.agent_runtime.summarizer import apply_summary_compression
from lazymind.config import config


VALID_SUMMARY = '\n'.join(
    [
        '## Current task',
        'Ship summary compression.',
        '## Key constraints',
        'Do not delete original history.',
        '## Progress and decisions',
        'Stage1 prune done; Stage2 pending.',
        '## Important files and tool results',
        'Touched summarizer.py; run_script exit 0.',
        '## Pending work',
        'Wire tests and lint.',
    ]
)


def _long(text: str, times: int = 80) -> str:
    return (text + '\n') * times


def _history_with_turns() -> list[dict[str, Any]]:
    return [
        {'role': 'user', 'content': _long('old goal turn1')},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': 'c1', 'function': {'name': 'run_script'}}],
        },
        {
            'role': 'tool',
            'name': 'run_script',
            'tool_call_id': 'c1',
            'content': _long('old tool result one'),
        },
        {'role': 'assistant', 'content': _long('old assistant reply turn1')},
        {'role': 'user', 'content': _long('mid goal turn2')},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': 'c2', 'function': {'name': 'run_script'}}],
        },
        {
            'role': 'tool',
            'name': 'run_script',
            'tool_call_id': 'c2',
            'content': _long('mid tool result two'),
        },
        {'role': 'assistant', 'content': _long('mid assistant reply turn2')},
        {'role': 'user', 'content': 'latest user request keep me'},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': 'c3', 'function': {'name': 'run_script'}}],
        },
        {
            'role': 'tool',
            'name': 'run_script',
            'tool_call_id': 'c3',
            'content': 'latest tool keep me',
        },
    ]


def test_apply_summary_projection_is_immutable_and_commits() -> None:
    history = _history_with_turns()
    original = [dict(item) for item in history]
    budget = build_context_budget(4_000, reserved_output_tokens=0, target_ratio=0.01)

    def summarizer(system_prompt: str, user_prompt: str) -> str:
        assert 'runtime summary' in system_prompt
        assert 'old goal turn1' in user_prompt or 'old tool' in user_prompt
        return VALID_SUMMARY

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_summary_keep_recent_ratio', 0.10), \
            config.temp('context_summary_min_recent_user_turns', 1):
        projected, event = apply_summary_compression(
            history,
            budget=budget,
            trigger='pre_turn',
            summarizer=summarizer,
        )
    assert validate_tool_pairing(projected)
    assert history == original
    assert event.decision == 'summarized'
    assert is_runtime_summary_message(projected[0])
    assert projected[-1]['content'] == 'latest tool keep me'
    assert any(m.get('content') == 'latest user request keep me' for m in projected)


def test_apply_summary_abandons_on_missing_sections() -> None:
    history = _history_with_turns()
    original = [dict(item) for item in history]
    budget = build_context_budget(4_000, reserved_output_tokens=0, target_ratio=0.01)

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_summary_keep_recent_ratio', 0.10), \
            config.temp('context_summary_min_recent_user_turns', 1):
        projected, event = apply_summary_compression(
            history,
            budget=budget,
            trigger='pre_turn',
            summarizer=lambda _s, _u: '## Current task\nbroken',
        )
    assert event.decision == 'abandoned'
    assert event.reason == 'missing_required_sections'
    assert projected == original
