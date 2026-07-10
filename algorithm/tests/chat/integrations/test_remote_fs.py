from __future__ import annotations

import base64
import json
import os

import lazyllm
import pytest
import requests

from lazymind.chat.integrations.remote_fs import RemoteFS


class FakeResponse:
    def __init__(self, json_data=None, content: bytes = b'', status_code: int = 200, text: str = ''):
        self._json_data = json_data
        self.content = content if content else json.dumps(json_data).encode()
        self.status_code = status_code
        self.text = text

    def json(self):
        if self._json_data is None:
            raise ValueError('no json')
        return self._json_data

    def raise_for_status(self):
        if self.status_code >= 400:
            raise requests.HTTPError(f'{self.status_code} error', response=self)


@pytest.fixture(autouse=True)
def agentic_config():
    previous = lazyllm.globals.get('agentic_config')
    RemoteFS.clear_instance_cache()
    lazyllm.globals['agentic_config'] = {'user_id': 'user-1', 'task_id': 'task-1', 'session_id': 'session-1'}
    yield
    RemoteFS.clear_instance_cache()
    if previous is None:
        lazyllm.globals.pop('agentic_config', None)
    else:
        lazyllm.globals['agentic_config'] = previous


@pytest.fixture
def captured_requests(monkeypatch):
    calls = []
    responses = []

    def fake_request(method, url, **kwargs):
        calls.append({'method': method, 'url': url, **kwargs})
        if not responses:
            raise AssertionError('unexpected request')
        return responses.pop(0)

    monkeypatch.setattr(requests, 'request', fake_request)
    return calls, responses


def remote_params(**params):
    return {**params, 'user_id': 'user-1', 'task_id': 'session-1'}


def test_ls_qualifies_names_and_sends_user_and_task_id(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse({
        'items': [
            {'name': 'skills/coding/pkg/SKILL.md', 'type': 'file', 'size': 12},
        ],
    }))

    result = RemoteFS(base_url='http://core').ls('remote://skills/coding/pkg')

    assert result[0]['name'] == 'remote://skills/coding/pkg/SKILL.md'
    assert calls[0]['method'] == 'GET'
    assert calls[0]['url'] == 'http://core/remote-fs/list'
    assert calls[0]['params'] == remote_params(
        path='skills/coding/pkg',
    )


def test_open_binary_reads_raw_content(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse(content=b'\x89PNG\r\n'))

    with RemoteFS(base_url='http://core').open('remote://skills/coding/pkg/assets/logo.png', 'rb') as fh:
        assert fh.read() == b'\x89PNG\r\n'

    assert calls[0]['method'] == 'GET'
    assert calls[0]['url'] == 'http://core/remote-fs/content'
    assert calls[0]['params'] == remote_params(
        path='skills/coding/pkg/assets/logo.png',
        encoding='raw',
    )


def test_open_text_decodes_raw_content(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse(content='你好\n'.encode('utf-8')))

    with RemoteFS(base_url='http://core').open('remote://skills/coding/pkg/references/doc.md', 'r') as fh:
        assert fh.read() == '你好\n'

    assert calls[0]['params']['encoding'] == 'raw'


def test_open_text_honors_decode_errors_option(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse(content=b'\xffbroken'))

    with RemoteFS(base_url='http://core').open(
        'remote://memory/memory.md',
        'r',
        encoding='utf-8',
        errors='replace',
    ) as fh:
        assert fh.read() == '\ufffdbroken'

    assert calls[0]['params']['encoding'] == 'raw'


def test_write_and_write_file_send_raw_body_with_content_type(captured_requests):
    calls, responses = captured_requests
    responses.extend([
        FakeResponse({'ok': True}),
        FakeResponse({'ok': True}),
    ])
    fs = RemoteFS(base_url='http://core')

    fs.write('remote://skills/coding/pkg/SKILL.md', 'body')
    fs.write_file('remote://skills/coding/pkg/assets/logo.png', b'\x89PNG', content_type='image/png')

    assert calls[0]['method'] == 'PUT'
    assert calls[0]['url'] == 'http://core/remote-fs/content'
    assert calls[0]['params'] == remote_params(path='skills/coding/pkg/SKILL.md')
    assert calls[0]['data'] == b'body'
    assert calls[0]['headers'] == {'Content-Type': 'text/plain; charset=utf-8'}
    assert calls[1]['params'] == remote_params(path='skills/coding/pkg/assets/logo.png')
    assert calls[1]['data'] == b'\x89PNG'
    assert calls[1]['headers'] == {'Content-Type': 'image/png'}


def test_open_write_uses_text_content_type_for_text_mode(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse({'ok': True}))

    with RemoteFS(base_url='http://core').open('remote://skills/coding/pkg/references/doc.md', 'w') as fh:
        fh.write('hello')

    assert calls[0]['data'] == b'hello'
    assert calls[0]['headers'] == {'Content-Type': 'text/plain; charset=utf-8'}


def test_move_calls_remote_fs_move(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse({'ok': True}))

    RemoteFS(base_url='http://core').move(
        'remote://skills/coding/pkg/references/old.md',
        'remote://skills/coding/pkg/references/new.md',
    )

    assert calls[0]['method'] == 'POST'
    assert calls[0]['url'] == 'http://core/remote-fs/move'
    assert calls[0]['params'] == remote_params()
    assert calls[0]['json'] == {
        'from': 'skills/coding/pkg/references/old.md',
        'to': 'skills/coding/pkg/references/new.md',
    }


def test_read_base64_decodes_json_content(captured_requests):
    calls, responses = captured_requests
    encoded = base64.b64encode(b'binary-data').decode('ascii')
    responses.append(FakeResponse({
        'encoding': 'base64',
        'content': encoded,
    }))

    assert RemoteFS(base_url='http://core').read_base64('remote://skills/coding/pkg/assets/blob.bin') == b'binary-data'
    assert calls[0]['params'] == remote_params(
        path='skills/coding/pkg/assets/blob.bin',
        encoding='base64',
    )


def test_materialize_dir_recursively_downloads_files(captured_requests, tmp_path):
    calls, responses = captured_requests
    responses.extend([
        FakeResponse({
            'items': [
                {'name': 'skills/coding/pkg/SKILL.md', 'type': 'file', 'size': 12},
                {'name': 'skills/coding/pkg/scripts', 'type': 'directory', 'size': 0},
            ],
        }),
        FakeResponse(content=b'---\nname: pkg\n---\nBody\n'),
        FakeResponse({
            'items': [
                {'name': 'skills/coding/pkg/scripts/check.py', 'type': 'file', 'size': 12},
            ],
        }),
        FakeResponse(content=b'print("ok")\n'),
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
    assert os.path.isdir(tmp_path / 'scripts')


@pytest.mark.parametrize(
    'remote_name',
    [
        'skills/coding/pkg/..',
        'skills/coding/pkg/../escape.py',
        'skills/coding/pkg/..\\escape.py',
    ],
)
def test_materialize_dir_rejects_paths_outside_local_dir(captured_requests, tmp_path, remote_name):
    calls, responses = captured_requests
    responses.append(FakeResponse({
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


def test_task_id_falls_back_to_session_id(captured_requests):
    calls, responses = captured_requests
    lazyllm.globals['agentic_config'] = {'user_id': 'user-1', 'session_id': 'session-fallback'}
    responses.append(FakeResponse({'exists': True}))

    assert RemoteFS(base_url='http://core').exists('remote://skills/coding/pkg/SKILL.md') is True
    assert calls[0]['params'] == {
        'path': 'skills/coding/pkg/SKILL.md',
        'user_id': 'user-1',
        'task_id': 'session-fallback',
    }


def test_task_id_falls_back_to_explicit_task_id(captured_requests):
    calls, responses = captured_requests
    lazyllm.globals['agentic_config'] = {'user_id': 'user-1', 'task_id': 'task-fallback'}
    responses.append(FakeResponse({'exists': True}))

    assert RemoteFS(base_url='http://core').exists('remote://skills/coding/pkg/SKILL.md') is True
    assert calls[0]['params'] == {
        'path': 'skills/coding/pkg/SKILL.md',
        'user_id': 'user-1',
        'task_id': 'task-fallback',
    }


def test_json_error_message_is_preserved(captured_requests):
    _calls, responses = captured_requests
    responses.append(FakeResponse({'message': 'draft is pending'}, status_code=409))

    with pytest.raises(RuntimeError, match='draft is pending'):
        RemoteFS(base_url='http://core').write('remote://skills/coding/pkg/SKILL.md', 'body')
