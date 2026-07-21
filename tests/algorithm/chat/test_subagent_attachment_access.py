from __future__ import annotations

from types import SimpleNamespace

from lazymind.chat.engine.subagent import SUBAGENT_ATTACHMENT_CONTEXT_KEY
from lazymind.chat.engine.subagent.context import SubAgentContext
import lazymind.chat.engine.subagent.runner as runner
import lazymind.chat.engine.subagent.tools as attachment_tools
import lazymind.chat.engine.tools.subagent_chat_tools as subagent_chat_tools


def test_create_subagent_snapshots_parent_attachment_context(monkeypatch):
    emitted = []
    monkeypatch.setattr(subagent_chat_tools, '_agentic_config', lambda: {
        'mode': 'manual',
        'conversation_id': 'conversation-1',
        'user_id': 'user-1',
        'files': ['/uploads/current.txt'],
        'history_files_per_turn': {
            '1': ['/uploads/older.txt'],
            '2': ['/uploads/current.txt'],
        },
    })
    monkeypatch.setattr(
        subagent_chat_tools,
        '_write_agent_data',
        lambda tag, **payload: emitted.append((tag, payload)),
    )

    subagent_chat_tools.create_subagent(
        agent_type='document_edit',
        title='edit attachment',
        objective='translate and replace one paragraph',
        tools=['string_replace'],
    )

    context = emitted[0][1]['params'][SUBAGENT_ATTACHMENT_CONTEXT_KEY]
    assert context == {
        'files': ['/uploads/current.txt'],
        'history_files_per_turn': {
            '1': ['/uploads/older.txt'],
            '2': ['/uploads/current.txt'],
        },
        'user_id': 'user-1',
        'conversation_id': 'conversation-1',
    }


def test_runner_restores_attachment_context_for_ordinary_subagent():
    params = {
        SUBAGENT_ATTACHMENT_CONTEXT_KEY: {
            'files': ['/uploads/current.txt'],
            'history_files_per_turn': {'2': ['/uploads/current.txt']},
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
        },
    }

    config = runner._build_agentic_config(
        {
            'conversation_id': 'conversation-1',
            'objective': 'edit current.txt',
        },
        params,
        'document_edit',
    )

    assert config['files'] == ['/uploads/current.txt']
    assert config['history_files_per_turn'] == {'2': ['/uploads/current.txt']}
    assert config['user_id'] == 'user-1'
    assert config['conversation_id'] == 'conversation-1'
    assert config['is_subagent'] is True


def test_subagent_plan_renders_inherited_attachments_without_exposing_internal_params(tmp_path):
    attachment = tmp_path / 'current.txt'
    attachment.write_text('content', encoding='utf-8')
    ctx = SubAgentContext(
        task_id='task-1',
        conversation_id='conversation-1',
        agent_type='document_edit',
        objective='edit current.txt',
        params={
            SUBAGENT_ATTACHMENT_CONTEXT_KEY: {
                'files': [str(attachment)],
                'history_files_per_turn': {'2': [str(attachment)]},
                'user_id': 'user-1',
                'conversation_id': 'conversation-1',
            },
        },
        workspace_path=str(tmp_path / 'workspace'),
        input_slots=[],
        output_slots=[],
        db=None,
        emit=lambda event: None,
    )

    plan = runner._build_subagent_plan(
        ctx,
        None,
        tools=[],
        tool_prompt_appendices={},
    )
    sections = {section.section_id: section.content for section in plan.prompt.sections}

    assert 'current.txt' in sections['subagent_attachments']
    assert SUBAGENT_ATTACHMENT_CONTEXT_KEY not in sections.get('subagent_parameters', '')


def test_subagent_registers_attachment_tools_as_one_conditional_group():
    attachment_configs = [
        *runner.USER_ATTACHMENT_TOOL_CONFIGS,
        runner.ATTACHMENT_EDIT_TOOL_CONFIG,
    ]

    without_attachments = {
        tool.__name__ for tool in runner._build_subagent_tools([])
    }
    with_attachments = {
        tool.__name__
        for tool in runner._build_subagent_tools([], attachment_configs)
    }

    attachment_names = {'find_user_attachment', 'read_user_attachment', 'string_replace'}
    assert not attachment_names & without_attachments
    assert attachment_names <= with_attachments
    assert runner._resolve_runtime_tools(['string_replace']) == []


def test_subagent_attachment_edit_publishes_through_task_artifact(monkeypatch, tmp_path):
    draft_path = tmp_path / 'edited.txt'
    draft_path.write_text('translated', encoding='utf-8')
    draft = SimpleNamespace(filename='source.txt', draft_path=str(draft_path))
    saved = {}

    monkeypatch.setattr(
        attachment_tools,
        'get_context',
        lambda: SimpleNamespace(output_slots=['translated_file']),
    )

    def fake_save(key, value, content_type='text', **kwargs):
        saved.update(
            key=key,
            value=value,
            content_type=content_type,
            source_tool=kwargs.get('source_tool'),
        )
        return {'success': True, 'result': {'status': 'ok'}}

    monkeypatch.setattr(attachment_tools, 'save_artifact', fake_save)

    result = attachment_tools._publish_attachment_edit(draft)

    assert result['artifact_key'] == 'translated_file'
    assert result['filename'] == 'source.txt'
    assert saved == {
        'key': 'translated_file',
        'value': str(draft_path),
        'content_type': 'file',
        'source_tool': 'string_replace',
    }
