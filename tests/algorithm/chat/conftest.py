"""Protocol fakes for unit tests; real Core round trips live in the Go integration test."""
import copy
import os
import time

import lazyllm
import pytest


@pytest.fixture
def workspace_runtime(monkeypatch, tmp_path):
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.engine.agent_runtime import workspace_authorization as transport
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware, FailureRetryPolicy
    from lazyllm.tools.agent import FileSystemToolkit
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext

    class Core:
        def __init__(self):
            self.events, self.operations = [], {}
            self.batch_requests = 0
            self.on_poll = self.on_claim = self.on_complete = None

        def post(self, path, payload, *, user_id):
            assert user_id == config['user_id']
            action = path.rsplit(':', 1)[-1]
            assert action != 'execute', 'the Core executor must not perform local IO'
            if action == 'prepare-batch':
                self.batch_requests += 1
                return {'operations': [self.post(path.replace(':prepare-batch', ':prepare'), request, user_id=user_id)
                                       for request in payload['calls']]}
            assert payload['execution_mode'] == 'host_access'
            assert os.path.isabs(payload['path']) or payload.get('capability') in {'shell', 'tool'}
            if action == 'prepare':
                identifier = transport.WorkspaceAuthorization._operation_id(payload)
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
                version, identity = payload.get('expected_version', ''), payload.get('target_identity', '')
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

    def create(root=None, *, extra_tools=(), gate=None, cancel_check=None, permission_mode='always_ask',
               execution_identity=None, trusted_local=True, trusted_opaque_tool_names=frozenset()):
        nonlocal config, mode
        root = root or tmp_path / 'workspace'
        root.mkdir(exist_ok=True)
        mode = permission_mode
        config = {
            'user_id': 'owner', 'conversation_id': 'conversation',
            '_workspace_execution': execution_identity or {'history_id': 'history', 'run_id': 'run'},
            'workspace_context': {
                'workspace_id': 'workspace', 'root': str(root.resolve()),
                'directory_identity': 'test-directory', 'workspace_version': 1,
                'permission_mode': mode, 'permission_version': 1,
            },
        }
        lazyllm.globals['agentic_config'] = lazyllm.globals.get('agentic_config') or {}
        monkeypatch.setitem(lazyllm.globals, 'agentic_config', config)
        toolkit = FileSystemToolkit()
        manager = ToolManager([toolkit, *extra_tools])
        core = Core()
        monkeypatch.setattr(transport, 'post_core_api', core.post)
        monkeypatch.setattr(transport, 'get_core_api', core.get)
        monkeypatch.setattr(transport.time, 'sleep', lambda _: None)
        middleware = ToolExecutionMiddleware(manager, authorization_gate=gate, cancel_check=cancel_check,
                                             workspace_permission=WorkspaceContext.from_snapshot(
                                                 config['workspace_context'],
                                                 user_id=config['user_id'],
                                                 conversation_id=config['conversation_id'],
                                                 execution=config['_workspace_execution'],
                                                 trusted_local=trusted_local,
                                             ),
                                             trusted_opaque_tools=tuple(
                                                 manager.tools_info[name]
                                                 for name in trusted_opaque_tool_names
                                             ),
                                             failure_policy=FailureRetryPolicy({'write': 1}))
        return middleware, core, config

    return create
