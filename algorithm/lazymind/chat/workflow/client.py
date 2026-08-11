"""LazyMind import compatibility for the public Host-neutral Workflow SDK."""

from typing import Any, Dict

import httpx

from lazymind.workflow_sdk import (
    AdvanceRequest,
    StepCommand,
    WorkflowClient,
    WorkflowClientError,
    WorkflowResponse,
)


class RemoteExecutorClient:
    """Async fenced transport used only by the out-of-process Host worker."""

    def __init__(self, base_url: str, executor_id: str, host: str, token: str = ''):
        self.base_url, self.executor_id, self.host, self.token = (
            base_url.rstrip('/'), executor_id, host, token)

    def headers(self, lease: str = '') -> Dict[str, str]:
        headers = {'Workflow-Contract-Version': 'workflow.v1',
                   'X-Workflow-Executor-Id': self.executor_id,
                   'X-Workflow-Host': self.host}
        if self.token:
            headers['Authorization'] = f'Bearer {self.token}'
        if lease:
            headers['X-Workflow-Lease-Token'] = lease
        return headers

    @staticmethod
    def data(response: httpx.Response) -> Dict[str, Any]:
        response.raise_for_status()
        body = response.json()
        value = body.get('data', body)
        return value if isinstance(value, dict) else {}

    async def claim(self, client: httpx.AsyncClient) -> httpx.Response:
        return await client.post(self.base_url + '/internal/workflow-attempts:claim', headers=self.headers())

    async def context(self, client: httpx.AsyncClient, attempt: str, lease: str) -> Dict[str, Any]:
        return self.data(await client.get(
            f'{self.base_url}/internal/workflow-attempts/{attempt}/context', headers=self.headers(lease)))

    async def execution_spec(self, client: httpx.AsyncClient, task: str, lease: str) -> Dict[str, Any]:
        return self.data(await client.get(
            f'{self.base_url}/internal/subagent/tasks/{task}/execution-spec', headers=self.headers(lease)))

    async def input(self, client: httpx.AsyncClient, attempt: str, lease: str,
                    material: str) -> Dict[str, Any]:
        return self.data(await client.get(
            f'{self.base_url}/internal/workflow-attempts/{attempt}/inputs/{material}',
            headers=self.headers(lease)))

    async def heartbeat(self, client: httpx.AsyncClient, attempt: str, lease: str) -> httpx.Response:
        response = await client.post(f'{self.base_url}/internal/workflow-attempts/{attempt}:heartbeat',
                                     headers=self.headers(lease), json={'lease_token': lease})
        response.raise_for_status()
        return response

    async def task_event(self, client: httpx.AsyncClient, task: str, lease: str,
                         event: Dict[str, Any]) -> httpx.Response:
        response = await client.post(f'{self.base_url}/internal/subagent/tasks/{task}/events',
                                     headers=self.headers(lease), json=event)
        response.raise_for_status()
        return response

    async def progress(self, client: httpx.AsyncClient, attempt: str, lease: str,
                       progress: Dict[str, Any]) -> httpx.Response:
        response = await client.post(f'{self.base_url}/internal/workflow-attempts/{attempt}:progress',
                                     headers=self.headers(lease),
                                     json={'lease_token': lease, 'progress': progress})
        response.raise_for_status()
        return response

    async def artifact(self, client: httpx.AsyncClient, attempt: str, lease: str,
                       artifact: Dict[str, Any]) -> httpx.Response:
        response = await client.post(f'{self.base_url}/internal/workflow-attempts/{attempt}/artifacts',
                                     headers=self.headers(lease), json=artifact)
        response.raise_for_status()
        return response

    async def fail(self, client: httpx.AsyncClient, attempt: str, lease: str,
                   message: str) -> httpx.Response:
        payload = {
            'lease_token': lease,
            'error_code': 'LAZYMIND_EXECUTION_FAILED',
            'result': {'error': message},
        }
        response = await client.post(f'{self.base_url}/internal/workflow-attempts/{attempt}:fail',
                                     headers=self.headers(lease), json=payload)
        response.raise_for_status()
        return response

    async def complete(self, client: httpx.AsyncClient, attempt: str, lease: str,
                       result: Dict[str, Any]) -> httpx.Response:
        response = await client.post(f'{self.base_url}/internal/workflow-attempts/{attempt}:complete',
                                     headers=self.headers(lease),
                                     json={'lease_token': lease, 'result': result})
        response.raise_for_status()
        return response


__all__ = [
    'AdvanceRequest',
    'StepCommand',
    'WorkflowClient',
    'WorkflowClientError',
    'WorkflowResponse',
    'RemoteExecutorClient',
]
