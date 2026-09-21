"""A0 contracts that do not import the optional full algorithm dependency graph."""
from __future__ import annotations

from pathlib import Path

import pytest


@pytest.mark.parametrize('workspace_id', ['', 'bound-workspace'])
def test_workflow_scripts_load_with_workspace_snapshot(monkeypatch, tmp_path, workspace_id):
    import base64
    from types import SimpleNamespace
    from lazymind.workflow_sdk import WorkflowClient
    from lazymind.chat.engine.subagent.runner import load_workflow_tools
    from lazymind.chat.engine.tools.workspace_context import workflow_execution_scope

    package = {'revision_id': 'revision', 'tree_hash': 'hash', 'files': {
        'scripts/tools.py': base64.b64encode(b'def run(): return "ready"').decode(),
    }}
    monkeypatch.setattr(WorkflowClient, 'get_workflow', lambda *_: SimpleNamespace(result=package))
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    params = {'workflow_id': 'workflow', 'revision_id': 'revision', 'tree_hash': 'hash',
              'workspace_context': {'workspace_id': workspace_id, 'permission_mode': 'always_ask'}}
    with workflow_execution_scope():
        assert load_workflow_tools(params, ['run'])['run']() == 'ready'
        for key in ('revision_id', 'tree_hash'):
            with pytest.raises(RuntimeError, match='different'):
                load_workflow_tools({**params, key: 'mismatch'}, ['run'])


def test_workspace_real_core_http_roundtrip():
    """Run by Go's HTTP fixture; real middleware, HTTP client, Core and disk."""
    import json
    import os
    import threading
    import time
    from concurrent.futures import ThreadPoolExecutor

    raw = os.environ.get('LAZYMIND_WORKSPACE_HTTP_FIXTURE')
    if not raw:
        pytest.skip('run TestWorkspacePythonCoreHTTP with LAZYMIND_WORKSPACE_E2E_PYTHON')

    import lazyllm
    import requests
    from lazyllm.tools.agent import ToolManager
    from lazymind.config import config
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext

    fixture = json.loads(raw)
    config['core_api_url'] = fixture['url']
    config['core_internal_token'] = fixture['token']
    context = {
        'user_id': 'owner', 'conversation_id': fixture['conversation'],
        '_workspace_execution': fixture['identity'],
        '_core_workspace_context': {
            'workspace_id': fixture['workspace'], 'root': fixture['root'],
            'workspace_version': 1, 'permission_mode': 'always_ask', 'permission_version': 1,
        },
    }
    from lazyllm.tools.agent import FileSystemToolkit
    from lazyllm.tools.agent.shell_tool import shell
    manager = ToolManager([FileSystemToolkit(), shell])
    cancelled = threading.Event()
    def check_cancel(_):
        if cancelled.is_set():
            raise RuntimeError('cancelled')
    middleware = ToolExecutionMiddleware(manager, cancel_check=check_cancel,
        workspace_permission=WorkspaceContext.from_config(context, trusted_local=True))
    session = requests.Session()
    session.trust_env = False
    session.headers['X-User-Id'] = 'owner'
    base = fixture['url'] + '/conversations/' + fixture['conversation']
    root = Path(fixture['root'])
    def invoke(method, arguments):
        lazyllm.globals['agentic_config'] = context
        return middleware.execute_with_records({'id': 'provider-id', 'function': {
            'name': method, 'arguments': arguments}}).results[0]
    def approved(method, arguments, action='allow_once'):
        with ThreadPoolExecutor(max_workers=1) as pool:
            future = pool.submit(invoke, method, arguments)
            try:
                deadline = time.monotonic() + 10
                item = None
                while time.monotonic() < deadline:
                    response = session.get(base + ':workspace-approvals', timeout=2)
                    response.raise_for_status()
                    item = next((value for value in response.json()['data']['items']
                                 if value['status'] == 'pending'), None)
                    if item:
                        break
                    if future.done():
                        pytest.fail(f'tool finished before approval: {future.result()}')
                    time.sleep(.02)
                assert item is not None and not future.done()
                assert not (root / 'denied.txt').exists()
                if method == 'write' and arguments.get('mode') == 'create':
                    assert not (root / arguments['path']).exists()
                assert not {'content', 'lease_token', 'root'} & item.keys()
                if action == 'cancel':
                    cancelled.set()
                else:
                    response = session.post(base + '/workspace-approvals/' + item['operation_id'] + ':decide',
                                            json={'action': action}, timeout=2)
                    response.raise_for_status()
                    assert response.json()['data']['status'] != 'completed'
                return future.result(timeout=5)
            except BaseException:
                cancelled.set()
                raise
    try:
        assert approved('write', {'path': 'created.txt', 'content': 'one'})['ok']
        assert invoke('read', {'path': 'created.txt'})['value']['content'] == 'one'
        assert approved('write', {'mode': 'append', 'path': 'created.txt', 'content': '+two'})['ok']
        assert invoke('read', {'path': 'created.txt'})['value']['content'] == 'one+two'
        assert approved('edit', {'path': 'created.txt', 'old_text': 'two', 'new_text': 'three'})['ok']
        assert (root / 'created.txt').read_text() == 'one+three'
        assert not approved('write', {'path': 'denied.txt', 'content': 'deny'}, 'reject')['ok']
        assert approved('remove', {'path': 'created.txt'})['ok']
        assert not (root / 'created.txt').exists()
        if not fixture['identity'].get('attempt_id'):
            assert approved('shell', {'cmd': 'echo once'})['ok']
            assert approved('shell', {'cmd': 'echo second'})['ok']
            assert approved('shell', {'cmd': 'echo third'})['ok']
        assert not approved('write', {'path': 'cancelled.txt', 'content': 'cancel'}, 'cancel')['ok']
        assert not (root / 'cancelled.txt').exists()
    finally:
        cancelled.set()
        session.close()
