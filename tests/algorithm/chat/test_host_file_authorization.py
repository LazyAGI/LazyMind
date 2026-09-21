from types import SimpleNamespace

import pytest

from lazyllm.tools import AuthorizationDecision, ToolManager, fc_register
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


def call(name='external', **arguments):
    return {'id': 'call', 'function': {'name': name, 'arguments': arguments}}


def external_tool(identity='mcp:v1:' + 'a' * 64):
    @fc_register(tool_identity=identity, execute_in_sandbox=False)
    def external(value: int):
        '''Return a value.

        Args:
            value: Input value.
        '''
        return value
    return external


@pytest.mark.parametrize('mode', ['always_ask', 'ask_as_needed', 'allow_all'])
def test_undeclared_approval_and_conversation_grant(workspace_runtime, mode):
    identity = 'mcp:v1:' + 'a' * 64
    middleware, core, _ = workspace_runtime(extra_tools=[external_tool(identity)], permission_mode=mode)

    def approve(operation):
        assert operation['payload']['capability'] == 'tool'
        assert operation['payload']['tool_identity'] == identity
        assert operation['payload']['path'] == ''
        operation.update(status='allowed', decision='allowed')
        if mode == 'ask_as_needed':
            operation['tool_granted'] = 'tool:' + identity
    core.on_poll = approve
    assert middleware.execute_with_records(call(value=1)).results[0]['value'] == 1
    assert middleware.execute_with_records(call(value=2)).results[0]['value'] == 2
    assert core.batch_requests == {'always_ask': 2, 'ask_as_needed': 1, 'allow_all': 0}[mode]


def test_always_ask_ignores_existing_grants(workspace_runtime):
    tool = external_tool()
    middleware, core, _ = workspace_runtime(extra_tools=[tool])
    middleware._run_grants.add('tool:mcp:v1:' + 'a' * 64)
    assert middleware.execute_with_records(call(value=1)).results[0]['ok']
    assert core.batch_requests == 1


def test_nonlocal_allows_and_missing_local_context_denies():
    manager = ToolManager([external_tool()])
    server = ToolExecutionMiddleware(manager, workspace_permission=WorkspaceContext(local_runtime=False))
    assert server.execute_with_records(call(value=1)).results[0]['value'] == 1
    local = ToolExecutionMiddleware(manager, workspace_permission=WorkspaceContext(local_runtime=True))
    assert not local.execute_with_records(call(value=1)).results[0]['ok']


def test_unbound_workspace_mutations_are_outside(tmp_path):
    from lazyllm.tools import FileSystemToolkit
    from lazymind.chat.engine.agent_runtime.workspace_policy import WorkspaceAuthorizationPolicy
    permission = WorkspaceContext.from_snapshot({'permission_mode': 'ask_as_needed'}, cwd=str(tmp_path))
    manager = ToolManager([FileSystemToolkit()])
    batch = manager.prepare_tool_calls(call('write', path='new.txt', content='test'),
                                      working_directory=str(tmp_path),
                                      authorization_policy=WorkspaceAuthorizationPolicy(permission, set(), lambda _: False))
    assert batch[0].authorization is AuthorizationDecision.ASK
    assert not (tmp_path / 'new.txt').exists()


def test_mcp_identity_is_stable_and_server_scoped(monkeypatch):
    from lazymind.chat.service import chat_service as service
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool

    class Client:
        def __init__(self, command_or_url, **kwargs):
            self._args = []
        def _resolve_transport(self):
            return 'streamable-http'
        def get_tools(self, **kwargs):
            return [generate_lazyllm_tool(self, SimpleNamespace(
                name='remote.echo', description='Echo.', inputSchema={
                    'type': 'object', 'properties': {'value': {'type': 'integer'}}, 'required': ['value']}))]
    monkeypatch.setattr(service, 'MCPClient', Client)
    monkeypatch.setattr(service, '_mcp_tool_cache', {})

    def identity(server):
        tools = service._load_mcp_server_tools(server)
        assert len(tools) == 1
        manager = ToolManager(tools)
        name = next(iter(manager.atomic_tool_catalog()))
        return manager.prepare_tool_calls(call(name, value=1))[0].tool_identity
    server = {'id': 'persistent-a', 'name': 'A', 'url': 'https://example.test/mcp'}
    first = identity(server)
    assert first.startswith('mcp:v1:') and len(first) == 71
    assert identity({**server, 'headers': {'Authorization': 'rotated'}, 'name': 'renamed'}) == first
    assert identity({**server, 'id': 'persistent-b'}) != first
    assert identity({**server, 'url': 'https://other.test/mcp'}) != first
    assert identity({'name': 'temporary', 'url': server['url']}).startswith('temporary:')


def test_non_host_tool_does_not_require_a_workspace_snapshot():
    @fc_register(host_file='NONE', execute_in_sandbox=False)
    def resource_read():
        '''Read a conversation-owned resource.'''
        return 'resource'

    manager = ToolManager([resource_read])
    local = ToolExecutionMiddleware(manager, workspace_permission=WorkspaceContext(local_runtime=True))
    assert local.execute_with_records(call('resource_read')).results[0]['value'] == 'resource'
