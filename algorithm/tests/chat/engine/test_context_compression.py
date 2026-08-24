from __future__ import annotations

import json

from lazyllm.tools.agent.base import TOOL_OBSERVATION_KEY

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
from lazymind.chat.engine.agent_runtime.executor import AgentExecutor
from lazymind.chat.engine.agent_runtime.models import (
    AgentExecutionOptions,
    AgentRole,
    AgentRunPlan,
    PromptBundle,
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


def test_resolve_max_input_tokens_reads_llm_config() -> None:
    assert resolve_max_input_tokens(llm_config={'llm': {'max_input_tokens': '32K'}}) == 32_000


def test_resolve_max_input_tokens_prefers_catalog() -> None:
    assert resolve_max_input_tokens(llm_config={'llm': {'max_input_tokens': '128K'}}) == 128_000


def test_resolve_max_input_tokens_explicit_arg_beats_catalog() -> None:
    assert resolve_max_input_tokens('8K', llm_config={'llm': {'max_input_tokens': '128K'}}) == 8_000


def test_resolve_max_input_tokens_uses_64k_fallback() -> None:
    from lazymind.config import config

    with config.temp('context_compression_default_max_input_tokens', 64_000):
        budget = build_context_budget(llm_config={'llm': {'max_input_tokens': None}})
    assert budget.max_input_tokens == 64_000
    assert budget.source == 'fallback'


def test_enrich_role_types_preserves_catalog_window(monkeypatch) -> None:
    from lazymind.model_config import _enrich_role_types

    monkeypatch.setattr(
        'lazymind.model_config.load_model_config',
        lambda: {'llm': {'type': 'llm', 'max_input_tokens': '64K'}},
    )
    enriched = _enrich_role_types({'llm': {'model': 'selected', 'max_input_tokens': '128K'}})
    assert enriched['llm']['max_input_tokens'] == '128K'
    enriched_null = _enrich_role_types({'llm': {'model': 'custom', 'max_input_tokens': None}})
    assert enriched_null['llm']['max_input_tokens'] is None


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


def test_file_compactor_keeps_json_fallback_without_python_repr_protocol() -> None:
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

    assert repr_kind == 'file'
    assert json_kind == 'file_locator'
    assert 'Target: paper.pdf' not in from_repr
    for expected in ('Target: paper.pdf', 'offset=21', 'end=40', 'offset=41'):
        assert expected in from_json


def test_semantic_compactors_prefer_structured_observation() -> None:
    shell, shell_kind = compact_shell_result(
        'run_script',
        'unparseable display\n' * 200,
        {'version': 1, 'ok': True, 'value': {
            'command': 'pytest -q',
            'exit_code': 2,
            'stdout': 'AssertionError: failed\n' * 100,
        }, 'error': ''},
    )
    file_text, file_kind = compact_file_result(
        'read_file',
        'unparseable display\n' * 200,
        {'version': 1, 'ok': True, 'value': {
            'result': {
                'path': '/tmp/report.txt',
                'offset': 10,
                'next_offset': 30,
                'content': 'body\n' * 200,
            },
        }, 'error': ''},
    )
    search, search_kind = compact_search_result(
        'web_search',
        'unparseable display\n' * 200,
        {'version': 1, 'ok': True, 'value': {
            'query': 'structured observations',
            'hits': [{
                'title': 'Protocol',
                'url': 'https://example.com/protocol',
                'snippet': 'Stable machine data',
            }],
        }, 'error': ''},
    )

    assert shell_kind == 'shell'
    assert 'pytest -q' in shell
    assert 'exit code 2' in shell
    assert file_kind == 'file_locator'
    assert '/tmp/report.txt' in file_text
    assert 'offset=30' in file_text
    assert search_kind == 'search'
    assert 'structured observations' in search
    assert 'https://example.com/protocol' in search


def test_pruner_passes_structured_observation_to_compactor() -> None:
    content = 'unparseable display\n' * 300
    history = [{
        'role': 'tool',
        'name': 'read_file',
        'tool_call_id': 'call-read',
        'content': content,
        TOOL_OBSERVATION_KEY: {
            'version': 1,
            'ok': True,
            'value': {
                'path': '/tmp/structured.txt',
                'content': 'important body\n' * 300,
            },
            'error': '',
        },
    }]
    budget = build_context_budget(
        8_000,
        reserved_output_tokens=0,
        trigger_ratio=0.1,
        target_ratio=0.05,
    )

    projected, event = prune_tool_results(
        history,
        keep_recent=0,
        budget=budget,
        trigger='pre_turn',
        force=True,
    )

    assert event.decision == 'pruned'
    assert 'Target: /tmp/structured.txt' in projected[0]['content']
    assert projected[0][TOOL_OBSERVATION_KEY] == history[0][TOOL_OBSERVATION_KEY]
    assert history[0]['content'] == content


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
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', False):
        compact = make_history_compactor(max_input_tokens=3_000, keep_recent=1, trigger='mid_turn')
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


def test_mid_turn_compactor_accepts_live_prefix_and_keeps_zero() -> None:
    from lazymind.config import config

    history = [{'role': 'tool', 'name': 'url_fetch', 'content': 'old result'}]
    prefix = {
        'system_prompt': 'system',
        'tool_definitions': [{'type': 'function', 'function': {'name': 'search'}}],
        'skills_prompt': 'skills',
    }
    state = {}
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', False):
        compact = make_history_compactor(max_input_tokens=64_000, keep_recent=2)
        projected = compact(
            history,
            keep_full_turns=0,
            prefix=prefix,
            current_input='new request',
            runtime_state=state,
        )

    assert projected == history
    assert len(state['source_fingerprints']) == 1


def test_mid_turn_compactor_triggers_from_prefix_plus_history() -> None:
    from lazymind.config import config

    history = [{
        'role': 'tool',
        'name': 'url_fetch',
        'content': {
            'query': 'x',
            'result': {'final_url': 'https://example.com', 'text': 'body\n' * 1200},
        },
    }]
    prefix = {'system_prompt': 'S' * 33_000, 'tool_definitions': [], 'skills_prompt': ''}
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', False), \
            config.temp('context_compression_reserved_output_tokens', 0):
        compact = make_history_compactor(max_input_tokens=10_000, keep_recent=0)
        projected = compact(history, keep_full_turns=0, prefix=prefix, current_input='')

    assert isinstance(projected[0]['content'], str)
    assert len(projected[0]['content']) < len(str(history[0]['content']))


def test_mid_turn_compactor_reconciles_tool_continuation_once() -> None:
    from lazymind.config import config

    history = [{'role': 'tool', 'name': 'search', 'content': 'current tool result'}]
    state = {}
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', False):
        compact = make_history_compactor(max_input_tokens=64_000)
        projected = compact(
            history,
            keep_full_turns=0,
            prefix={},
            current_input='',
            runtime_state=state,
        )

    assert projected == history
    assert len(state['entries']) == 1


def test_describe_context_is_inspect_only() -> None:
    from lazyllm.tools.agent.reactAgent import ReactAgent

    class InspectAgent:
        _workspace = '/tmp/workspace'

        def _prepare_tool_context(self, current_input, history):
            self.prepared = (current_input, history)

        def _model_facing_prefix(self):
            return {
                'system_prompt': 'system',
                'tool_definitions': [],
                'skills_prompt': '',
                'skill_prompt_parts': [],
            }

    agent = InspectAgent()
    history = [{'role': 'tool', 'content': 'raw result'}]
    description = ReactAgent.describe_context(agent, history, 'inspect')

    assert description['history'] == history
    assert description['history'] is not history
    assert agent.prepared == ('inspect', history)


def test_function_call_passes_live_prefix_and_current_input() -> None:
    from lazyllm.tools.agent.functionCall import FunctionCall

    captured = {}

    def compactor(history, keep, **kwargs):
        captured['keep'] = keep
        captured.update(kwargs)
        return list(history)

    class ToolManager:
        tools_description = [{'type': 'function', 'function': {'name': 'search'}}]

    class FunctionCallState:
        _history_compactor = staticmethod(compactor)
        _keep_full_turns = 0
        _system_prompt = 'live system'
        _tools_manager = ToolManager()
        _skill_manager = None

    history = [{'role': 'user', 'content': 'old'}]
    workspace = {}
    projected, current = FunctionCall._compact_history(
        FunctionCallState(),
        history,
        current_input='new user message',
        workspace=workspace,
        remaining_rounds=7,
    )

    assert projected == history
    assert current == []
    assert captured['current_round_messages'] == []
    assert captured['keep'] == 0
    assert captured['current_input'] == 'new user message'
    assert captured['prefix']['system_prompt'] == 'live system'
    assert captured['prefix']['tool_definitions'] == ToolManager.tools_description
    assert captured['runtime_state'] is workspace['_history_projection_state']
    assert captured['remaining_rounds'] == 7


def _test_plan(history: list[dict[str, object]]) -> AgentRunPlan:
    return AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBundle(
            sections=(),
            system_prompt='system',
            current_input='request',
            input_title='Request',
            input_content='request',
        ),
        history=history,
        execution_options=AgentExecutionOptions(keep_full_turns=2),
    )


class _FakeToolManager:
    def __call__(self, tools, verbose=False):
        return []


class _FakeReactAgent:
    last_kwargs = {}

    def __init__(self, **kwargs):
        self.last_kwargs = kwargs
        type(self).last_kwargs = kwargs
        self._tools_manager = _FakeToolManager()

    def _prepare_tool_context(self, current_input, history):
        return None

    def _model_facing_prefix(self):
        return {
            'system_prompt': 'system',
            'tool_definitions': [],
            'skills_prompt': '',
            'skill_prompt_parts': [],
        }

    def set_stop_tools(self, stop_tools):
        return None


def test_executor_master_off_does_not_attach_compactor(monkeypatch) -> None:
    from lazymind.config import config

    monkeypatch.setattr(
        'lazymind.chat.engine.agent_runtime.executor._agent_mod.ReactAgent',
        _FakeReactAgent,
    )
    history = [{'role': 'tool', 'content': 'x' * 1000}]
    with config.temp('context_compression_enabled', False):
        AgentExecutor().create_agent(object(), _test_plan(history))

    assert 'history_compactor' not in _FakeReactAgent.last_kwargs


def test_executor_does_not_replace_authoritative_history_with_projection(monkeypatch) -> None:
    from lazymind.config import config

    monkeypatch.setattr(
        'lazymind.chat.engine.agent_runtime.executor._agent_mod.ReactAgent',
        _FakeReactAgent,
    )
    history = [{'role': 'tool', 'content': 'history'}]
    plan = _test_plan(history)
    with config.temp('context_compression_enabled', True):
        AgentExecutor().create_agent(object(), plan)
    assert plan.history is history
    assert plan.history == [{'role': 'tool', 'content': 'history'}]


def test_compact_tool_result_routes_by_tool_name() -> None:
    content, compactor, before, after = compact_tool_result(
        'kb_search',
        {'query': 'q', 'results': [{'title': 't', 'url': 'https://x', 'snippet': 's' * 2000}]},
    )
    assert compactor == 'search'
    assert after < before
    assert 'https://x' in content


def test_prior_and_current_round_split_by_length() -> None:
    prior = [{'role': 'assistant', 'content': '', 'tool_calls': []}]
    current = [
        {'role': 'tool', 'tool_call_id': 'a', 'name': 'TavilySearch_get_content', 'content': 'short-a'},
        {'role': 'tool', 'tool_call_id': 'b', 'name': 'TavilySearch_get_content', 'content': 'short-b'},
    ]
    compacted = prior + current
    remainder, llm_input = compacted[:len(prior)], compacted[len(prior):]
    assert remainder == prior
    assert [item['content'] for item in llm_input] == ['short-a', 'short-b']


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


def test_oversized_file_result_spills_as_one_file(tmp_path) -> None:
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
    history = [{
        'role': 'tool',
        'name': 'read_file',
        'tool_call_id': 'read-1',
        'content': str(payload),
        TOOL_OBSERVATION_KEY: {
            'version': 1,
            'ok': True,
            'value': payload,
            'error': '',
        },
    }]
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

    assert event.decision == 'spilled'
    assert event.details[0].compactor == 'spill'
    assert 'offloaded to workspace' in projected[0]['content']
    spilled = list((tmp_path / 'tool_spills').glob('*.txt'))
    assert len(spilled) == 1
    assert spilled[0].read_text(encoding='utf-8') == str(payload)


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
    from lazymind.config import config

    huge = 'X' * 25_000
    originals = [
        {'role': 'tool', 'tool_call_id': 'pdf1', 'name': 'TavilySearch_get_content', 'content': huge},
    ]
    prior = [
        {'role': 'user', 'content': 'survey papers'},
        {'role': 'assistant', 'content': '', 'tool_calls': [{'id': 'pdf1'}]},
    ]
    compact = make_history_compactor(
        max_input_tokens=8_000,
        keep_recent=2,
        trigger='mid_turn',
        workspace=str(tmp_path),
    )
    with config.temp('context_compression_spill_bytes', 1024), config.temp(
        'context_compression_enabled', True,
    ):
        projected = compact(
            prior,
            2,
            prefix={},
            current_input='',
            current_round_messages=originals,
        )
    remainder, llm_input = projected[:len(prior)], projected[len(prior):]
    assert huge not in llm_input[0]['content']
    assert len(llm_input[0]['content']) < 4_000
    assert remainder[-1]['role'] == 'assistant'
