import base64
import json
import os
import threading
from pathlib import Path

import httpx
import pytest

from lazymind.chat.workflow.remote_executor import RemoteWorkflowExecutor


class StopReceiptRuntime:
    async def acknowledge_stop(self, *_):
        pass


@pytest.mark.asyncio
async def test_stop_receipt_retries_transport_and_service_errors_without_rerunning_tools(monkeypatch):
    from unittest.mock import AsyncMock
    from lazymind.chat.workflow.client import RemoteExecutorClient

    worker = RemoteWorkflowExecutor()
    worker.runtime = RemoteExecutorClient('http://core', 'executor', 'lazymind')
    execute = AsyncMock()
    monkeypatch.setattr(worker, '_execute_claim', execute)
    sleep = AsyncMock()
    monkeypatch.setattr('lazymind.chat.workflow.client.asyncio.sleep', sleep)
    requests = []

    def transport(request):
        requests.append(request)
        if len(requests) == 1:
            raise httpx.ReadTimeout('receipt response lost', request=request)
        return httpx.Response(503 if len(requests) == 2 else 200)

    async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
        await worker._run_claim(client, {'attempt_id': 'attempt', 'lease_token': 'lease'})
    execute.assert_awaited_once()
    assert len(requests) == 3
    assert all(request.url.path == '/internal/workflow-attempts/attempt:stop-ack'
               and json.loads(request.content) == {'lease_token': 'lease'} for request in requests)
    assert [call.args[0] for call in sleep.await_args_list] == [0.5, 1.0]


@pytest.mark.asyncio
@pytest.mark.parametrize('status', [401, 403, 404, 409, 422])
async def test_stop_receipt_does_not_retry_permanent_rejection(monkeypatch, status):
    from unittest.mock import AsyncMock
    from lazymind.chat.workflow.client import RemoteExecutorClient

    runtime = RemoteExecutorClient('http://core', 'executor', 'lazymind')
    sleep = AsyncMock()
    monkeypatch.setattr('lazymind.chat.workflow.client.asyncio.sleep', sleep)
    async with httpx.AsyncClient(transport=httpx.MockTransport(lambda _: httpx.Response(status))) as client:
        with pytest.raises(httpx.HTTPStatusError):
            await runtime.acknowledge_stop(client, 'attempt', 'lease')
    sleep.assert_not_awaited()


@pytest.mark.asyncio
async def test_stop_receipt_retry_is_cancellable(monkeypatch):
    import asyncio
    from unittest.mock import AsyncMock
    from lazymind.chat.workflow.client import RemoteExecutorClient

    sleep = AsyncMock(side_effect=asyncio.CancelledError)
    monkeypatch.setattr('lazymind.chat.workflow.client.asyncio.sleep', sleep)
    runtime = RemoteExecutorClient('http://core', 'executor', 'lazymind')
    async with httpx.AsyncClient(transport=httpx.MockTransport(lambda _: httpx.Response(503))) as client:
        with pytest.raises(asyncio.CancelledError):
            await runtime.acknowledge_stop(client, 'attempt', 'lease')
    sleep.assert_awaited_once()


@pytest.mark.asyncio
@pytest.mark.parametrize('outcome', ['success', 'lease_lost', 'setup_failed'])
async def test_stop_receipt_is_sent_only_after_execution_cleanup(monkeypatch, outcome):
    from unittest.mock import AsyncMock

    worker = RemoteWorkflowExecutor()
    worker.runtime = AsyncMock()
    order = []

    async def execute(*_):
        try:
            worker.runtime.acknowledge_stop.assert_not_awaited()
            if outcome == 'setup_failed':
                raise RuntimeError('setup failed')
            return outcome
        finally:
            order.append('executor exited')

    async def acknowledge(*_):
        assert order == ['executor exited']
        order.append('receipt')

    monkeypatch.setattr(worker, '_execute_claim', execute)
    worker.runtime.acknowledge_stop.side_effect = acknowledge
    claim = {'attempt_id': 'attempt', 'lease_token': 'lease'}
    if outcome == 'setup_failed':
        with pytest.raises(RuntimeError, match='setup failed'):
            await worker._run_claim(None, claim)
    else:
        await worker._run_claim(None, claim)
    worker.runtime.acknowledge_stop.assert_awaited_once_with(None, 'attempt', 'lease')
    assert order == ['executor exited', 'receipt']


@pytest.mark.asyncio
@pytest.mark.parametrize('status', ['interrupted', 'cancelled', 'canceled'])
async def test_interrupted_subagent_cancels_attempt_without_failure_or_output_checks(monkeypatch, tmp_path, status):
    from unittest.mock import AsyncMock
    from lazymind.chat.engine.subagent import runner

    worker = RemoteWorkflowExecutor()
    runtime = AsyncMock()
    runtime.context.return_value = {'metadata': {'task_id': 'task-1'}, 'inputs': {}}
    runtime.execution_spec.return_value = {
        'task': {'input_slots': [], 'output_slots': ['required-image']},
        'workspace_path': str(tmp_path), 'params': {}, 'steps': [], 'llm_config': {},
        'system_mcp_config': [{'id': 'lazymind-knowledge', 'allowed_tools': []}],
    }
    worker.runtime = runtime
    checks = AsyncMock()
    monkeypatch.setattr(worker, '_run_post_step_checks', checks)

    async def stream(**_kwargs):
        assert _kwargs['system_mcp_config'] == [{'id': 'lazymind-knowledge', 'allowed_tools': []}]
        yield 'data: ' + json.dumps({'type': 'done', 'status': status, 'summary': 'stopped by user'}) + '\n\n'

    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    await worker._run_claim(object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'})
    runtime.cancel.assert_awaited_once()
    runtime.fail.assert_not_awaited()
    runtime.complete.assert_not_awaited()
    checks.assert_not_awaited()
    assert runtime.task_event.call_args.args[-1]['status'] == status


def test_remote_executor_preserves_ordinary_subagent_stream_event_shape():
    event = {'type': 'text', 'text': 'hello', 'think': ''}
    assert RemoteWorkflowExecutor._parse_frame(
        'data: ' + json.dumps(event) + '\n\n') == event
    assert RemoteWorkflowExecutor._parse_frame('data: [DONE]\n\n') is None


def test_remote_executor_ignores_non_json_stream_frames():
    assert RemoteWorkflowExecutor._parse_frame('event: heartbeat\n\n') is None


@pytest.mark.asyncio
async def test_remote_event_failure_closes_subagent_inside_execution_scope(monkeypatch, tmp_path):
    from types import SimpleNamespace
    from unittest.mock import AsyncMock
    from lazymind.chat.engine.subagent import runner
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext

    order = []

    async def stream(**kwargs):
        try:
            yield 'data: {"type":"text","text":"ready"}\n\n'
        finally:
            assert WorkspaceContext.from_config({}).workflow_full_trust
            order.append('closed')

    async def event(_client, _task, _lease, payload):
        if payload['type'] == 'text':
            raise RuntimeError('event transport failed')

    async def fail(*_args, **_kwargs):
        assert order == ['closed']
        order.append('failed')

    worker = RemoteWorkflowExecutor()
    worker.runtime = SimpleNamespace(
        context=AsyncMock(return_value={'metadata': {'task_id': 'test-close'}, 'inputs': {}}),
        execution_spec=AsyncMock(return_value={
            'task': {'input_slots': [], 'output_slots': []}, 'params': {}, 'steps': [],
            'workspace_path': str(tmp_path), 'llm_config': {},
        }),
        task_event=event, fail=fail, acknowledge_stop=AsyncMock(),
    )
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    await worker._run_claim(object(), {'attempt_id': 'attempt', 'lease_token': 'lease'})
    assert order == ['closed', 'failed']
    assert not WorkspaceContext.from_config({}).workflow_full_trust


@pytest.mark.asyncio
async def test_post_step_capability_check_runs_in_analysis_attempt_without_another_subagent(
    monkeypatch, tmp_path,
):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        events = []
        completed = None

        async def context(self, *_):
            return {'metadata': {'task_id': 'task-analysis'}, 'inputs': {},
                    'prompt': 'Analyze requirements only; save the capability marker.',
                    'acceptance_criteria': ['Requirement brief is complete']}

        async def execution_spec(self, *_):
            return {
                'task': {'input_slots': [], 'output_slots': ['workflow_routing']},
                'workspace_path': str(tmp_path / 'task-analysis'),
                'params': {
                    'workflow_id': 'image-workflow',
                    'revision_id': 'revision-1',
                    'step_id': 'analyze_subject',
                    'workflow_runtime': {'post_step_checks': [{
                        'step_id': 'analyze_subject',
                        'tool': 'check_image_workflow_capabilities',
                        'arguments': {'workflow_routing': 'workflow_routing'},
                    }]},
                },
                'steps': [], 'llm_config': {},
            }

        async def task_event(self, _client, _task, _lease, event):
            self.events.append(event)

        async def artifact(self, *_):
            return None

        async def progress(self, *_):
            return None

        async def complete(self, _client, _attempt, _lease, result):
            self.completed = result

        async def fail(self, *_):
            pytest.fail('ready capability check must not fail the attempt')

    subagent_runs = 0

    async def stream(**_kwargs):
        nonlocal subagent_runs
        subagent_runs += 1
        from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
        assert WorkspaceContext.from_config({}).workflow_full_trust
        scope = _kwargs['task_spec']['params']['_display_plan_scope']
        assert scope['prompt'] == 'Analyze requirements only; save the capability marker.'
        assert scope['acceptance_criteria'] == ['Requirement brief is complete']
        yield 'data: ' + json.dumps({
            'type': 'artifact', 'slot': 'workflow_routing', 'content_type': 'text',
            'seq': 1, 'value': {'text': 'WORKFLOW: CREATE_NEW\nREQUIRES: image_generator'},
        }) + '\n\n'
        yield 'data: {"type":"done","status":"succeeded","summary":"analyzed"}\n\n'

    checked = []

    def check_image_workflow_capabilities(workflow_routing):
        from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
        assert WorkspaceContext.from_config({}).workflow_full_trust
        checked.append(workflow_routing)
        return {'status': 'ready', 'required': ['image_generator']}

    from lazymind.chat.engine.subagent import runner
    runtime = Runtime()
    worker.runtime = runtime
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    monkeypatch.setattr(runner, 'load_workflow_tools', lambda _params, _names: {
        'check_image_workflow_capabilities': check_image_workflow_capabilities,
    })

    await worker._run_claim(
        object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'},
    )

    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    assert not WorkspaceContext.from_config({}).workflow_full_trust
    assert subagent_runs == 1
    assert checked == ['WORKFLOW: CREATE_NEW\nREQUIRES: image_generator']
    assert runtime.completed['summary'] == 'analyzed'
    assert [event['type'] for event in runtime.events] == [
        'artifact', 'tool_calls', 'tool_results', 'done',
    ]
    assert runtime.events[2]['tool_results'][0]['result']['value']['status'] == 'ready'


@pytest.mark.parametrize('next_step', ['collect_materials', 'optimize_prompt'])
@pytest.mark.parametrize('large', [False, True])
@pytest.mark.asyncio
async def test_post_step_capability_failure_is_terminal_and_keeps_card_marker(
    monkeypatch, tmp_path, next_step, large,
):
    worker = RemoteWorkflowExecutor()
    marker = (
        'MEDIA_CAPABILITY_DEPENDENCY_MISSING '
        '{"status":"blocked","missing":[{"id":"video_generator"}]}'
    )

    class Runtime(StopReceiptRuntime):
        events = []
        failure = ''
        checkpoint = None
        completed = None
        artifacts = []

        async def context(self, *_):
            return {'metadata': {'task_id': 'task-analysis'}, 'inputs': {},
                    'declared_output_types': {'workflow_routing': 'text'},
                    'workflow_revision': 'revision-1',
                    'post_step_checkpoint': self.checkpoint}

        async def execution_spec(self, *_):
            return {
                'task': {'conversation_id': 'conversation-1',
                         'input_slots': [], 'output_slots': ['workflow_routing']},
                'workspace_path': str(tmp_path / 'task-analysis'),
                'user_env_vars': {'Mixed_API_KEY': 'synthetic-secret'} if configured else {},
                'params': {
                    'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
                    'step_id': 'analyze_subject',
                    'user_id': 'user-1',
                    'parent_agentic_config': {'_core_workspace_context': {
                        'workspace_id': 'workspace-1', 'root': str(tmp_path),
                        'workspace_version': 1,
                        'permission_mode': 'always_ask',
                        'permission_version': 2 if configured else 1,
                        'opaque_tool_grants': ['shell'] if configured else [],
                    }},
                    'workflow_runtime': {'post_step_checks': [{
                        'step_id': 'analyze_subject',
                        'tool': 'check_image_workflow_capabilities',
                        'arguments': {'workflow_routing': 'workflow_routing'},
                    }]},
                },
                'steps': [], 'llm_config': ({'video_generator': {
                    'source': 'test-provider', 'model': 'configured-video', 'type': 'text2video',
                }} if configured else {}),
            }

        async def task_event(self, _client, _task, _lease, event):
            self.events.append(event)

        async def artifact(self, _client, _attempt, _lease, artifact):
            self.artifacts.append(artifact)

        async def upload_artifact_file(self, *_):
            return '/core-host/uploads/workflow-artifacts/route.txt'

        async def progress(self, *_):
            return None

        async def complete(self, _client, _attempt, _lease, result):
            self.completed = result

        async def fail(self, _client, _attempt, _lease, message, *, post_step_checkpoint=None):
            self.failure = message
            self.checkpoint = post_step_checkpoint

    subagent_runs = 0

    async def stream(**_kwargs):
        nonlocal subagent_runs
        subagent_runs += 1
        route = 'WORKFLOW: CREATE_ANIMATED_MEME\nREQUIRES: video_generator'
        if large:
            route += '\n' + 'Detailed requirements. ' * 3000
            path = Path(_kwargs['task_spec']['workspace_path']) / 'route.txt'
            path.write_text(route)
            content_type, value = 'file', {'type': 'text', 'path': str(path)}
        else:
            content_type, value = 'text', {'text': route}
        yield 'data: ' + json.dumps({
            'type': 'artifact', 'slot': 'workflow_routing', 'content_type': content_type,
            'seq': 1, 'value': value,
        }) + '\n\n'
        yield 'data: ' + json.dumps({
            'type': 'done', 'status': 'succeeded', 'summary': 'analyzed',
            'control': {'next_step': next_step},
        }) + '\n\n'

    model_yaml = tmp_path / 'models.yaml'
    model_yaml.write_text('video_generator:\n  source: dynamic\n  type: text2video\n')
    configured = False
    checks = []

    def blocked(**kwargs):
        checks.append(kwargs)
        if len(checks) > 1:
            # This is the real dynamic-role reader used by capability checks.
            # Resuming without a SubAgent must still inject Core's fresh config.
            from lazymind.model_config import is_model_role_available
            assert is_model_role_available('video_generator', config_path=str(model_yaml)) == configured
            import lazyllm
            from lazyllm.tools import get_dynamic_env_vars
            assert get_dynamic_env_vars() == (
                {'Mixed_API_KEY': 'synthetic-secret'} if configured else {}
            )
            assert lazyllm.globals['conversation_env_overrides'] == {}
            from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
            restored = lazyllm.globals['agentic_config']
            permission = WorkspaceContext.from_config(restored)
            assert permission.workflow_full_trust
            assert permission.workspace_id == 'workspace-1'
            assert permission.root == str(tmp_path)
            assert permission.user_id == 'user-1'
            assert permission.conversation_id == 'conversation-1'
            assert permission.permission_version == (2 if configured else 1)
            assert permission.opaque_tool_grants == (frozenset({'shell'}) if configured else frozenset())
            assert dict(permission.execution) == {
                'task_id': 'task-analysis', 'attempt_id': f'attempt-{len(checks)}',
                'generation': str(len(checks)), 'lease_token': f'lease-{len(checks)}',
            }
            assert restored['_subagent_workspace'] == str(tmp_path / 'task-analysis')
        if not configured:
            raise RuntimeError(marker)
        return {'status': 'ready'}

    from lazymind.chat.engine.subagent import runner
    runtime = Runtime()
    worker.runtime = runtime
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    monkeypatch.setattr(runner, 'load_workflow_tools', lambda _params, _names: {
        'check_image_workflow_capabilities': blocked,
    })

    await worker._run_claim(
        object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'},
    )

    assert runtime.failure == marker
    tool_result = next(
        event for event in runtime.events if event['type'] == 'tool_results'
    )
    assert tool_result['tool_results'][0]['result'] == {
        'ok': False, 'value': marker,
    }
    assert runtime.events[-1] == {
        'type': 'error', 'status': 'failed', 'message': marker,
    }

    assert runtime.completed is None
    checkpoint = runtime.checkpoint
    assert checkpoint['control'] == {'next_step': next_step}
    assert checkpoint['summary'] == 'analyzed'
    assert checkpoint['workflow_revision'] == 'revision-1'
    if large:
        assert checks[0]['workflow_routing'].startswith('WORKFLOW: CREATE_ANIMATED_MEME\n')
        assert len(checks[0]['workflow_routing']) > 32768
        (tmp_path / 'task-analysis' / 'route.txt').unlink()

    # A second blocked attempt must preserve the same checkpoint. The third
    # attempt succeeds with the exact saved route and zero new model work.
    await worker._run_claim(object(), {'attempt_id': 'attempt-2', 'lease_token': 'lease-2', 'fencing_generation': 2})
    assert runtime.completed is None
    assert runtime.checkpoint == checkpoint
    configured = True
    await worker._run_claim(object(), {'attempt_id': 'attempt-3', 'lease_token': 'lease-3', 'fencing_generation': 3})
    assert subagent_runs == 1
    assert len(checks) == 3
    assert checks[0] == checks[1] == checks[2]
    assert runtime.completed['control'] == {'next_step': next_step}
    assert runtime.completed['artifacts'] == checkpoint['artifacts']
    assert 'synthetic-secret' not in json.dumps(checkpoint)
    assert 'synthetic-secret' not in json.dumps(runtime.events)
    assert runtime.events[-1]['status'] == 'succeeded'
    assert len(runtime.artifacts) == 3


@pytest.mark.asyncio
async def test_remote_executor_persists_artifact_before_task_center_event(monkeypatch, tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        task_artifact = None
        workflow_artifact = None
        uploaded = None

        async def context(self, *_):
            return {'metadata': {'task_id': 'task-1'}, 'inputs': {}}

        async def execution_spec(self, *_):
            return {'task': {'input_slots': [], 'output_slots': ['image_output']},
                    'workspace_path': str(tmp_path / 'task-1'),
                    'params': {}, 'steps': [], 'llm_config': {}}

        async def heartbeat(self, *_):
            return None

        async def task_event(self, _client, _task, _lease, event):
            if event.get('type') == 'artifact':
                self.task_artifact = event

        async def artifact(self, _client, _attempt, _lease, artifact):
            self.workflow_artifact = artifact

        async def upload_artifact_file(self, _client, attempt, lease, filename, content):
            self.uploaded = (attempt, lease, filename, content)
            return '/var/lib/lazymind/uploads/workflow-artifacts/session-1/attempt-1/result.png'

        async def complete(self, *_):
            return None

        async def fail(self, *_):
            pytest.fail('the attempt should not fail')

    async def stream(**kwargs):
        output = os.path.join(kwargs['task_spec']['workspace_path'], 'result.png')
        with open(output, 'wb') as handle:
            handle.write(b'png')
        yield 'data: ' + json.dumps({
            'type': 'artifact', 'slot': 'image_output', 'content_type': 'image',
            'seq': 1, 'value': {'path': output},
        }) + '\n\n'
        yield 'data: {"type":"done","status":"succeeded","summary":"done"}\n\n'

    from lazymind.chat.engine.subagent import runner
    runtime = Runtime()
    worker.runtime = runtime
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)

    await worker._run_claim(object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'})

    task_value = runtime.task_artifact['value']['path']
    workflow_value = runtime.workflow_artifact['value']['path']
    assert task_value == '/var/lib/lazymind/uploads/workflow-artifacts/session-1/attempt-1/result.png'
    assert task_value == workflow_value
    assert runtime.uploaded == ('attempt-1', 'lease-1', 'result.png', b'png')


@pytest.mark.asyncio
@pytest.mark.parametrize('declared_type,large', [
    ('json', False), ('json', True), ('text', False), ('text', True), ('file', True),
])
async def test_post_step_checks_receive_scalar_content_and_keep_file_references(
    monkeypatch, tmp_path, declared_type, large,
):
    import importlib.util
    from lazymind.chat.engine.subagent import context, runner, tools

    source = Path(__file__).resolve().parents[3] / 'workflows/paper_expansion/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('paper_result_check', source)
    package = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(package)
    description = 'Full-text analysis. ' * (4000 if large else 1)
    body = {
        'user_instruction': 'Research requirements', 'candidate_directions': ['agents'],
        'selected_directions': ['agents'],
        'papers': [{'paper': {'paper_id': 'paper-1'}, 'is_seed': True,
                    'is_important': False, 'key_contribution': description}],
    }
    checked = []
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        completed = None
        failure = None
        published = None

        async def context(self, *_):
            return {'metadata': {'task_id': 'scalar-task'}, 'inputs': {},
                    'declared_output_types': {'result': declared_type}}

        async def execution_spec(self, *_):
            return {
                'task': {'input_slots': [], 'output_slots': ['result']},
                'workspace_path': str(tmp_path / 'scalar-task'), 'steps': [], 'llm_config': {},
                'params': {'step_id': 'compose', 'workflow_runtime': {'post_step_checks': [{
                    'step_id': 'compose', 'tool': 'validate_result', 'arguments': {'result': 'result'},
                }]}},
            }

        async def task_event(self, *_):
            return None

        async def artifact(self, _client, _attempt, _lease, artifact):
            self.published = artifact

        async def upload_artifact_file(self, _client, _attempt, _lease, _name, content):
            assert content
            # Core can run on a different host; its durable path is not readable here.
            return '/core-host/uploads/workflow-artifacts/session/attempt/result.txt'

        async def complete(self, _client, _attempt, _lease, result):
            self.completed = result

        async def fail(self, _client, _attempt, _lease, message):
            self.failure = message

    def validate_result(result):
        checked.append(result)
        if declared_type == 'json':
            return package.academic_validate_result(result)
        if declared_type == 'text':
            assert result == description
        else:
            assert result['path'] == '/core-host/uploads/workflow-artifacts/session/attempt/result.txt'
        return {'status': 'valid'}

    async def stream(**kwargs):
        events = []
        ctx = context.SubAgentContext(
            'scalar-task', 'chat', 'workflow_step', 'compose', kwargs['task_spec']['params'],
            kwargs['task_spec']['workspace_path'], [], ['result'], None, events.append,
        )
        monkeypatch.setattr(tools, 'require_context', lambda: ctx)
        if declared_type == 'file':
            path = Path(ctx.workspace_path) / 'data.json'
            path.write_text(json.dumps(body))
            # A file slot remains a reference even when it contains JSON.
            events.append({'type': 'artifact', 'slot': 'result', 'content_type': 'file',
                           'seq': 1, 'value': {'type': 'json', 'path': str(path)}})
        else:
            tools._save_artifact('result', body if declared_type == 'json' else description,
                                 content_type=declared_type)
        for event in events:
            yield 'data: ' + json.dumps(event) + '\n\n'
        yield 'data: {"type":"done","status":"succeeded","summary":"saved"}\n\n'

    runtime = Runtime()
    worker.runtime = runtime
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    monkeypatch.setattr(runner, 'load_workflow_tools', lambda *_: {'validate_result': validate_result})
    await worker._run_claim(object(), {'attempt_id': 'attempt', 'lease_token': 'lease'})

    assert runtime.failure is None
    assert runtime.completed is not None
    assert len(checked) == 1
    if declared_type == 'json':
        assert checked[0] == body
    if large:
        assert runtime.published['content_type'] == 'file'
        assert runtime.completed['artifacts'][0]['value']['path'].startswith('/core-host/')


def test_post_step_scalar_check_rejects_files_outside_its_task_workspace(tmp_path):
    workspace = tmp_path / 'task'
    workspace.mkdir()
    outside = tmp_path / 'other-task.json'
    outside.write_text('{"private": true}')
    artifact = {'slot': 'result', 'content_type': 'file',
                'value': {'type': 'json', 'path': str(outside)}}
    params = {'step_id': 'compose', 'output_slot_types': {'result': 'json'},
              'workflow_runtime': {'post_step_checks': [{
                  'step_id': 'compose', 'arguments': {'result': 'result'},
              }]}}
    with pytest.raises(ValueError, match='outside its task workspace'):
        RemoteWorkflowExecutor._inline_post_step_artifact(artifact, params, str(workspace))


@pytest.mark.asyncio
async def test_remote_executor_materializes_fenced_inputs_in_host_workspace(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        calls = []

        async def input(self, _client, attempt, lease, material):
            self.calls.append((attempt, lease, material))
            return {'name': '../brief.txt', 'content_base64': 'aGVsbG8='}

    runtime = Runtime()
    worker.runtime = runtime
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', {'inputs': {'brief': {}}}, str(tmp_path))
    assert runtime.calls == [('attempt-1', 'lease-1', 'brief')]
    assert values['brief'] == str(tmp_path / 'inputs' / 'brief.txt')
    assert (tmp_path / 'inputs' / 'brief.txt').read_text() == 'hello'


@pytest.mark.asyncio
async def test_remote_executor_passes_external_scalar_inputs_as_values(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        async def input(self, _client, _attempt, _lease, material):
            values = {
                'topic': ('topic.txt', '5Lq65bel5pm66IO96L6F5Yqp6L2v5Lu25rWL6K+V'),
                'word_target': ('word_target.txt', 'NDAw'),
                'options': ('options.txt', 'eyJmb3JtYXQiOiAiZG9jeCJ9'),
            }
            name, encoded = values[material]
            return {'name': name, 'mime_type': 'text/plain', 'content_base64': encoded}

    worker.runtime = Runtime()
    context = {
        'declared_input_types': {
            'topic': 'text', 'word_target': 'text', 'options': 'json',
        },
        'inputs': {
            key: {'source_type': 'input_resource'}
            for key in ('topic', 'word_target', 'options')
        },
    }
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', context, str(tmp_path),
    )

    assert values == {
        'topic': '人工智能辅助软件测试',
        'word_target': '400',
        'options': {'format': 'docx'},
    }
    assert not (tmp_path / 'inputs').exists()


@pytest.mark.asyncio
async def test_remote_executor_unwraps_upstream_artifacts_by_declared_type(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        async def input(self, _client, _attempt, _lease, material):
            values = {
                'workflow_routing': {
                    'text': 'WORKFLOW: FIND_AND_EDIT\nNEXT_STEPS: collect_materials',
                },
                'source_image': {
                    'path': 'https://images.example.test/haaland.png',
                    'caption': 'validated source',
                },
            }
            raw = json.dumps(values[material]).encode('utf-8')
            return {
                'name': material + '.json',
                'mime_type': 'application/json',
                'content_base64': base64.b64encode(raw).decode('ascii'),
            }

    worker.runtime = Runtime()
    context = {
        'declared_input_types': {
            'workflow_routing': 'text', 'source_image': 'image',
        },
        'declared_input_transports': {
            'workflow_routing': 'value', 'source_image': 'reference',
        },
        'inputs': {
            'workflow_routing': {'source_type': 'artifact'},
            'source_image': {'source_type': 'artifact'},
        },
    }
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', context, str(tmp_path),
    )

    assert values['workflow_routing'].startswith('WORKFLOW: FIND_AND_EDIT')
    assert values['source_image'] == {
        'path': 'https://images.example.test/haaland.png',
        'caption': 'validated source',
    }
    assert worker._direct_input_value_slots(context) == {
        'workflow_routing', 'source_image',
    }
    assert worker._resolved_input_transports(context, values) == {
        'workflow_routing': 'value', 'source_image': 'reference',
    }
    assert not (tmp_path / 'inputs').exists()


@pytest.mark.asyncio
async def test_remote_executor_infers_artifact_types_when_core_omits_them(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        async def input(self, _client, _attempt, _lease, material):
            values = {
                'workflow_routing': {'text': 'WORKFLOW: FIND_AND_EDIT'},
                'material_images': {
                    'path': 'https://images.example.test/haaland.jpg',
                    'caption': 'validated source',
                },
            }
            raw = json.dumps(values[material]).encode('utf-8')
            item = {
                'name': material + '.json',
                'mime_type': 'application/json',
                'content_base64': base64.b64encode(raw).decode('ascii'),
            }
            return {'items': [item]} if material == 'material_images' else item

    worker.runtime = Runtime()
    context = {
        'inputs': {
            'workflow_routing': {'source_type': 'artifact'},
            'material_images': [{'source_type': 'artifact'}],
        },
    }
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', context, str(tmp_path),
    )

    assert values == {
        'workflow_routing': 'WORKFLOW: FIND_AND_EDIT',
        'material_images': [{
            'path': 'https://images.example.test/haaland.jpg',
            'caption': 'validated source',
        }],
    }
    assert context['declared_input_types'] == {
        'workflow_routing': 'text',
        'material_images': 'image',
    }
    assert not (tmp_path / 'inputs').exists()


@pytest.mark.asyncio
async def test_remote_executor_materializes_scalar_artifacts_when_transport_is_path(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        async def input(self, _client, _attempt, _lease, material):
            payloads = {
                'brief': {'text': 'approved brief'},
                'parameters': {'data': {'output_format': 'docx'}},
            }
            raw = json.dumps(payloads[material]).encode('utf-8')
            return {
                'name': material + '.json',
                'mime_type': 'application/json',
                'content_base64': base64.b64encode(raw).decode('ascii'),
            }

    worker.runtime = Runtime()
    context = {
        'declared_input_types': {'brief': 'text', 'parameters': 'json'},
        'declared_input_transports': {'brief': 'path', 'parameters': 'path'},
        'inputs': {
            'brief': {'source_type': 'artifact'},
            'parameters': {'source_type': 'artifact'},
        },
    }
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', context, str(tmp_path),
    )

    assert json.loads(Path(values['brief']).read_text(encoding='utf-8')) == {
        'text': 'approved brief',
    }
    assert json.loads(Path(values['parameters']).read_text(encoding='utf-8')) == {
        'data': {'output_format': 'docx'},
    }
    assert worker._direct_input_value_slots(context) == set()
    assert worker._resolved_input_transports(context, values) == {
        'brief': 'path', 'parameters': 'path',
    }


@pytest.mark.asyncio
async def test_remote_executor_materializes_binary_image_inputs(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        async def input(self, _client, _attempt, _lease, _material):
            return {
                'name': 'upload.png',
                'mime_type': 'image/png',
                'content_base64': base64.b64encode(b'\x89PNG\r\n').decode('ascii'),
            }

    worker.runtime = Runtime()
    context = {
        'declared_input_types': {'source_image': 'image'},
        'inputs': {'source_image': {'source_type': 'input_resource'}},
    }
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', context, str(tmp_path),
    )

    path = tmp_path / 'inputs' / 'upload.png'
    assert values['source_image'] == str(path)
    assert path.read_bytes() == b'\x89PNG\r\n'
    assert worker._resolved_input_transports(context, values) == {'source_image': 'path'}


@pytest.mark.asyncio
async def test_remote_executor_materializes_every_list_input_in_order(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        async def input(self, _client, _attempt, _lease, _material):
            return {'items': [
                {'name': '../effect.png', 'content_base64': 'Zmlyc3Q='},
                {'name': '../effect.png', 'content_base64': 'c2Vjb25k'},
            ]}

    worker.runtime = Runtime()
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', {'inputs': {'effect_images': [{}, {}]}}, str(tmp_path))
    expected = [
        tmp_path / 'inputs' / 'effect_images' / '0001_effect.png',
        tmp_path / 'inputs' / 'effect_images' / '0002_effect.png',
    ]
    assert values['effect_images'] == [str(path) for path in expected]
    assert [path.read_text() for path in expected] == ['first', 'second']


@pytest.mark.asyncio
async def test_remote_executor_preserves_single_item_list_cardinality(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        async def input(self, _client, _attempt, _lease, _material):
            return {'items': [{'name': 'only.png', 'content_base64': 'b25seQ=='}]}

    worker.runtime = Runtime()
    values = await worker._resolve_inputs(
        object(), 'attempt-1', 'lease-1', {'inputs': {'images': [{}]}}, str(tmp_path))
    assert values['images'] == [str(tmp_path / 'inputs' / 'images' / '0001_only.png')]


@pytest.mark.asyncio
async def test_remote_executor_keeps_workflow_inputs_out_of_user_attachments(
    monkeypatch, tmp_path,
):
    worker = RemoteWorkflowExecutor()
    captured = {}

    class Runtime(StopReceiptRuntime):
        async def context(self, *_):
            return {
                'metadata': {'task_id': 'task-1'},
                'declared_input_types': {'brief': 'text'},
                'inputs': {'brief': {'source_type': 'input_resource'}},
            }

        async def input(self, *_):
            return {'name': 'brief.txt', 'content_base64': 'aGVsbG8='}

        async def execution_spec(self, *_):
            return {
                'task': {'input_slots': ['brief'], 'output_slots': []},
                'workspace_path': str(tmp_path / 'task-1'),
                'params': {
                    'history_files_per_turn': {'1': ['/uploads/real-user-file.txt']},
                },
                'steps': [],
                'llm_config': {},
            }

        async def complete(self, *_):
            return None

        async def fail(self, *_):
            pytest.fail('the attempt should not fail')

        async def task_event(self, *_):
            return None

    async def stream(**kwargs):
        captured.update(kwargs['task_spec']['params'])
        yield 'data: {"type":"done","status":"succeeded","summary":"done"}\n\n'

    from lazymind.chat.engine.subagent import runner
    worker.runtime = Runtime()
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)

    await worker._run_claim(object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'})

    assert captured['remote_inputs']['brief'] == 'hello'
    assert captured['remote_input_types'] == {'brief': 'text'}
    assert captured['remote_input_transports'] == {'brief': 'value'}
    assert captured['remote_input_value_slots'] == ['brief']
    assert '_attachment_context' not in captured
    agentic = runner._build_agentic_config(
        {
            'conversation_id': 'conversation-1',
            'objective': 'test',
            'workspace_path': str(tmp_path / 'task-1'),
        },
        captured,
        'workflow_step',
    )
    assert agentic['files'] == ['/uploads/real-user-file.txt']
    assert agentic['workflow_workspace_path'] == str(tmp_path / 'task-1')
    assert captured['remote_inputs']['brief'] not in agentic['files']


def test_workflow_material_bindings_keep_values_paths_and_references_distinct():
    from lazymind.chat.engine.subagent.runner import (
        _resolve_attachment_configs,
        _workflow_material_bindings_section,
    )

    params = {
        'remote_inputs': {
            'topic': '人工智能辅助软件测试',
            'outline_document': '/workspace/inputs/outline_document.md',
            'source_image': {'path': 'https://images.example.test/source.png'},
        },
        'remote_input_types': {
            'topic': 'text', 'outline_document': 'file', 'source_image': 'image',
        },
        'remote_input_transports': {
            'topic': 'value', 'outline_document': 'path', 'source_image': 'reference',
        },
        'remote_input_value_slots': ['source_image', 'topic'],
    }
    prompt = _workflow_material_bindings_section(params)

    assert '"value": "人工智能辅助软件测试"' in prompt
    assert '"path": "/workspace/inputs/outline_document.md"' in prompt
    assert '"kind": "reference"' in prompt
    assert '"reference": {"path": "https://images.example.test/source.png"}' in prompt
    assert _resolve_attachment_configs({}, 'workflow_step', params) == []
    assert _resolve_attachment_configs(
        {'history_files_per_turn': {'1': ['/uploads/real-user-file.txt']}},
        'ordinary_subagent',
        {},
    )


@pytest.mark.asyncio
async def test_execution_spec_failure_marks_claimed_attempt_failed():
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        failure = ''
        terminal = None

        async def context(self, *_):
            return {'metadata': {'task_id': 'missing-task'}, 'inputs': {}}

        async def execution_spec(self, *_):
            raise httpx.HTTPStatusError(
                'not found', request=httpx.Request('GET', 'http://runtime/spec'),
                response=httpx.Response(404))

        async def fail(self, _client, _attempt, _lease, message):
            self.failure = message

        async def task_event(self, _client, task, _lease, event):
            self.terminal = (task, event)

    runtime = Runtime()
    worker.runtime = runtime
    await worker._run_claim(object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'})
    assert runtime.failure.startswith('executor setup failed:')
    assert runtime.terminal == ('missing-task', {
        'type': 'error', 'status': 'failed', 'message': runtime.failure,
    })


@pytest.mark.asyncio
async def test_completion_rejection_becomes_explicit_failure(monkeypatch, tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime(StopReceiptRuntime):
        failed = False
        terminal = None

        async def context(self, *_):
            return {'metadata': {'task_id': 'task-1'}, 'inputs': {}}

        async def execution_spec(self, *_):
            return {'task': {'input_slots': [], 'output_slots': []},
                    'workspace_path': str(tmp_path / 'task-1'), 'params': {}, 'steps': [],
                    'llm_config': {}}

        async def heartbeat(self, *_):
            return None

        async def complete(self, *_):
            request = httpx.Request('POST', 'http://runtime/complete')
            response = httpx.Response(422, request=request)
            raise httpx.HTTPStatusError('missing output', request=request, response=response)

        async def fail(self, *_):
            self.failed = True

        async def task_event(self, _client, _task, _lease, event):
            self.terminal = event

    async def stream(**_kwargs):
        yield 'data: {"type":"done","status":"succeeded","summary":"done"}\n\n'
        yield 'data: [DONE]\n\n'

    from lazymind.chat.engine.subagent import runner
    runtime = Runtime()
    worker.runtime = runtime
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    await worker._run_claim(object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'})
    assert runtime.failed is True
    assert runtime.terminal['type'] == 'error'


@pytest.mark.asyncio
async def test_reclaimed_attempt_resumes_durable_steps_in_workspace(monkeypatch, tmp_path):
    worker = RemoteWorkflowExecutor()
    captured = {}
    workspace = tmp_path / 'task-1'

    class Runtime(StopReceiptRuntime):
        completed = False

        async def context(self, *_):
            return {'metadata': {'task_id': 'task-1'}, 'inputs': {}}

        async def execution_spec(self, *_):
            return {'task': {'input_slots': [], 'output_slots': []},
                    'workspace_path': str(workspace), 'params': {},
                    'steps': [{'seq': 0, 'role': 'text', 'content': {'content': 'checkpoint'}}],
                    'llm_config': {}, 'tool_config': {'tavily': 'test-token'},
                    'user_env_vars': {'Mixed_API_KEY': 'synthetic-secret'}}

        async def heartbeat(self, *_):
            return None

        async def complete(self, *_):
            self.completed = True

        async def task_event(self, *_):
            return None

    async def stream(**kwargs):
        captured.update(kwargs)
        yield 'data: {"type":"done","status":"succeeded","summary":"done"}\n\n'

    from lazymind.chat.engine.subagent import runner
    runtime = Runtime()
    worker.runtime = runtime
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    await worker._run_claim(object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'})
    assert runtime.completed is True
    assert captured['resume'] is True
    assert captured['tool_config'] == {'tavily': 'test-token'}
    assert captured['user_env_vars'] == {'Mixed_API_KEY': 'synthetic-secret'}
    assert 'synthetic-secret' not in json.dumps(captured['task_spec'])
    assert captured['task_spec']['workspace_path'] == str(workspace)
    assert captured['initial_steps'][0]['content']['content'] == 'checkpoint'
    assert workspace.exists()


@pytest.mark.asyncio
async def test_lease_loss_cancels_subagent_and_suppresses_stale_terminal(monkeypatch, tmp_path):
    from lazymind.chat.engine.subagent import runner

    worker = RemoteWorkflowExecutor()
    worker.heartbeat_seconds = 0.01
    cancelled = threading.Event()

    class Runtime(StopReceiptRuntime):
        terminal_calls = 0

        async def context(self, *_):
            return {'metadata': {'task_id': 'task-1'}, 'inputs': {}}

        async def execution_spec(self, *_):
            return {'task': {'input_slots': [], 'output_slots': []},
                    'workspace_path': str(tmp_path / 'task-1'), 'params': {}, 'steps': []}

        def heartbeat_sync(self, *_):
            raise httpx.HTTPStatusError(
                'lease lost', request=httpx.Request('POST', 'http://runtime/heartbeat'),
                response=httpx.Response(409))

        async def fail(self, *_):
            self.terminal_calls += 1

        async def complete(self, *_):
            self.terminal_calls += 1

        async def task_event(self, *_):
            self.terminal_calls += 1

    async def stream(**_kwargs):
        cancelled.wait(timeout=1)
        yield 'data: {"type":"error","status":"failed","message":"cancelled"}\n\n'

    runtime = Runtime()
    worker.runtime = runtime
    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    monkeypatch.setattr(worker, '_cancel_subagent', lambda _task: cancelled.set())
    await worker._run_claim(object(), {'attempt_id': 'attempt-1', 'lease_token': 'lease-1'})
    assert cancelled.is_set()
    assert runtime.terminal_calls == 0


@pytest.mark.asyncio
async def test_worker_claim_loop_runs_up_to_configured_concurrency(monkeypatch):
    import asyncio

    worker = RemoteWorkflowExecutor()
    worker.concurrency = 2
    started = asyncio.Event()
    release = asyncio.Event()
    active = 0
    maximum = 0

    class Runtime(StopReceiptRuntime):
        claims = 0

        async def claim(self, _client):
            self.claims += 1
            request = httpx.Request('POST', 'http://runtime/claim')
            if self.claims <= 2:
                return httpx.Response(200, request=request, json={
                    'data': {'attempt_id': f'a{self.claims}', 'lease_token': f'l{self.claims}'}})
            return httpx.Response(404, request=request, json={'error': {}})

        @staticmethod
        def data(response):
            return response.json()['data']

    async def run_claim(_client, _claim):
        nonlocal active, maximum
        active += 1
        maximum = max(maximum, active)
        if maximum == 2:
            started.set()
        await release.wait()
        active -= 1

    worker.runtime = Runtime()
    monkeypatch.setattr(worker, '_run_claim', run_claim)
    loop = asyncio.create_task(worker.run_forever())
    await asyncio.wait_for(started.wait(), timeout=1)
    assert maximum == 2
    release.set()
    loop.cancel()
    with pytest.raises(asyncio.CancelledError):
        await loop


@pytest.mark.asyncio
async def test_argument_failure_code_reaches_the_existing_attempt_terminal_protocol(monkeypatch, tmp_path):
    from unittest.mock import AsyncMock
    from lazymind.chat.engine.subagent import runner
    from lazymind.chat.workflow.client import RemoteExecutorClient

    request_bodies = []

    async def transport(request):
        request_bodies.append(json.loads(request.content))
        return httpx.Response(200, json={'ok': True, 'data': {}}, request=request)

    client_transport = RemoteExecutorClient('http://core.test', 'executor', 'local')
    runtime = AsyncMock()
    runtime.context.return_value = {'metadata': {'task_id': 'task'}, 'inputs': {}}
    runtime.execution_spec.return_value = {
        'task': {'input_slots': [], 'output_slots': []},
        'workspace_path': str(tmp_path), 'params': {}, 'steps': [], 'llm_config': {},
    }
    runtime.fail.side_effect = client_transport.fail
    worker = RemoteWorkflowExecutor()
    worker.runtime = runtime

    async def stream(**_kwargs):
        yield 'data: ' + json.dumps({
            'type': 'error', 'status': 'failed', 'message': '参数修复次数已耗尽',
            'error_code': 'TOOL_ARGUMENT_RETRY_EXHAUSTED',
        }) + '\n\n'

    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
        await worker._run_claim(client, {'attempt_id': 'attempt', 'lease_token': 'lease'})
    assert len(request_bodies) == 1
    assert request_bodies[0]['error_code'] == 'TOOL_ARGUMENT_RETRY_EXHAUSTED'
    assert request_bodies[0]['result'] == {'error': '参数修复次数已耗尽'}
    runtime.complete.assert_not_awaited()
