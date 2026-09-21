import json
from pathlib import Path
from unittest.mock import MagicMock

import yaml

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
    assert 'preflight_skill_workflow_conversion' in names


def test_agent_kit_profiles_declare_only_real_mcp_tools():
    profiles = sorted((Path(__file__).resolve().parents[2]
                       / 'skills/workflow-agent-kit/profiles').glob('*.yaml'))
    assert profiles
    conversion_tools = {
        'list_skills', 'preflight_skill_workflow_conversion',
        'get_skill_conversion_context', 'create_workflow_draft',
        'update_workflow_draft_file', 'validate_workflow_draft',
        'get_workflow_diagnostics', 'publish_workflow',
    }
    assert conversion_tools <= set(TOOL_SCHEMAS)
    # Host-only transport tools; everything else a profile declares must exist as a
    # real MCP tool so the Skill never instructs an Agent to call a missing one.
    host_only = {'advance_step_and_hand_off', 'resume_workflow'}
    for path in profiles:
        declared = set(yaml.safe_load(path.read_text())['workflow_tools'])
        assert conversion_tools <= declared, path.name
        assert declared - set(TOOL_SCHEMAS) <= host_only, path.name


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
        'contract_version': 'workflow.authoring.v1',
        'snapshot': {'revision_id': 'r1', 'tree_hash': 'sha256:tree'},
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


def test_mcp_authoring_uses_sdk_decoded_handler_skill_context():
    from lazymind.workflow_sdk import WorkflowClient

    transport = MagicMock()
    transport.get.return_value = MagicMock(status_code=200, json=lambda: {
        'ok': True,
        'data': {
            'contract_version': 'workflow.authoring.v1',
            'snapshot': {
                'skill_id': 's1',
                'revision_id': 'r1',
                'tree_hash': 'sha256:tree',
                'files': [{'path': 'SKILL.md', 'content': '# Skill'}],
            },
        },
    })
    transport.post.return_value = MagicMock(status_code=200, json=lambda: {
        'ok': True,
        'data': {'draft': {'id': 'd1', 'version': 1}},
    })
    client = WorkflowClient('http://core/api/core', 'u1', transport=transport)
    files = {
        'workflow.yaml': 'id: report\n',
        'scenario/state.yml': 'transitions: {}\n',
        'scenario/scenario.md': '# Report\n',
    }
    result = WorkflowMCPServer(lambda: client).call_tool('create_workflow_draft', {
        'name': 'Report', 'skill_id': 's1', 'files': files,
    })

    assert result['structuredContent']['draft']['id'] == 'd1'
    assert transport.get.call_args.args[0].endswith(
        '/workflow-authoring/v1/skill-context?skill_id=s1',
    )
    assert transport.post.call_args.kwargs['json']['revision_id'] == 'r1'
    assert transport.post.call_args.kwargs['json']['tree_hash'] == 'sha256:tree'


def test_mcp_exposes_preflight_skill_workflow_conversion():
    client = MagicMock()
    client.preflight_skill_workflow_conversion.return_value = MagicMock(result={
        'skill_id': 's1', 'status': 'pass', 'checks': [],
    })
    result = WorkflowMCPServer(lambda: client).call_tool(
        'preflight_skill_workflow_conversion', {'skill_id': 's1'},
    )
    assert result['structuredContent']['status'] == 'pass'
    client.preflight_skill_workflow_conversion.assert_called_once_with('s1')


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


def test_sdk_preflight_skill_workflow_conversion_route():
    transport = MagicMock()
    transport.post.return_value = MagicMock(
        status_code=200,
        json=lambda: {'ok': True, 'data': {'skill_id': 's1', 'status': 'pass'}},
    )
    from lazymind.workflow_sdk import WorkflowClient

    result = WorkflowClient(
        'http://core/api/core', 'u1', transport=transport,
    ).preflight_skill_workflow_conversion('s1')

    assert result.result['status'] == 'pass'
    call = transport.post.call_args
    assert call.args[0].endswith('/workflow-conversions:preflight')
    assert call.kwargs['json'] == {'skill_id': 's1'}


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


def test_advance_inherits_retrieval_snapshot_only_when_host_provides_it():
    import httpx
    from lazymind.workflow_sdk import AdvanceRequest, StepCommand, WorkflowClient
    from lazymind.chat.engine.subagent.runner import _build_agentic_config

    for enabled in (None, False, True):
        transport = MagicMock()
        transport.post.return_value = httpx.Response(200, json={'result': {}})
        client = WorkflowClient('http://core', 'user', transport=transport, enable_tool_retrieval=enabled)
        client.advance(AdvanceRequest(session_id='s', expected_state_version=1, steps=[StepCommand(step_id='step')]))
        payload = transport.post.call_args.kwargs['json']
        if enabled is None:
            assert 'parent_agentic_config' not in payload
        else:
            restored = _build_agentic_config({'conversation_id': 'c'}, payload, 'workflow_step')
            assert restored['enable_tool_retrieval'] is enabled
