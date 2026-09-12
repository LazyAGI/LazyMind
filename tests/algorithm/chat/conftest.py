"""Protocol fakes for unit tests; real Core round trips live in the Go integration test."""
import copy
import hashlib
import os
import time

import lazyllm
import pytest


@pytest.fixture
def workspace_runtime(monkeypatch, tmp_path):
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.engine.agent_runtime import workspace_authorization as transport
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware, FailureRetryPolicy
    from lazymind.chat.engine.tools.local_fs import LocalFileToolkit
    from lazymind.chat.service.component.tool_registry import ToolConfig, workspace_tool_metadata

    class Core:
        def __init__(self):
            self.events, self.operations = [], {}
            self.on_poll = self.on_claim = self.on_complete = None

        def post(self, path, payload, *, user_id):
            assert user_id == config['user_id']
            action = path.rsplit(':', 1)[-1]
            assert action != 'execute', 'the Core executor must not perform local IO'
            assert payload['execution_mode'] == 'local' and os.path.isabs(payload['path'])
            if action == 'prepare':
                identifier = hashlib.sha256(payload['call_id'].encode()).hexdigest()
                self.operations[identifier] = {
                    'payload': copy.deepcopy(payload), 'operation_id': identifier,
                    'status': 'pending', 'decision': 'pending',
                    'expires_at': int(time.time() * 1000) + 300000,
                    'permission_mode': mode,
                }
            else:
                identifier = path.rsplit('/', 1)[-1].split(':')[0]
            operation = self.operations[identifier]
            self.events.append((action, payload['path']))
            if action == 'claim':
                assert operation['payload'] == payload
                if self.on_claim:
                    self.on_claim(operation)
                assert operation['status'] == 'allowed'
                operation['status'] = 'executing'
                version, identity = payload.get('expected_version', ''), payload['target_identity']
                if payload.get('depends_on'):
                    previous = self.operations[payload['depends_on']]
                    assert previous['status'] == 'completed'
                    version = version or previous.get('version', '')
                    identity = previous['result_identity']
                return {**operation, 'execute_allowed': True, 'version': version, 'target_identity': identity}
            if action == 'complete':
                if self.on_complete:
                    self.on_complete(operation)
                operation.update({key: payload.get(key, '') for key in ('status', 'reason', 'version', 'result_identity')})
            return {key: value for key, value in operation.items() if key != 'payload'}

        def get(self, path, params, *, user_id):
            assert user_id == config['user_id'] and 'lease_token' not in params
            operation = self.operations[path.rsplit('/', 1)[-1]]
            self.events.append(('approve', operation['payload']['path']))
            if self.on_poll:
                self.on_poll(operation)
            else:
                operation.update(status='allowed', decision='allowed')
            return {key: value for key, value in operation.items() if key != 'payload'}

    config, mode = {}, 'always_ask'

    def create(root=None, *, extra_tools=(), gate=None, cancel_check=None, permission_mode='always_ask'):
        nonlocal config, mode
        root = root or tmp_path / 'workspace'
        root.mkdir(exist_ok=True)
        mode = permission_mode
        config = {
            'user_id': 'owner', 'conversation_id': 'conversation',
            '_workspace_execution': {'history_id': 'history', 'run_id': 'run'},
            'workspace_context': {'workspace_id': 'workspace', 'permission_mode': mode, 'permission_version': 1},
            'local_fs_sources': [{'source_id': 'local-workspace:workspace', 'paths': [str(root.resolve())],
                                  'file_extensions': ['txt', 'md']}],
        }
        lazyllm.globals['agentic_config'] = lazyllm.globals.get('agentic_config') or {}
        monkeypatch.setitem(lazyllm.globals, 'agentic_config', config)
        toolkit = LocalFileToolkit()
        registration = ToolConfig('local', 'Local', 'Local', toolkit, 'data', authorization={
            name: 'write' if name in {'create', 'append', 'delete', 'overwrite', 'mkdir', 'string_replace'} else 'read'
            for name in toolkit.__public_apis__
        })
        manager = ToolManager([toolkit, *extra_tools])
        core = Core()
        monkeypatch.setattr(transport, 'post_core_api', core.post)
        monkeypatch.setattr(transport, 'get_core_api', core.get)
        monkeypatch.setattr(transport.time, 'sleep', lambda _: None)
        middleware = ToolExecutionMiddleware(manager, authorization_gate=gate, cancel_check=cancel_check,
                                             workspace_tools=workspace_tool_metadata(manager.tools_info, [registration]),
                                             failure_policy=FailureRetryPolicy({'LocalFileToolkit_append': 1}))
        return middleware, core, config

    return create
