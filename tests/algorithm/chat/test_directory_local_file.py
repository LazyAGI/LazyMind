import base64
import os
import socket

from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api import directory_file_routes as files


def test_local_preview_identity_limits_and_read_only(tmp_path, monkeypatch):
    monkeypatch.setattr(files, 'config', {'runtime_mode': 'local'})
    path = tmp_path / '会议 纪要.md'
    path.write_text('# 会议纪要\n内容', encoding='utf-8')
    req = files.LocalFileRequest(path=str(path), namespace=f'{socket.gethostname()}:{os.name}')
    content = path.read_bytes()
    assert base64.b64decode(files.read_local_file(req)['content_base64']) == content
    assert path.read_bytes() == content
    assert 'content_base64' not in files.read_local_file(req.model_copy(update={'metadata_only': True}))
    assert files.read_local_file(req.model_copy(update={'namespace': 'other:nt'}))['error_code'] == 'different_host'
    monkeypatch.setattr(files, 'MAX_PREVIEW_BYTES', 1)
    assert files.read_local_file(req)['error_code'] == 'too_large'
    path.unlink()
    assert files.read_local_file(req)['error_code'] == 'not_found'
    target = tmp_path / 'target.txt'
    target.write_text('private')
    path.symlink_to(target)
    assert files.read_local_file(req)['error_code'] == 'unsupported'


def test_local_preview_requires_internal_auth_and_local_runtime(tmp_path, monkeypatch):
    app = FastAPI()
    app.include_router(files.router)
    client = TestClient(app)
    payload = {'path': str(tmp_path / 'a'), 'namespace': 'host:posix'}
    assert client.post('/api/document-directories/local-file', json=payload).status_code == 401
    app.dependency_overrides[files.require_env_input_service] = lambda: None
    monkeypatch.setattr(files, 'config', {'runtime_mode': 'cloud'})
    assert client.post('/api/document-directories/local-file', json=payload).json()['error_code'] == 'unavailable'
