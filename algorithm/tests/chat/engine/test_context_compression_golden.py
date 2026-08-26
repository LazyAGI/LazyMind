"""Five golden scenarios for deterministic context compression.

These scenarios encode the production invariants we care about:
1. shell logs shrink while preserving command/errors
2. file reads shrink while preserving path/range
3. search/fetch results shrink while preserving query/sources
4. over-budget multi-tool history prunes old tools and keeps recent ones
5. below-trigger history is left untouched
"""

from __future__ import annotations

import json
from typing import Any

from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.compactors import compact_tool_result
from lazymind.chat.engine.agent_runtime.pruner import (
    estimate_history_tokens,
    prune_tool_results,
)


MARKER = '[Earlier tool result compacted]'


def _tool(name: str, content: Any, call_id: str) -> list[dict[str, Any]]:
    return [
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': call_id, 'function': {'name': name}}],
        },
        {
            'role': 'tool',
            'name': name,
            'tool_call_id': call_id,
            'content': content,
        },
    ]


def test_golden_01_shell_error_log_preserves_signal() -> None:
    payload = {
        'command': 'pytest -q algorithm/tests',
        'exit_code': 1,
        'stdout': (
            '.......F........\n' * 400
            + 'ERROR AssertionError: expected 200, got 500\n'
            + 'Traceback (most recent call last):\n'
            + '  File "test_api.py", line 42, in test_status\n'
            + 'FAILED test_api.py::test_status\n'
            + 'ok line\n' * 400
        ),
    }
    compacted, kind, before, after = compact_tool_result('run_script', payload)
    assert kind == 'shell'
    assert after < before
    assert after * 3 < before
    assert MARKER in compacted
    assert 'pytest -q algorithm/tests' in compacted
    assert 'exit code 1' in compacted
    assert 'AssertionError' in compacted


def test_golden_02_file_read_preserves_path_and_range() -> None:
    payload = {
        'result': {
            'filepath': '/workspace/lazymind/chat/service/chat_service.py',
            'start_line': 0,
            'end_line': 120,
            'total_lines': 960,
            'content': 'def stream_chat():\n' + ('    pass\n' * 2000),
        }
    }
    compacted, kind, before, after = compact_tool_result('LocalFileToolkit_read', payload)
    assert kind == 'file_locator'
    assert after < before
    assert MARKER in compacted
    assert '/workspace/lazymind/chat/service/chat_service.py' in compacted
    assert 'total_lines=960' in compacted


def test_golden_03_search_and_fetch_preserve_query_sources() -> None:
    search_payload = {
        'query': 'LazyMind context compression tool prune',
        'results': [
            {
                'title': 'Context growth notes',
                'url': 'https://example.com/docs/context-growth',
                'snippet': 'tool results accumulate ' + ('x' * 3000),
            },
            {
                'title': 'Pruner design',
                'url': 'https://example.com/docs/pruner',
                'snippet': 'keep recent tool turns ' + ('y' * 3000),
            },
        ],
    }
    fetch_payload = {
        'query': 'https://example.com/docs/pruner',
        'result': {
            'final_url': 'https://example.com/docs/pruner',
            'text': '# Pruner\n' + ('long body paragraph\n' * 800),
        },
    }
    search_text, search_kind, search_before, search_after = compact_tool_result(
        'web_search', search_payload,
    )
    fetch_text, fetch_kind, fetch_before, fetch_after = compact_tool_result(
        'url_fetch', fetch_payload,
    )
    assert search_kind == 'search'
    assert fetch_kind == 'search'
    assert search_after < search_before
    assert fetch_after < fetch_before
    assert 'LazyMind context compression tool prune' in search_text
    assert 'https://example.com/docs/context-growth' in search_text
    assert 'https://example.com/docs/pruner' in fetch_text


def test_golden_04_over_budget_history_prunes_old_keeps_recent() -> None:
    old_shell = {
        'command': 'make lint',
        'exit_code': 1,
        'stdout': 'ERROR boom\n' + ('shell noise\n' * 1200),
    }
    old_search = {
        'query': 'old query',
        'results': [{'title': 'Old', 'url': 'https://example.com/old', 'snippet': 'z' * 4000}],
    }
    recent_file = {
        'result': {
            'filepath': '/tmp/recent.py',
            'start_line': 0,
            'end_line': 20,
            'total_lines': 20,
            'content': 'print("keep me intact")\n',
        }
    }
    history = [{'role': 'user', 'content': 'debug failing pipeline'}]
    history.extend(_tool('run_script', old_shell, '1'))
    history.extend(_tool('web_search', old_search, '2'))
    history.extend(_tool('LocalFileToolkit_read', recent_file, '3'))
    original = json.loads(json.dumps(history))
    budget = build_context_budget(
        6_000,
        reserved_output_tokens=0,
        trigger_ratio=0.2,
        target_ratio=0.1,
    )
    before = estimate_history_tokens(history)
    projected, event = prune_tool_results(
        history,
        keep_recent=1,
        budget=budget,
        trigger='pre_turn',
        estimated_total_tokens=before,
        force=True,
        min_reclaim_tokens=1,
    )
    assert history == original
    assert event.decision == 'pruned'
    assert event.reclaimed_tokens > 0
    assert event.estimated_after < event.estimated_before
    assert projected[-1]['content'] == recent_file
    assert MARKER in projected[2]['content']
    assert MARKER in projected[4]['content']
    assert projected[2]['tool_call_id'] == '1'
    assert projected[4]['tool_call_id'] == '2'


def test_golden_05_below_trigger_skips_compression() -> None:
    history = [{'role': 'user', 'content': 'tiny question'}]
    history.extend(_tool('calculator', '42', '1'))
    history.extend(_tool('calculator', '43', '2'))
    budget = build_context_budget(
        100_000,
        reserved_output_tokens=0,
        trigger_ratio=0.9,
        target_ratio=0.5,
    )
    projected, event = prune_tool_results(
        history,
        keep_recent=1,
        budget=budget,
        trigger='pre_turn',
        force=False,
    )
    assert event.decision == 'skipped'
    assert event.reason == 'below_trigger'
    assert event.reclaimed_tokens == 0
    assert projected == history
