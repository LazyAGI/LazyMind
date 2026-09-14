from __future__ import annotations

from lazymind.chat.engine.agent_runtime.active_context import (
    pin_active_skills_into_builder,
    project_skill_tool_value,
    resolve_summary_profile,
)
from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.compactors import compact_tool_result
from lazymind.chat.engine.agent_runtime.prompt_builder import PromptBuilder
from lazymind.chat.engine.agent_runtime.models import AgentRole
from lazymind.chat.engine.agent_runtime.summarizer import apply_summary_compression
from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
from lazymind.chat.engine.subagent.tools import get_artifact
from lazymind.config import config
import lazyllm


SKILL_BODY = '''# Skill

## Step 1
Do the middle thing that generic head/tail would drop.

## Forbidden
Never skip validation.
'''


def test_get_skill_compactor_keeps_locator_not_mid_body() -> None:
    compacted, kind, _before, _after = compact_tool_result(
        'get_skill',
        {'status': 'ok', 'name': 'demo', 'path': '/skills/demo/SKILL.md', 'content': SKILL_BODY * 40},
    )
    assert kind == 'skill_locator'
    assert 'Never skip validation' not in compacted
    assert 'demo' in compacted
    assert 'Hash:' in compacted


def test_get_artifact_and_writer_tools_use_artifact_compactor() -> None:
    payload = {
        'status': 'ok',
        'key': 'draft',
        'path': '/tmp/draft.md',
        'artifacts': [{'value': {'text': 'huge draft body\n' * 200, 'path': '/tmp/draft.md'}}],
    }
    compacted, kind, _before, _after = compact_tool_result('get_artifact', payload)
    assert kind == 'file_locator'
    assert 'huge draft body' not in compacted
    compacted_writer, writer_kind, _b, _a = compact_tool_result(
        'product_writer_assemble_draft', payload,
    )
    assert writer_kind == 'file_locator'
    assert 'huge draft body' not in compacted_writer


def test_project_skill_pins_authoritative_and_returns_locator() -> None:
    projected, locator = project_skill_tool_value(
        'get_skill',
        {'status': 'ok', 'name': 'demo', 'path': '/skills/demo/SKILL.md', 'content': SKILL_BODY},
    )
    assert locator is not None
    assert projected['name'] == 'demo'
    assert 'content' not in projected
    assert projected['hash'] == locator['hash']


def test_pin_active_skills_into_builder_rehydrates_from_sidecar(tmp_path) -> None:
    skill_path = tmp_path / 'SKILL.md'
    skill_path.write_text(SKILL_BODY, encoding='utf-8')
    builder = PromptBuilder.for_role(AgentRole.CHAT)
    pin_active_skills_into_builder(
        builder,
        {'active_skills': [{'name': 'demo', 'path': str(skill_path), 'hash': ''}]},
    )
    bundle = builder.input(content='go', source='user').build()
    assert 'Never skip validation' in bundle.current_input
    assert '[AUTHORITATIVE]' in bundle.current_input


def test_get_artifact_default_returns_locator_not_body(tmp_path) -> None:
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {}
    ctx = SubAgentContext(
        task_id='task-1',
        conversation_id='conv-1',
        agent_type='test',
        objective='test',
        params={},
        workspace_path=str(tmp_path),
        input_slots=[],
        output_slots=['draft'],
        db=None,
        emit=lambda _event: None,
    )
    ctx.record_local_artifact('draft', 'text', {'text': 'full secret draft\n' * 20}, 1)
    set_context(ctx)
    try:
        result = get_artifact('draft')
        assert result['status'] == 'ok'
        assert 'full secret draft' not in str(result)
        assert result.get('path')
        sliced = get_artifact('draft', start_line=1, end_line=2)
        assert 'full secret draft' in sliced['content']
        assert sliced['total_lines'] > 2
    finally:
        lazyllm.globals['agentic_config'] = previous


def test_workflow_summary_skips_without_artifact_coords() -> None:
    import lazyllm

    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {
        'workflow_session_id': 'wf-1',
        'artifact_coords': [],
        'workflow_step_id': '',
    }
    history = [
        {'role': 'user', 'content': ('old goal\n' * 80), 'history_seq': 1},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{'id': 'c1', 'function': {'name': 'get_artifact'}}],
            'history_seq': 1,
        },
        {
            'role': 'tool',
            'name': 'get_artifact',
            'tool_call_id': 'c1',
            'content': 'locator only',
            'history_seq': 1,
        },
        {'role': 'user', 'content': 'keep me', 'history_seq': 2},
    ]
    budget = build_context_budget(400, reserved_output_tokens=0, target_ratio=0.40)
    try:
        with config.temp('context_compression_enabled', True), \
                config.temp('context_summary_compression_enabled', True), \
                config.temp('context_summary_keep_recent_ratio', 0.05), \
                config.temp('context_summary_min_recent_user_turns', 1):
            _projected, event = apply_summary_compression(
                history,
                budget=budget,
                trigger='pre_turn',
                summarizer=lambda _s, _u: 'should not run',
            )
        assert event.decision == 'abandoned'
        assert event.reason == 'workflow_missing_coords'
        assert resolve_summary_profile() == 'workflow'
    finally:
        lazyllm.globals['agentic_config'] = previous
