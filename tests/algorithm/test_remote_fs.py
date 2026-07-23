import json

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
