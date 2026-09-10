import json
import os
import threading
import httpx
import pytest
from lazymind.chat.workflow.remote_executor import RemoteWorkflowExecutor


@pytest.mark.asyncio
async def test_remote_executor_persists_artifact_before_task_center_event(monkeypatch, tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime:
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
async def test_remote_executor_materializes_fenced_inputs_in_host_workspace(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime:
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
async def test_completion_rejection_becomes_explicit_failure(monkeypatch, tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime:
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

    class Runtime:
        completed = False

        async def context(self, *_):
            return {'metadata': {'task_id': 'task-1'}, 'inputs': {}}

        async def execution_spec(self, *_):
            return {'task': {'input_slots': [], 'output_slots': []},
                    'workspace_path': str(workspace), 'params': {},
                    'steps': [{'seq': 0, 'role': 'text', 'content': {'content': 'checkpoint'}}],
                    'llm_config': {}, 'tool_config': {'tavily': 'test-token'}}

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
    assert captured['task_spec']['workspace_path'] == str(workspace)
    assert captured['initial_steps'][0]['content']['content'] == 'checkpoint'
    assert workspace.exists()


@pytest.mark.asyncio
async def test_lease_loss_cancels_subagent_and_suppresses_stale_terminal(monkeypatch, tmp_path):
    from lazymind.chat.engine.subagent import runner

    worker = RemoteWorkflowExecutor()
    worker.heartbeat_seconds = 0.01
    cancelled = threading.Event()

    class Runtime:
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
