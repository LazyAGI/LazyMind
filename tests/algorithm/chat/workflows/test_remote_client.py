import json

import httpx
import pytest

from lazymind.chat.workflow.client import RemoteExecutorClient


@pytest.mark.asyncio
async def test_remote_executor_client_sends_identity_lease_and_version_headers():
    requests = []

    async def handle(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.url.path.endswith(':claim'):
            return httpx.Response(200, json={'data': {'attempt_id': 'a1', 'lease_token': 'l1'}})
        return httpx.Response(200, json={'data': {'accepted': True}})

    runtime = RemoteExecutorClient('http://core', 'executor-1', 'lazymind', 'secret')
    async with httpx.AsyncClient(transport=httpx.MockTransport(handle)) as client:
        assert RemoteExecutorClient.data(await runtime.claim(client))['attempt_id'] == 'a1'
        await runtime.context(client, 'a1', 'l1')
        await runtime.execution_spec(client, 'task-1', 'l1')
        await runtime.input(client, 'a1', 'l1', 'brief')
        await runtime.heartbeat(client, 'a1', 'l1')
        await runtime.progress(client, 'a1', 'l1', {'progress': 20})
        await runtime.task_event(client, 'task-1', 'l1', {'type': 'text', 'text': 'hello'})
        await runtime.artifact(client, 'a1', 'l1', {'slot': 'report', 'value': {'text': 'ok'}})
        await runtime.complete(client, 'a1', 'l1', {'summary': 'done'})
        await runtime.fail(client, 'a1', 'l1', 'failed')

    assert [request.url.path for request in requests] == [
        '/internal/workflow-attempts:claim',
        '/internal/workflow-attempts/a1/context',
        '/internal/subagent/tasks/task-1/execution-spec',
        '/internal/workflow-attempts/a1/inputs/brief',
        '/internal/workflow-attempts/a1:heartbeat',
        '/internal/workflow-attempts/a1:progress',
        '/internal/subagent/tasks/task-1/events',
        '/internal/workflow-attempts/a1/artifacts',
        '/internal/workflow-attempts/a1:complete',
        '/internal/workflow-attempts/a1:fail',
    ]
    for request in requests:
        assert request.headers['workflow-contract-version'] == 'workflow.v1'
        assert request.headers['x-workflow-executor-id'] == 'executor-1'
        assert request.headers['x-workflow-host'] == 'lazymind'
        assert request.headers['authorization'] == 'Bearer secret'
    for request in requests[1:]:
        assert request.headers['x-workflow-lease-token'] == 'l1'
    assert json.loads(requests[5].content)['lease_token'] == 'l1'
    assert json.loads(requests[8].content)['result']['summary'] == 'done'


def test_remote_executor_client_rejects_http_errors():
    response = httpx.Response(409, request=httpx.Request('GET', 'http://core/context'))
    with pytest.raises(httpx.HTTPStatusError):
        RemoteExecutorClient.data(response)


@pytest.mark.asyncio
async def test_remote_executor_failure_persists_checkpoint_with_error():
    checkpoint = {
        'workflow_revision': 'revision-1',
        'summary': 'analyzed',
        'artifacts': [{'slot': 'workflow_routing', 'content_type': 'text',
                       'seq': 1, 'value': {'text': 'WORKFLOW: EDIT_UPLOAD'}}],
        'control': {'next_step': 'collect_materials'},
    }
    requests = []

    async def handle(request):
        requests.append(request)
        return httpx.Response(200, json={'data': {'accepted': True}})

    runtime = RemoteExecutorClient('http://core', 'executor-1', 'lazymind')
    async with httpx.AsyncClient(transport=httpx.MockTransport(handle)) as client:
        await runtime.fail(client, 'attempt-1', 'lease-1',
                           'MEDIA_CAPABILITY_DEPENDENCY_MISSING {}',
                           post_step_checkpoint=checkpoint)
    payload = json.loads(requests[0].content)
    assert payload['lease_token'] == 'lease-1'
    assert payload['result'] == {
        'error': 'MEDIA_CAPABILITY_DEPENDENCY_MISSING {}',
        'post_step_checkpoint': checkpoint,
    }
