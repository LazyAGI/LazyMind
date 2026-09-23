import asyncio
from unittest.mock import AsyncMock, Mock

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from lazyllm.tools.fs import FS, GoogleDriveFS

from lazymind.chat.api import document_routes, knowledge_search_routes
from lazymind.document_tools import discovery, reading, read_execution


def request(**kwargs):
    return discovery.DocumentDiscoveryRequest(
        user_id='user', tenant_id='tenant', source_id='connection', provider='googledrive',
        tool_config={'googledrive': 'secret'}, **kwargs)


def test_browse_continuation_then_read_real_provider(monkeypatch):
    fs = GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
    entry = {'id': 'doc-1', 'name': '方案', 'mimeType': 'application/vnd.google-apps.document',
             'webViewLink': 'https://docs.google.com/document/d/doc-1/edit'}
    fs._get = Mock(side_effect=[
        {'files': [], 'nextPageToken': 'next'},
        {'files': [entry, {'id': 'folder-1', 'name': '目录', 'mimeType': 'application/vnd.google-apps.folder'}]},
        entry, entry,
    ])
    fs._request = Mock(return_value=Mock(content='正文'.encode()))
    monkeypatch.setattr(FS, '_get_or_create_fs', Mock(return_value=fs))
    first = discovery.browse_documents(request())
    assert not first.items and first.next_page_token
    for changes in ({'source_id': 'other'}, {'folder_id': 'other'}, {'page_size': 1}):
        req = request(page_token=first.next_page_token).model_copy(update=changes)
        with pytest.raises(reading.DocumentReadError, match='page token'):
            discovery.browse_documents(req)
    second = discovery.browse_documents(request(page_token=first.next_page_token))
    assert second.next_page_token is None
    assert second.items[1].read_locator is None and second.items[1].folder_id == 'folder-1'
    result = reading.read_document(reading.DocumentReadRequest(
        **request().model_dump(include={'user_id', 'tenant_id', 'source_id', 'provider', 'tool_config'}),
        locator=second.items[0].read_locator,
    ))
    assert result.title == '方案' and '正文' in result.content
    assert fs._get.call_args_list[0].kwargs['params']['q'] == "trashed = false and 'root' in parents"
    assert fs._get.call_args_list[1].kwargs['params']['pageToken'] == 'next'


@pytest.mark.parametrize('operation', ['browse', 'search'])
def test_discovery_route_auth_scoping_and_warning(monkeypatch, operation):
    fs = GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
    fs._get = Mock(return_value={'files': [], 'incompleteSearch': True})
    monkeypatch.setattr(FS, '_get_or_create_fs', Mock(return_value=fs))
    monkeypatch.setattr(knowledge_search_routes, 'expected_internal_token', lambda: 'internal')

    async def in_process(op, req):
        return getattr(discovery, f'{op}_documents')(req)

    monkeypatch.setattr(document_routes, 'run_document_operation', in_process)
    app = FastAPI()
    app.include_router(document_routes.router)
    data = request(query='plan' if operation == 'search' else '', drive_id='drive-1').model_dump(mode='json')
    data['tool_config'] = {'googledrive': 'secret'}
    with TestClient(app) as client:
        url = f'/internal/documents:{operation}'
        assert client.post(url, json=data).status_code == 401
        result = client.post(url, json=data, headers={'X-LazyMind-Internal-Token': 'internal'})
    assert result.status_code == 200
    assert result.json()['incomplete'] and result.json()['warnings']
    assert 'secret' not in result.text
    assert fs._get.call_args.kwargs['params']['driveId'] == 'drive-1'


def test_actual_worker_imports_and_rejects_invalid_locator_without_network():
    req = reading.DocumentReadRequest(
        user_id='user', tenant_id='tenant', source_id='connection', provider='googledrive',
        tool_config={'googledrive': 'secret'}, locator='/invalid/local/path',
    )

    async def run():
        with pytest.raises(reading.DocumentReadError) as error:
            await asyncio.wait_for(read_execution.run_document_operation('read', req), 25)
        assert error.value.code == 'INVALID_ARGUMENT'

    asyncio.run(run())


@pytest.mark.parametrize('cancel', [False, True])
def test_real_worker_is_killed_and_reaped_on_timeout_or_cancel(monkeypatch, tmp_path, cancel):
    script = tmp_path / 'slow.py'
    pid_file = tmp_path / 'pid'
    script.write_text(
        'import os, time\nfrom pathlib import Path\n'
        f'Path({str(pid_file)!r}).write_text(str(os.getpid()))\ntime.sleep(60)\n')
    monkeypatch.setattr(read_execution, '_WORKER', script)
    spawn = asyncio.create_subprocess_exec
    processes = []

    async def track_process(*args, **kwargs):
        process = await spawn(*args, **kwargs)
        process.wait = AsyncMock(wraps=process.wait)
        processes.append(process)
        return process

    monkeypatch.setattr(asyncio, 'create_subprocess_exec', track_process)

    async def run():
        task = asyncio.create_task(read_execution.run_document_operation('browse', request()))
        for _ in range(100):
            if pid_file.exists():
                break
            await asyncio.sleep(0.02)
        assert pid_file.exists()
        if cancel:
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task
        else:
            with pytest.raises(TimeoutError):
                await asyncio.wait_for(task, 0.02)
        assert len(processes) == 1
        # Process state is portable; os.kill(pid, 0) is not a probe on Windows.
        assert processes[0].returncode is not None
        processes[0].wait.assert_awaited_once()

    asyncio.run(run())


def test_unportable_ir_returns_unsupported(monkeypatch):
    from lazyllm.tools.writer.data_models import TargetDocument
    monkeypatch.setattr(reading, 'export_writer_document', Mock(side_effect=ValueError('private block payload')))
    with pytest.raises(reading.DocumentReadError) as error:
        reading._normalize({'representation': 'ir', 'provider': 'googledrive', 'source_document': object(),
                            'target_document': TargetDocument(doc_id='doc')}, request())
    assert error.value.code == 'UNSUPPORTED'
    assert 'private' not in str(error.value)


def test_worker_success_over_stdin_stdout(monkeypatch, tmp_path):
    script = tmp_path / 'mock_platform.py'
    script.write_text(
        'import contextlib, sys\n'
        'with contextlib.redirect_stdout(sys.stderr):\n'
        '    from lazyllm.tools.fs import GoogleDriveFS\n'
        '    from lazymind.document_tools.read_worker import main\n'
        '    GoogleDriveFS._get = lambda *a, **kw: {"files": [\n'
        '        {"id": "doc-1", "name": "Plan", "mimeType": "text/plain"}]}\n'
        'main()\n')
    monkeypatch.setattr(read_execution, '_WORKER', script)

    async def run():
        result = await asyncio.wait_for(read_execution.run_document_operation('browse', request()), 25)
        assert result['items'][0]['title'] == 'Plan'
        assert result['items'][0]['read_locator']
        assert 'secret' not in str(result)

    asyncio.run(run())


def test_disconnect_cancels_and_awaits_work(monkeypatch):
    from fastapi import HTTPException

    async def run():
        started, cleaned = asyncio.Event(), asyncio.Event()

        async def work(*args):
            try:
                started.set()
                await asyncio.Event().wait()
            finally:
                cleaned.set()

        async def receive():
            await started.wait()
            return {'type': 'http.disconnect'}

        monkeypatch.setattr(document_routes, 'run_document_operation', work)
        with pytest.raises(HTTPException) as error:
            await asyncio.wait_for(document_routes._run_connected(Mock(receive=receive), 'browse', request()), 1)
        assert error.value.status_code == 499 and cleaned.is_set()

    asyncio.run(run())


def test_worker_spawn_failure_is_sanitized(monkeypatch):
    async def fail(*args, **kwargs):
        raise OSError('private environment detail')

    monkeypatch.setattr(asyncio, 'create_subprocess_exec', fail)

    async def run():
        with pytest.raises(reading.DocumentReadError) as error:
            await read_execution.run_document_operation('browse', request())
        assert error.value.code == 'PROVIDER_UNAVAILABLE'
        assert 'private' not in str(error.value)

    asyncio.run(run())
