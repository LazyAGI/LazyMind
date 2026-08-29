import base64
import json
import os
import threading
from pathlib import Path

import httpx
import pytest

from lazymind.chat.workflow.remote_executor import RemoteWorkflowExecutor


def test_remote_executor_preserves_ordinary_subagent_stream_event_shape():
    event = {'type': 'text', 'text': 'hello', 'think': ''}
    assert RemoteWorkflowExecutor._parse_frame(
        'data: ' + json.dumps(event) + '\n\n') == event
    assert RemoteWorkflowExecutor._parse_frame('data: [DONE]\n\n') is None


def test_remote_executor_ignores_non_json_stream_frames():
    assert RemoteWorkflowExecutor._parse_frame('event: heartbeat\n\n') is None


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
async def test_remote_executor_passes_external_scalar_inputs_as_values(tmp_path):
    worker = RemoteWorkflowExecutor()

    class Runtime:
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

    class Runtime:
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

    class Runtime:
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

    class Runtime:
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

    class Runtime:
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

    class Runtime:
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

    class Runtime:
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

    class Runtime:
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


def test_workflow_material_prompt_forbids_attachment_tools():
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

    assert 'not user-uploaded attachments' in prompt
    assert 'kind=value' in prompt
    assert '"value": "人工智能辅助软件测试"' in prompt
    assert '"path": "/workspace/inputs/outline_document.md"' in prompt
    assert '"kind": "reference"' in prompt
    assert '"reference": {"path": "https://images.example.test/source.png"}' in prompt
    assert 'read_user_attachment' in prompt
    assert _resolve_attachment_configs({}, 'workflow_step', params) == []
    assert _resolve_attachment_configs(
        {'history_files_per_turn': {'1': ['/uploads/real-user-file.txt']}},
        'ordinary_subagent',
        {},
    )


@pytest.mark.asyncio
async def test_execution_spec_failure_marks_claimed_attempt_failed():
    worker = RemoteWorkflowExecutor()

    class Runtime:
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


@pytest.mark.asyncio
async def test_worker_claim_loop_runs_up_to_configured_concurrency(monkeypatch):
    import asyncio

    worker = RemoteWorkflowExecutor()
    worker.concurrency = 2
    started = asyncio.Event()
    release = asyncio.Event()
    active = 0
    maximum = 0

    class Runtime:
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
