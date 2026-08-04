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
    assert 'stop_workflow' not in names
    assert 'patch_artifact' not in names


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
