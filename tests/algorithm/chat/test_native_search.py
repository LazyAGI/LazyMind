import json
import subprocess
import sys
from unittest.mock import patch

import pytest
import importlib

from lazyllm.tools.agent.toolsManager import ToolManager
from lazymind.chat.engine.tools import spotlight
native_search = importlib.import_module('lazymind.chat.engine.tools.native_search')


def test_registered_search_declares_read_only(tmp_path):
    from lazyllm.tools.agent import HostFileAccess
    manager = ToolManager([native_search.native_search])
    assert 'native_search' in {item['function']['name'] for item in manager.tools_description}
    call = {'id': 'search', 'function': {'name': 'native_search', 'arguments': {
        'query': 'plan', 'path': str(tmp_path),
    }}}
    prepared = manager.prepare_tool_calls([call])[0]
    assert prepared.ready
    assert prepared.host_file_access is HostFileAccess.DECLARED
    assert [(item.path, item.operation) for item in prepared.host_files] == [(str(tmp_path), 'read')]


def test_search_exposure_follows_host_capability(monkeypatch):
    from lazymind.chat.service.chat_service import _build_chat_artifact_tools
    monkeypatch.setattr(native_search, 'native_search_available', lambda: True)
    assert native_search.native_search in _build_chat_artifact_tools(host_filesystem_enabled=True)
    assert native_search.native_search not in _build_chat_artifact_tools(host_filesystem_enabled=False)
    monkeypatch.setattr(native_search, 'native_search_available', lambda: False)
    assert native_search.native_search not in _build_chat_artifact_tools(host_filesystem_enabled=True)


_POPEN = subprocess.Popen


def _mock_index(monkeypatch, paths, exit_code=0):
    real = _POPEN
    commands = []

    def run(command, **kwargs):
        commands.append(command)
        return real([sys.executable, '-c',
                     f'import os; os.write(1, {bytes(chr(0).join(map(str, paths)), "utf-8")!r} + b"\\0"); '
                     f'raise SystemExit({exit_code})'], **kwargs)
    monkeypatch.setattr(spotlight.subprocess, 'Popen', run)
    monkeypatch.setattr(spotlight.sys, 'platform', 'darwin')
    monkeypatch.setattr(spotlight, '_index_available', lambda _: True)
    return commands


def test_system_scope_accepts_outside_home_and_deduplicates(monkeypatch, tmp_path):
    home = tmp_path / 'home'
    home.mkdir()
    document = tmp_path / '中文 plan.txt'
    document.write_text('test')
    monkeypatch.setattr(spotlight.Path, 'home', lambda: home)
    commands = _mock_index(monkeypatch, [document, document])
    result = spotlight.search_spotlight('plan')
    assert result['status'] == 'ok'
    assert result['scope'] is None
    assert len(result['results']) == 1
    assert result['results'][0]['path'] == str(document)
    assert result['results'][0]['size'] == 4
    assert '-onlyin' not in commands[0]


def test_macos_failure_is_not_empty_success(monkeypatch):
    _mock_index(monkeypatch, [], 1)
    result = spotlight.search_spotlight('plan')
    assert result['status'] == 'error'
    assert result['stop_reason'] == 'search_error'


def test_unavailable(monkeypatch):
    monkeypatch.setattr(native_search, 'native_search_available', lambda: False)
    assert native_search.native_search('plan')['status'] == 'unavailable'


@pytest.mark.parametrize('kwargs', [{'limit': True}, {'limit': 101}, {'match': 'sql'},
                                    {'query': 'x" OR 1=1'}, {'kind': 'email'}, {'path': 1}])
def test_invalid_input_never_starts_process(kwargs):
    with patch.object(native_search.subprocess, 'Popen') as process:
        with pytest.raises(ValueError):
            native_search.native_search(**({'query': 'plan'} | kwargs))
        process.assert_not_called()


def test_windows_data_transport_and_partial_results(monkeypatch, tmp_path):
    hit = tmp_path / '中文.txt'
    hit.write_text('x')
    monkeypatch.setattr(native_search.sys, 'platform', 'win32')
    monkeypatch.setattr(native_search, 'native_search_available', lambda: True)
    monkeypatch.setattr(native_search.shutil, 'which', lambda _: 'powershell.exe')
    calls = []

    def output(command, payload):
        calls.append((command, json.loads(payload)))
        return json.dumps({'path': str(hit), 'title': hit.name}).encode() + b'\n{"path":', 'timeout'
    monkeypatch.setattr(native_search, '_windows_output', output)
    result = native_search.native_search("O'Brien", path=str(tmp_path))
    assert result['status'] == 'partial'
    assert result['results'][0]['path'] == str(hit)
    assert "O'Brien" not in ' '.join(calls[0][0])
    assert calls[0][1]['query'] == "O'Brien"


def test_bounded_subprocess_timeout(monkeypatch):
    monkeypatch.setattr(native_search, '_TIMEOUT', 0.05)
    raw, reason = native_search._windows_output(
        [sys.executable, '-c', 'import time; time.sleep(5)'], b'{}')
    assert reason == 'timeout'
    assert raw == b''


def test_bounded_subprocess_output(monkeypatch):
    monkeypatch.setattr(native_search, '_MAX_OUTPUT', 8192)
    _, reason = native_search._windows_output(
        [sys.executable, '-c', 'import os; os.write(1, b"x"*100000)'], b'{}')
    assert reason == 'output_limit'


def test_scope_limit_and_zero_results(monkeypatch, tmp_path):
    scope = tmp_path / 'scope'
    scope.mkdir()
    files = [scope / 'first.txt', scope / 'second.txt', tmp_path / 'outside.txt']
    for path in files:
        path.write_text('text')
    commands = _mock_index(monkeypatch, files[::-1])
    result = spotlight.search_spotlight('text', path=str(scope), limit=1)
    assert result['status'] == 'partial'
    assert result['stop_reason'] == 'result_limit'
    assert result['results'][0]['path'] == str(files[1])
    assert commands[0][1:4] == ['-0', '-onlyin', str(scope)]
    _mock_index(monkeypatch, [])
    result = spotlight.search_spotlight('absent')
    assert result['status'] == 'ok' and result['results'] == []


def test_disabled_index_is_not_zero_hits(monkeypatch):
    monkeypatch.setattr(spotlight.sys, 'platform', 'darwin')
    monkeypatch.setattr(spotlight, '_index_available', lambda _: False)
    with patch.object(spotlight.subprocess, 'Popen') as process:
        result = spotlight.search_spotlight('plan')
    process.assert_not_called()
    assert result['status'] == 'unavailable'
    assert result['stop_reason'] == 'index_unavailable'
