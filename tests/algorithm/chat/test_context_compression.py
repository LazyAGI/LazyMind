from __future__ import annotations

from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor, prune_tool_results


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
        remainder, llm_input = compact(
            prior,
            2,
            prefix={},
            current_input='',
            current_round_messages=originals,
        )
    assert huge not in llm_input[0]['content']
    assert len(llm_input[0]['content']) < 4_000
    assert remainder[-1]['role'] == 'assistant'
