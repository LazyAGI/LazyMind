from __future__ import annotations

import json

from lazymind.chat.engine.agent_runtime.budget import (
    build_context_budget,
    needs_compression,
    parse_token_limit,
    resolve_max_input_tokens,
)
from lazymind.chat.engine.agent_runtime.compactors import (
    compact_file_result,
    compact_generic_result,
    compact_search_result,
    compact_shell_result,
    compact_tool_result,
)
from lazymind.chat.engine.agent_runtime.pruner import (
    make_history_compactor,
    prune_tool_results,
)


def test_parse_token_limit_supports_k_m_suffixes() -> None:
    assert parse_token_limit('128K') == 128_000
    assert parse_token_limit('1M') == 1_000_000
    assert parse_token_limit(4096) == 4096
    assert parse_token_limit('bad') is None


def test_build_context_budget_uses_trigger_and_target_ratios() -> None:
    budget = build_context_budget(
        100_000,
        trigger_ratio=0.70,
        target_ratio=0.45,
        reserved_output_tokens=10_000,
    )
    assert budget.effective_input_budget == 90_000
    assert budget.trigger_tokens == 63_000
    assert budget.target_tokens == 40_500
    assert needs_compression(63_000, budget)
    assert not needs_compression(62_999, budget)


def test_build_context_budget_caps_reserved_on_small_windows() -> None:
    budget = build_context_budget(
        8_000,
        trigger_ratio=0.70,
        target_ratio=0.45,
        reserved_output_tokens=50_000,
    )
    assert budget.reserved_output_tokens == 4_000
    assert budget.effective_input_budget == 4_000
    assert budget.trigger_tokens == 2_800


def test_resolve_max_input_tokens_reads_llm_config(monkeypatch) -> None:
    monkeypatch.setattr(
        'lazymind.chat.engine.agent_runtime.budget.runtime_yaml_max_input_tokens',
        lambda role='llm': None,
    )
    assert resolve_max_input_tokens(llm_config={'llm': {'max_input_tokens': '32K'}}) == 32_000


def test_resolve_max_input_tokens_prefers_runtime_yaml_over_catalog(monkeypatch) -> None:
    monkeypatch.setattr(
        'lazymind.chat.engine.agent_runtime.budget.runtime_yaml_max_input_tokens',
        lambda role='llm': 64_000,
    )
    assert resolve_max_input_tokens(llm_config={'llm': {'max_input_tokens': '128K'}}) == 64_000


def test_resolve_max_input_tokens_explicit_arg_beats_runtime_yaml(monkeypatch) -> None:
    monkeypatch.setattr(
        'lazymind.chat.engine.agent_runtime.budget.runtime_yaml_max_input_tokens',
        lambda role='llm': 64_000,
    )
    assert resolve_max_input_tokens('8K', llm_config={'llm': {'max_input_tokens': '128K'}}) == 8_000


def test_shell_compactor_keeps_command_and_errors() -> None:
    payload = {
        'command': 'pytest -q',
        'exit_code': 1,
        'stdout': 'ok\n' * 200 + 'AssertionError: expected 200, got 500\n' + 'tail\n' * 200,
    }
    compacted, kind = compact_shell_result('run_script', payload)
    assert kind == 'shell'
    assert 'pytest -q' in compacted
    assert 'exit code 1' in compacted
    assert 'AssertionError' in compacted
    assert '[Earlier tool result compacted]' in compacted


def test_file_compactor_keeps_path_and_excerpt() -> None:
    payload = {
        'result': {
            'filepath': '/tmp/demo.py',
            'start_line': 0,
            'end_line': 40,
            'total_lines': 400,
            'content': 'line\n' * 500,
        }
    }
    compacted, kind = compact_file_result('LocalFileToolkit_read', payload)
    assert kind == 'file_locator'
    assert '/tmp/demo.py' in compacted
    assert 'total_lines=400' in compacted


def test_file_compactor_treats_runtime_repr_and_persisted_json_equally() -> None:
    payload = {
        'success': True,
        'tool': 'read_file',
        'result': {
            'target': 'paper.pdf',
            'offset': 21,
            'end_line': 40,
            'next_offset': 41,
            'total_lines': 100,
            'eof': False,
            'text': 'body\n' * 1000,
        },
    }
    from_repr, repr_kind = compact_file_result('read_file', str(payload))
    from_json, json_kind = compact_file_result('read_file', json.dumps(payload))

    assert repr_kind == json_kind == 'file_locator'
    for expected in ('Target: paper.pdf', 'offset=21', 'end=40', 'offset=41'):
        assert expected in from_repr
        assert expected in from_json


def test_search_compactor_keeps_query_and_sources() -> None:
    payload = {
        'query': 'lazymind compression',
        'results': [
            {'title': 'Doc A', 'url': 'https://example.com/a', 'snippet': 'prune tools'},
            {'title': 'Doc B', 'url': 'https://example.com/b', 'snippet': 'summary later'},
        ],
    }
    compacted, kind = compact_search_result('web_search', payload)
    assert kind == 'search'
    assert 'lazymind compression' in compacted
    assert 'https://example.com/a' in compacted
    assert 'Doc A' in compacted


def test_generic_compactor_short_circuits_small_payloads() -> None:
    text, kind = compact_generic_result('calculator', '42')
    assert kind == 'generic'
    assert text == '42'


def test_prune_preserves_original_history_and_recent_tool_results() -> None:
    old = 'ERROR boom\n' + ('shell log line\n' * 800)
    recent = 'fresh tool output that must stay intact'
    history = [
        {'role': 'user', 'content': 'start'},
        {'role': 'assistant', 'content': '', 'tool_calls': [{'id': '1', 'function': {'name': 'run_script'}}]},
        {'role': 'tool', 'name': 'run_script', 'tool_call_id': '1', 'content': old},
        {'role': 'assistant', 'content': '', 'tool_calls': [{'id': '2', 'function': {'name': 'run_script'}}]},
        {'role': 'tool', 'name': 'run_script', 'tool_call_id': '2', 'content': recent},
    ]
    original = [dict(item) for item in history]
    budget = build_context_budget(8_000, reserved_output_tokens=0, trigger_ratio=0.1, target_ratio=0.05)
    projected, event = prune_tool_results(
        history,
        keep_recent=1,
        budget=budget,
        trigger='pre_turn',
        force=True,
        min_reclaim_tokens=1,
    )
    assert history == original
    assert event.decision == 'pruned'
    assert projected[-1]['content'] == recent
    assert '[Earlier tool result compacted]' in projected[2]['content']
    assert projected[1].get('tool_calls')
    assert projected[2]['tool_call_id'] == '1'


def test_mid_turn_compactor_callback_compacts_old_tools() -> None:
    from lazymind.config import config

    history = [
        {
            'role': 'tool',
            'name': 'url_fetch',
            'content': {
                'query': 'x',
                'result': {'final_url': 'https://example.com', 'text': 'body\n' * 1000},
            },
        },
        {'role': 'tool', 'name': 'url_fetch', 'content': 'keep me'},
    ]
    with config.temp('context_compression_enabled', True):
        compact = make_history_compactor(max_input_tokens=4_000, keep_recent=1, trigger='mid_turn')
        projected = compact(history, keep_full_turns=1)
    assert projected[1]['content'] == 'keep me'
    assert isinstance(projected[0]['content'], str)
    assert 'https://example.com' in projected[0]['content'] or 'compacted' in projected[0]['content']


def test_mid_turn_compactor_does_not_force_below_trigger() -> None:
    from lazymind.config import config

    history = []
    for index in range(3):
        history.extend([
            {
                'role': 'assistant',
                'content': '',
                'tool_calls': [{'id': str(index)}],
            },
            {
                'role': 'tool',
                'name': 'calculator',
                'tool_call_id': str(index),
                'content': 'x' * 2_000,
            },
        ])
    with config.temp('context_compression_enabled', True):
        compact = make_history_compactor(
            max_input_tokens=64_000,
            keep_recent=2,
            trigger='mid_turn',
        )
        projected = compact(history, keep_full_turns=2)

    assert projected == history


def test_compact_tool_result_routes_by_tool_name() -> None:
    content, compactor, before, after = compact_tool_result(
        'kb_search',
        {'query': 'q', 'results': [{'title': 't', 'url': 'https://x', 'snippet': 's' * 2000}]},
    )
    assert compactor == 'search'
    assert after < before
    assert 'https://x' in content


def test_split_current_tool_input_avoids_duplicate_tools() -> None:
    from lazyllm.tools.agent.functionCall import _split_current_tool_input

    originals = [
        {'role': 'tool', 'tool_call_id': 'a', 'name': 'TavilySearch_get_content', 'content': 'HUGE-A'},
        {'role': 'tool', 'tool_call_id': 'b', 'name': 'TavilySearch_get_content', 'content': 'HUGE-B'},
    ]
    compacted_history = [
        {'role': 'assistant', 'content': '', 'tool_calls': []},
        {'role': 'tool', 'tool_call_id': 'a', 'name': 'TavilySearch_get_content', 'content': 'short-a'},
        {'role': 'tool', 'tool_call_id': 'b', 'name': 'TavilySearch_get_content', 'content': 'short-b'},
    ]
    remainder, current = _split_current_tool_input(compacted_history, originals)
    assert remainder == [{'role': 'assistant', 'content': '', 'tool_calls': []}]
    assert [item['content'] for item in current] == ['short-a', 'short-b']


def test_keep_recent_still_spills_oversized_tool_results(tmp_path) -> None:
    from lazymind.config import config

    huge = 'P' * 20_000
    recent_small = 'keep me'
    history = [
        {'role': 'assistant', 'content': '', 'tool_calls': [{'id': '1'}]},
        {'role': 'tool', 'name': 'read_user_attachment', 'tool_call_id': '1', 'content': huge},
        {'role': 'assistant', 'content': '', 'tool_calls': [{'id': '2'}]},
        {'role': 'tool', 'name': 'url_fetch', 'tool_call_id': '2', 'content': recent_small},
    ]
    budget = build_context_budget(100_000, reserved_output_tokens=0, trigger_ratio=0.99, target_ratio=0.9)
    with config.temp('context_compression_spill_bytes', 1024):
        projected, event = prune_tool_results(
            history,
            keep_recent=2,
            budget=budget,
            trigger='mid_turn',
            force=True,
            min_reclaim_tokens=1,
            workspace=str(tmp_path),
        )
    assert event.decision == 'spilled'
    assert projected[-1]['content'] == recent_small
    assert 'offloaded to workspace' in projected[1]['content']
    assert 'tool_spills/' in projected[1]['content']
    spilled = list((tmp_path / 'tool_spills').glob('*.txt'))
    assert len(spilled) == 1
    assert spilled[0].read_text(encoding='utf-8') == huge
    assert history[1]['content'] == huge


def test_oversized_replayable_file_result_uses_locator_instead_of_spill(tmp_path) -> None:
    payload = {
        'success': True,
        'tool': 'read_file',
        'result': {
            'target': 'paper.pdf',
            'offset': 1,
            'end_line': 200,
            'next_offset': 201,
            'total_lines': 500,
            'eof': False,
            'text': 'document line\n' * 2000,
        },
    }
    history = [
        {'role': 'tool', 'name': 'read_file', 'tool_call_id': 'read-1', 'content': str(payload)},
    ]
    budget = build_context_budget(100_000, reserved_output_tokens=0, trigger_ratio=0.99, target_ratio=0.9)

    projected, event = prune_tool_results(
        history,
        keep_recent=1,
        budget=budget,
        trigger='mid_turn',
        force=True,
        min_reclaim_tokens=1,
        workspace=str(tmp_path),
    )

    assert event.decision == 'pruned'
    assert event.details[0].compactor == 'file_locator'
    assert 'Target: paper.pdf' in projected[0]['content']
    assert 'offset=201' in projected[0]['content']
    assert not (tmp_path / 'tool_spills').exists()


def test_spill_stays_internal_and_uses_stable_content_path(tmp_path, monkeypatch) -> None:
    from lazymind.chat.engine.agent_runtime.compactors import compact_or_spill_tool_result
    from lazymind.config import config

    calls: list[dict[str, object]] = []

    def fake_save_chat_file(**kwargs):
        calls.append(kwargs)
        return {'ok': True}

    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.workspace.save_chat_file',
        fake_save_chat_file,
    )
    huge = 'P' * 20_000
    with config.temp('context_compression_spill_bytes', 1024):
        notice, compactor, _before, _after, first_path, _size = compact_or_spill_tool_result(
            'read_user_attachment',
            huge,
            workspace=str(tmp_path),
        )
        _notice, _compactor, _before, _after, second_path, _size = (
            compact_or_spill_tool_result(
                'read_user_attachment',
                huge,
                workspace=str(tmp_path),
            )
        )
    assert compactor == 'spill'
    assert first_path == second_path
    assert first_path.startswith('tool_spills/read_user_attachment_')
    assert first_path.endswith('.txt')
    assert 'offloaded to workspace' in notice
    assert calls == []
    assert len(list((tmp_path / 'tool_spills').glob('*.txt'))) == 1


def test_current_round_projection_is_what_llm_input_would_see(tmp_path) -> None:
    from lazyllm.tools.agent.functionCall import _split_current_tool_input
    from lazymind.config import config

    huge = 'X' * 25_000
    originals = [
        {'role': 'tool', 'tool_call_id': 'pdf1', 'name': 'TavilySearch_get_content', 'content': huge},
    ]
    history = [
        {'role': 'user', 'content': 'survey papers'},
        {'role': 'assistant', 'content': '', 'tool_calls': [{'id': 'pdf1'}]},
        originals[0],
    ]
    budget = build_context_budget(8_000, reserved_output_tokens=0, trigger_ratio=0.1, target_ratio=0.05)
    with config.temp('context_compression_spill_bytes', 1024):
        projected, event = prune_tool_results(
            history,
            keep_recent=2,
            budget=budget,
            trigger='mid_turn',
            force=True,
            min_reclaim_tokens=1,
            workspace=str(tmp_path),
        )
    remainder, llm_input = _split_current_tool_input(projected, originals)
    assert event.decision == 'spilled'
    assert huge not in llm_input[0]['content']
    assert len(llm_input[0]['content']) < 4_000
    assert remainder[-1]['role'] == 'assistant'
