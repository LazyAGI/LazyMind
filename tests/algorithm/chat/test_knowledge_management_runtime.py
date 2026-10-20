import asyncio
import json
from dataclasses import replace
from types import SimpleNamespace

import pytest
from lazyllm.tools.agent import PreparedToolCall, ToolManager, ToolExecutionError
from lazyllm.tools import get_tool_runtime_metadata

from lazymind.chat.service.knowledge_management import (
    KnowledgeManagementRuntime, MANAGEMENT_TOOLS, requires_confirmation,
)


EXECUTION = {'user_id': 'alice', 'conversation_id': 'conv', 'history_id': 'history', 'run_id': 'run'}


def prepared(name='knowledge.resource.delete', arguments=None):
    return PreparedToolCall(index=0, tool_call={}, call_id='call-1', tool_name='alias',
                            arguments=arguments or {'targets': [{'kind': 'document',
                                'knowledge_base_id': 'kb', 'document_id': 'doc'}]},
                            validated_arguments=arguments or {'targets': [{'kind': 'document',
                                'knowledge_base_id': 'kb', 'document_id': 'doc'}]},
                            tool_source='mcp', tool_origin='lazymind-knowledge', tool_identity='identity')


def runtime(monkeypatch, responses, events=None, allowed_tools=None):
    from lazymind.chat.service import knowledge_management as module
    requests = []

    def post(path, payload, *, user_id):
        requests.append((path, payload, user_id))
        value = responses[path.split(':')[-1]]
        return value(payload) if callable(value) else value

    monkeypatch.setattr(module, 'post_core_api', post)
    monkeypatch.setattr(module, '_write_agent_data', lambda kind, **payload: (events if events is not None else []).append(
        (kind, payload)))
    host = KnowledgeManagementRuntime(EXECUTION, 'http://core/api/core', allowed_tools=allowed_tools)
    host._tools['identity'] = 'knowledge.resource.delete'
    return host, requests


@pytest.mark.parametrize('name,args,want', [
    ('knowledge.create', {'name': 'Papers'}, False),
    ('knowledge.document.upload', {}, False),
    ('knowledge.folder.create', {}, False),
    ('knowledge.resource.update_metadata', {}, False),
    ('knowledge.resource.delete', {}, True),
    ('knowledge.resource.move', {'sources': [{'knowledge_base_id': 'a'}],
                                 'destination': {'knowledge_base_id': 'a'}}, False),
    ('knowledge.resource.move', {'sources': [{'knowledge_base_id': 'a'}, {'knowledge_base_id': 'b'}],
                                 'destination': {'knowledge_base_id': 'a'}}, True),
])
def test_only_delete_and_cross_library_move_need_confirmation(name, args, want):
    assert requires_confirmation(name, args) is want


def test_server_factory_intersects_positive_whitelist_and_keeps_empty(monkeypatch):
    host, _ = runtime(monkeypatch, {})
    server = host.server_config({'id': 'lazymind-knowledge', 'allowed_tools': ['knowledge.create', 'knowledge.search']})
    assert server['allowed_tools'] == ['knowledge.create']
    assert server['result_mode'] == 'structured'
    assert server['url'] == 'http://core/api/core/internal/mcp/knowledge-management'
    assert host.server_config({'allowed_tools': []})['allowed_tools'] == []
    assert set(host.server_config({})['allowed_tools']) == set(MANAGEMENT_TOOLS)
    assert 'knowledge.document.get' not in MANAGEMENT_TOOLS


def test_missing_active_identity_fails_closed():
    with pytest.raises(ToolExecutionError):
        KnowledgeManagementRuntime({'user_id': 'alice'}, 'http://core/api/core')


def test_dispatch_rejects_spoofed_identity_or_non_mcp_source(monkeypatch):
    host, requests = runtime(monkeypatch, {})
    for call in (replace(prepared(), tool_identity='forged'), replace(prepared(), tool_source='local')):
        with pytest.raises(ToolExecutionError):
            with host.execution_context(call):
                pytest.fail('must not execute')
    assert requests == []


def test_direct_call_without_prepared_dispatch_cannot_get_metadata(monkeypatch):
    host, requests = runtime(monkeypatch, {})
    with pytest.raises(ToolExecutionError):
        asyncio.run(host.request_metadata('knowledge.resource.delete', prepared().validated_arguments))
    assert requests == []


def test_pending_confirmation_emits_bound_card_then_claims_before_dispatch(monkeypatch):
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    call = prepared()
    summary = {'operation_key': 'op', 'arguments_digest': digest(call.validated_arguments),
               'snapshot': 'version-1', 'requires_confirmation': True, 'confirmation_id': 'confirm',
               'status': 'pending', 'preview': {'title': 'Delete document?', 'description': 'Papers / A.pdf'}}
    events = []
    host, requests = runtime(monkeypatch, {'prepare': summary, 'status': {**summary, 'status': 'allowed'},
                                         'claim': {'status': 'allowed', 'metadata': {'operation_key': 'op'}}}, events)
    with host.execution_context(call):
        metadata = asyncio.run(host.request_metadata('knowledge.resource.delete', call.validated_arguments))
        assert metadata == {'operation_key': 'op'}
    assert [path.split(':')[-1] for path, _, _ in requests] == ['prepare', 'status', 'claim']
    assert requests[0][1]['execution'] == EXECUTION
    assert requests[0][1]['call_id'] == 'call-1'
    assert requests[-1][1]['snapshot'] == 'version-1'
    assert events[0][0] == 'ask_pending'
    assert events[0][1]['knowledge_management'] == {
        'confirmation_id': 'confirm', 'arguments_digest': summary['arguments_digest'], 'snapshot': 'version-1'}
    with pytest.raises(ToolExecutionError):
        asyncio.run(host.request_metadata('knowledge.resource.delete', call.validated_arguments))


@pytest.mark.parametrize('status', ['rejected', 'expired', 'changed'])
def test_denied_or_stale_confirmation_never_dispatches(monkeypatch, status):
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    call = prepared()
    host, requests = runtime(monkeypatch, {'prepare': {
        'operation_key': 'op', 'arguments_digest': digest(call.validated_arguments), 'snapshot': 'version-1',
        'requires_confirmation': True, 'confirmation_id': 'confirm', 'status': status}})
    with pytest.raises(ToolExecutionError):
        with host.execution_context(call):
            pytest.fail('must not execute')
    assert len(requests) == 1


def test_claim_and_metadata_must_match_prepared_parameters(monkeypatch):
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    call = prepared()
    summary = {'operation_key': 'op', 'arguments_digest': digest(call.validated_arguments),
               'snapshot': 'version-1', 'requires_confirmation': True, 'confirmation_id': 'confirm', 'status': 'allowed'}
    host, _ = runtime(monkeypatch, {'prepare': summary, 'claim': {
        'status': 'allowed', 'metadata': {'operation_key': 'op'}}})
    with host.execution_context(call):
        with pytest.raises(ToolExecutionError):
            asyncio.run(host.request_metadata('knowledge.resource.delete', {'targets': []}))
        with pytest.raises(ToolExecutionError):
            asyncio.run(host.request_metadata('knowledge.create', call.validated_arguments))


def test_allowed_low_risk_write_does_not_emit_confirmation(monkeypatch):
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    call = prepared(arguments={'name': 'Papers'})
    events = []
    host, requests = runtime(monkeypatch, {'prepare': {
        'operation_key': 'op', 'arguments_digest': digest(call.validated_arguments), 'snapshot': 'version-1',
        'requires_confirmation': False, 'status': 'allowed'},
        'claim': {'status': 'allowed', 'metadata': {'operation_key': 'op'}}}, events)
    host._tools['identity'] = 'knowledge.create'
    with host.execution_context(call):
        assert asyncio.run(host.request_metadata('knowledge.create', {'name': 'Papers'})) == {'operation_key': 'op'}
    assert events == []
    assert [path.split(':')[-1] for path, _, _ in requests] == ['prepare', 'claim']


def test_workflow_full_trust_still_applies_management_dispatch_policy():
    from contextlib import contextmanager
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    from lazyllm.tools.agent import fc_register
    calls = []

    @fc_register(host_file='NONE')
    def guarded_delete():
        '''Delete a resource.'''
        calls.append('executed')

    class Policy:
        def record_result(self, call, result):
            return result

        @contextmanager
        def execution_context(self, call):
            calls.append('policy')
            raise ToolExecutionError('confirmation required')
            yield

    middleware = ToolExecutionMiddleware(ToolManager([guarded_delete]),
        workspace_permission=WorkspaceContext(workflow_full_trust=True), knowledge_management=Policy())
    middleware.execute_with_records({'id': 'call', 'function': {'name': 'guarded_delete', 'arguments': {}}})
    assert calls == ['policy']


def test_bound_server_keeps_schema_identity_and_dispatch_policy(monkeypatch):
    from lazymind.chat.service import chat_service
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    host, _ = runtime(monkeypatch, {})
    def get_tools(client, **kwargs):
        return [generate_lazyllm_tool(client, SimpleNamespace(name='knowledge.create', description='Create.',
            inputSchema={'type': 'object', 'properties': {'name': {'type': 'string'}}, 'required': ['name']}))]
    monkeypatch.setattr(chat_service.MCPClient, 'get_tools', get_tools)
    tools = chat_service._load_mcp_server_tools(host.server_config({}), 'system')
    metadata = get_tool_runtime_metadata(tools[0])
    assert host._tools[metadata.tool_identity] == 'knowledge.create'
    assert metadata.host_file_access.value == 'NONE'


def test_management_runtime_context_is_preserved_by_sync_adapter_inside_event_loop(monkeypatch):
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    from lazyllm.tools.mcp.utils import patch_sync
    call = prepared()
    host, _ = runtime(monkeypatch, {'prepare': {
        'operation_key': 'op', 'arguments_digest': digest(call.validated_arguments), 'snapshot': 'v',
        'requires_confirmation': True, 'confirmation_id': 'confirm', 'status': 'allowed'},
        'claim': {'status': 'allowed', 'metadata': {'operation_key': 'op'}}})

    async def invoke():
        with host.execution_context(call):
            return patch_sync(host.request_metadata)('knowledge.resource.delete', call.validated_arguments)

    assert asyncio.run(invoke()) == {'operation_key': 'op'}


def test_default_provider_uses_trusted_current_execution_and_rebinds_duplicate_endpoint(monkeypatch):
    from lazymind.chat.service.knowledge_management import build_knowledge_runtime, bind_knowledge_server
    import importlib
    monkeypatch.setattr(importlib.import_module('lazymind.config'), 'config', {'core_api_url': 'http://core/api/core'})
    cfg = {'user_id': 'alice', 'conversation_id': 'conv',
           '_workspace_execution': {'history_id': 'history', 'run_id': 'run'},
           'system_mcp_config': [{'id': 'lazymind-browser', 'url': 'http://browser'},
                                 {'id': 'lazymind-knowledge', 'allowed_tools': ['knowledge.create']}]}
    host = build_knowledge_runtime(cfg)
    bound = bind_knowledge_server({'id': 'duplicate', 'url': 'http://core/api/core/mcp/capabilities/v1',
                                   'allowed_tools': ['knowledge.create', 'knowledge.search']}, host)
    assert bound['id'] == 'lazymind-knowledge'
    assert bound['allowed_tools'] == ['knowledge.create']
    assert bound['_knowledge_runtime'] is host
    assert dict(host.execution) == EXECUTION
    browser = cfg['system_mcp_config'][0]
    assert bind_knowledge_server(browser, host) is browser
    assert build_knowledge_runtime({**cfg, 'system_mcp_config': []}) is None


def test_builtin_without_trusted_runtime_fails_closed(monkeypatch):
    from lazymind.chat.service import chat_service
    monkeypatch.setattr(chat_service, 'MCPClient', lambda **_: pytest.fail('unbound builtin loaded'))
    assert chat_service._load_mcp_server_tools({'id': 'lazymind-knowledge',
        'url': 'http://core/api/core/internal/mcp/knowledge-management', 'allowed_tools': ['knowledge.create']}) == []


def test_subagent_provider_uses_child_lease_and_never_parent_run(monkeypatch):
    from lazymind.chat.service.knowledge_management import build_knowledge_runtime
    import importlib
    monkeypatch.setattr(importlib.import_module('lazymind.config'), 'config', {'core_api_url': 'http://core/api/core'})
    cfg = {'user_id': 'alice', 'conversation_id': 'conv',
           '_workspace_execution': {'task_id': 'child', 'generation': 2, 'attempt_id': 'try', 'lease_token': 'lease'},
           'system_mcp_config': [{'id': 'lazymind-knowledge', 'allowed_tools': []}],
           'parent_agentic_config': {'_workspace_execution': {'history_id': 'old', 'run_id': 'parent'}}}
    host = build_knowledge_runtime(cfg)
    assert host.execution['task_id'] == 'child'
    assert 'run_id' not in host.execution
    assert host.allowed_tools == ()


@pytest.mark.parametrize('error_code', ['CONFLICT', 'MCP_CALL_FAILED'])
def test_real_manager_dispatch_attaches_claimed_metadata_and_keeps_structured_errors(monkeypatch, error_code):
    from contextlib import asynccontextmanager
    from mcp.types import CallToolResult
    from lazyllm.tools.mcp.client import MCPClient
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    arguments = {'name': 'Papers'}
    host, requests = runtime(monkeypatch, {'prepare': {
        'operation_key': 'stable', 'arguments_digest': digest(arguments), 'snapshot': 'v',
        'requires_confirmation': False, 'status': 'allowed'},
        'claim': {'status': 'allowed', 'metadata': {'operation_key': 'stable'}}})
    client = MCPClient('https://core/mcp', server_id='lazymind-knowledge', result_mode='structured',
                       request_metadata_provider=host.request_metadata)
    dispatched = []
    @asynccontextmanager
    async def session():
        class Session:
            async def call_tool(self, name, arguments, *, meta):
                dispatched.append((name, arguments, meta))
                return CallToolResult(content=[], isError=True, structuredContent={'code': error_code})
        yield Session()
    client._run_session = session
    tools = host.bind_tools([generate_lazyllm_tool(client, SimpleNamespace(name='knowledge.create',
        description='Create.', inputSchema={'type': 'object', 'properties': {'name': {'type': 'string'}},
                                            'required': ['name']}))])
    manager = ToolManager(tools)
    alias = next(iter(manager.atomic_tool_catalog()))
    middleware = ToolExecutionMiddleware(manager, knowledge_management=host,
                                         workspace_permission=WorkspaceContext(local_runtime=False))
    batch = middleware.execute_with_records({'id': 'call', 'function': {'name': alias, 'arguments': arguments}})
    assert dispatched == [('knowledge.create', {'name': 'Papers'}, {'operation_key': 'stable'})]
    assert batch.results[0]['ok'] is False
    assert json.loads(batch.results[0]['value'])['structuredContent'] == {'code': error_code}
    assert json.loads(batch.results[0]['value'])['isError'] is True
    if error_code == 'MCP_CALL_FAILED':
        middleware.execute_with_records({'id': 'retry-new-call-id', 'function': {
            'name': alias, 'arguments': arguments}})
        assert len(dispatched) == 1


def test_local_upload_is_declared_authorized_then_transferred_before_claim(monkeypatch, tmp_path):
    from contextlib import asynccontextmanager
    from mcp.types import CallToolResult
    from lazyllm.tools.mcp.client import MCPClient
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext, get_workspace_permission_context
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    file = tmp_path / 'paper.pdf'
    file.write_bytes(b'original bytes')
    args = {'knowledge_base_id': 'kb', 'files': [{'source': {'kind': 'local_file', 'path': str(file)}}]}
    final_args = {'knowledge_base_id': 'kb', 'files': [{'source': {'kind': 'uploaded_file', 'file_ref': 'original-1'}}]}
    host, requests = runtime(monkeypatch, {'prepare': {
        'operation_key': 'upload', 'arguments_digest': digest(final_args), 'snapshot': 'v',
        'requires_confirmation': False, 'status': 'allowed'},
        'claim': {'status': 'allowed', 'metadata': {'operation_key': 'upload'}}})
    client = MCPClient('http://core', server_id='lazymind-knowledge', result_mode='structured',
                       request_metadata_provider=host.request_metadata)
    dispatched, uploaded = [], []
    @asynccontextmanager
    async def session():
        class Session:
            async def call_tool(self, name, arguments, *, meta):
                dispatched.append(arguments)
                return CallToolResult(content=[], structuredContent={'status': 'ok'})
        yield Session()
    client._run_session = session
    tool = generate_lazyllm_tool(client, SimpleNamespace(name='knowledge.document.upload', description='Upload.',
        inputSchema={'type': 'object', 'properties': {'knowledge_base_id': {'type': 'string'},
                                                     'files': {'type': 'array'}},
                     'required': ['knowledge_base_id', 'files']}))
    tools = host.bind_tools([tool])
    manager = ToolManager(tools)
    alias = next(iter(manager.atomic_tool_catalog()))
    call = {'id': 'upload', 'function': {'name': alias, 'arguments': args}}
    prepared_call = manager.prepare_tool_calls(call)[0]
    assert len(prepared_call.host_files) == 1
    assert prepared_call.host_files[0].path == str(file.resolve())
    def upload(path):
        assert get_workspace_permission_context().workflow_full_trust
        assert requests == []
        uploaded.append(path)
        return 'original-1'
    monkeypatch.setattr(host, '_upload_original', upload)
    denied = ToolExecutionMiddleware(manager, knowledge_management=host,
        workspace_permission=WorkspaceContext(local_runtime=True))
    denied.execute_with_records(call)
    assert uploaded == [] and requests == [] and dispatched == []
    middleware = ToolExecutionMiddleware(manager, knowledge_management=host,
        workspace_permission=WorkspaceContext(local_runtime=True, workflow_full_trust=True))
    result = middleware.execute_with_records(call)
    assert uploaded == [str(file.resolve())]
    assert dispatched == [final_args]
    assert result.results[0]['ok'] is True
    assert requests[0][1]['arguments'] == final_args


def test_original_transfer_streams_approved_fd_with_captured_identity(monkeypatch, tmp_path):
    import importlib
    import httpx
    file = tmp_path / 'original.txt'
    file.write_bytes(b'approved original')
    host = KnowledgeManagementRuntime(EXECUTION, 'http://core/api/core')
    captured = []
    def handle(request):
        captured.append(request)
        assert b'approved original' in request.read()
        assert b'filename="original.txt"' in request.content
        return httpx.Response(200, json={'file_ref': 'uploaded-1'})
    original_client = httpx.Client
    monkeypatch.setattr(httpx, 'Client', lambda **kwargs: original_client(
        **kwargs, transport=httpx.MockTransport(handle)))
    monkeypatch.setattr(importlib.import_module('lazymind.config'), 'config', {
        'core_internal_token': 'private-token', 'core_api_timeout': 5})
    assert host._upload_original(str(file)) == 'uploaded-1'
    assert captured[0].url.path == '/api/core/internal/knowledge-management:upload-original'
    assert json.loads(captured[0].headers['X-LazyMind-Execution']) == EXECUTION
    assert captured[0].headers['X-LazyMind-Internal-Token'] == 'private-token'
    assert 'X-User-Id' not in captured[0].headers
    link = tmp_path / 'link.txt'
    link.symlink_to(file)
    with pytest.raises(OSError):
        host._upload_original(str(link))
    assert len(captured) == 1


@pytest.mark.parametrize('stop', ['deadline', 'cancel'])
def test_confirmation_wait_is_bounded_and_never_claims_after_stop(monkeypatch, stop):
    from lazymind.chat.service import knowledge_management as module
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError
    call = prepared()
    pending = {'operation_key': 'op', 'arguments_digest': digest(call.validated_arguments),
               'snapshot': 'v', 'requires_confirmation': True, 'confirmation_id': 'c', 'status': 'pending'}
    host, requests = runtime(monkeypatch, {'prepare': pending, 'status': pending})
    if stop == 'deadline':
        times = iter([0, 301])
        monkeypatch.setattr(module.time, 'monotonic', lambda: next(times))
        error = ToolExecutionError
    else:
        def cancel(_):
            if requests:
                raise UserCancelledError('cancelled')
        host.cancel_check = cancel
        error = UserCancelledError
    with pytest.raises(error):
        with host.execution_context(call):
            pytest.fail('stopped confirmation dispatched')
    assert [path.split(':')[-1] for path, _, _ in requests] == ['prepare']


@pytest.mark.parametrize('operation', ['prepare', 'status', 'claim', 'credential'])
def test_private_http_error_retains_structured_recovery_details(monkeypatch, operation):
    from lazymind.chat.service import knowledge_management as module
    from lazymind.chat.engine.tools.infra.core_api_client import CoreAPIError
    error = {'code': 'RESOURCE_LIMIT_EXCEEDED', 'message': 'Split this batch',
             'details': {'limit': 100, 'actual': 201, 'suggestion': 'Use batches of at most 100 resources'}}
    def post(*args, **kwargs):
        raise CoreAPIError('POST', 'http://core/private', 422, {'error': error})
    monkeypatch.setattr(module, 'post_core_api', post)
    host = KnowledgeManagementRuntime(EXECUTION, 'http://core')
    with pytest.raises(ToolExecutionError) as raised:
        host._post(operation, {})
    envelope = json.loads(str(raised.value))
    assert envelope == {'content': [], 'structuredContent': error, 'isError': True}


def test_pending_confirmation_keeps_complete_preview_for_live_and_persisted_card(monkeypatch):
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    call = prepared()
    preview = {'title': 'Delete folder', 'description': 'Papers / Archive',
               'resources': [{'kind': 'folder', 'knowledge_base_id': 'kb', 'directory_id': 'folder'}],
               'document_count': 12, 'folder_count': 2, 'total_bytes': 8192,
               'impact': ['Remove source documents and index entries'], 'counts': {'references': 3}}
    pending = {'operation_key': 'op', 'arguments_digest': digest(call.validated_arguments),
               'snapshot': 'v', 'requires_confirmation': True, 'confirmation_id': 'c', 'status': 'pending',
               'preview': preview}
    events = []
    host, _ = runtime(monkeypatch, {'prepare': pending, 'status': {**pending, 'status': 'rejected'}}, events)
    with pytest.raises(ToolExecutionError):
        with host.execution_context(call):
            pytest.fail('rejected')
    assert events[0][1]['knowledge_management_preview'] == preview


@pytest.mark.parametrize('fail_at', ['prepare', 'credential'])
def test_agent_receives_private_errors_through_real_manager_and_mcp_adapter(monkeypatch, fail_at):
    from contextlib import asynccontextmanager
    from lazyllm.tools.mcp.client import MCPClient
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    from lazymind.chat.engine.agent_runtime.workspace_authorization import digest
    from lazymind.chat.engine.tools.infra.core_api_client import CoreAPIError
    from lazymind.chat.service import knowledge_management as module
    arguments = {'name': 'Papers'}
    error = {'code': 'AUTHORIZATION_CHANGED', 'message': 'Refresh access',
             'details': {'suggestion': 'Refresh before retrying', 'limit': 9}}
    def post(path, payload, **kwargs):
        if path.endswith(':' + fail_at):
            raise CoreAPIError('POST', 'http://private?token=must-not-leak', 403, {'error': error})
        if path.endswith(':prepare'):
            return {'operation_key': 'op', 'arguments_digest': digest(arguments), 'snapshot': 'v',
                    'requires_confirmation': False, 'status': 'allowed'}
        return {'status': 'allowed', 'metadata': {'operation_key': 'op'}}
    monkeypatch.setattr(module, 'post_core_api', post)
    host = KnowledgeManagementRuntime(EXECUTION, 'http://core')
    client = MCPClient('http://core', server_id='lazymind-knowledge', result_mode='structured',
                       request_metadata_provider=host.request_metadata)
    @asynccontextmanager
    async def session():
        await host.headers()
        pytest.fail('private error must prevent dispatch')
        yield
    client._run_session = session
    tool = generate_lazyllm_tool(client, SimpleNamespace(name='knowledge.create', description='Create.',
        inputSchema={'type': 'object', 'properties': {'name': {'type': 'string'}}, 'required': ['name']}))
    manager = ToolManager(host.bind_tools([tool]))
    alias = next(iter(manager.atomic_tool_catalog()))
    middleware = ToolExecutionMiddleware(manager, knowledge_management=host,
        workspace_permission=WorkspaceContext(local_runtime=False))
    result = middleware.execute_with_records({'id': 'call', 'function': {'name': alias, 'arguments': arguments}})
    assert result.results[0]['ok'] is False
    assert json.loads(result.results[0]['value']) == {'content': [], 'structuredContent': error, 'isError': True}
    assert 'must-not-leak' not in str(result.results)


def test_original_transfer_retains_structured_http_error(monkeypatch, tmp_path):
    import importlib
    import httpx
    file = tmp_path / 'original.txt'
    file.write_bytes(b'original')
    error = {'code': 'ORIGINAL_TOO_LARGE', 'message': 'Reduce the file size',
             'details': {'limit_bytes': 128 * 1024 * 1024, 'suggestion': 'Split the original'}}
    original_client = httpx.Client
    monkeypatch.setattr(httpx, 'Client', lambda **kwargs: original_client(
        **kwargs, transport=httpx.MockTransport(lambda _: httpx.Response(413, json={'error': error}))))
    monkeypatch.setattr(importlib.import_module('lazymind.config'), 'config', {
        'core_internal_token': 'private-token', 'core_api_timeout': 5})
    host = KnowledgeManagementRuntime(EXECUTION, 'http://core')
    with pytest.raises(ToolExecutionError) as raised:
        host._upload_original(str(file))
    assert json.loads(str(raised.value))['structuredContent'] == error
