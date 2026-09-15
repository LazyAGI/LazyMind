import os
import ntpath
from pathlib import Path

import pytest

from lazyllm.tools import ToolManager
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.tool_runtime import _accesses_conflict
from lazyllm.tools.fs import client as fs_client
from lazymind.chat.engine.tools import chat_artifact, conversation_workspace
from lazyllm.tools.agent.file_tool import write


def test_file_uri_with_windows_drive_becomes_native_path(monkeypatch):
    monkeypatch.setattr(fs_client.os, 'name', 'nt')

    protocol, space_id, path = fs_client._FSRouter()._parse(
        'file:///C:/Users/test/AppData/Roaming/LazyMind/skills',
    )

    assert (protocol, space_id) == ('file', None)
    assert path == r'C:\Users\test\AppData\Roaming\LazyMind\skills'


def test_chat_file_publish_supports_long_filename(tmp_path, monkeypatch):
    agent_root = tmp_path / 'agent-workspace-with-a-realistically-long-name'
    shared_root = tmp_path / 'published-workspace-with-a-realistically-long-name'
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', str(shared_root))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('windows-user', 'windows-conversation'),
    )
    monkeypatch.setattr(chat_artifact, '_write_agent_data', lambda *_args, **_kwargs: None)
    workspace = agent_root / 'chat-artifacts' / 'workspace'
    monkeypatch.setattr(conversation_workspace, 'chat_agent_workspace', lambda *_args: str(workspace))
    workspace.mkdir(parents=True)
    filename = 'technical_requirements_document.md'
    (workspace / filename).write_text('requirements', encoding='utf-8')

    result = chat_artifact.save_chat_artifact(filename, filename, content_type='file')

    published_dir = chat_artifact._published_file_directory(
        'windows-user', 'windows-conversation', result['artifact_id'],
    )
    assert (Path(published_dir) / filename).read_text(encoding='utf-8') == 'requirements'
    assert not [name for name in os.listdir(published_dir) if name.endswith('.tmp')]


def test_published_artifact_path_fits_legacy_windows_limit():
    runtime_root = r'C:\Users\test-user\AppData\Local\LazyMind\runtime\data\subagent'
    filename = 'technical_requirements_document.md'
    generated = ntpath.join(
        runtime_root,
        'chat-artifacts',
        chat_artifact._scope_hash('windows-user'),
        chat_artifact._scope_hash('windows-conversation'),
        '12345678-1234-1234-1234-123456789abc',
        filename,
    )

    assert len(generated) < 260


def test_chat_workspace_reuses_legacy_hash_until_current_exists(tmp_path, monkeypatch):
    monkeypatch.setitem(conversation_workspace._cfg._impl, 'agentic_workspace', str(tmp_path))
    legacy = (
        tmp_path
        / 'chat-artifacts'
        / conversation_workspace._legacy_scope_hash('user-1')
        / conversation_workspace._legacy_scope_hash('conversation-1')
    )
    legacy.mkdir(parents=True)

    assert Path(conversation_workspace.chat_agent_workspace('user-1', 'conversation-1')) == legacy

    current = (
        tmp_path
        / 'chat-artifacts'
        / chat_artifact._scope_hash('user-1')
        / chat_artifact._scope_hash('conversation-1')
    )
    current.mkdir(parents=True)

    assert Path(conversation_workspace.chat_agent_workspace('user-1', 'conversation-1')) == current


def test_filesystem_append_preserves_content(tmp_path):
    from lazyllm.tools.agent.file_tool import read
    target = str(tmp_path / 'document.md')
    write(target, 'first')
    write(target, ' second', mode='append')
    assert read(target)['content'] == 'first second'


@pytest.mark.parametrize('value', [r'C:\Users\a\x.png', 'D:/project/x.pdf', r'\\server\share\x'])
def test_windows_local_path_is_not_interpreted_as_a_uri(value, monkeypatch):
    from lazymind.chat.engine.tools import host_file_resolution as paths
    seen = []
    monkeypatch.setattr(paths, 'canonical_host_path', lambda raw, **_: seen.append(raw) or '/resolved')
    monkeypatch.setattr(paths, 'managed_path', lambda _: True)
    assert paths.FileResolution().local(value) == '/resolved'
    assert seen == [value]


def test_filesystem_prepared_relative_and_absolute_paths_share_resources(tmp_path):
    from lazyllm.tools.agent import FileSystemToolkit
    manager = ToolManager([FileSystemToolkit()])
    batch = manager.prepare_tool_calls([
        {'function': {'name': 'write', 'arguments': {'path': 'document.md', 'content': 'relative'}}},
        {'function': {'name': 'write', 'arguments': {'path': str(tmp_path / 'document.md'), 'content': 'absolute'}}},
        {'function': {'name': 'ls', 'arguments': {'path': '.'}}},
    ], working_directory=str(tmp_path))
    assert batch[0].access.write_keys == batch[1].access.write_keys
    assert _accesses_conflict(batch[2].access, batch[0].access)
