from __future__ import annotations

from collections import Counter
from typing import Any

from lazyllm.tools.agent.functionCall import FunctionCall

from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor
from lazymind.chat.engine.agent_runtime.summary_range import is_runtime_summary_message
from lazymind.config import config


VALID_SUMMARY = '\n'.join([
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
])


def _long(text: str, times: int = 80) -> str:
    return (text + '\n') * times


def _prior_with_open_tool_calls() -> list[dict[str, Any]]:
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
            'tool_calls': [
                {'id': 'c3a', 'function': {'name': 'run_script'}},
                {'id': 'c3b', 'function': {'name': 'run_script'}},
            ],
        },
    ]


def _current_tools() -> list[dict[str, Any]]:
    return [
        {
            'role': 'tool',
            'name': 'run_script',
            'tool_call_id': 'c3a',
            'content': 'latest tool a keep me',
        },
        {
            'role': 'tool',
            'name': 'run_script',
            'tool_call_id': 'c3b',
            'content': 'latest tool b keep me',
        },
    ]


class _FunctionCallState:
    def __init__(self, compactor, keep_full_turns: int = 1):
        self._history_compactor = compactor
        self._keep_full_turns = keep_full_turns
        self._system_prompt = 'system'
        self._tools_manager = type('Tools', (), {
            'tools_description': [{'type': 'function', 'function': {'name': 'run_script'}}],
        })()
        self._skill_manager = None


def _tool_id_counts(messages: list[dict[str, Any]]) -> Counter:
    return Counter(
        str(message.get('tool_call_id'))
        for message in messages
        if message.get('role') == 'tool' and message.get('tool_call_id')
    )


def test_function_call_uses_structured_split_when_summary_shortens_history() -> None:
    prior = _prior_with_open_tool_calls()
    current = _current_tools()
    calls = {'n': 0}

    def summarizer(_system: str, _user: str) -> str:
        calls['n'] += 1
        return VALID_SUMMARY

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_compression_reserved_output_tokens', 0), \
            config.temp('context_compression_trigger_ratio', 0.20), \
            config.temp('context_compression_target_ratio', 0.10), \
            config.temp('context_summary_keep_recent_ratio', 0.05), \
            config.temp('context_summary_min_recent_user_turns', 1), \
            config.temp('agentic_keep_full_turns', 1):
        compact = make_history_compactor(
            max_input_tokens=4_000,
            keep_recent=1,
            trigger='mid_turn',
            summarizer=summarizer,
        )
        compacted_prior, compacted_current = FunctionCall._compact_history(
            _FunctionCallState(compact),
            prior,
            current_input='',
            current_round_messages=current,
            workspace={},
        )

    sent = compacted_prior + compacted_current
    counts = _tool_id_counts(sent)
    assert calls['n'] >= 1
    assert is_runtime_summary_message(compacted_prior[0])
    assert len(sent) != len(prior) + len(current)
    assert counts['c3a'] == 1
    assert counts['c3b'] == 1
    assert [message.get('tool_call_id') for message in compacted_current] == ['c3a', 'c3b']
    assert all(message.get('tool_call_id') not in {'c3a', 'c3b'} for message in compacted_prior
               if message.get('role') == 'tool')


def test_function_call_list_compactor_still_splits_by_unchanged_length() -> None:
    prior = [{'role': 'user', 'content': 'old'}]
    current = [{'role': 'tool', 'content': 'now', 'tool_call_id': 't1'}]

    def list_compactor(history, _keep, current_round_messages=None, **_kwargs):
        return list(history) + list(current_round_messages or [])

    compacted_prior, compacted_current = FunctionCall._compact_history(
        _FunctionCallState(list_compactor),
        prior,
        current_round_messages=current,
        workspace={},
    )
    assert compacted_prior == prior
    assert compacted_current == current


def test_function_call_tuple_compactor_does_not_reappend_current() -> None:
    prior = [{'role': 'user', 'content': 'old'}] * 4
    current = [{'role': 'tool', 'content': 'now', 'tool_call_id': 't1'}]

    def tuple_compactor(history, _keep, current_round_messages=None, **_kwargs):
        return [{'role': 'user', 'content': 'summary'}], list(current_round_messages or [])

    compacted_prior, compacted_current = FunctionCall._compact_history(
        _FunctionCallState(tuple_compactor),
        prior,
        current_round_messages=current,
        workspace={},
    )
    assert compacted_prior == [{'role': 'user', 'content': 'summary'}]
    assert compacted_current == current
    assert _tool_id_counts(compacted_prior + compacted_current)['t1'] == 1
