from typing import get_args, get_type_hints
from unittest.mock import patch

import pytest

from lazymind.chat.engine.tools.intent_writer import (
    build_intentwrite_tool,
    enable_plugin_intent_scopes,
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
    assert 'plugin_session' not in tool.__doc__
    assert 'available_steps' not in tool.__doc__


def test_plugin_manager_extension_changes_scopes_without_listing_steps():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='修改初稿', current_intent={},
    )
    updated = enable_plugin_intent_scopes(
        tool, session_id='ps-1', plugin_id='writer', valid_step_ids=['outline', 'draft'],
    )

    assert updated is tool
    assert get_args(get_type_hints(tool)['scope']) == (
        'conversation', 'plugin_session', 'plugin_step',
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


def test_intentwrite_rejects_non_user_evidence():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='请总结经验', current_intent={},
    )
    with pytest.raises(ValueError, match='evidence'):
        tool('conversation', [_operation(evidence='用户没有说过')])


def test_plugin_step_is_validated_without_exposing_step_list():
    tool = build_intentwrite_tool(
        conversation_id='conv-1', current_query='修改初稿', current_intent={},
    )
    enable_plugin_intent_scopes(
        tool, session_id='ps-1', plugin_id='writer', valid_step_ids=['draft'],
    )
    with pytest.raises(ValueError, match='unknown step_id'):
        tool('plugin_step', [_operation(evidence='初稿')], step_id='unknown')


def test_legacy_intent_is_rendered_as_inherited_constraint():
    normalized = normalize_intent_document({'text': '执行到初稿后确认'})
    rendered = render_intent_section('Conversation Intent', normalized)

    assert normalized['constraints'] == ['执行到初稿后确认']
    assert '执行到初稿后确认' in rendered


def test_chat_plugin_intent_section_excludes_step_intents():
    from lazymind.chat.plugin import plugin_manager

    fake_db = type('FakeDB', (), {
        'get_session_intent': lambda self, session_id: '本次执行到初稿后确认',
        'list_step_intents': lambda self, session_id: {'draft': '初稿不超过500字'},
    })()
    with patch('lazymind.chat.engine.subagent.db.TaskQueryDB', return_value=fake_db):
        section = plugin_manager._build_intent_section('ps-1', step_id='draft')

    assert '本次执行到初稿后确认' in section
    assert '初稿不超过500字' not in section


def test_subagent_receives_conversation_session_and_current_step_intent():
    from lazymind.chat.engine.subagent.runner import _build_intent_context_section

    fake_db = type('FakeDB', (), {
        'get_conversation_intent': lambda self, conversation_id: '只总结经验',
        'get_session_intent': lambda self, session_id: '执行到初稿后确认',
        'get_step_intent': lambda self, session_id, step_id: '初稿不超过500字',
    })()
    lines = _build_intent_context_section(fake_db, 'conv-1', 'ps-1', 'draft')
    section = '\n'.join(lines)

    assert '只总结经验' in section
    assert '执行到初稿后确认' in section
    assert '初稿不超过500字' in section
