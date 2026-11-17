from typing import get_args, get_type_hints
from unittest.mock import patch

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.intent_writer import (
    append_intentwrite_guidance,
    build_intentwrite_tool,
    enable_workflow_intent_scopes,
    normalize_intent_document,
    render_intent_section,
)


def test_consecutive_guidance_extends_user_evidence_without_trusting_model_output():
    tool = build_intentwrite_tool(conversation_id='conv-1', current_query='比较周末旅行')
    append_intentwrite_guidance(tool, ['只比较天津和济南'])
    append_intentwrite_guidance(tool, ['两人预算1500以内'])
    with patch('lazymind.chat.engine.tools.intent_writer._write_agent_data') as write:
        for evidence in ['比较周末旅行', '只比较天津和济南', '两人预算1500以内']:
            tool('conversation', [_operation(value=evidence, evidence=evidence)])
        assert write.call_count == 3
        with pytest.raises(ToolExecutionError, match='evidence'):
            tool('conversation', [_operation(evidence='模型推测的需求')])


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


def test_subagent_current_edit_has_priority_over_original_style():
    from lazymind.chat.engine.subagent.runner import _build_intent_context_section

    section = '\n'.join(_build_intent_context_section({
        'launch_user_input': '生成8页深色科研PPT',
        'current_user_input': '我不喜欢黑底ppt 给我换成 白底黑字',
        'retry_hint': '只修改现有8页的配色，保留内容和讲稿',
        'runtime_instruction': '使用当前工作区',
    }))

    assert '生成8页深色科研PPT' in section
    assert '保留内容和讲稿' in section
    assert section.endswith('我不喜欢黑底ppt 给我换成 白底黑字')
    assert 'overrides conflicting original requirements' in section


@pytest.mark.parametrize('query', [
    '把这篇论文加入已选资料库：Test Paper。请尝试一次导入；未匹配就停止。',
    '将BERT论文导入知识库',
    '将 Attention Is All You Need 加入已选资料库',
    '将 Always Learning 加入资料库',
    '把这篇论文加入默认资料库',
    '将论文加入资料库；不要修改后续研究计划',
    '把《未来研究计划》这篇论文加入资料库',
    '将论文加入资料库，不要从现在起每次都这样做',
    '把这些文献添加到文献库',
    'Import this paper into the selected library; stop if no match.',
])
def test_one_off_paper_import_omits_intent_tool(query):
    from lazymind.chat.engine.tools.intent_writer import intent_tools_for_query
    tool = build_intentwrite_tool(conversation_id='c', current_query=query)
    assert intent_tools_for_query(tool, query) == []


@pytest.mark.parametrize('query', [
    '以后导入论文时都先确认题录再加入资料库',
    '以后不要自动把论文加入资料库',
    '以后自动把论文加入资料库',
    '今后将找到的论文加入资料库',
    '把论文加入资料库，并将研究目标设为机器学习',
    '把论文加入资料库，并将研究目标设成机器学习',
    '把研究目标改成比较不同论文检索方法',
    '这篇论文加入资料库，同时把后续研究计划改为分析液滴质谱',
    '暂停研究任务',
    '精读资料库里的这篇论文',
    'From now on, always confirm metadata before importing papers into the library.',
])
def test_other_requests_keep_intent_tool(query):
    from lazymind.chat.engine.tools.intent_writer import intent_tools_for_query
    tool = build_intentwrite_tool(conversation_id='c', current_query=query)
    assert intent_tools_for_query(tool, query) == [tool]


@pytest.mark.parametrize('query,omitted', [
    ('将 Attention Is All You Need 加入', True),
    ('把这篇论文导入', True),
    ('编辑研究计划.md文件', False),
    ('把论文标题加入报告文件', False),
    ('把这篇论文加入资料库，同时修改研究目标为机器学习', False),
    ('把这篇论文加入资料库；不要修改后续研究计划，但把研究目标改成机器学习', False),
])
def test_selected_library_import_filter_preserves_editing_and_durable_changes(query, omitted):
    from lazymind.chat.engine.tools.intent_writer import intent_tools_for_query
    tool = object()
    assert intent_tools_for_query(tool, query, has_selected_library=True) == ([] if omitted else [tool])
