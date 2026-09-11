import json
from dataclasses import dataclass

import httpx
import lazyllm

from lazymind.chat.workflow.workflow_manager import resolve_workflow_injection
from lazymind.workflow_sdk import WorkflowClient


@dataclass
class ScriptedMockModel:
    """Minimal deterministic model double that emits the intended tool calls."""

    calls: list[tuple[str, dict]]

    def next_tool_call(self) -> tuple[str, dict]:
        return self.calls.pop(0)


def _tool(tools, name):
    return next(tool for tool in tools if tool.__name__ == name)


def test_mock_model_runs_prepare_start_projection_and_advance_end_to_end(monkeypatch):
    """Exercise Agent contribution -> toolkit -> SDK -> HTTP contract as one chain."""
    requests = []
    steps = ['prompt', 'script', 'typed_artifacts', 'rewrite', 'list_artifacts', 'verify']
    state = {'index': 0, 'version': 1}

    def runtime(request: httpx.Request) -> httpx.Response:
        requests.append((request.method, request.url.path, json.loads(request.content or b'{}')))
        path = request.url.path
        if path.endswith('/workflow-runtime/v1/workflows/test-workflow'):
            return httpx.Response(200, json={'ok': True, 'result': {
                'workflow_id': 'test-workflow', 'revision_id': 'revision-1',
            }})
        if path.endswith('/workflow-preparations'):
            # This is the exact flat public shape guaranteed by the Go facade.
            return httpx.Response(200, json={'ok': True, 'result': {
                'preparation_id': 'prep-1', 'status': 'ready',
                'workflow_ref': 'builtin:test-workflow',
                'workflow_revision': 'revision-1', 'missing_inputs': [], 'warnings': [],
            }})
        if path.endswith('/workflow-preparations/prep-1:consume'):
            return httpx.Response(200, json={'ok': True, 'result': {
                'session_id': 'session-1', 'status': 'active', 'state_version': 1,
            }})
        if path.endswith('/workflow-sessions/session-1/projection'):
            return httpx.Response(200, json={'ok': True, 'result': {
                'session_id': 'session-1', 'status': 'active',
                'state_version': state['version'],
                'ready_steps': [{'step_id': steps[state['index']]}],
            }})
        if path.endswith('/workflow-sessions/session-1:advance-step'):
            payload = json.loads(request.content)
            assert payload['expected_state_version'] == state['version']
            assert payload['steps'][0]['step_id'] == steps[state['index']]
            state['index'] += 1
            state['version'] += 1
            return httpx.Response(200, json={'ok': True, 'result': {
                'accepted': True, 'state_version': state['version'],
                'attempt_status': 'succeeded',
                'status': 'completed' if state['index'] == len(steps) else 'active',
            }})
        raise AssertionError(f'unexpected request: {request.method} {path}')

    client = WorkflowClient(
        'http://core/api/core', 'owner', host='lazymind',
        transport=httpx.Client(transport=httpx.MockTransport(runtime)),
    )
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'enable_workflow': True}
    monkeypatch.setattr('lazymind.chat.workflow.workflow_manager._client', lambda: client)
    try:
        contribution = resolve_workflow_injection(
            None,
            conversation_id='conversation-1',
            workflow_catalog=[{
                'workflow_ref': 'builtin:test-workflow',
                'workflow_id': 'test-workflow', 'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:test-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:test-workflow',
                'workflow_id': 'test-workflow', 'revision_id': 'revision-1',
                'tool_name': 'trigger_test_workflow',
            }],
        )
        model = ScriptedMockModel([
            ('trigger_test_workflow', {}),
            *[
                call
                for index, step in enumerate(steps, start=1)
                for call in (
                    ('get_ready_steps', {}),
                    ('advance_step', {'step_ids': [step]}),
                )
            ],
        ])

        name, arguments = model.next_tool_call()
        prepared = _tool(contribution.tools, name)(**arguments)
        assert prepared['session_id'] == 'session-1'
        assert prepared['ready_steps'] == ['prompt']

        contribution = resolve_workflow_injection({
            'session_id': 'session-1', 'workflow_id': 'test-workflow',
            'revision_id': 'revision-1', 'status': 'active',
        })

        for index, step in enumerate(steps, start=1):
            name, arguments = model.next_tool_call()
            ready = _tool(contribution.tools, name)(**arguments)
            assert ready['state_version'] == index
            assert ready['ready_steps'] == [step]

            name, arguments = model.next_tool_call()
            advanced = _tool(contribution.tools, name)(**arguments)
            assert advanced['attempt_status'] == 'succeeded'
            assert advanced['command_id']
            assert arguments['step_ids'] == [step]
        assert not model.calls
    finally:
        if previous is None:
            lazyllm.globals.pop('agentic_config', None)
        else:
            lazyllm.globals['agentic_config'] = previous

    assert [path for _, path, _ in requests[:4]] == [
        '/api/core/workflow-runtime/v1/workflows/test-workflow',
        '/api/core/workflow-preparations',
        '/api/core/workflow-preparations/prep-1:consume',
        '/api/core/workflow-sessions/session-1/projection',
    ]
    advance_requests = [request for request in requests if request[1].endswith(':advance-step')]
    assert [request[2]['steps'][0]['step_id'] for request in advance_requests] == steps
