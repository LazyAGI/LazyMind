from __future__ import annotations

import subprocess

import lazymind.chat.engine.tools.local_fs as local_fs_mod
from lazymind.chat.engine.tools.local_fs import LocalFSToolGroup
from lazymind.chat.service.chat_service import _normalize_localfs_paths


def _set_localfs_paths(monkeypatch, paths):
    monkeypatch.setattr(local_fs_mod.lazyllm, 'globals', {
        'agentic_config': {'localfs_paths': paths},
    })


def test_local_fs_key_source_is_empty_without_config(monkeypatch):
    monkeypatch.setattr(local_fs_mod.lazyllm, 'globals', {})

    assert LocalFSToolGroup().__key_source__() == []


def test_normalize_localfs_paths_resolves_strings_and_lists(tmp_path):
    root = tmp_path / 'root'
    root.mkdir()

    assert _normalize_localfs_paths(str(root)) == [str(root.resolve())]
    assert _normalize_localfs_paths(['', str(root)]) == [str(root.resolve())]
    assert _normalize_localfs_paths(None) == []


def test_local_fs_rejects_symlink_escape(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    outside = tmp_path / 'outside'
    allowed.mkdir()
    outside.mkdir()
    secret = outside / 'secret.txt'
    secret.write_text('secret', encoding='utf-8')
    link = allowed / 'link.txt'
    link.symlink_to(secret)
    _set_localfs_paths(monkeypatch, [str(allowed.resolve())])

    result = LocalFSToolGroup().read(str(link))

    assert result['success'] is False
    assert result['tool'] == 'read'
    assert result['error']['type'] == 'PermissionError'


def test_local_fs_read_keeps_pagination_behavior(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    target = allowed / 'sample.txt'
    target.write_text('a\nb\nc\nd\n', encoding='utf-8')
    _set_localfs_paths(monkeypatch, [str(allowed.resolve())])

    result = LocalFSToolGroup().read(str(target), start_line=1, max_lines=2)

    assert result['success'] is True
    assert result['result']['total_lines'] == 4
    assert result['result']['start_line'] == 1
    assert result['result']['end_line'] == 3
    assert result['result']['content'] == 'b\nc\n'


def test_local_fs_glob_returns_tool_error_when_rg_fails(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    _set_localfs_paths(monkeypatch, [str(allowed.resolve())])
    monkeypatch.setattr(LocalFSToolGroup, '_has_rg', staticmethod(lambda: True))
    monkeypatch.setattr(
        LocalFSToolGroup,
        '_run_rg',
        staticmethod(lambda _args, cwd: subprocess.CompletedProcess([], 2, '', 'bad glob')),
    )

    result = LocalFSToolGroup().glob('[')

    assert result['success'] is False
    assert result['tool'] == 'glob'
    assert 'ripgrep glob failed' in result['error']['reason']


def test_local_fs_grep_returns_tool_error_when_rg_fails(monkeypatch, tmp_path):
    allowed = tmp_path / 'allowed'
    allowed.mkdir()
    _set_localfs_paths(monkeypatch, [str(allowed.resolve())])
    monkeypatch.setattr(LocalFSToolGroup, '_has_rg', staticmethod(lambda: True))
    monkeypatch.setattr(
        LocalFSToolGroup,
        '_run_rg',
        staticmethod(lambda _args, cwd: subprocess.CompletedProcess([], 2, '', 'bad regex')),
    )

    result = LocalFSToolGroup().grep('[')

    assert result['success'] is False
    assert result['tool'] == 'grep'
    assert 'ripgrep search failed' in result['error']['reason']
