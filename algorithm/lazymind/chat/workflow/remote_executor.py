"""Out-of-process LazyMind Workflow Executor.

The worker talks to Workflow Runtime exclusively through the fenced HTTP
protocol. It reuses the ordinary LazyMind SubAgent runtime and mirrors its
events through Core's generic SubAgent event API, preserving Chat/Task streams.
"""
from __future__ import annotations

import asyncio
import base64
import json
import logging
import mimetypes
import os
import pathlib
import shutil
import tempfile
import threading
from typing import Any, Dict, Optional

import httpx

from lazymind.config import config
from lazymind.chat.workflow.client import RemoteExecutorClient

LOG = logging.getLogger(__name__)


def _base_url() -> str:
    return str(config['core_service_url'] or config['core_api_url']).rstrip('/')


class RemoteWorkflowExecutor:
    def __init__(self) -> None:
        self.base_url = _base_url()
        self.executor_id = os.getenv('LAZYMIND_WORKFLOW_EXECUTOR_ID', 'lazymind-remote-1')
        self.token = os.getenv('LAZYMIND_WORKFLOW_EXECUTOR_TOKEN', '').strip()
        self.poll_seconds = float(os.getenv('LAZYMIND_WORKFLOW_EXECUTOR_POLL_SECONDS', '0.5'))
        self.concurrency = max(1, int(os.getenv('LAZYMIND_WORKFLOW_EXECUTOR_CONCURRENCY', '4')))
        self.runtime = RemoteExecutorClient(self.base_url, self.executor_id, 'lazymind', self.token)

    async def run_forever(self) -> None:
        semaphore = asyncio.Semaphore(self.concurrency)
        running: set[asyncio.Task[Any]] = set()
        async with httpx.AsyncClient(timeout=30.0) as client:
            while True:
                acquired = False
                try:
                    await semaphore.acquire()
                    acquired = True
                    response = await self.runtime.claim(client)
                    if response.status_code == 404:
                        semaphore.release()
                        acquired = False
                        await asyncio.sleep(self.poll_seconds)
                        continue
                    claim = self.runtime.data(response)

                    async def execute(value: Dict[str, Any]) -> None:
                        try:
                            await self._run_claim(client, value)
                        except Exception:
                            LOG.exception('remote Workflow Attempt execution failed')
                        finally:
                            semaphore.release()
                    task = asyncio.create_task(execute(claim))
                    acquired = False
                    running.add(task)
                    task.add_done_callback(running.discard)
                except asyncio.CancelledError:
                    raise
                except Exception:
                    if acquired:
                        semaphore.release()
                    LOG.exception('remote Workflow Executor iteration failed')
                    await asyncio.sleep(self.poll_seconds)

    async def _run_claim(self, client: httpx.AsyncClient, claim: Dict[str, Any]) -> None:
        attempt_id = str(claim['attempt_id'])
        lease = str(claim['lease_token'])
        workspace = ''
        try:
            context = await self.runtime.context(client, attempt_id, lease)
            metadata = context.get('metadata') or {}
            task_id = str(metadata.get('task_id') or attempt_id)
            spec = await self.runtime.execution_spec(client, task_id, lease)
            workspace = tempfile.mkdtemp(prefix=f'lazymind-workflow-{attempt_id}-')
            inputs = await self._materialize_inputs(client, attempt_id, lease, context, workspace)
        except Exception as exc:
            # A claimed Attempt must never remain stuck merely because Host setup
            # failed before the SubAgent stream started.
            await self.runtime.fail(client, attempt_id, lease, f'executor setup failed: {exc}')
            return

        stopped = asyncio.Event()
        lease_lost = asyncio.Event()

        async def heartbeat() -> None:
            while not stopped.is_set():
                await asyncio.sleep(10)
                try:
                    await self.runtime.heartbeat(client, attempt_id, lease)
                except Exception:
                    lease_lost.set()
                    self._cancel_subagent(task_id)
                    return

        heartbeat_task = asyncio.create_task(heartbeat())
        artifacts: list[Dict[str, Any]] = []
        summary = ''
        failure: Optional[str] = None
        terminal_event: Optional[Dict[str, Any]] = None
        try:
            from lazymind.chat.engine.subagent.runner import run_subagent_stream
            task = dict(spec.get('task') or {})
            params = dict(spec.get('params') or {})
            attachment_context = dict(params.get('_attachment_context') or {})
            attachment_context['files'] = list(inputs.values())
            params['_attachment_context'] = attachment_context
            params['remote_inputs'] = inputs
            task.update({
                'id': task_id,
                'params': params,
                'workspace_path': workspace,
                'input_slots': task.get('input_slots') or [],
                'output_slots': task.get('output_slots') or [],
            })
            initial_steps = list(spec.get('steps') or [])
            async for frame in run_subagent_stream(
                task_id=task_id,
                resume=bool(initial_steps),
                model_config=spec.get('llm_config'),
                tool_config=spec.get('tool_config'),
                agent_type='workflow_step',
                task_spec=task,
                initial_steps=initial_steps,
            ):
                event = self._parse_frame(frame)
                if event is None:
                    continue
                kind = event.get('type')
                if kind not in {'done', 'error'}:
                    await self.runtime.task_event(client, task_id, lease, event)
                if kind in {'task_start', 'progress'}:
                    await self.runtime.progress(client, attempt_id, lease, {
                        'progress': event.get('progress', 0),
                        'phase': event.get('current_phase', kind)})
                elif kind == 'artifact':
                    artifact = {'slot': event.get('slot'), 'content_type': event.get('content_type'),
                                'seq': event.get('seq', 1), 'value': event.get('value')}
                    artifact['value'] = self._embed_files(
                        artifact['value'], str(artifact.get('content_type') or ''), workspace)
                    await self.runtime.artifact(client, attempt_id, lease, artifact)
                    artifacts.append(artifact)
                elif kind == 'done':
                    terminal_event = event
                    summary = str(event.get('summary') or '')
                    if event.get('status') not in {None, '', 'succeeded'}:
                        failure = summary or str(event.get('status'))
                elif kind == 'error':
                    terminal_event = event
                    failure = str(event.get('message') or 'LazyMind SubAgent failed')
        except Exception as exc:
            failure = str(exc)
        finally:
            stopped.set()
            heartbeat_task.cancel()
            await asyncio.gather(heartbeat_task, return_exceptions=True)

        try:
            if lease_lost.is_set():
                return
            try:
                if failure:
                    await self.runtime.fail(client, attempt_id, lease, failure)
                else:
                    await self.runtime.complete(client, attempt_id, lease, {
                        'summary': summary, 'executor_ref': task_id, 'artifacts': artifacts})
            except httpx.HTTPStatusError as exc:
                # A Runtime completion validation error is a terminal execution
                # failure, not a reason to leave the Attempt running until expiry.
                if exc.response.status_code in {401, 409}:
                    return
                failure = str(exc)
                await self.runtime.fail(client, attempt_id, lease, failure)
                terminal_event = {'type': 'error', 'status': 'failed', 'message': failure}
            if terminal_event is not None:
                # Runtime terminal state wins first; the ordinary LazyMind event is
                # then persisted and invokes existing Chat handoff/synthetic hooks.
                await self.runtime.task_event(client, task_id, lease, terminal_event)
        finally:
            shutil.rmtree(workspace, ignore_errors=True)

    async def _materialize_inputs(self, client: httpx.AsyncClient, attempt: str, lease: str,
                                  context: Dict[str, Any], workspace: str) -> Dict[str, str]:
        result: Dict[str, str] = {}
        root = pathlib.Path(workspace) / 'inputs'
        root.mkdir(parents=True, exist_ok=True)
        for material in (context.get('inputs') or {}):
            value = await self.runtime.input(client, attempt, lease, str(material))
            name = pathlib.Path(str(value.get('name') or material)).name or str(material)
            target = root / name
            target.write_bytes(base64.b64decode(str(value.get('content_base64') or '')))
            result[str(material)] = str(target)
        return result

    @staticmethod
    def _embed_files(value: Any, content_type: str, workspace: str) -> Any:
        if not isinstance(value, dict) or content_type not in {'file', 'file_list', 'image'}:
            return value
        result = dict(value)
        keys = ['path'] if content_type in {'file', 'image'} else ['paths']
        for key in keys:
            paths = result.get(key)
            scalar = isinstance(paths, str)
            values = [paths] if scalar else paths if isinstance(paths, list) else []
            embedded = []
            for raw in values:
                raw_text = str(raw)
                if raw_text.startswith((
                    'http://', 'https://', 'data:', '/static-files/', '/api/core/static-files/',
                )):
                    embedded.append(raw_text)
                    continue
                path = pathlib.Path(raw_text)
                if not path.is_absolute():
                    path = pathlib.Path(workspace) / path
                try:
                    resolved = path.resolve(strict=True)
                    resolved.relative_to(pathlib.Path(workspace).resolve())
                    mime = mimetypes.guess_type(resolved.name)[0] or 'application/octet-stream'
                    data = base64.b64encode(resolved.read_bytes()).decode('ascii')
                    embedded.append(f'data:{mime};base64,{data}')
                except (OSError, ValueError):
                    embedded.append('')
            result[key] = embedded[0] if scalar and embedded else embedded
        return result

    @staticmethod
    def _parse_frame(frame: str) -> Optional[Dict[str, Any]]:
        line = frame.strip()
        if not line or line == 'data: [DONE]':
            return None
        if line.startswith('data:'):
            line = line[5:].strip()
        try:
            value = json.loads(line)
        except ValueError:
            return None
        return value if isinstance(value, dict) else None

    @staticmethod
    def _cancel_subagent(task_id: str) -> None:
        """Use the ordinary LazyMind cancellation channel; no Workflow-only loop."""
        try:
            import lazyllm
            from lazyllm.common.queue import FileSystemQueue
            lazyllm.globals._init_sid(sid=task_id)
            FileSystemQueue(klass='cancel').enqueue(json.dumps({'tag': 'cancel'}))
        except Exception:
            LOG.exception('failed to cancel LazyMind SubAgent task %s', task_id)


_worker_thread: Optional[threading.Thread] = None


def start_remote_workflow_executor() -> None:
    global _worker_thread
    if _worker_thread is not None:
        return
    worker = RemoteWorkflowExecutor()
    _worker_thread = threading.Thread(
        target=lambda: asyncio.run(worker.run_forever()),
        name='lazymind-workflow-executor', daemon=True)
    _worker_thread.start()
