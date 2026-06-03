import asyncio
import importlib
import runpy
import sys
import types
from pathlib import Path

import pytest


class _FakeUploadFile:
    def __init__(self, filename, content):
        self.filename = filename
        self._content = content

    async def read(self):
        return self._content


class _FakeRequest:
    def __init__(self, query_params=None):
        self.query_params = query_params or {}


class _FakeResponse:
    def __init__(self, status_code=200, payload=None, text=''):
        self.status_code = status_code
        self._payload = payload or {}
        self.text = text

    def json(self):
        return self._payload


class _BadJsonResponse(_FakeResponse):
    def json(self):
        raise ValueError('bad json')


class _FakeAsyncClient:
    response = _FakeResponse(payload={'data': {'task_id': 'task-1'}})
    posts = []

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc, tb):
        return False

    async def post(self, url, json, timeout):
        self.__class__.posts.append({'url': url, 'json': json, 'timeout': timeout})
        return self.__class__.response


def _fresh_import_upload_handler(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_UPLOAD_DIR', str(tmp_path))
    monkeypatch.setenv('LAZYMIND_DEFAULT_ALGO_ID', 'default-algo')
    monkeypatch.setenv('LAZYMIND_DEFAULT_GROUP', 'default-group')
    monkeypatch.setenv('LAZYMIND_DOCUMENT_PROCESSOR_PORT', '18000')
    multipart = types.ModuleType('multipart')
    multipart.__version__ = '0.0-test'
    multipart_submodule = types.ModuleType('multipart.multipart')
    multipart_submodule.parse_options_header = lambda value: ('multipart/form-data', {})
    monkeypatch.setitem(sys.modules, 'multipart', multipart)
    monkeypatch.setitem(sys.modules, 'multipart.multipart', multipart_submodule)
    sys.modules.pop('lazymind.processor.service.upload_handler', None)
    return importlib.import_module('lazymind.processor.service.upload_handler')


def test_upload_and_add_saves_files_and_posts_add_doc_request(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)
    _FakeAsyncClient.posts = []
    _FakeAsyncClient.response = _FakeResponse(payload={'data': {'task_id': 'task-1'}})
    monkeypatch.setattr(module.httpx, 'AsyncClient', _FakeAsyncClient)
    monkeypatch.setattr(module, 'gen_docid', lambda path: 'doc-' + Path(path).name)

    response = asyncio.run(
        module.upload_and_add(
            _FakeRequest({'group_name': 'query-group', 'algo_id': 'query-algo', 'override': 'false'}),
            [_FakeUploadFile('a.txt', b'alpha')],
            group_name=None,
            algo_id=None,
            override=None,
        )
    )

    saved_file = next(tmp_path.glob('*/a.txt'))
    assert saved_file.read_bytes() == b'alpha'
    assert response.code == 200
    assert response.data == {'task_id': 'task-1', 'ids': ['doc-a.txt']}
    assert _FakeAsyncClient.posts[0]['url'] == 'http://127.0.0.1:18000/doc/add'
    # algo_id is deprecated and omitted from AddDocRequest.
    assert 'algo_id' not in _FakeAsyncClient.posts[0]['json']
    # kb_id is now a top-level field on AddDocRequest
    assert _FakeAsyncClient.posts[0]['json']['kb_id'] == 'query-group'
    # metadata no longer carries a redundant kb_id
    assert not (_FakeAsyncClient.posts[0]['json']['file_infos'][0].get('metadata') or {}).get('kb_id')


def test_upload_and_add_uses_form_values_before_query_params(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)
    _FakeAsyncClient.posts = []
    _FakeAsyncClient.response = _FakeResponse(payload={'data': {'task_id': 'task-2'}})
    monkeypatch.setattr(module.httpx, 'AsyncClient', _FakeAsyncClient)
    monkeypatch.setattr(module, 'gen_docid', lambda path: 'fixed-doc-id')

    response = asyncio.run(
        module.upload_and_add(
            _FakeRequest({'group_name': 'query-group', 'algo_id': 'query-algo'}),
            [_FakeUploadFile('b.txt', b'beta')],
            group_name='form-group',
            algo_id='form-algo',
            override=None,
        )
    )

    assert response.data == {'task_id': 'task-2', 'ids': ['fixed-doc-id']}
    # algo_id is deprecated and omitted from AddDocRequest.
    assert 'algo_id' not in _FakeAsyncClient.posts[0]['json']
    # kb_id is now a top-level field; form-group takes precedence over query-group
    assert _FakeAsyncClient.posts[0]['json']['kb_id'] == 'form-group'
    assert not (_FakeAsyncClient.posts[0]['json']['file_infos'][0].get('metadata') or {}).get('kb_id')


def test_upload_and_add_uses_defaults_and_unnamed_file(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)
    _FakeAsyncClient.posts = []
    _FakeAsyncClient.response = _FakeResponse(payload={'data': {}})
    monkeypatch.setattr(module.httpx, 'AsyncClient', _FakeAsyncClient)
    monkeypatch.setattr(module.uuid, 'uuid4', lambda: 'fixed-subdir')
    monkeypatch.setattr(module, 'gen_docid', lambda path: 'doc-' + Path(path).name)

    response = asyncio.run(
        module.upload_and_add(
            _FakeRequest(),
            [_FakeUploadFile(None, b'content')],
            group_name=None,
            algo_id=None,
            override=None,
        )
    )

    saved_file = tmp_path / 'fixed-subdir' / 'unnamed'
    assert saved_file.read_bytes() == b'content'
    assert response.data == {'task_id': None, 'ids': ['doc-unnamed']}
    # algo_id is deprecated and omitted from AddDocRequest.
    assert 'algo_id' not in _FakeAsyncClient.posts[0]['json']
    # kb_id (= default-group) is now a top-level field on AddDocRequest
    assert _FakeAsyncClient.posts[0]['json']['kb_id'] == 'default-group'
    assert not (_FakeAsyncClient.posts[0]['json']['file_infos'][0].get('metadata') or {}).get('kb_id')


def test_upload_and_add_rejects_missing_files(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)

    with pytest.raises(module.fastapi.HTTPException) as exc_info:
        asyncio.run(module.upload_and_add(_FakeRequest(), [], group_name=None, algo_id=None, override=None))

    assert exc_info.value.status_code == 400
    assert exc_info.value.detail == 'files is required'


def test_upload_and_add_forwards_processor_error(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)
    _FakeAsyncClient.posts = []
    _FakeAsyncClient.response = _FakeResponse(status_code=503, payload={'detail': 'processor unavailable'})
    monkeypatch.setattr(module.httpx, 'AsyncClient', _FakeAsyncClient)

    with pytest.raises(module.fastapi.HTTPException) as exc_info:
        asyncio.run(
            module.upload_and_add(
                _FakeRequest(),
                [_FakeUploadFile('c.txt', b'gamma')],
                group_name=None,
                algo_id=None,
                override=None,
            )
        )

    assert exc_info.value.status_code == 503
    assert exc_info.value.detail == 'processor unavailable'


def test_upload_and_add_uses_response_text_when_error_json_is_invalid(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)
    _FakeAsyncClient.posts = []
    _FakeAsyncClient.response = _BadJsonResponse(status_code=502, text='bad gateway')
    monkeypatch.setattr(module.httpx, 'AsyncClient', _FakeAsyncClient)

    with pytest.raises(module.fastapi.HTTPException) as exc_info:
        asyncio.run(
            module.upload_and_add(
                _FakeRequest(),
                [_FakeUploadFile('e.txt', b'epsilon')],
                group_name=None,
                algo_id=None,
                override=None,
            )
        )

    assert exc_info.value.status_code == 502
    assert exc_info.value.detail == 'bad gateway'


def test_upload_and_add_cleans_saved_files_on_unexpected_error(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)

    class _FailingAsyncClient(_FakeAsyncClient):
        async def post(self, url, json, timeout):
            raise RuntimeError('network down')

    monkeypatch.setattr(module.httpx, 'AsyncClient', _FailingAsyncClient)

    with pytest.raises(module.fastapi.HTTPException) as exc_info:
        asyncio.run(
            module.upload_and_add(
                _FakeRequest(),
                [_FakeUploadFile('d.txt', b'delta')],
                group_name=None,
                algo_id=None,
                override=None,
            )
        )

    assert exc_info.value.status_code == 500
    assert exc_info.value.detail == 'network down'
    assert list(tmp_path.rglob('d.txt')) == []


def test_upload_and_add_ignores_cleanup_remove_errors(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)

    class _FailingAsyncClient(_FakeAsyncClient):
        async def post(self, url, json, timeout):
            raise RuntimeError('network down')

    monkeypatch.setattr(module.httpx, 'AsyncClient', _FailingAsyncClient)
    monkeypatch.setattr(module.os, 'remove', lambda path: (_ for _ in ()).throw(OSError('remove failed')))
    monkeypatch.setattr(module.os, 'rmdir', lambda path: (_ for _ in ()).throw(OSError('rmdir failed')))

    with pytest.raises(module.fastapi.HTTPException) as exc_info:
        asyncio.run(
            module.upload_and_add(
                _FakeRequest(),
                [_FakeUploadFile('f.txt', b'zeta')],
                group_name=None,
                algo_id=None,
                override=None,
            )
        )

    assert exc_info.value.status_code == 500
    assert exc_info.value.detail == 'network down'


def test_run_upload_server_passes_app_host_and_port(monkeypatch, tmp_path):
    module = _fresh_import_upload_handler(monkeypatch, tmp_path)
    seen = {}

    class FakeUvicorn:
        @staticmethod
        def run(app, host, port):
            seen.update({'app': app, 'host': host, 'port': port})

    monkeypatch.setitem(sys.modules, 'uvicorn', FakeUvicorn)

    module.run_upload_server(18099)

    assert seen == {'app': module.app, 'host': '0.0.0.0', 'port': 18099}


def test_upload_handler_main_uses_env_port(monkeypatch, tmp_path):
    seen = {}

    class FakeUvicorn:
        @staticmethod
        def run(app, host, port):
            seen.update({'host': host, 'port': port})

    monkeypatch.setenv('LAZYMIND_UPLOAD_DIR', str(tmp_path))
    monkeypatch.setenv('LAZYMIND_UPLOAD_SERVER_PORT', '18100')
    multipart = types.ModuleType('multipart')
    multipart.__version__ = '0.0-test'
    multipart_submodule = types.ModuleType('multipart.multipart')
    multipart_submodule.parse_options_header = lambda value: ('multipart/form-data', {})
    monkeypatch.setitem(sys.modules, 'multipart', multipart)
    monkeypatch.setitem(sys.modules, 'multipart.multipart', multipart_submodule)
    monkeypatch.setitem(sys.modules, 'uvicorn', FakeUvicorn)
    sys.modules.pop('lazymind.processor.service.upload_handler', None)

    runpy.run_module('lazymind.processor.service.upload_handler', run_name='__main__')

    assert seen == {'host': '0.0.0.0', 'port': 18100}
