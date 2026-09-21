import os
from pathlib import Path

import lazyllm
import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools import chat_artifact, conversation_workspace
from lazymind.chat.engine.tools.file_resources.tools import read_file_resource
from lazyllm.tools.agent.file_tool import write, ls
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


@pytest.mark.parametrize(
    'filename',
    ['../escape.txt', 'dir/file.txt', r'dir\file.txt', 'bad\u0085.txt', '', '..'],
)
def test_save_chat_artifact_rejects_unsafe_filename(filename):
    with pytest.raises(ToolExecutionError):
        chat_artifact.save_chat_artifact(filename, 'x')


def test_save_chat_artifact_file_copies_to_persistent_workspace(tmp_path, monkeypatch):
    agent_workspace_root = tmp_path / 'agent'
    shared_workspace = tmp_path / 'shared'
    monkeypatch.setitem(conversation_workspace._cfg._impl, 'agentic_workspace', str(agent_workspace_root))
    agent_workspace = Path(conversation_workspace.chat_agent_workspace('user-1', 'conversation-1'))
    agent_workspace.mkdir(parents=True)
    source = agent_workspace / 'report.docx'
    source.write_bytes(b'fake-docx')
    emitted = []
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', str(shared_workspace))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'),
    )
    monkeypatch.setattr(
        chat_artifact,
        '_write_agent_data',
        lambda tag, **payload: emitted.append({'tag': tag, **payload}),
    )

    result = chat_artifact.save_chat_artifact(
        'report.docx', 'report.docx', content_type='file', caption='技术方案',
    )

    artifact_id = result['artifact_id']
    assert result['file_markdown'] == f'[report.docx](file_id:{artifact_id})'
    assert emitted[0]['content_type'] == 'file'
    assert emitted[0]['filename'] == 'report.docx'
    published = emitted[0]['value']['path']
    assert os.path.commonpath((str(shared_workspace), published)) == str(shared_workspace)
    assert Path(published).read_bytes() == b'fake-docx'
    assert emitted[0]['value']['size'] == len(b'fake-docx')


def test_save_chat_artifact_file_rejects_source_outside_agent_workspace(
    tmp_path, monkeypatch,
):
    agent_workspace = tmp_path / 'agent'
    agent_workspace.mkdir()
    outside = tmp_path / 'outside.zip'
    outside.write_bytes(b'zip')
    monkeypatch.setitem(conversation_workspace._cfg._impl, 'agentic_workspace', str(agent_workspace))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'),
    )

    with pytest.raises(ToolExecutionError, match='inside the current main-Agent workspace'):
        chat_artifact.save_chat_artifact(
            'outside.zip', str(outside), content_type='file',
        )


def test_workspace_file_tools_share_chat_agent_workspace(tmp_path, monkeypatch):
    monkeypatch.setitem(conversation_workspace._cfg._impl, 'agentic_workspace', str(tmp_path))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'),
    )

    workspace = Path(conversation_workspace.chat_agent_workspace('user-1', 'conversation-1'))
    monkeypatch.setattr(conversation_workspace, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'))
    written = write(str(workspace / 'bid_output/outline.json'), '{"chapters": []}')
    loaded = read_file_resource('bid_output/outline.json')
    listing = ls(str(workspace / 'bid_output'))

    workspace = Path(conversation_workspace.chat_agent_workspace('user-1', 'conversation-1'))
    assert written['status'] == 'ok'
    assert Path(written['path']) == workspace / 'bid_output' / 'outline.json'
    assert '{"chapters": []}' in loaded['text']
    assert listing['entries'] == ['outline.json']



def test_read_file_accepts_only_current_workflow_attempt_workspace(tmp_path, monkeypatch):
    main_root = tmp_path / 'main'
    workflow_workspace = tmp_path / 'workflow' / 'task-1'
    workflow_workspace.mkdir(parents=True)
    workflow_input = workflow_workspace / 'inputs' / 'workflow_routing.json'
    workflow_input.parent.mkdir()
    workflow_input.write_text('{"text":"WORKFLOW: FIND_AND_EDIT"}', encoding='utf-8')
    outside = tmp_path / 'workflow' / 'task-2' / 'secret.txt'
    outside.parent.mkdir(parents=True)
    outside.write_text('secret', encoding='utf-8')

    monkeypatch.setitem(conversation_workspace._cfg._impl, 'agentic_workspace', str(main_root))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'),
    )
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {
        'user_id': 'user-1', 'conversation_id': 'conversation-1',
        'agent_type': 'workflow_step',
        'workflow_workspace_path': str(workflow_workspace),
    }
    try:
        loaded = read_file_resource(str(workflow_input))
        with pytest.raises(ToolExecutionError, match='current main-Agent workspace'):
            read_file_resource(str(outside))
    finally:
        lazyllm.globals['agentic_config'] = previous

    assert 'WORKFLOW: FIND_AND_EDIT' in loaded['text']
    assert loaded['kind'] == 'attachment_text'


def test_artifact_event_translator_preserves_structured_payload():
    translator = AgentEventFrameTranslator(query='创建一个 txt')
    frames = translator.feed({
        'tag': 'artifact_created',
        'artifact_id': 'artifact-1',
        'filename': 'a.txt',
        'content_type': 'text',
        'value': {'text': 'a'},
    })
    assert frames == [{
        'think': None,
        'text': None,
        'sources': [],
        'artifact_created': {
            'artifact_id': 'artifact-1',
            'filename': 'a.txt',
            'content_type': 'text',
            'value': {'text': 'a'},
        },
    }]
