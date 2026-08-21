from __future__ import annotations

from typing import Any

from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor
from lazymind.chat.engine.agent_runtime.summary_prompt import (
    REQUIRED_SUMMARY_SECTIONS,
    has_required_summary_sections,
)
from lazymind.chat.engine.agent_runtime.summary_range import (
    RUNTIME_SUMMARY_DISCLAIMER_PREFIX,
    is_runtime_summary_message,
    select_summary_range,
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


def test_required_summary_sections_helper() -> None:
    assert has_required_summary_sections(VALID_SUMMARY)
    assert not has_required_summary_sections('## Current task\nonly one')


def test_select_summary_range_keeps_min_user_turns_and_turn_boundary() -> None:
    history = _history_with_turns()
    # Small budget so only the latest protected turn fits in Tail.
    selected = select_summary_range(
        history,
        effective_input_budget=500,
        keep_recent_ratio=0.10,
        min_recent_user_turns=1,
    )
    assert selected is not None
    assert selected.tail[0]['role'] == 'user'
    assert selected.tail[0]['content'] == 'latest user request keep me'
    assert any(m.get('tool_call_id') == 'c3' for m in selected.tail)
    assert not any(m.get('tool_call_id') == 'c3' for m in selected.summary_messages)
    assert any(m.get('tool_call_id') == 'c1' for m in selected.summary_messages)


def test_select_summary_range_does_not_split_tool_pairs() -> None:
    history = [
        {'role': 'user', 'content': _long('u1')},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': 'x', 'function': {'name': 'run_script'}}],
        },
        {
            'role': 'tool',
            'name': 'run_script',
            'tool_call_id': 'x',
            'content': _long('tool'),
        },
        {'role': 'user', 'content': 'u2 keep'},
    ]
    selected = select_summary_range(
        history,
        effective_input_budget=5_000,
        keep_recent_ratio=0.05,
        min_recent_user_turns=1,
    )
    assert selected is not None
    assert selected.replace_end == 3
    ok, _ = validate_tool_pairing(selected.summary_messages)
    assert ok
    ok, _ = validate_tool_pairing(selected.tail)
    assert ok


def test_select_summary_range_rolls_prior_summary() -> None:
    prior = {
        'role': 'user',
        'content': f'{RUNTIME_SUMMARY_DISCLAIMER_PREFIX}\n\n{VALID_SUMMARY}',
        '_lazymind_meta': {'kind': 'runtime_summary', 'version': 1},
    }
    history = [
        prior,
        {'role': 'user', 'content': _long('new old history after prior summary')},
        {'role': 'assistant', 'content': _long('assistant after prior')},
        {'role': 'user', 'content': 'fresh tail'},
    ]
    selected = select_summary_range(
        history,
        effective_input_budget=800,
        keep_recent_ratio=0.10,
        min_recent_user_turns=1,
    )
    assert selected is not None
    assert selected.prior_summary_index == 0
    assert is_runtime_summary_message(selected.summary_messages[0])
    assert selected.replace_start == 0
    assert selected.tail[0]['content'] == 'fresh tail'


def test_apply_summary_skips_when_strategy_disabled() -> None:
    calls = {'n': 0}

    def summarizer(system_prompt: str, user_prompt: str) -> str:
        calls['n'] += 1
        return VALID_SUMMARY

    history = _history_with_turns()
    budget = build_context_budget(4_000, reserved_output_tokens=0, target_ratio=0.01)
    with config.temp('context_compression_enabled', True):
        with config.temp('context_summary_compression_enabled', False):
            projected, event = apply_summary_compression(
                history,
                budget=budget,
                trigger='pre_turn',
                summarizer=summarizer,
            )
    assert calls['n'] == 0
    assert event.decision == 'skipped'
    assert event.reason == 'strategy_disabled'
    assert projected == history


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
            config.temp('context_summary_min_recent_user_turns', 1), \
            config.temp('context_compression_min_reclaim_tokens', 1):
        projected, event = apply_summary_compression(
            history,
            budget=budget,
            trigger='pre_turn',
            summarizer=summarizer,
        )
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
            config.temp('context_summary_min_recent_user_turns', 1), \
            config.temp('context_compression_min_reclaim_tokens', 1):
        projected, event = apply_summary_compression(
            history,
            budget=budget,
            trigger='pre_turn',
            summarizer=lambda _s, _u: '## Current task\nbroken',
        )
    assert event.decision == 'abandoned'
    assert event.reason == 'missing_required_sections'
    assert projected == original


def test_apply_summary_skips_when_at_or_below_target() -> None:
    history = [{'role': 'user', 'content': 'tiny'}]
    budget = build_context_budget(100_000, reserved_output_tokens=0, target_ratio=0.99)
    calls = {'n': 0}

    def summarizer(system_prompt: str, user_prompt: str) -> str:
        calls['n'] += 1
        return VALID_SUMMARY

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True):
        projected, event = apply_summary_compression(
            history,
            budget=budget,
            trigger='mid_turn',
            summarizer=summarizer,
        )
    assert calls['n'] == 0
    assert event.decision == 'skipped'
    assert event.reason == 'at_or_below_target'
    assert projected == history


def test_make_history_compactor_runs_stage2_after_prune() -> None:
    history = _history_with_turns()
    calls = {'n': 0}

    def summarizer(system_prompt: str, user_prompt: str) -> str:
        calls['n'] += 1
        return VALID_SUMMARY

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_summary_keep_recent_ratio', 0.10), \
            config.temp('context_summary_min_recent_user_turns', 1), \
            config.temp('context_compression_min_reclaim_tokens', 1), \
            config.temp('agentic_keep_full_turns', 1):
        compact = make_history_compactor(
            max_input_tokens=4_000,
            keep_recent=1,
            trigger='mid_turn',
            summarizer=summarizer,
        )
        projected = compact(history, keep_full_turns=1)
    assert calls['n'] >= 1
    assert is_runtime_summary_message(projected[0])
    assert all(section in REQUIRED_SUMMARY_SECTIONS for section in REQUIRED_SUMMARY_SECTIONS)


def test_apply_summary_emits_covered_through_seq(monkeypatch) -> None:
    history = [
        {'role': 'user', 'content': _long('old'), 'history_seq': 3},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': 'c1', 'function': {'name': 'run_script'}}],
            'history_seq': 3,
        },
        {
            'role': 'tool',
            'name': 'run_script',
            'tool_call_id': 'c1',
            'content': _long('tool'),
            'history_seq': 3,
        },
        {'role': 'user', 'content': 'keep', 'history_seq': 4},
    ]
    emitted = {}

    def fake_write(tag, **payload):
        emitted['tag'] = tag
        emitted.update(payload)

    import lazymind.chat.engine.agent_runtime.summarizer as summarizer_mod
    monkeypatch.setattr(summarizer_mod, '_write_agent_data', fake_write)

    budget = build_context_budget(500, reserved_output_tokens=0, target_ratio=0.01)
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_summary_keep_recent_ratio', 0.05), \
            config.temp('context_summary_min_recent_user_turns', 1), \
            config.temp('context_compression_min_reclaim_tokens', 1):
        projected, event = apply_summary_compression(
            history,
            budget=budget,
            trigger='pre_turn',
            summarizer=lambda _s, _u: VALID_SUMMARY,
        )
    assert event.decision == 'summarized'
    assert emitted.get('tag') == 'model_context_updated'
    assert emitted.get('covered_through_seq') == 3
    assert 'Current task' in emitted.get('summary_text', '')
    assert is_runtime_summary_message(projected[0])


def test_emit_model_context_updated_requires_strictly_new_coverage(monkeypatch) -> None:
    import lazyllm
    from lazymind.chat.engine.agent_runtime.summarizer import _emit_model_context_updated

    emitted: list[dict[str, object]] = []

    def fake_write(tag: str, **payload) -> None:
        emitted.append({'tag': tag, **payload})

    monkeypatch.setattr(
        'lazymind.chat.engine.agent_runtime.summarizer._write_agent_data',
        fake_write,
    )
    lazyllm.globals['agentic_config'] = {
        'conversation_id': 'conv-1',
        'model_context': {
            'summary_text': VALID_SUMMARY,
            'covered_through_seq': 8,
            'version': 1,
        },
    }

    _emit_model_context_updated(VALID_SUMMARY, 8)
    _emit_model_context_updated(VALID_SUMMARY, 7)
    assert emitted == []

    _emit_model_context_updated(VALID_SUMMARY, 9)
    assert len(emitted) == 1
    assert emitted[0]['tag'] == 'model_context_updated'
    assert emitted[0]['covered_through_seq'] == 9
    assert lazyllm.globals['agentic_config']['model_context']['covered_through_seq'] == 9


def test_validate_tool_pairing_allows_inflight_at_end() -> None:
    history = [
        {'role': 'user', 'content': 'go'},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': 'pending', 'function': {'name': 'run_script'}}],
        },
    ]
    ok, reason = validate_tool_pairing(history)
    assert ok
    assert reason == 'ok'
