import os
import socket
import subprocess
import sys
import time

import pytest
import requests
from lazyllm.tools.agent.skill_manager import SkillManager as LazySkillManager
from lazyllm.tools.fs.client import FS

from lazymind.chat.engine.tools.skill_manager import list_all_skill_entries
from lazymind.chat.integrations.remote_fs import RemoteFS
from lazymind.config import config as algo_config


class _Response:
    def __init__(self, payload=None, content=b''):
        self._payload = payload or {}
        self.content = content

    def raise_for_status(self):
        return None

    def json(self):
        return self._payload


def test_remote_fs_uses_core_readonly_api(monkeypatch):
    calls = []

    def fake_get(url, params, timeout):
        calls.append((url, params, timeout))
        if url.endswith('/list'):
            return _Response({'code': 0, 'data': {'entries': [{'name': 'skills/a', 'type': 'directory', 'size': 0}]}})
        if url.endswith('/info'):
            return _Response({'code': 0, 'data': {'name': 'skills/a/SKILL.md', 'type': 'file', 'size': 4}})
        if url.endswith('/exists'):
            return _Response({'code': 0, 'data': {'exists': True}})
        if url.endswith('/content'):
            return _Response(content=b'test')
        raise AssertionError(url)

    monkeypatch.setattr('lazymind.chat.integrations.remote_fs.requests.get', fake_get)
    monkeypatch.setattr(
        'lazymind.chat.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'session_id': 'sid-1'}},
    )

    fs = RemoteFS(base_url='http://core:8000', timeout=3)

    assert fs.ls('remote://skills') == [{'name': 'remote://skills/a', 'type': 'directory', 'size': 0}]
    assert fs.info('skills/a/SKILL.md')['size'] == 4
    assert fs.exists('skills/a/SKILL.md') is True
    assert fs.open('skills/a/SKILL.md', 'rb').read() == b'test'
    assert calls == [
        ('http://core:8000/remote-fs/list', {'path': 'skills', 'detail': 'true', 'session_id': 'sid-1'}, 3),
        ('http://core:8000/remote-fs/info', {'path': 'skills/a/SKILL.md', 'session_id': 'sid-1'}, 3),
        ('http://core:8000/remote-fs/exists', {'path': 'skills/a/SKILL.md', 'session_id': 'sid-1'}, 3),
        ('http://core:8000/remote-fs/content', {'path': 'skills/a/SKILL.md', 'session_id': 'sid-1'}, 3),
    ]


def test_remote_fs_reads_core_api_url_from_model_config(monkeypatch):
    del monkeypatch

    with algo_config.temp('core_api_url', 'http://inner-core:9000'):
        fs = RemoteFS()

        assert fs.base_url == 'http://inner-core:9000'


def test_remote_fs_omits_session_id_when_not_available(monkeypatch):
    calls = []

    def fake_get(url, params, timeout):
        calls.append((url, params, timeout))
        return _Response({'code': 0, 'data': {'exists': False}})

    monkeypatch.setattr('lazymind.chat.integrations.remote_fs.requests.get', fake_get)
    monkeypatch.setattr('lazymind.chat.integrations.remote_fs.lazyllm.globals', {'agentic_config': {}})

    fs = RemoteFS(base_url='http://core:8000', timeout=3)

    assert fs.exists('skills/a/SKILL.md') is False
    assert calls == [
        ('http://core:8000/remote-fs/exists', {'path': 'skills/a/SKILL.md'}, 3),
    ]


@pytest.fixture
def mock_remote_fs_server(tmp_path):
    FS._instances.clear()
    RemoteFS.clear_instance_cache()
    sock = socket.socket()
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
    sock.close()

    env = os.environ.copy()
    env.pop('http_proxy', None)
    env.pop('https_proxy', None)
    env.pop('HTTP_PROXY', None)
    env.pop('HTTPS_PROXY', None)

    proc = subprocess.Popen(
        [
            sys.executable,
            'scripts/mock_remote_fs_server.py',
            '--host',
            '127.0.0.1',
            '--port',
            str(port),
            '--root',
            str(tmp_path),
            '--seed-demo-data',
        ],
        cwd=os.getcwd(),
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )

    base_url = f'http://127.0.0.1:{port}'
    deadline = time.time() + 10
    last_error = None
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f'mock remote fs server exited early with code {proc.returncode}')
        try:
            response = requests.get(
                f'{base_url}/remote-fs/exists',
                params={'path': 'skills', 'session_id': 'sid-demo'},
                timeout=0.5,
            )
            if response.ok:
                break
        except requests.RequestException as exc:
            last_error = exc
        time.sleep(0.1)
    else:
        proc.terminate()
        proc.wait(timeout=5)
        raise RuntimeError(f'mock remote fs server did not become ready: {last_error}')

    try:
        yield base_url
    finally:
        FS._instances.clear()
        RemoteFS.clear_instance_cache()
        proc.terminate()
        proc.wait(timeout=5)


def test_skill_manager_lists_remote_skills_from_mock_server(monkeypatch, mock_remote_fs_server):
    FS._instances.clear()
    monkeypatch.setattr(
        'lazymind.chat.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'session_id': 'sid-demo'}},
    )
    with algo_config.temp('core_api_url', mock_remote_fs_server):
        assert list_all_skill_entries('remote://skills') == {
            'writing/example': {
                'name': 'example',
                'category': 'writing',
                'path': 'remote://skills/writing/example',
                'source': 'remote',
            },
            'ops/deploy-checklist': {
                'name': 'deploy-checklist',
                'category': 'ops',
                'path': 'remote://skills/ops/deploy-checklist',
                'source': 'remote',
            },
        }


def test_skill_manager_reads_reference_from_remote_mock_server(monkeypatch, mock_remote_fs_server):
    FS._instances.clear()
    monkeypatch.setattr(
        'lazymind.chat.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'session_id': 'sid-demo'}},
    )
    with algo_config.temp('core_api_url', mock_remote_fs_server):
        manager = LazySkillManager(dir='remote://skills', fs=FS)

        skill_doc = manager.get_skill('example')
        reference_doc = manager.read_reference('example', 'references/examples/daily-update.md')

        assert skill_doc['status'] == 'ok'
        assert 'Example Skill' in skill_doc['content']
        assert '(source: remote,' in manager.build_prompt()
        assert reference_doc == {
            'status': 'ok',
            'path': 'remote://skills/writing/example/references/examples/daily-update.md',
            'content': '# Daily Update\n\nCompleted API integration and validated the smoke test.\n',
        }
