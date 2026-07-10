import json
import os
import socket
import subprocess
import sys
import time

import pytest
import requests
from lazyllm.tools.agent.skill_manager import SkillManager as LazySkillManager
from lazyllm.tools.fs.client import FS

from lazymind.chat.integrations.remote_fs import RemoteFS
from lazymind.config import config as algo_config


class _Response:
    def __init__(self, payload=None, content=b''):
        self._payload = payload or {}
        self.content = content if content else json.dumps(self._payload).encode()

    def raise_for_status(self):
        return None

    def json(self):
        return self._payload


def test_remote_fs_uses_core_readonly_api(monkeypatch):
    calls = []

    def fake_request(method, url, params, timeout, **kwargs):
        calls.append((method, url, params, timeout, kwargs.get('data')))
        if url.endswith('/list'):
            return _Response({'items': [{'name': 'a', 'path': 'skills/a', 'type': 'dir', 'size': 0}]})
        if url.endswith('/info'):
            return _Response({'path': 'skills/a/SKILL.md', 'type': 'file', 'size': 4})
        if url.endswith('/exists'):
            return _Response({'exists': True})
        if url.endswith('/content'):
            return _Response(content=b'test')
        raise AssertionError(url)

    monkeypatch.setattr('lazymind.chat.integrations.remote_fs.requests.request', fake_request)
    monkeypatch.setattr(
        'lazymind.chat.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'user_id': 'user-1', 'session_id': 'sid-1'}},
    )

    fs = RemoteFS(base_url='http://core:8000', timeout=3)

    assert fs.ls('remote://skills') == [{'name': 'remote://skills/a', 'path': 'skills/a', 'type': 'dir', 'size': 0}]
    assert fs.info('skills/a/SKILL.md')['size'] == 4
    assert fs.exists('skills/a/SKILL.md') is True
    assert fs.open('skills/a/SKILL.md', 'rb').read() == b'test'
    assert calls == [
        ('GET', 'http://core:8000/remote-fs/list', {'path': 'skills', 'user_id': 'user-1', 'task_id': 'sid-1'}, 3, None),
        ('GET', 'http://core:8000/remote-fs/info', {'path': 'skills/a/SKILL.md', 'user_id': 'user-1', 'task_id': 'sid-1'}, 3, None),
        ('GET', 'http://core:8000/remote-fs/exists', {'path': 'skills/a/SKILL.md', 'user_id': 'user-1', 'task_id': 'sid-1'}, 3, None),
        (
            'GET',
            'http://core:8000/remote-fs/content',
            {'path': 'skills/a/SKILL.md', 'encoding': 'raw', 'user_id': 'user-1', 'task_id': 'sid-1'},
            3,
            None,
        ),
    ]


def test_remote_fs_omits_session_id_when_not_available(monkeypatch):
    calls = []

    def fake_request(method, url, params, timeout, **kwargs):
        calls.append((method, url, params, timeout))
        return _Response({'exists': False})

    monkeypatch.setattr('lazymind.chat.integrations.remote_fs.requests.request', fake_request)
    monkeypatch.setattr('lazymind.chat.integrations.remote_fs.lazyllm.globals', {'agentic_config': {}})

    fs = RemoteFS(base_url='http://core:8000', timeout=3)

    assert fs.exists('skills/a/SKILL.md') is False
    assert calls == [
        ('GET', 'http://core:8000/remote-fs/exists', {'path': 'skills/a/SKILL.md'}, 3),
    ]


def test_remote_fs_write_mkdir_rm_and_trash_use_core_api(monkeypatch):
    calls = []

    def fake_request(method, url, params, timeout, **kwargs):
        calls.append((method, url, params, timeout, kwargs.get('data'), kwargs.get('json')))
        return _Response({'ok': True})

    monkeypatch.setattr('lazymind.chat.integrations.remote_fs.requests.request', fake_request)
    monkeypatch.setattr(
        'lazymind.chat.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'user_id': 'user-1', 'session_id': 'sid-1'}},
    )

    fs = RemoteFS(base_url='http://core:8000', timeout=3)
    fs.mkdir('remote://skills/a/b', create_parents=True)
    fs.write('remote://skills/a/b/SKILL.md', 'hello')
    fs.rm('remote://skills/a/b', recursive=True)
    fs.trash('remote://skills/a/b')

    assert calls == [
        (
            'POST',
            'http://core:8000/remote-fs/dir',
            {'user_id': 'user-1', 'task_id': 'sid-1'},
            3,
            None,
            {'path': 'skills/a/b', 'recursive': True},
        ),
        (
            'PUT',
            'http://core:8000/remote-fs/content',
            {'path': 'skills/a/b/SKILL.md', 'user_id': 'user-1', 'task_id': 'sid-1'},
            3,
            b'hello',
            None,
        ),
        (
            'DELETE',
            'http://core:8000/remote-fs/path',
            {'path': 'skills/a/b', 'recursive': 'true', 'user_id': 'user-1', 'task_id': 'sid-1'},
            3,
            None,
            None,
        ),
        (
            'POST',
            'http://core:8000/remote-fs/trash',
            {'user_id': 'user-1', 'task_id': 'sid-1'},
            3,
            None,
            {'path': 'skills/a/b'},
        ),
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
                params={'path': 'skills', 'user_id': 'user-demo', 'task_id': 'sid-demo'},
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
        {'agentic_config': {'user_id': 'user-demo', 'session_id': 'sid-demo'}},
    )
    with algo_config.temp('core_api_url', mock_remote_fs_server):
        manager = LazySkillManager(dir='remote://skills', fs=FS)

        skill_files = dict(manager._iter_skill_files())
        assert skill_files['remote://skills/writing/example'] == 'remote://skills/writing/example/SKILL.md'
        assert skill_files['remote://skills/ops/deploy-checklist'] == (
            'remote://skills/ops/deploy-checklist/SKILL.md'
        )

        example_content = manager._fs_read(skill_files['remote://skills/writing/example'])
        example_meta = manager._extract_yaml_meta(example_content)
        deploy_content = manager._fs_read(skill_files['remote://skills/ops/deploy-checklist'])
        deploy_meta = manager._extract_yaml_meta(deploy_content)

        assert example_meta['name'] == 'example'
        assert example_meta['category'] == 'writing'
        assert 'Example Skill' in example_content
        assert deploy_meta['name'] == 'deploy-checklist'
        assert deploy_meta['category'] == 'ops'


def test_skill_manager_reads_reference_from_remote_mock_server(monkeypatch, mock_remote_fs_server):
    FS._instances.clear()
    monkeypatch.setattr(
        'lazymind.chat.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'user_id': 'user-demo', 'session_id': 'sid-demo'}},
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
