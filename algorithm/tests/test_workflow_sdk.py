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
    assert {'list_workflows', 'prepare_workflow', 'start_workflow',
            'get_workflow_state', 'get_ready_steps', 'advance_step'} <= names
    assert {'stop_workflow', 'resume_workflow', 'list_artifacts', 'patch_artifact',
            'delete_artifact', 'import_input_resource', 'bind_workflow_input'} <= names
    assert {
        'get_skill_conversion_context', 'create_workflow_draft',
        'update_workflow_draft_file', 'validate_workflow_draft',
        'get_workflow_diagnostics', 'publish_workflow',
    } <= names


def test_mcp_uses_shared_sdk_client():
    client = MagicMock()
    client.get_ready_steps.return_value = {
        'session_id': 's1', 'state_version': 3, 'ready_steps': ['draft'],
    }
    server = WorkflowMCPServer(lambda: client)
    result = server.call_tool('get_ready_steps', {'session_id': 's1'})
    assert result['structuredContent']['ready_steps'] == ['draft']
    client.get_ready_steps.assert_called_once_with('s1')


def test_mcp_initialize_and_tools_list_protocol():
    server = WorkflowMCPServer()
    initialized = server.handle({'jsonrpc': '2.0', 'id': 1, 'method': 'initialize'})
    assert initialized['result']['capabilities']['tools'] == {'listChanged': False}
    listed = server.handle({'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list'})
    assert {tool['name'] for tool in listed['result']['tools']} == set(TOOL_SCHEMAS)


def test_mcp_authoring_submits_agent_text_to_deterministic_sdk():
    client = MagicMock()
    client.create_workflow_draft.return_value = MagicMock(result={
        'draft': {'id': 'd1', 'version': 1},
    })
    server = WorkflowMCPServer(lambda: client)
    files = {
        'plugin.yaml': 'id: report\n',
        'scenario/state.yml': 'initial: __start__\n',
        'scenario/scenario.md': '# Report\n',
    }
    result = server.call_tool('create_workflow_draft', {
        'name': 'Report', 'skill_id': 's1', 'revision_id': 'r1',
        'tree_hash': 'sha256:tree', 'files': files,
    })
    assert result['structuredContent']['draft']['id'] == 'd1'
    client.create_workflow_draft.assert_called_once_with(
        'Report', 's1', 'r1', 'sha256:tree', files,
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
