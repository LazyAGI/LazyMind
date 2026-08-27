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
import os
import pathlib
import re
import threading
from typing import Any, Dict, Optional
from urllib.parse import urlparse

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
        self.heartbeat_seconds = float(os.getenv('LAZYMIND_WORKFLOW_EXECUTOR_HEARTBEAT_SECONDS', '10'))
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
        task_id = ''
        try:
            context = await self.runtime.context(client, attempt_id, lease)
            metadata = context.get('metadata') or {}
            task_id = str(metadata.get('task_id') or attempt_id)
            spec = await self.runtime.execution_spec(client, task_id, lease)
            task = dict(spec.get('task') or {})
            workspace = str(spec['workspace_path'])
            pathlib.Path(workspace).mkdir(parents=True, exist_ok=True)
            inputs = await self._resolve_inputs(client, attempt_id, lease, context, workspace)
        except Exception as exc:
            # A claimed Attempt must never remain stuck merely because Host setup
            # failed before the SubAgent stream started.
            message = f'executor setup failed: {exc}'
            await self.runtime.fail(client, attempt_id, lease, message)
            # Keep the ordinary SubAgent projection in sync with the authoritative
            # Attempt. Without this terminal event the task remains pending forever
            # even though Workflow Runtime has already marked the Attempt failed.
            if task_id:
                try:
                    await self.runtime.task_event(client, task_id, lease, {
                        'type': 'error', 'status': 'failed', 'message': message,
                    })
                except Exception:
                    LOG.exception('failed to mirror Workflow setup failure to SubAgent task %s', task_id)
            return

        stopped = threading.Event()
        lease_lost = threading.Event()

        def heartbeat() -> None:
            with httpx.Client(timeout=5.0) as heartbeat_client:
                while not stopped.wait(self.heartbeat_seconds):
                    try:
                        self.runtime.heartbeat_sync(heartbeat_client, attempt_id, lease)
                    except Exception:
                        lease_lost.set()
                        self._cancel_subagent(task_id)
                        return

        heartbeat_thread = threading.Thread(
            target=heartbeat, name=f'workflow-heartbeat-{attempt_id}', daemon=True)
        heartbeat_thread.start()
        artifacts: list[Dict[str, Any]] = []
        summary = ''
        control: Dict[str, Any] = {}
        failure: Optional[str] = None
        terminal_event: Optional[Dict[str, Any]] = None
        try:
            from lazymind.chat.engine.subagent.runner import run_subagent_stream
            params = dict(spec.get('params') or {})
            output_types = dict(context.get('declared_output_types') or {})
            if output_types:
                params['output_slot_types'] = output_types
            input_types = dict(context.get('declared_input_types') or {})
            direct_value_slots = sorted(self._direct_input_value_slots(context))
            # Workflow bindings are typed execution inputs, not user uploads. Scalar
            # values and public image references stay typed values; only binary inputs
            # use fenced local paths. Keep all forms out of attachment-only tools.
            params['remote_inputs'] = inputs
            params['remote_input_types'] = input_types
            params['remote_input_value_slots'] = direct_value_slots
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
                if kind == 'artifact':
                    artifact = {'slot': event.get('slot'), 'content_type': event.get('content_type'),
                                'seq': event.get('seq', 1), 'value': event.get('value')}
                    artifact['value'] = await self._persist_files(
                        client, attempt_id, lease, artifact['value'],
                        str(artifact.get('content_type') or ''), workspace)
                    # The ordinary Task Center projection must receive the same
                    # host-neutral value as the Workflow artifact sink.  A raw
                    # path here points into this executor's temporary workspace
                    # and is inaccessible to Core after the attempt finishes.
                    await self.runtime.task_event(client, task_id, lease, {
                        **event, 'value': artifact['value'],
                    })
                    await self.runtime.artifact(client, attempt_id, lease, artifact)
                    artifacts.append(artifact)
                elif kind not in {'done', 'error'}:
                    await self.runtime.task_event(client, task_id, lease, event)

                if kind in {'task_start', 'progress'}:
                    await self.runtime.progress(client, attempt_id, lease, {
                        'progress': event.get('progress', 0),
                        'phase': event.get('current_phase', kind)})
                elif kind == 'done':
                    terminal_event = event
                    summary = str(event.get('summary') or '')
                    event_control = event.get('control')
                    if isinstance(event_control, dict):
                        control = dict(event_control)
                    if event.get('status') not in {None, '', 'succeeded'}:
                        failure = summary or str(event.get('status'))
                elif kind == 'error':
                    terminal_event = event
                    failure = str(event.get('message') or 'LazyMind SubAgent failed')
        except Exception as exc:
            failure = str(exc)
        finally:
            stopped.set()
            heartbeat_thread.join(timeout=1)

        if lease_lost.is_set():
            return
        try:
            if failure:
                await self.runtime.fail(client, attempt_id, lease, failure)
            else:
                await self.runtime.complete(client, attempt_id, lease, {
                    'summary': summary, 'executor_ref': task_id, 'artifacts': artifacts,
                    **({'control': control} if control else {}),
                })
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

    @staticmethod
    def _input_resource_binding(value: Any) -> bool:
        if isinstance(value, dict):
            return value.get('source_type') == 'input_resource'
        if isinstance(value, list) and value:
            return all(
                isinstance(item, dict) and item.get('source_type') == 'input_resource'
                for item in value
            )
        return False

    @staticmethod
    def _artifact_binding(value: Any) -> bool:
        if isinstance(value, dict):
            return value.get('source_type') == 'artifact'
        if isinstance(value, list) and value:
            return all(
                isinstance(item, dict) and item.get('source_type') == 'artifact'
                for item in value
            )
        return False

    @classmethod
    def _infer_artifact_input_type(cls, binding: Any, items: list[Dict[str, Any]]) -> str:
        """Recover typed artifact envelopes from older Core context responses."""
        if not cls._artifact_binding(binding) or not items:
            return ''
        values = []
        for item in items:
            if str(item.get('mime_type') or '').lower() != 'application/json':
                return ''
            try:
                value = json.loads(base64.b64decode(str(item.get('content_base64') or '')))
            except (ValueError, TypeError, json.JSONDecodeError):
                return ''
            if not isinstance(value, dict):
                return ''
            values.append(value)
        if all(isinstance(value.get('text'), str) for value in values):
            return 'text'
        if all('data' in value for value in values):
            return 'json'
        image_suffixes = {'.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp', '.tif', '.tiff'}
        paths = [
            value.get('path') or value.get('image_url') or value.get('url')
            for value in values
        ]
        if all(
            isinstance(path, str)
            and pathlib.Path(urlparse(path).path).suffix.lower() in image_suffixes
            for path in paths
        ):
            return 'image'
        return ''

    @classmethod
    def _direct_input_value_slots(cls, context: Dict[str, Any]) -> set[str]:
        input_types = context.get('declared_input_types') or {}
        bindings = context.get('inputs') or {}
        if not isinstance(input_types, dict) or not isinstance(bindings, dict):
            return set()
        return {
            str(material) for material in bindings
            if str(input_types.get(str(material)) or '').strip().lower()
            in {'text', 'json', 'image'}
        }

    @staticmethod
    def _decode_input_value(item: Dict[str, Any], material_type: str) -> Any:
        content = base64.b64decode(str(item.get('content_base64') or ''))
        try:
            text = content.decode('utf-8')
        except UnicodeDecodeError as exc:
            if material_type == 'image':
                return None
            raise ValueError('scalar Workflow input must be valid UTF-8') from exc
        parsed: Any = None
        parsed_ok = False
        try:
            parsed = json.loads(text)
            parsed_ok = True
        except json.JSONDecodeError:
            pass
        if material_type == 'json':
            if not parsed_ok:
                raise ValueError('json Workflow input must contain valid JSON')
            # Durable JSON artifacts use {"data": ...} as their storage envelope.
            if isinstance(parsed, dict) and 'data' in parsed:
                return parsed['data']
            return parsed
        if material_type == 'text':
            # Durable text artifacts use {"text": ...}; external text resources
            # remain ordinary UTF-8 strings even when their contents look like JSON.
            mime_type = str(item.get('mime_type') or '').lower()
            if mime_type == 'application/json' and isinstance(parsed, dict):
                if isinstance(parsed.get('text'), str):
                    return parsed['text']
                if 'data' in parsed and isinstance(parsed['data'], str):
                    return parsed['data']
            return text
        if material_type == 'image':
            # Public image artifacts are sent by Core as a small JSON reference;
            # uploaded images are binary and must still be materialized locally.
            if isinstance(parsed, dict):
                path = parsed.get('path') or parsed.get('image_url') or parsed.get('url')
                if isinstance(path, str) and path.strip():
                    value = dict(parsed)
                    value['path'] = path.strip()
                    return value
            return None
        return text

    async def _resolve_inputs(self, client: httpx.AsyncClient, attempt: str, lease: str,
                              context: Dict[str, Any], workspace: str) -> Dict[str, Any]:
        result: Dict[str, Any] = {}
        root = pathlib.Path(workspace) / 'inputs'
        direct_slots = self._direct_input_value_slots(context)
        input_types = context.get('declared_input_types') or {}
        if not isinstance(input_types, dict):
            input_types = {}
        bindings = context.get('inputs') or {}
        for material, binding in bindings.items():
            value = await self.runtime.input(client, attempt, lease, str(material))
            is_list = isinstance(value.get('items'), list)
            items = value.get('items') if is_list else [value]
            material_type = str(input_types.get(str(material)) or '').strip().lower()
            inferred_type = ''
            if not material_type:
                inferred_type = self._infer_artifact_input_type(binding, items)
                if inferred_type:
                    material_type = inferred_type
                    input_types[str(material)] = inferred_type
                    context['declared_input_types'] = input_types
            if (str(material) in direct_slots or inferred_type) and material_type in {'text', 'json'}:
                values = [self._decode_input_value(item, material_type) for item in items]
                result[str(material)] = values if is_list else values[0]
                continue
            if material_type == 'image':
                references = [self._decode_input_value(item, material_type) for item in items]
                if all(reference is not None for reference in references):
                    result[str(material)] = references if is_list else references[0]
                    continue
            paths = []
            list_root = root
            if is_list:
                safe_material = re.sub(r'[^0-9A-Za-z_.-]+', '_', str(material)).strip('._') or 'input'
                list_root = root / safe_material
                list_root.mkdir(parents=True, exist_ok=True)
            else:
                list_root.mkdir(parents=True, exist_ok=True)
            for index, item in enumerate(items, start=1):
                if material_type == 'image':
                    image_reference = self._decode_input_value(item, material_type)
                    if image_reference is not None:
                        paths.append(image_reference)
                        continue
                name = pathlib.Path(str(item.get('name') or material)).name or str(material)
                if is_list:
                    name = f'{index:04d}_{name}'
                target = list_root / name
                target.write_bytes(base64.b64decode(str(item.get('content_base64') or '')))
                paths.append(str(target))
            result[str(material)] = paths if is_list else paths[0]
        return result

    async def _persist_files(self, client: httpx.AsyncClient, attempt: str, lease: str,
                             value: Any, content_type: str, workspace: str) -> Any:
        if not isinstance(value, dict) or content_type not in {'file', 'file_list', 'image'}:
            return value
        result = dict(value)
        keys = ['path'] if content_type in {'file', 'image'} else ['paths']
        for key in keys:
            paths = result.get(key)
            scalar = isinstance(paths, str)
            values = [paths] if scalar else paths if isinstance(paths, list) else []
            persisted = []
            for raw in values:
                raw_text = str(raw)
                if raw_text.startswith((
                    'http://', 'https://', '/static-files/', '/api/core/static-files/',
                )):
                    persisted.append(raw_text)
                    continue
                path = pathlib.Path(raw_text)
                if not path.is_absolute():
                    path = pathlib.Path(workspace) / path
                try:
                    resolved = path.resolve(strict=True)
                    resolved.relative_to(pathlib.Path(workspace).resolve())
                    stable_path = await self.runtime.upload_artifact_file(
                        client, attempt, lease, resolved.name, resolved.read_bytes())
                    persisted.append(stable_path)
                except (OSError, ValueError):
                    persisted.append('')
            result[key] = persisted[0] if scalar and persisted else persisted
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
