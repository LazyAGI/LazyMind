import json

import pytest

from lazymind.common.integrations.remote_fs import RemoteFS


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

    monkeypatch.setattr('lazymind.common.integrations.remote_fs.requests.request', fake_request)
    monkeypatch.setattr(
        'lazymind.common.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'user_id': 'user-1', 'session_id': 'sid-1'}},
    )

    fs = RemoteFS(base_url='http://core:8000', timeout=3)

    assert fs.ls('remote://skills') == [{'name': 'remote://skills/a', 'path': 'skills/a', 'type': 'dir', 'size': 0}]
    assert fs.info('skills/a/SKILL.md')['size'] == 4
    assert fs.exists('skills/a/SKILL.md') is True
    assert fs.open('skills/a/SKILL.md', 'rb').read() == b'test'
    assert calls == [
        ('GET', 'http://core:8000/remote-fs/list', {'path': 'skills', 'user_id': 'user-1', 'task_id': 'sid-1'}, 3, None),
        (
            'GET', 'http://core:8000/remote-fs/info',
            {'path': 'skills/a/SKILL.md', 'user_id': 'user-1', 'task_id': 'sid-1'}, 3, None,
        ),
        (
            'GET', 'http://core:8000/remote-fs/exists',
            {'path': 'skills/a/SKILL.md', 'user_id': 'user-1', 'task_id': 'sid-1'}, 3, None,
        ),
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

    monkeypatch.setattr('lazymind.common.integrations.remote_fs.requests.request', fake_request)
    monkeypatch.setattr(
        'lazymind.common.integrations.remote_fs.lazyllm.globals', {'agentic_config': {}},
    )

    fs = RemoteFS(base_url='http://core:8000', timeout=3)

    assert fs.exists('skills/a/SKILL.md') is False
    assert calls == [
        ('GET', 'http://core:8000/remote-fs/exists', {'path': 'skills/a/SKILL.md'}, 3),
    ]


def test_remote_fs_keeps_absolute_memory_list_paths(monkeypatch):
    def fake_request(method, url, params, timeout, **kwargs):
        assert method == 'GET'
        assert url.endswith('/remote-fs/list')
        return _Response({
            'items': [
                {
                    'name': 'references',
                    'path': 'memory/users/references',
                    'type': 'dir',
                },
            ],
        })

    monkeypatch.setattr('lazymind.common.integrations.remote_fs.requests.request', fake_request)
    monkeypatch.setattr(
        'lazymind.common.integrations.remote_fs.lazyllm.globals',
        {'agentic_config': {'user_id': 'user-1'}},
    )

    result = RemoteFS(base_url='http://core:8000').ls('memory/users')

    assert result == [{
        'name': 'remote://memory/users/references',
        'path': 'memory/users/references',
        'type': 'dir',
    }]


def test_remote_fs_write_mkdir_rm_and_trash_use_core_api(monkeypatch):
    calls = []

    def fake_request(method, url, params, timeout, **kwargs):
        calls.append((method, url, params, timeout, kwargs.get('data'), kwargs.get('json')))
        return _Response({'ok': True})

    monkeypatch.setattr('lazymind.common.integrations.remote_fs.requests.request', fake_request)
    monkeypatch.setattr(
        'lazymind.common.integrations.remote_fs.lazyllm.globals',
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
def materialize_requests(monkeypatch):
    import lazymind.common.integrations.remote_fs as remote_fs_module

    calls, responses = [], []
    monkeypatch.setattr(remote_fs_module.lazyllm, 'globals', {
        'agentic_config': {'user_id': 'user-1', 'session_id': 'session-1'},
    })

    def request(method, url, **kwargs):
        calls.append({'method': method, 'url': url, **kwargs})
        assert responses, 'unexpected remote-fs request'
        return responses.pop(0)

    monkeypatch.setattr(remote_fs_module.requests, 'request', request)
    RemoteFS.clear_instance_cache()
    yield calls, responses
    RemoteFS.clear_instance_cache()


def test_materialize_dir_recursively_downloads_files(materialize_requests, tmp_path):
    calls, responses = materialize_requests
    responses.extend([
        _Response({
            'items': [
                {'name': 'skills/coding/pkg/SKILL.md', 'type': 'file', 'size': 12},
                {'name': 'skills/coding/pkg/scripts', 'type': 'directory', 'size': 0},
            ],
        }),
        _Response(content=b'---\nname: pkg\n---\nBody\n'),
        _Response({
            'items': [
                {'name': 'skills/coding/pkg/scripts/check.py', 'type': 'file', 'size': 12},
            ],
        }),
        _Response(content=b'print("ok")\n'),
    ])

    result = RemoteFS(base_url='http://core').materialize_dir(
        'remote://skills/coding/pkg',
        str(tmp_path),
    )

    assert result['materialized'] is True
    assert result['files'] == ['SKILL.md', 'scripts/check.py']
    assert (tmp_path / 'SKILL.md').read_text(encoding='utf-8') == '---\nname: pkg\n---\nBody\n'
    assert (tmp_path / 'scripts' / 'check.py').read_text(encoding='utf-8') == 'print("ok")\n'
    assert [call['params']['path'] for call in calls] == [
        'skills/coding/pkg',
        'skills/coding/pkg/SKILL.md',
        'skills/coding/pkg/scripts',
        'skills/coding/pkg/scripts/check.py',
    ]
    assert (tmp_path / 'scripts').is_dir()


def test_materialize_dir_rejects_paths_outside_local_dir(materialize_requests, tmp_path):
    remote_name = 'skills/coding/pkg/../escape.py'
    calls, responses = materialize_requests
    responses.append(_Response({
        'items': [
            {'name': remote_name, 'type': 'file', 'size': 12},
        ],
    }))

    with pytest.raises(RuntimeError, match='invalid relative path'):
        RemoteFS(base_url='http://core').materialize_dir(
            'remote://skills/coding/pkg',
            str(tmp_path),
        )

    assert len(calls) == 1
    assert not (tmp_path.parent / 'escape.py').exists()
