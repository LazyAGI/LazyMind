import os
from pathlib import Path

import lazyllm
import pytest

from lazymind.chat.engine.tools.local_file import workspace as chat_artifact
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


def test_save_chat_artifact_emits_downloadable_event(monkeypatch):
    emitted = []
    monkeypatch.setattr(
        chat_artifact,
        '_write_agent_data',
        lambda tag, **payload: emitted.append({'tag': tag, **payload}),
    )

    result = chat_artifact.save_chat_artifact('hello.txt', '你好')

    assert result['success'] is True
    artifact_id = result['result']['artifact_id']
    assert result['result']['file_markdown'] == f'[hello.txt](file_id:{artifact_id})'
    assert 'file_markdown' in (chat_artifact.save_chat_artifact.__doc__ or '')
    assert emitted[0]['tag'] == 'artifact_created'
    assert emitted[0]['filename'] == 'hello.txt'
    assert emitted[0]['value'] == {'text': '你好'}


@pytest.mark.parametrize(
    'filename',
    ['../escape.txt', 'dir/file.txt', r'dir\file.txt', 'bad\u0085.txt', '', '..'],
)
def test_save_chat_artifact_rejects_unsafe_filename(filename):
    with pytest.raises(ValueError):
        chat_artifact.save_chat_artifact(filename, 'x')


def test_save_chat_artifact_file_copies_to_persistent_workspace(tmp_path, monkeypatch):
    agent_workspace_root = tmp_path / 'agent'
    shared_workspace = tmp_path / 'shared'
    monkeypatch.setitem(chat_artifact._cfg._impl, 'agentic_workspace', str(agent_workspace_root))
    agent_workspace = Path(chat_artifact.chat_agent_workspace('user-1', 'conversation-1'))
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

    assert result['success'] is True
    artifact_id = result['result']['artifact_id']
    assert result['result']['file_markdown'] == f'[report.docx](file_id:{artifact_id})'
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
    monkeypatch.setitem(chat_artifact._cfg._impl, 'agentic_workspace', str(agent_workspace))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'),
    )

    with pytest.raises(ValueError, match='inside the current main-Agent workspace'):
        chat_artifact.save_chat_artifact(
            'outside.zip', str(outside), content_type='file',
        )


def test_workspace_file_tools_share_chat_agent_workspace(tmp_path, monkeypatch):
    monkeypatch.setitem(chat_artifact._cfg._impl, 'agentic_workspace', str(tmp_path))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'),
    )

    written = chat_artifact.write_file('bid_output/outline.json', '{"chapters": []}')
    loaded = chat_artifact.read_file('bid_output/outline.json')
    listing = chat_artifact.list_dir('bid_output')

    workspace = Path(chat_artifact.chat_agent_workspace('user-1', 'conversation-1'))
    assert written['status'] == 'ok'
    assert Path(written['path']) == workspace / 'bid_output' / 'outline.json'
    assert '{"chapters": []}' in loaded['result']['text']
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

    monkeypatch.setitem(chat_artifact._cfg._impl, 'agentic_workspace', str(main_root))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('user-1', 'conversation-1'),
    )
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {
        'agent_type': 'workflow_step',
        'workflow_workspace_path': str(workflow_workspace),
    }
    try:
        loaded = chat_artifact.read_file(str(workflow_input))
        rejected = chat_artifact.read_file(str(outside))
    finally:
        lazyllm.globals['agentic_config'] = previous

    assert 'WORKFLOW: FIND_AND_EDIT' in loaded['result']['text']
    assert loaded['result']['kind'] == 'attachment_text'
    assert rejected['success'] is False
    assert 'current main-Agent workspace' in rejected['error']['detail']


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
