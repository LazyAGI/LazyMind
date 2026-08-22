from __future__ import annotations

import copy
import json

from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor
from lazymind.chat.engine.agent_runtime.projection_state import (
    clone_entries,
    reconcile_projection,
    render_projection,
    transition_metrics,
)
from lazymind.config import config


VALID_SUMMARY = '\n'.join([
    '## Current task',
    'Compress context.',
    '## Key constraints',
    'Keep authoritative history.',
    '## Progress and decisions',
    'Older context was summarized.',
    '## Important files and tool results',
    'No files changed.',
    '## Pending work',
    'Continue the task.',
])


def test_projection_append_preserves_existing_entries_byte_for_byte() -> None:
    state: dict[str, object] = {}
    history = [{'role': 'user', 'content': 'first'}]
    reconcile_projection(history, state)
    before = json.dumps(state['entries'], ensure_ascii=False, sort_keys=True)

    reconcile_projection(history + [{'role': 'assistant', 'content': 'second'}], state)

    assert json.dumps(state['entries'][:1], ensure_ascii=False, sort_keys=True) == before
    assert state['entries'][1]['source_start'] == 1
    assert state['entries'][1]['model_visible'] is False


def test_projection_prefix_mutation_and_shorter_view_rebuild_safely() -> None:
    state: dict[str, object] = {}
    history = [
        {'role': 'user', 'content': 'first'},
        {'role': 'assistant', 'content': 'second'},
    ]
    reconcile_projection(history, state)

    mutated = copy.deepcopy(history)
    mutated[0]['content'] = 'changed'
    _state, rebuilt = reconcile_projection(mutated, state)
    assert rebuilt
    assert state['last_reconcile_reason'] == 'source_prefix_mismatch'
    assert state['entries'][0]['message']['content'] == 'changed'

    _state, rebuilt = reconcile_projection(mutated[:1], state)
    assert rebuilt
    assert state['last_reconcile_reason'] == 'source_shortened'
    assert len(state['entries']) == 1


def test_projection_render_strips_internal_summary_metadata_only_upstream() -> None:
    state: dict[str, object] = {}
    reconcile_projection([{'role': 'user', 'content': 'raw', 'history_seq': 3}], state)
    state['entries'][0]['message']['_lazymind_meta'] = {'kind': 'runtime_summary'}

    rendered = render_projection(state['entries'])

    assert '_lazymind_meta' in state['entries'][0]['message']
    assert 'history_seq' in state['entries'][0]['message']
    assert '_lazymind_meta' not in rendered[0]
    assert 'history_seq' not in rendered[0]


def test_cache_disruption_excludes_newly_appended_unseen_entries() -> None:
    previous = [
        {
            'source_start': 0,
            'source_end': 1,
            'message': {'role': 'tool', 'content': 'old visible'},
            'kind': 'full',
            'model_visible': True,
        },
        {
            'source_start': 1,
            'source_end': 2,
            'message': {'role': 'tool', 'content': 'new unseen'},
            'kind': 'full',
            'model_visible': False,
        },
    ]
    candidate = clone_entries(previous)
    candidate[0]['message']['content'] = 'short'
    candidate[0]['kind'] = 'compacted'
    metrics = transition_metrics(
        previous,
        candidate,
        before_total=100,
        after_total=80,
    )

    assert metrics['first_changed_projection_index'] == 0
    assert metrics['changed_model_visible'] == [True, False]
    assert metrics['cache_disruption_tokens'] > 0


def test_new_oversized_result_spills_before_first_exposure(tmp_path) -> None:
    state: dict[str, object] = {}
    compact = make_history_compactor(
        max_input_tokens=100_000,
        keep_recent=2,
        workspace=str(tmp_path),
    )
    history: list[dict[str, object]] = []
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', False), \
            config.temp('context_compression_spill_bytes', 1024):
        compact(history, runtime_state=state)
        history.append({
            'role': 'tool',
            'name': 'read_user_attachment',
            'tool_call_id': 'new',
            'content': 'X' * 20_000,
        })
        projected = compact(history, runtime_state=state)

    assert state['entries'][0]['kind'] == 'spilled'
    assert state['entries'][0]['model_visible'] is True
    assert 'offloaded to workspace' in projected[0]['content']
    assert len(list((tmp_path / 'tool_spills').glob('*.txt'))) == 1


def test_cache_unjustified_old_prune_is_rolled_back_when_summary_fails() -> None:
    old_tool = 'old output\n' * 800
    history = [
        {'role': 'tool', 'name': 'run_script', 'content': old_tool},
        {'role': 'user', 'content': 'cached suffix\n' * 12_000},
        {'role': 'user', 'content': 'recent'},
    ]
    state: dict[str, object] = {}
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_compression_trigger_ratio', 0.20), \
            config.temp('context_compression_target_ratio', 0.10), \
            config.temp('context_prune_cache_amortization_calls', 1), \
            config.temp('context_prune_cached_token_cost_ratio', 1.0):
        compact = make_history_compactor(
            max_input_tokens=100_000,
            keep_recent=0,
            summarizer=lambda _system, _user: 'invalid summary',
        )
        projected = compact(history, runtime_state=state, remaining_rounds=1)

    assert projected[0]['content'] == old_tool
    assert state['entries'][0]['kind'] == 'full'


def test_rejected_spill_candidate_creates_no_file(tmp_path) -> None:
    huge = 'X' * 20_000
    history = [
        {'role': 'tool', 'name': 'read_user_attachment', 'content': huge},
        {'role': 'user', 'content': 'cached suffix\n' * 12_000},
    ]
    state: dict[str, object] = {}
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', False), \
            config.temp('context_compression_spill_bytes', 1024), \
            config.temp('context_compression_trigger_ratio', 0.20), \
            config.temp('context_compression_target_ratio', 0.05), \
            config.temp('context_prune_cache_amortization_calls', 1), \
            config.temp('context_prune_cached_token_cost_ratio', 1.0):
        compact = make_history_compactor(
            max_input_tokens=100_000,
            keep_recent=0,
            workspace=str(tmp_path),
        )
        projected = compact(history, runtime_state=state, remaining_rounds=1)

    assert projected[0]['content'] == huge
    assert not (tmp_path / 'tool_spills').exists()


def test_summary_is_stable_until_context_reaches_trigger_again() -> None:
    history = [
        {'role': 'user', 'content': 'old request\n' * 500},
        {'role': 'assistant', 'content': 'old response\n' * 500},
        {'role': 'user', 'content': 'recent request'},
    ]
    state: dict[str, object] = {}
    calls: list[str] = []

    def summarize(_system: str, user: str) -> str:
        calls.append(user)
        return VALID_SUMMARY

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_compression_trigger_ratio', 0.20), \
            config.temp('context_compression_target_ratio', 0.10), \
            config.temp('context_summary_keep_recent_ratio', 0.05):
        compact = make_history_compactor(
            max_input_tokens=4_000,
            keep_recent=0,
            summarizer=summarize,
        )
        first = compact(history, runtime_state=state)
        summary_bytes = json.dumps(state['entries'][0], ensure_ascii=False, sort_keys=True)
        second = compact(history, runtime_state=state)

    assert len(calls) == 1
    assert first == second
    assert json.dumps(state['entries'][0], ensure_ascii=False, sort_keys=True) == summary_bytes
    assert state['entries'][0]['kind'] == 'summary'
    assert '_lazymind_meta' not in first[0]


def test_accepted_prune_below_trigger_does_not_summarize_above_target() -> None:
    history = [
        {'role': 'tool', 'name': 'calculator', 'content': 'X' * 8_000},
        {'role': 'user', 'content': 'fixed recent context ' * 800},
    ]
    calls = {'summary': 0}

    def summarize(_system: str, _user: str) -> str:
        calls['summary'] += 1
        return VALID_SUMMARY

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_compression_reserved_output_tokens', 0), \
            config.temp('context_compression_trigger_ratio', 0.50), \
            config.temp('context_compression_target_ratio', 0.30):
        compact = make_history_compactor(
            max_input_tokens=10_000,
            keep_recent=0,
            summarizer=summarize,
        )
        projected = compact(history, runtime_state={})

    assert '[Earlier tool result compacted]' in projected[0]['content']
    assert calls['summary'] == 0


def test_rolling_summary_uses_prior_summary_plus_new_delta() -> None:
    history = [
        {'role': 'user', 'content': 'ORIGINAL_RAW_PREFIX\n' * 500},
        {'role': 'assistant', 'content': 'old response\n' * 500},
        {'role': 'user', 'content': 'first recent request'},
    ]
    state: dict[str, object] = {}
    prompts: list[str] = []

    def summarize(_system: str, user: str) -> str:
        prompts.append(user)
        return VALID_SUMMARY

    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_compression_trigger_ratio', 0.20), \
            config.temp('context_compression_target_ratio', 0.10), \
            config.temp('context_summary_keep_recent_ratio', 0.05):
        compact = make_history_compactor(
            max_input_tokens=4_000,
            keep_recent=0,
            summarizer=summarize,
        )
        compact(history, runtime_state=state)
        history.extend([
            {'role': 'assistant', 'content': 'NEW_COVERABLE_DELTA\n' * 800},
            {'role': 'user', 'content': 'latest protected request'},
        ])
        compact(history, runtime_state=state)

    assert len(prompts) == 2
    assert 'runtime-generated summary' in prompts[1]
    assert 'NEW_COVERABLE_DELTA' in prompts[1]
    assert 'ORIGINAL_RAW_PREFIX' not in prompts[1]
    assert state['entries'][0]['source_end'] > 2
