from typing import get_args, get_type_hints
from unittest.mock import patch

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.intent_writer import (
    build_intentwrite_tool,
    enable_workflow_intent_scopes,
    normalize_intent_document,
    render_intent_section,
)


def _operation(**overrides):
    value = {
        'op': 'set',
        'field': 'goal',
        'value': '总结经验',
        'evidence': '总结经验',
    }
    value.update(overrides)
    return value


def test_conversation_writer_exposes_only_conversation_scope():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='请总结经验', current_intent={},
    )

    assert get_args(get_type_hints(tool)['scope']) == ('conversation',)
    assert 'workflow_session' not in tool.__doc__
    assert 'available_steps' not in tool.__doc__


def test_workflow_manager_extension_changes_scopes_without_listing_steps():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='修改初稿', current_intent={},
    )
    updated = enable_workflow_intent_scopes(
        tool, session_id='ws-1', workflow_id='writer', valid_step_ids=['outline', 'draft'],
    )

    assert updated is tool
    assert get_args(get_type_hints(tool)['scope']) == (
        'conversation', 'workflow_session', 'workflow_step',
    )
    assert 'available_steps' not in tool.__doc__
    assert 'outline' not in tool.__doc__
    assert 'draft' not in tool.__doc__


def test_intentwrite_emits_atomic_patch_with_current_evidence():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='后面只总结经验，不要执行', current_intent={},
    )
    with patch('lazymind.chat.engine.tools.intent_writer._write_agent_data') as write:
        result = tool('conversation', [
            _operation(evidence='总结经验'),
            _operation(op='set', field='execution_mode', value='analysis_only', evidence='不要执行'),
        ])

    assert result == 'Intent updated for conversation.'
    payload = write.call_args.kwargs
    assert payload['scope'] == 'conversation'
    assert len(payload['operations']) == 2


def test_intentwrite_normalizes_singular_constraint_field():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='知识底座已经基本完成', current_intent={},
    )
    with patch('lazymind.chat.engine.tools.intent_writer._write_agent_data') as write:
        result = tool('conversation', [
            _operation(
                op='add',
                field='constraint',
                value='知识底座已基本完成',
                evidence='知识底座已经基本完成',
            ),
        ])

    assert result == 'Intent updated for conversation.'
    assert write.call_args.kwargs['operations'][0]['field'] == 'constraints'


def test_intentwrite_rejects_non_user_evidence():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='请总结经验', current_intent={},
    )
    with pytest.raises(ToolExecutionError, match='evidence'):
        tool('conversation', [_operation(evidence='用户没有说过')])


def test_workflow_step_is_validated_without_exposing_step_list():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='修改初稿', current_intent={},
    )
    enable_workflow_intent_scopes(
        tool, session_id='ws-1', workflow_id='writer', valid_step_ids=['draft'],
    )
    with pytest.raises(ToolExecutionError, match='unknown step_id'):
        tool('workflow_step', [_operation(evidence='初稿')], step_id='unknown')


def test_legacy_intent_is_rendered_as_inherited_constraint():
    normalized = normalize_intent_document({'text': '执行到初稿后确认'})
    rendered = render_intent_section('Conversation Intent', normalized)

    assert normalized['constraints'] == ['执行到初稿后确认']
    assert '执行到初稿后确认' in rendered


def test_chat_workflow_manager_does_not_rebuild_runtime_intent_from_db():
    from lazymind.chat.workflow import workflow_manager

    assert not hasattr(workflow_manager, '_build_intent_section')


def test_subagent_receives_frozen_runtime_instruction():
    from lazymind.chat.engine.subagent.runner import _build_intent_context_section

    lines = _build_intent_context_section({
        'runtime_instruction': '执行到初稿后确认；初稿不超过500字',
    })
    section = '\n'.join(lines)

    assert '执行到初稿后确认' in section
    assert '初稿不超过500字' in section
