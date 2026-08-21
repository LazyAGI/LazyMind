import json
from unittest.mock import MagicMock

from lazymind.workflow_mcp.server import TOOL_SCHEMAS, WorkflowMCPServer
from lazymind.workflow_sdk import ConnectionInfo, discover_connection


def test_discovery_prefers_explicit_workflow_url(monkeypatch):
    monkeypatch.setenv('LAZYMIND_WORKFLOW_BASE_URL', 'http://127.0.0.1:54321/api/core/')
    found = discover_connection()
    assert found == ConnectionInfo('http://127.0.0.1:54321/api/core',
                                   'env:LAZYMIND_WORKFLOW_BASE_URL')


def test_discovery_reads_dynamic_runtime_endpoint(tmp_path, monkeypatch):
    monkeypatch.delenv('LAZYMIND_WORKFLOW_BASE_URL', raising=False)
    monkeypatch.delenv('LAZYMIND_ENDPOINT_HOST_CORE_BASE_URL', raising=False)
    monkeypatch.delenv('LAZYMIND_CORE_API_URL', raising=False)
    monkeypatch.delenv('LAZYMIND_CORE_SERVICE_URL', raising=False)
    monkeypatch.setenv('LAZYMIND_RUNTIME_ROOT', str(tmp_path))
    generated = tmp_path / 'generated'
    generated.mkdir()
    (generated / 'service-endpoints.json').write_text(json.dumps({
        'host': {'coreBaseUrl': 'http://127.0.0.1:49152'},
    }))
    found = discover_connection()
    assert found.base_url == 'http://127.0.0.1:49152/api/core'
    assert found.source == 'runtime-service-endpoints'


def test_mcp_lists_only_real_public_tools():
    names = set(TOOL_SCHEMAS)
    assert {'list_workflows', 'get_workflow_state', 'get_ready_steps',
            'advance_step'} <= names
    assert 'prepare_workflow' not in names
    assert 'start_workflow' not in names
    assert {'list_artifacts', 'patch_artifact'} <= names
    assert not {'stop_workflow', 'resume_workflow', 'delete_artifact',
                'import_input_resource', 'bind_workflow_input', 'get_workflow_command'} & names
    assert {
        'get_skill_conversion_context', 'create_workflow_draft',
        'update_workflow_draft_file', 'validate_workflow_draft',
        'get_workflow_diagnostics', 'publish_workflow',
    } <= names


def test_ready_steps_are_read_from_authoritative_projection():
    client = MagicMock()
    client.get_state.return_value = {
        'state_version': 4,
        'projection': {
            'ready': ['draft'], 'retryable': ['review'], 'rewindable': ['source'],
        },
    }
    from lazymind.workflow_sdk.client import WorkflowClient
    value = WorkflowClient.get_ready_steps(client, 'session-1')
    assert value['ready_steps'] == ['draft']
    assert value['retryable_steps'] == ['review']
    assert value['rewindable_steps'] == ['source']


def test_mcp_uses_shared_sdk_client():
    client = MagicMock()
    client.get_ready_steps.return_value = {
        'session_id': 's1', 'state_version': 3, 'ready_steps': ['draft'],
    }
    server = WorkflowMCPServer(lambda: client, session_id='s1')
    result = server.call_tool('get_ready_steps', {})
    assert result['structuredContent']['ready_steps'] == ['draft']
    client.get_ready_steps.assert_called_once_with('s1')


def test_mcp_initialize_and_tools_list_protocol():
    server = WorkflowMCPServer()
    initialized = server.handle({'jsonrpc': '2.0', 'id': 1, 'method': 'initialize'})
    assert initialized['result']['capabilities']['tools'] == {'listChanged': False}
    listed = server.handle({'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list'})
    listed_names = {tool['name'] for tool in listed['result']['tools']}
    assert listed_names == set(TOOL_SCHEMAS) - WorkflowMCPServer._SESSION_TOOLS


def test_mcp_authoring_submits_agent_text_to_deterministic_sdk():
    client = MagicMock()
    client.create_workflow_draft.return_value = MagicMock(result={
        'draft': {'id': 'd1', 'version': 1},
    })
    client.get_skill_conversion_context.return_value = MagicMock(result={
        'revision_id': 'r1', 'tree_hash': 'sha256:tree',
    })
    server = WorkflowMCPServer(lambda: client)
    files = {
        'workflow.yaml': 'id: report\n',
        'scenario/state.yml': 'initial: __start__\n',
        'scenario/scenario.md': '# Report\n',
    }
    result = server.call_tool('create_workflow_draft', {
        'name': 'Report', 'skill_id': 's1', 'files': files,
    })
    assert result['structuredContent']['draft']['id'] == 'd1'
    client.create_workflow_draft.assert_called_once_with(
        'Report', 's1', 'r1', 'sha256:tree', files, 'skill',
    )


def test_sdk_authoring_routes_do_not_use_generation_endpoints():
    transport = MagicMock()
    transport.get.return_value = MagicMock(
        status_code=200, json=lambda: {'ok': True, 'data': {'valid': True}},
    )
    from lazymind.workflow_sdk import WorkflowClient

    client = WorkflowClient('http://core/api/core', 'u1', transport=transport)
    client.get_workflow_diagnostics('d1')
    path = transport.get.call_args.args[0]
    assert path.endswith('/workflow-authoring/v1/drafts/d1/diagnostics')
    assert 'ai-' not in path


def test_sdk_delete_artifact_creates_public_tombstone_request():
    transport = MagicMock()
    transport.delete.return_value = MagicMock(
        status_code=200, json=lambda: {'ok': True, 'result': {'deleted': True, 'revision': 3}},
    )
    from lazymind.workflow_sdk import WorkflowClient

    result = WorkflowClient('http://core/api/core', 'u1', transport=transport).delete_artifact(
        'a2', 2, 'cmd-delete')
    assert result.result['deleted'] is True
    call = transport.delete.call_args
    assert call.args[0].endswith('/workflow-artifacts/a2')
    assert call.kwargs['json'] == {'base_revision': 2, 'command_id': 'cmd-delete'}


def test_sdk_reads_durable_slot_order():
    transport = MagicMock()
    transport.get.return_value = MagicMock(
        status_code=200,
        json=lambda: {'ok': True, 'data': {'order_list': [7, 3], 'order_version': 2}},
    )
    from lazymind.workflow_sdk import WorkflowClient

    result = WorkflowClient(
        'http://core/api/core', 'u1', transport=transport,
    ).get_slot_order('session 1', 'preview/html')

    assert result.result['order_list'] == [7, 3]
    assert transport.get.call_args.args[0].endswith(
        '/workflow-sessions/session%201/slots/preview%2Fhtml/order'
    )
