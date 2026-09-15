import os
import ntpath
from pathlib import Path

import pytest

from lazyllm.tools import ToolManager
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.tool_runtime import _accesses_conflict
from lazyllm.tools.fs import client as fs_client
from lazymind.chat.engine.tools.local_file import workspace as chat_artifact


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
    monkeypatch.setattr(chat_artifact, 'chat_agent_workspace', lambda *_args: str(workspace))
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
    monkeypatch.setitem(chat_artifact._cfg._impl, 'agentic_workspace', str(tmp_path))
    legacy = (
        tmp_path
        / 'chat-artifacts'
        / chat_artifact._legacy_scope_hash('user-1')
        / chat_artifact._legacy_scope_hash('conversation-1')
    )
    legacy.mkdir(parents=True)

    assert Path(chat_artifact.chat_agent_workspace('user-1', 'conversation-1')) == legacy

    current = (
        tmp_path
        / 'chat-artifacts'
        / chat_artifact._scope_hash('user-1')
        / chat_artifact._scope_hash('conversation-1')
    )
    current.mkdir(parents=True)

    assert Path(chat_artifact.chat_agent_workspace('user-1', 'conversation-1')) == current


def test_chat_write_file_append_does_not_require_overwrite_approval(tmp_path, monkeypatch):
    monkeypatch.setattr(chat_artifact, 'chat_agent_workspace', lambda *_args: str(tmp_path))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('windows-user', 'windows-conversation'),
    )

    first = chat_artifact.write_file('document.md', 'first')
    appended = chat_artifact.write_file('document.md', ' second', mode='append')

    assert first['status'] == 'ok'
    assert appended['status'] == 'ok'
    assert 'first second' in chat_artifact.read_file('document.md')['text']


def test_chat_file_runtime_access_uses_resolved_workspace_paths(tmp_path, monkeypatch):
    monkeypatch.setattr(chat_artifact, 'chat_agent_workspace', lambda *_args: str(tmp_path))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('windows-user', 'windows-conversation'),
    )
    manager = ToolManager([
        chat_artifact.write_file,
        chat_artifact.list_dir,
        chat_artifact.read_file,
        chat_artifact.grep,
    ])
    prepared = []
    manager.execute_with_records([
        {'function': {'name': 'write_file', 'arguments': {
            'path': 'document.md', 'content': 'relative',
        }}},
        {'function': {'name': 'write_file', 'arguments': {
            'path': str(tmp_path / 'document.md'), 'content': 'absolute',
        }}},
        {'function': {'name': 'list_dir', 'arguments': {'path': '.'}}},
        {'function': {'name': 'read_file', 'arguments': {'target': 'document.md'}}},
        {'function': {'name': 'grep', 'arguments': {
            'target': 'document.md', 'pattern': 'relative',
        }}},
    ], dispatch_selector=lambda calls: prepared.extend(calls) or ())

    assert prepared[0].access.write_keys == prepared[1].access.write_keys
    assert _accesses_conflict(prepared[2].access, prepared[0].access)
    assert prepared[3].access.exclusive is True
    assert prepared[4].access.exclusive is True


def test_chat_file_tools_reject_outside_workspace_by_default(tmp_path, monkeypatch):
    workspace = tmp_path / 'workspace'
    outside = tmp_path / 'outside' / 'document.md'
    monkeypatch.setattr(chat_artifact, 'chat_agent_workspace', lambda *_args: str(workspace))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('windows-user', 'windows-conversation'),
    )

    with pytest.raises(ToolExecutionError, match='inside the current main-Agent workspace'):
        chat_artifact.write_file(str(outside), 'blocked')


def test_chat_file_tools_allow_absolute_host_paths_in_trusted_local_mode(tmp_path, monkeypatch):
    workspace = tmp_path / 'workspace'
    outside_dir = tmp_path / 'outside'
    outside = outside_dir / 'document.md'
    monkeypatch.setattr(chat_artifact, 'chat_agent_workspace', lambda *_args: str(workspace))
    monkeypatch.setattr(
        chat_artifact, '_current_artifact_scope', lambda: ('windows-user', 'windows-conversation'),
    )

    with chat_artifact._cfg.temp('trusted_local_mode', True):
        written = chat_artifact.write_file(str(outside), 'trusted')
        loaded = chat_artifact.read_file(str(outside))
        listed = chat_artifact.list_dir(str(outside_dir))

    assert written['status'] == 'ok'
    assert 'trusted' in loaded['text']
    assert listed['entries'] == ['document.md']
