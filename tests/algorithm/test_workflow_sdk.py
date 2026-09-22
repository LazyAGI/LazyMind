import json
import base64
from pathlib import Path
from unittest.mock import MagicMock

import httpx
import pytest
import yaml

from lazymind.workflow_mcp.server import TOOL_SCHEMAS, WorkflowMCPServer
from lazymind.workflow_sdk import ConnectionInfo, discover_connection


AGENT_TYPE_CASES = json.loads(
    (Path(__file__).parents[1] / 'fixtures' / 'external_agent_types.json').read_text(encoding='utf-8'))


@pytest.mark.parametrize('case', AGENT_TYPE_CASES)
@pytest.mark.parametrize('bound', [False, True])
def test_mcp_call_normalizes_alias_before_schema_validation(case, bound):
    client = MagicMock()
    client.start_skill_workflow_task.return_value.result = {'task_id': 'task-1'}
    server = WorkflowMCPServer(lambda: client, mode='external',
                               agent_type=case['canonical'] if bound else None)
    arguments = {'agent_type': case['input'],
                 'skill': {'name': 'demo', 'url': 'https://example.com/demo.zip'},
                 'task_description': 'Run it.'}
    server.call_tool('start_skill_workflow_task', arguments)
    assert client.start_skill_workflow_task.call_args.args[0] == case['canonical']
    assert arguments['agent_type'] == case['input']


def test_connection_requires_explicit_instance(monkeypatch):
    from lazymind.workflow_sdk import WorkflowClientError
    from lazymind.workflow_sdk.client import _default_runtime_roots
    for name in ('LAZYMIND_WORKFLOW_BASE_URL', 'LAZYMIND_SERVER_URL', 'LAZYMIND_PUBLIC_URL',
                 'LAZYMIND_ENDPOINT_HOST_CORE_BASE_URL', 'LAZYMIND_CORE_API_URL',
                 'LAZYMIND_CORE_SERVICE_URL', 'LAZYMIND_RUNTIME_ROOT'):
        monkeypatch.delenv(name, raising=False)
    assert _default_runtime_roots() == []
    with pytest.raises(WorkflowClientError) as error:
        discover_connection()
    assert error.value.code == 'LAZYMIND_NOT_FOUND'


def test_external_mcp_hides_and_rejects_internal_tools():
    client = MagicMock()
    server = WorkflowMCPServer(lambda: client, mode='external', agent_type='codex')
    assert {tool['name'] for tool in server.list_tools()} == server._EXTERNAL_TOOLS
    denied = server.handle({'id': 1, 'method': 'tools/call', 'params': {'name': 'list_skills'}})
    assert denied['result']['isError']
    client.list_skills.assert_not_called()


@pytest.mark.parametrize('agent_type', ['raccoon', 'xiaohuanxiong', 'xiaohuan-xiong', 'xiaohuan_xiong', '小浣熊'])
def test_external_mcp_normalizes_raccoon_agent_aliases(agent_type):
    client = MagicMock()
    client.start_skill_workflow_task.return_value = MagicMock(result={'task_id': 'task-1'})
    server = WorkflowMCPServer(lambda: client, mode='external', agent_type=agent_type)
    assert server.agent_type == 'raccoon-work'
    result = server.call_tool('start_skill_workflow_task', {
        'skill': {'name': 'demo', 'url': 'https://example.com/demo.zip'},
        'task_description': 'Run it.',
    })
    assert result['structuredContent']['task_id'] == 'task-1'
    assert client.start_skill_workflow_task.call_args.args[0] == 'raccoon-work'


@pytest.mark.parametrize('patch', [
    {'extra': True},
    {'skill': {'name': 'demo', 'url': 'https://example.com/demo.zip', 'zip_path': '/tmp/demo.zip'}},
    {'skill': {'name': 'demo'}},
    {'skill': {'name': 'demo', 'zip_path': 12}},
    {'config': {'not_implemented': True}},
    {'agent_type': 'cursor'},
    {'agent_type': 'raccoon_work'},
    {'agent_type': 123},
    {'agent_type': ['codex']},
])
def test_external_mcp_validates_before_sending(patch):
    client = MagicMock()
    arguments = {'skill': {'name': 'demo', 'url': 'https://example.com/demo.zip'},
                 'task_description': 'Make a report', **patch}
    server = WorkflowMCPServer(lambda: client, mode='external', agent_type='codex')
    response = server.handle({'id': 1, 'method': 'tools/call', 'params': {
        'name': 'start_skill_workflow_task', 'arguments': arguments}})
    assert response['result']['structuredContent']['error']['code'] == 'INVALID_REQUEST'
    client.start_skill_workflow_task.assert_not_called()


def test_external_mcp_zip_path_to_http_contract(tmp_path):
    from lazymind.workflow_sdk import WorkflowClient
    archive = tmp_path / 'demo.zip'
    archive.write_bytes(b'PK test file bytes')
    transport = MagicMock()
    transport.post.return_value = MagicMock(status_code=200, json=lambda: {
        'code': 0, 'data': {'task_id': 'task-1', 'status': 'queued', 'lazymind_url': '/agent/chat/home/c1'}})
    client = WorkflowClient('https://lazymind.example/api/core', transport=transport)
    server = WorkflowMCPServer(lambda: client, mode='external', agent_type='codex')
    response = server.call_tool('start_skill_workflow_task', {
        'skill': {'name': 'demo', 'zip_path': str(archive)}, 'task_description': 'Make a report'})
    payload = transport.post.call_args.kwargs['json']
    assert payload['agent_type'] == 'codex'
    assert base64.b64decode(payload['skill']['zip_base64']) == archive.read_bytes()
    assert 'zip_path' not in payload['skill']
    assert response['structuredContent']['lazymind_url'] == 'https://lazymind.example/agent/chat/home/c1'
    assert 'zip_base64' not in response['content'][0]['text']


def test_core_error_details_survive_sdk_and_mcp():
    from lazymind.workflow_sdk import WorkflowClient
    transport = MagicMock()
    transport.post.return_value = MagicMock(status_code=409, json=lambda: {
        'code': 2000107, 'message': 'Conflict', 'data': {
            'error_code': 'IDEMPOTENCY_CONFLICT', 'error_message': 'Different request',
            'suggestion': 'Use a new key'}})
    server = WorkflowMCPServer(lambda: WorkflowClient('http://core/api/core', transport=transport), mode='external')
    response = server.handle({'id': 1, 'method': 'tools/call', 'params': {
        'name': 'start_skill_workflow_task', 'arguments': {'agent_type': 'codex',
            'skill': {'name': 'demo', 'url': 'https://example.com/demo.zip'}, 'task_description': 'Make a report'}}})
    error = response['result']['structuredContent']['error']
    assert error['code'] == 'IDEMPOTENCY_CONFLICT'
    assert error['message'] == 'Different request'
    assert error['details']['suggestion'] == 'Use a new key'


def test_uncertain_submission_returns_retry_key():
    from lazymind.workflow_sdk import WorkflowClient, WorkflowClientError
    transport = MagicMock()
    transport.post.side_effect = httpx.ReadTimeout('timeout')
    with pytest.raises(WorkflowClientError) as error:
        WorkflowClient('http://core/api/core', transport=transport).start_skill_workflow_task(
            'codex', {'name': 'demo', 'url': 'https://example.com/demo.zip'}, 'Run')
    assert error.value.code == 'TASK_SUBMISSION_UNCERTAIN'
    assert error.value.details['idempotency_key'] == transport.post.call_args.kwargs['json']['idempotency_key']


def test_external_connection_uses_capabilities_without_catalog():
    from lazymind.workflow_sdk import WorkflowClient
    transport = MagicMock()
    transport.get.return_value = MagicMock(status_code=200, json=lambda: {
        'code': 0, 'data': {'contract_version': 'external-workflow.v1'}})
    server = WorkflowMCPServer(lambda: WorkflowClient('http://core/api/core', transport=transport), mode='external')
    result = server.call_tool('workflow_connection_status', {})
    assert result['structuredContent']['connected']
    assert transport.get.call_args.args[0].endswith('/external-agent/workflow-capabilities')


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
    monkeypatch.delenv('LAZYMIND_SERVER_URL', raising=False)
    monkeypatch.delenv('LAZYMIND_PUBLIC_URL', raising=False)
    monkeypatch.setenv('LAZYMIND_RUNTIME_ROOT', str(tmp_path))
    generated = tmp_path / 'generated'
    generated.mkdir()
    (generated / 'service-endpoints.json').write_text(json.dumps({
        'host': {'coreBaseUrl': 'http://127.0.0.1:49152'},
    }))
    found = discover_connection()
    assert found.base_url == 'http://127.0.0.1:49152'
    assert found.source == 'runtime-service-endpoints'


@pytest.mark.parametrize('setting', ['LAZYMIND_SERVER_URL', 'LAZYMIND_PUBLIC_URL'])
def test_frontend_instance_precedes_unrelated_desktop_runtime(tmp_path, monkeypatch, setting):
    for name in ('LAZYMIND_WORKFLOW_BASE_URL', 'LAZYMIND_ENDPOINT_HOST_CORE_BASE_URL',
                 'LAZYMIND_CORE_API_URL', 'LAZYMIND_CORE_SERVICE_URL',
                 'LAZYMIND_SERVER_URL', 'LAZYMIND_PUBLIC_URL'):
        monkeypatch.delenv(name, raising=False)
    generated = tmp_path / 'generated'
    generated.mkdir()
    (generated / 'service-endpoints.json').write_text(json.dumps({
        'host': {'coreBaseUrl': 'http://127.0.0.1:18001'},
    }))
    monkeypatch.setenv('LAZYMIND_RUNTIME_ROOT', str(tmp_path))
    monkeypatch.setenv(setting, 'http://localhost:8090/')
    assert discover_connection() == ConnectionInfo('http://localhost:8090/api/core', f'env:{setting}')


def test_explicit_core_address_is_preserved_with_browser_address(monkeypatch):
    monkeypatch.setenv('LAZYMIND_WORKFLOW_BASE_URL', 'http://core:8000')
    monkeypatch.setenv('LAZYMIND_SERVER_URL', 'http://localhost:8090')
    monkeypatch.setenv('LAZYMIND_PUBLIC_URL', 'http://localhost:8090')
    assert discover_connection().base_url == 'http://core:8000'


def test_mcp_binds_one_client_for_status_submission_and_polling():
    client = MagicMock()
    client.external_connection_status.return_value = {'connected': True}
    client.start_skill_workflow_task.return_value.result = {'task_id': 'task'}
    client.get_skill_workflow_task.return_value.result = {'task_id': 'task', 'status': 'running'}
    factory = MagicMock(return_value=client)
    server = WorkflowMCPServer(factory, mode='external', agent_type='codex')
    server.call_tool('workflow_connection_status', {})
    server.call_tool('start_skill_workflow_task', {
        'skill': {'name': 'demo', 'url': 'https://example.com/demo.zip'}, 'task_description': 'Run'})
    server.call_tool('get_skill_workflow_task', {'task_id': 'task'})
    factory.assert_called_once_with()
    client.get_skill_workflow_task.assert_called_once_with('task')


def test_explicit_missing_runtime_does_not_fall_back_to_another_instance(tmp_path, monkeypatch):
    from lazymind.workflow_sdk import WorkflowClientError
    from lazymind.workflow_sdk.client import _default_runtime_roots
    for name in ('LAZYMIND_WORKFLOW_BASE_URL', 'LAZYMIND_SERVER_URL', 'LAZYMIND_PUBLIC_URL',
                 'LAZYMIND_ENDPOINT_HOST_CORE_BASE_URL', 'LAZYMIND_CORE_API_URL', 'LAZYMIND_CORE_SERVICE_URL'):
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setenv('LAZYMIND_RUNTIME_ROOT', str(tmp_path / 'missing-instance'))
    assert _default_runtime_roots() == [tmp_path / 'missing-instance']
    with pytest.raises(WorkflowClientError) as error:
        discover_connection()
    assert error.value.code == 'LAZYMIND_NOT_FOUND'


@pytest.mark.parametrize('value', ['localhost:8090', 'file:///tmp/core',
                                  'http://user:secret@localhost', 'http://localhost/?token=secret',
                                  'http://localhost/#settings', 'http://localhost:wrong', 'http://[bad'])
def test_invalid_instance_url_fails_before_transport(value):
    from lazymind.workflow_sdk import WorkflowClient, WorkflowClientError
    transport = MagicMock()
    with pytest.raises(WorkflowClientError) as error:
        WorkflowClient(value, transport=transport)
    assert error.value.code == 'INVALID_CONNECTION_URL'
    assert 'secret' not in str(error.value)
    assert not transport.mock_calls


def test_gateway_prefix_is_added_once(monkeypatch):
    monkeypatch.delenv('LAZYMIND_WORKFLOW_BASE_URL', raising=False)
    monkeypatch.setenv('LAZYMIND_SERVER_URL', 'http://localhost:8090/api/core/')
    assert discover_connection().base_url == 'http://localhost:8090/api/core'


def test_task_links_remain_bound_to_initial_browser_instance(monkeypatch):
    from lazymind.workflow_sdk import WorkflowClient
    monkeypatch.delenv('LAZYMIND_PUBLIC_URL', raising=False)
    monkeypatch.setenv('LAZYMIND_SERVER_URL', 'http://localhost:8090')
    transport = MagicMock()
    transport.get.return_value = MagicMock(status_code=200, json=lambda: {
        'code': 0, 'data': {'lazymind_url': '/agent/chat/home/conversation'}})
    client = WorkflowClient('http://localhost:18001', transport=transport)
    monkeypatch.setenv('LAZYMIND_SERVER_URL', 'http://localhost:5024')
    result = client.get_skill_workflow_task('task').result
    assert result['lazymind_url'] == 'http://localhost:8090/agent/chat/home/conversation'


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
    assert {
        'start_skill_workflow_task', 'get_skill_workflow_task',
        'get_skill_workflow_result',
    } <= names


def test_agent_kit_profiles_declare_only_real_mcp_tools():
    profiles = sorted((Path(__file__).resolve().parents[2]
                       / 'skills/workflow-agent-kit/profiles').glob('*.yaml'))
    assert profiles
    conversion_tools = {
        'list_skills', 'preflight_skill_workflow_conversion',
        'get_skill_conversion_context', 'create_workflow_draft',
        'update_workflow_draft_file', 'validate_workflow_draft',
        'get_workflow_diagnostics', 'publish_workflow',
        'start_skill_workflow_task', 'get_skill_workflow_task',
        'get_skill_workflow_result',
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


def test_mcp_starts_hosted_skill_workflow_task():
    client = MagicMock()
    client.start_skill_workflow_task.return_value = MagicMock(result={
        'task_id': 'task-1', 'status': 'running',
    })
    result = WorkflowMCPServer(lambda: client).call_tool('start_skill_workflow_task', {
        'agent_type': 'codex',
        'skill': {'name': 'demo-skill', 'url': 'https://skillhub.cn/skills/user_demo/demo-skill'},
        'task_description': 'Run the conversion',
        'external_thread_id': 'codex-thread',
        'input_bindings': {'brief': {'resource_id': 'res-1', 'revision': 1}},
        'input_files': [{'material_id': 'notes', 'name': 'notes.txt',
                         'mime_type': 'text/plain', 'content_base64': 'aGk='}],
        'idempotency_key': 'request-1',
    })
    assert result['structuredContent']['task_id'] == 'task-1'
    client.start_skill_workflow_task.assert_called_once_with(
        'codex', {'name': 'demo-skill', 'url': 'https://skillhub.cn/skills/user_demo/demo-skill'}, 'Run the conversion',
        external_conversation_id='',
        external_thread_id='codex-thread',
        input_bindings={'brief': {'resource_id': 'res-1', 'revision': 1}},
        input_files=[{'material_id': 'notes', 'name': 'notes.txt',
                      'mime_type': 'text/plain', 'content_base64': 'aGk='}],
        config={},
        idempotency_key='request-1',
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


def test_sdk_hosted_skill_workflow_task_routes():
    transport = MagicMock()
    transport.post.return_value = MagicMock(
        status_code=200,
        json=lambda: {'ok': True, 'data': {'task_id': 'task-1', 'status': 'queued'}},
    )
    transport.get.return_value = MagicMock(
        status_code=200,
        json=lambda: {'ok': True, 'data': {'task_id': 'task-1', 'status': 'succeeded'}},
    )
    from lazymind.workflow_sdk import WorkflowClient

    client = WorkflowClient('http://core/api/core', 'u1', transport=transport)
    created = client.start_skill_workflow_task(
        'codex', {'name': 'demo-skill', 'url': 'https://skillhub.cn/skills/user_demo/demo-skill'}, 'Run this Skill',
        external_thread_id='thread-1',
        input_files=[{'material_id': 'brief', 'name': 'brief.txt',
                      'mime_type': 'text/plain', 'content_base64': 'aGk='}],
        idempotency_key='task-key',
    )
    status = client.get_skill_workflow_task('task-1')
    result = client.get_skill_workflow_result('task-1')

    assert created.result['task_id'] == 'task-1'
    assert status.result['status'] == 'succeeded'
    assert result.result['status'] == 'succeeded'
    post_call = transport.post.call_args
    assert post_call.args[0].endswith('/external-agent/workflow-tasks')
    assert post_call.kwargs['json']['agent_type'] == 'codex'
    assert post_call.kwargs['json']['skill']['name'] == 'demo-skill'
    assert 'skill_id' not in post_call.kwargs['json']
    assert post_call.kwargs['json']['external_thread_id'] == 'thread-1'
    assert post_call.kwargs['json']['input_files'][0]['material_id'] == 'brief'
    assert post_call.kwargs['headers']['Idempotency-Key'] == 'task-key'
    assert transport.get.call_args_list[0].args[0].endswith(
        '/external-agent/workflow-tasks/task-1'
    )
    assert transport.get.call_args_list[1].args[0].endswith(
        '/external-agent/workflow-tasks/task-1/result'
    )


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
