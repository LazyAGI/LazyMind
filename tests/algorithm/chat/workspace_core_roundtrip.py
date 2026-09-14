"""Invoked by Core's integration test against real owner-scoped HTTP handlers."""
import json
import os
import threading
import time
from pathlib import Path

import lazyllm
import requests
from lazyllm.tools.agent import ToolManager, HostFileIntent, HostFileResolution, fc_register

from lazymind.config import config
from lazymind.chat.engine.tools.workspace_context import WorkspacePermissionContext
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
from lazymind.chat.engine.tools.local_fs import LocalFileToolkit
from lazymind.chat.engine.tools.calculator import calculator


def call(method, **arguments):
    return {'id': 'shared-provider-id', 'function': {
        'name': 'LocalFileToolkit_' + method, 'arguments': arguments,
    }}


def main():
    context = json.loads(os.environ['WORKSPACE_TEST_CONTEXT'])
    config['core_api_url'] = context['base_url']
    config['core_internal_token'] = os.environ['LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN']
    root = context['root']
    target = Path(context['outside'])
    lazyllm.globals['agentic_config'] = {
        'user_id': 'owner', 'conversation_id': context['conversation_id'],
        '_workspace_execution': {'history_id': 'history', 'run_id': 'run'},
        'workspace_context': {
            'workspace_id': context['workspace_id'], 'root': root, 'workspace_version': 1,
            'permission_mode': 'allow_all', 'permission_version': 1,
        },
    }
    toolkit = LocalFileToolkit()
    @fc_register(host_file_access='DECLARED', host_file_resolver=lambda args: HostFileResolution(
        args, (HostFileIntent(args['path'], 'read'),)))
    def declared_read(path: str):
        """Read a host file with the declared generic lifecycle.

        Args:
            path: Absolute file path.
        """
        with HostFileResolution.open_read(path) as file:
            return file.read().decode()

    manager = ToolManager([toolkit, calculator, declared_read])
    middleware = ToolExecutionMiddleware(
        manager,
        workspace_permission=WorkspacePermissionContext.from_config(
            lazyllm.globals['agentic_config'], trusted_local=True,
        ),
        tool_context=lazyllm.globals['agentic_config'],
    )
    ordinary_effects = []
    original = manager.tools_info['calculator'].apply
    def observed(*args, **kwargs):
        ordinary_effects.append(True)
        return original(*args, **kwargs)
    manager.tools_info['calculator'].apply = observed

    def run_with_user_decision(calls, count, action, expected='seed', expected_ordinary=0):
        finished = threading.Event()
        errors = []
        def decide():
            try:
                with requests.Session() as session:
                    session.trust_env = False
                    headers = {'X-User-Id': 'owner'}
                    base = context['base_url'] + '/conversations/' + context['conversation_id']
                    while not finished.wait(0.02):
                        result = session.get(base + ':workspace-approvals', headers=headers, timeout=5)
                        result.raise_for_status()
                        pending = [entry for entry in result.json()['data']['items'] if entry['status'] == 'pending']
                        if len(pending) == count:
                            if action == 'allow_once':
                                assert target.read_text() == expected
                                assert len(ordinary_effects) == expected_ordinary
                            for entry in pending:
                                response = session.post(base + '/workspace-approvals/' + entry['operation_id'] + ':decide',
                                                        json={'action': action}, headers=headers, timeout=5)
                                response.raise_for_status()
                            return
            except BaseException as error:
                errors.append(error)
        thread = threading.Thread(target=decide, daemon=True)
        thread.start()
        try:
            result = middleware.execute_with_records(calls)
        finally:
            finished.set()
            thread.join(timeout=6)
        assert not errors, errors
        return result

    result = run_with_user_decision([
        {'id': 'ordinary', 'function': {'name': 'calculator', 'arguments': {'expression': '1+1'}}},
        call('read', filepath=str(target)),
        call('append', filepath=str(target), content='+'),
        call('append', filepath=str(target), content='+'),
    ], 3, 'allow_once')
    assert all(item['ok'] for item in result.results), result.results
    assert target.read_text() == 'seed++'
    assert result.results[1]['value']['filepath'] == str(target)
    assert ordinary_effects == [True]
    denied = target.parent / 'denied.txt'
    result = run_with_user_decision([call('create', filepath=str(denied), content='must not appear')], 1, 'reject')
    assert not result.results[0]['ok'] and not denied.exists()
    generic = run_with_user_decision([
        {'function': {'name': 'declared_read', 'arguments': {'path': str(target)}}},
        call('read', filepath=str(target)),
    ], 2, 'allow_once', expected='seed++', expected_ordinary=1)
    assert all(item['ok'] for item in generic.results), generic.results
    assert generic.results[0]['value'] == 'seed++'
    assert generic.results[1]['value']['content'] == 'seed++'
    print('CORE_LOCAL_IO_ROUNDTRIP_OK')


if __name__ == '__main__':
    main()
