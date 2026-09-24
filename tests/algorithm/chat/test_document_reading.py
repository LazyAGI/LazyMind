import json
from concurrent.futures import ThreadPoolExecutor
from threading import Barrier
from unittest.mock import Mock

import lazyllm
import pytest
import requests
from fastapi import FastAPI
from fastapi.testclient import TestClient
from lazyllm.common.globals import new_session
from lazyllm.tools.fs.client import FS
from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS

from lazymind.chat.api import document_routes, knowledge_search_routes
from lazymind.document_tools import reading
from lazymind.router.api import proxy_routes


def payload(**changes):
    return dict(user_id='user-1', tenant_id='tenant-1', source_id='connection-1', provider='googledrive',
                locator='https://docs.google.com/document/d/doc-1/edit',
                tool_config={'googledrive': 'account-secret'}, **changes)


def read(data=None):
    return reading.read_document(reading.DocumentReadRequest.model_validate(data or payload()))


@pytest.fixture
def drive(monkeypatch):
    fs = GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
    fs._get = Mock(return_value={
        'id': 'doc-1', 'name': '方案', 'mimeType': 'application/vnd.google-apps.document',
        'webViewLink': 'https://docs.google.com/document/d/doc-1/edit',
    })
    fs._request = Mock(return_value=Mock(content='正文\n```\n最后一行'.encode()))
    monkeypatch.setattr(FS, '_get_or_create_fs', Mock(return_value=fs))
    return fs


def test_owner_scoped_connection_allows_reserved_empty_tenant(drive):
    data = payload()
    data['tenant_id'] = ''
    result = read(data)
    assert result.document_id == 'doc-1'
    assert result.version != read().version


def test_drive_pagination_reassembles_exact_content_and_rejects_changes(drive):
    full = read()
    first = read(payload(limit=7))
    pages = [first.content]
    current = first
    while current.next_offset is not None:
        current = read(payload(offset=current.next_offset, limit=7, expected_version=first.version))
        pages.append(current.content)
    assert ''.join(pages) == full.content
    assert full.title == '方案'
    assert full.original_format == 'text'
    assert full.document_id == 'doc-1'
    assert full.source_url == payload()['locator']
    assert full.warnings
    assert 'account-secret' not in full.model_dump_json()
    assert 'artifact' not in full.model_dump_json()
    for changes in ({'source_id': 'other'}, {'user_id': 'other'}, {'tenant_id': 'other'}):
        data = payload(offset=7, expected_version=first.version)
        data.update(changes)
        with pytest.raises(reading.DocumentReadError, match='changed'):
            read(data)
    drive._request.return_value.content = b'changed'
    with pytest.raises(reading.DocumentReadError) as error:
        read(payload(offset=7, expected_version=first.version))
    assert error.value.code == 'VERSION_CHANGED'


@pytest.mark.parametrize('changes', [
    {'tool_config': {}}, {'tool_config': {'notion': 'wrong'}},
    {'tool_config': {'googledrive': 'a', 'notion': 'b'}},
    {'locator': '/tmp/private'}, {'locator': 'https://www.notion.so/page'},
    {'offset': 1},
])
def test_invalid_requests_never_read(drive, changes):
    data = payload()
    data.update(changes)
    with pytest.raises(reading.DocumentReadError):
        read(data)
    drive._get.assert_not_called()


def test_session_restored_and_cleared_on_success_and_error(drive, monkeypatch):
    captured_sessions = []
    original_clear = lazyllm.globals.clear

    def clear():
        captured_sessions.append(lazyllm.globals._sid)
        original_clear()

    monkeypatch.setattr(lazyllm.globals, 'clear', clear)
    with new_session('caller-session'):
        lazyllm.globals.config['dynamic_fs_auth'] = {'notion': 'outer-secret'}
        read()
        drive._get.side_effect = RuntimeError('private token and native body')
        with pytest.raises(reading.DocumentReadError) as error:
            read()
        assert 'private token' not in str(error.value)
        assert lazyllm.globals._sid == 'caller-session'
        assert lazyllm.globals.config['dynamic_fs_auth'] == {'notion': 'outer-secret'}
        assert len(set(captured_sessions)) == 2
        assert all(s.startswith('document-read-') for s in captured_sessions)


@pytest.mark.parametrize('same_user', [True, False])
def test_shared_fs_builds_headers_from_each_concurrent_request(monkeypatch, same_user):
    fs = GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
    barrier = Barrier(2)

    def request(method, url, **kwargs):
        secret = kwargs['headers']['Authorization']
        barrier.wait(timeout=5)
        response = requests.Response()
        response.status_code = 200
        response._content_consumed = True
        if url.endswith('/export'):
            response._content = secret.encode()
        else:
            response._content = json.dumps({
                'id': 'doc-1', 'name': secret, 'mimeType': 'application/vnd.google-apps.document',
            }).encode()
        return response

    monkeypatch.setattr(fs._session, 'request', request)
    # Exercise the real shared FS router and credential header construction.
    monkeypatch.setattr(FS, '_instances', {('googledrive', None): fs})
    inputs = []
    for i in range(2):
        data = payload()
        data.update(user_id='same' if same_user else f'user-{i}', source_id=f'connection-{i}',
                    tool_config={'googledrive': f'credential-{i}'})
        inputs.append(data)
    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(read, inputs))
    for i, result in enumerate(results):
        assert result.title == f'Bearer credential-{i}'
        assert f'credential-{i}' in result.content
        assert f'credential-{1-i}' not in result.content


@pytest.mark.parametrize('provider', ['feishu', 'notion'])
def test_structured_providers_use_existing_ir_export(monkeypatch, provider):
    fs = Mock()
    data = payload()
    data.update(provider=provider, tool_config={provider: 'one-token'})
    if provider == 'feishu':
        data['locator'] = 'https://example.feishu.cn/docx/doc-1'
        fs.get_document_id.return_value = 'doc-1'
        fs.get_document_metadata.return_value = {'title': '方案', 'revision_id': 1}
        fs.get_doc_blocks.return_value = [
            {'block_id': 'doc-1', 'block_type': 1, 'children': ['p-1'],
             'page': {'elements': [{'text_run': {'content': '方案'}}]}},
            {'block_id': 'p-1', 'parent_id': 'doc-1', 'block_type': 2,
             'text': {'elements': [{'text_run': {'content': '正文'}}]}},
        ]
    else:
        data['locator'] = 'https://www.notion.so/0123456789abcdef0123456789abcdef'
        fs.get_document_metadata.return_value = {
            'object_type': 'page', 'document_id': 'doc-1', 'title': '方案', 'browser_url': data['locator'],
        }
        fs.get_doc_blocks.return_value = [{
            'id': 'p-1', 'type': 'paragraph',
            'paragraph': {'rich_text': [{'type': 'text', 'text': {'content': '正文'}, 'plain_text': '正文'}]},
        }]
    monkeypatch.setattr(FS, '_get_or_create_fs', Mock(return_value=fs))
    result = read(data)
    assert result.title == '方案'
    assert '正文' in result.content
    assert result.document_id == 'doc-1'
    assert result.source_url == data['locator']


def test_content_limit_returns_error_without_silent_truncation(drive, monkeypatch):
    monkeypatch.setattr(reading, 'MAX_CONTENT_CHARS', 3)
    with pytest.raises(reading.DocumentReadError) as error:
        read()
    assert error.value.code == 'CONTENT_TOO_LARGE'


def test_internal_route_auth_validation_errors_and_real_service(drive, monkeypatch):
    async def in_process(operation, request):
        return reading.read_document(request)

    monkeypatch.setattr(document_routes, 'run_document_operation', in_process)
    monkeypatch.setattr(knowledge_search_routes, 'expected_internal_token', lambda: 'internal-secret')
    app = FastAPI()
    app.include_router(document_routes.router)
    with TestClient(app) as client:
        assert client.post('/internal/documents:read', json=payload()).status_code == 401
        headers = {'X-LazyMind-Internal-Token': 'internal-secret'}
        invalid = payload()
        invalid['tool_config'] = {'googledrive': ['sensitive-token-1', 'sensitive-token-2']}
        response = client.post('/internal/documents:read', headers=headers, json=invalid)
        assert response.status_code == 422
        assert 'sensitive-token' not in response.text
        response = client.post('/internal/documents:read', headers=headers, json=payload())
        assert response.status_code == 200
        assert response.json()['title'] == '方案'
        drive._get.side_effect = PermissionError('secret-platform-body')
        response = client.post('/internal/documents:read', headers=headers, json=payload())
        assert response.status_code == 403
        assert response.json()['detail']['code'] == 'ACCESS_DENIED'
        assert 'secret-platform-body' not in response.text


def test_router_forwards_document_route_with_internal_auth(monkeypatch):
    import httpx
    captured = {}
    monkeypatch.setattr(knowledge_search_routes, 'expected_internal_token', lambda: 'internal-secret')
    monkeypatch.setattr(proxy_routes, 'expected_internal_token', lambda: 'internal-secret')

    async def select(algo):
        return 'default', Mock(url='http://child.test', host='child')

    class Client:
        def __init__(self, **kwargs):
            pass

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            pass

        async def request(self, method, url, **kwargs):
            captured.update(url=url, **kwargs)
            return httpx.Response(409, json={'detail': {'code': 'VERSION_CHANGED'}})

    monkeypatch.setattr(proxy_routes, '_select_instance', select)
    monkeypatch.setattr(proxy_routes.httpx, 'AsyncClient', Client)
    app = FastAPI()
    app.include_router(proxy_routes.router)
    with TestClient(app) as client:
        assert client.post('/internal/documents:read', json=payload()).status_code == 401
        response = client.post('/internal/documents:read', json=payload(),
                               headers={'X-LazyMind-Internal-Token': 'internal-secret'})
    assert response.status_code == 409
    assert captured['url'] == 'http://child.test/internal/documents:read'
    assert json.loads(captured['content']) == payload()
    assert captured['headers']['X-LazyMind-Internal-Token'] == 'internal-secret'


def test_route_timeout_is_sanitized(monkeypatch):
    import asyncio
    monkeypatch.setattr(knowledge_search_routes, 'expected_internal_token', lambda: 'internal-secret')
    monkeypatch.setattr(document_routes, 'READ_WAIT_SECONDS', 0.001)

    async def slow_thread(*args):
        await asyncio.sleep(10)

    monkeypatch.setattr(document_routes, 'run_document_operation', slow_thread)
    app = FastAPI()
    app.include_router(document_routes.router)
    with TestClient(app) as client:
        response = client.post('/internal/documents:read', json=payload(),
                               headers={'X-LazyMind-Internal-Token': 'internal-secret'})
    assert response.status_code == 504
    assert response.json()['detail']['code'] == 'TIMEOUT'
    assert 'account-secret' not in response.text


@pytest.mark.parametrize(('status', 'code'), [
    (400, 'INVALID_ARGUMENT'), (401, 'ACCESS_DENIED'), (403, 'ACCESS_DENIED'),
    (404, 'NOT_FOUND'), (422, 'INVALID_ARGUMENT'), (429, 'RATE_LIMITED'), (500, 'PROVIDER_UNAVAILABLE'),
])
def test_provider_http_errors_are_sanitized(drive, status, code):
    response = requests.Response()
    response.status_code = status
    drive._get.side_effect = requests.HTTPError('sensitive native response', response=response)
    with pytest.raises(reading.DocumentReadError) as error:
        read()
    assert error.value.code == code
    assert 'sensitive' not in str(error.value)


@pytest.mark.parametrize('provider_name', ['feishu', 'notion'])
@pytest.mark.parametrize('same_user', [True, False])
def test_structured_shared_fs_concurrent_reads_and_denied_account(monkeypatch, provider_name, same_user):
    from lazyllm.tools.fs import FeishuWikiFS, NotionFS
    # All requests share the same FS instance, while each resolves its own dynamic token.
    fs = (FeishuWikiFS(space_id='dynamic', dynamic_auth=True, skip_instance_cache=True)
          if provider_name == 'feishu' else NotionFS(dynamic_auth=True, skip_instance_cache=True))
    document_id = '01234567-89ab-cdef-0123-456789abcdef'
    barrier = Barrier(2)

    def send(method, url, **kwargs):
        credential = kwargs['headers']['Authorization']
        response = requests.Response()
        response._content_consumed = True
        response.status_code = 403 if credential.endswith('denied') else 200
        if response.status_code == 403:
            response._content = b'{}'
            return response
        barrier.wait(timeout=5)
        if provider_name == 'feishu':
            if url.endswith('/children'):
                data = {'data': {'items': [
                    {'block_id': document_id, 'block_type': 1, 'children': ['p-1'],
                     'page': {'elements': [{'text_run': {'content': credential}}]}},
                    {'block_id': 'p-1', 'parent_id': document_id, 'block_type': 2,
                     'text': {'elements': [{'text_run': {'content': credential}}]}},
                ], 'has_more': False}}
            else:
                data = {'data': {'document': {'document_id': document_id, 'title': credential, 'revision_id': 1}}}
        elif '/children' in url:
            data = {'results': [{'id': 'p-1', 'type': 'paragraph', 'has_children': False,
                                 'paragraph': {'rich_text': [{'type': 'text', 'text': {'content': credential},
                                                              'plain_text': credential}]}}], 'has_more': False}
        else:
            data = {'id': document_id, 'object': 'page', 'url': f'https://notion.so/{document_id}',
                    'properties': {'title': {'type': 'title', 'title': [{'type': 'text',
                                   'text': {'content': credential}, 'plain_text': credential}]}}}
        response._content = json.dumps(data).encode()
        return response

    monkeypatch.setattr(fs._session, 'request', send)
    monkeypatch.setattr(FS, '_instances', {(provider_name, 'dynamic'): fs})
    data = payload()
    data.update(provider=provider_name,
                locator=f'{provider_name}:/~{"docx" if provider_name == "feishu" else "page"}/{document_id}')
    inputs = [dict(data, user_id='same' if same_user else f'user-{i}', source_id=f'connection-{i}',
                   tool_config={provider_name: f'credential{i}'}) for i in range(2)]
    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(read, inputs))
    for i, result in enumerate(results):
        assert result.title == f'Bearer credential{i}'
        assert result.content.count(f'credential{i}') >= 2
        assert f'credential{1-i}' not in result.content
    # A later unauthorized account must not receive previously read content from shared state.
    with pytest.raises(reading.DocumentReadError) as error:
        read(dict(data, tool_config={provider_name: 'denied'}))
    assert error.value.code == 'ACCESS_DENIED'
