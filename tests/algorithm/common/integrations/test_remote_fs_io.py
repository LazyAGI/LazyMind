from __future__ import annotations

import base64
import json

import lazyllm
import pytest
import requests

from lazymind.common.integrations.remote_fs import RemoteFS
from lazymind.common.integrations import remote_fs as remote_fs_module


_INTERNAL_HEADERS = {'X-LazyMind-Internal-Token': 'test-internal-token'}


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
    previous_token = remote_fs_module._cfg._impl.get('core_internal_token')
    remote_fs_module._cfg._impl['core_internal_token'] = 'test-internal-token'
    yield
    RemoteFS.clear_instance_cache()
    remote_fs_module._cfg._impl['core_internal_token'] = previous_token
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
    assert calls[0]['headers'] == _INTERNAL_HEADERS
    assert calls[0]['params'] == remote_params(
        path='skills/coding/pkg',
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
        'remote://memory/agents/soul.yaml',
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
    assert calls[0]['headers'] == {
        'Content-Type': 'text/plain; charset=utf-8',
        **_INTERNAL_HEADERS,
    }
    assert calls[1]['params'] == remote_params(path='skills/coding/pkg/assets/logo.png')
    assert calls[1]['data'] == b'\x89PNG'
    assert calls[1]['headers'] == {'Content-Type': 'image/png', **_INTERNAL_HEADERS}


def test_open_write_uses_text_content_type_for_text_mode(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse({'ok': True}))

    with RemoteFS(base_url='http://core').open('remote://skills/coding/pkg/references/doc.md', 'w') as fh:
        fh.write('hello')

    assert calls[0]['data'] == b'hello'
    assert calls[0]['headers'] == {
        'Content-Type': 'text/plain; charset=utf-8',
        **_INTERNAL_HEADERS,
    }


def test_move_calls_remote_fs_move(captured_requests):
    calls, responses = captured_requests
    responses.append(FakeResponse({'ok': True}))

    RemoteFS(base_url='http://core').move(
        'remote://skills/coding/pkg/references/old.md',
        'remote://skills/coding/pkg/references/new.md',
    )

    assert calls[0]['method'] == 'POST'
    assert calls[0]['url'] == 'http://core/remote-fs/move'


def test_revision_id_is_forwarded_for_workflow_reads(captured_requests):
    calls, responses = captured_requests
    responses.extend([
        FakeResponse({'items': []}),
        FakeResponse({'exists': True}),
    ])
    fs = RemoteFS(base_url='http://core')
    fs.ls('remote://workflows/u_abc/my-workflow', revision_id='rev-3')
    assert fs.exists('remote://workflows/u_abc/my-workflow/workflow.yaml', revision_id='rev-3')
    assert calls[0]['params']['revision_id'] == 'rev-3'
    assert calls[1]['params']['revision_id'] == 'rev-3'
    assert calls[0]['params'] == remote_params(
        path='workflows/u_abc/my-workflow',
        revision_id='rev-3',
    )
    assert calls[1]['params'] == remote_params(
        path='workflows/u_abc/my-workflow/workflow.yaml',
        revision_id='rev-3',
    )


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
