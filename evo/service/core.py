from __future__ import annotations

import asyncio
import logging
import os
import re
import shutil
import time
import uuid
from collections.abc import Awaitable, Callable, Mapping
from pathlib import Path
from typing import Any, TypeVar

from evo import artifacts as A
from evo.artifact_flow import ArtifactFlow, FlowDefinition, FlowSnapshot
from evo.artifact_runtime import ArtifactCommit, ArtifactDraft, ArtifactKey
from evo.message_intent import MessageIntent, MessageRequest, MessageTurnResult
from evo.operations import evo_flow_definition
from evo.operations.repair.trace import RepairTraceStore
from evo.repair_model import EvoModelConfigError, resolve_evo_model

from .contracts import (
    CommandRequest,
    ControlRequest,
    RetryRequest,
    ServiceError,
    ThreadCreate,
)
from .projections import ProjectionService
from .public import public_message_history, public_thread_state, public_value
from .router import RouterService


T = TypeVar('T')
_STAGES = tuple(A.STEPS)
_FIRST_FRAME_TIMEOUT = 60.0
_THREAD_ID_ATTEMPTS = 32
_AUTO_WAIT_TIMEOUT = 30.0
_AUTO_STOPPED = frozenset({'idle', 'cancelled', 'failed', 'completed'})
_TRACE_ID = re.compile(r'^[0-9a-f]{32}$')


logger = logging.getLogger(__name__)


class EvoService:
    def __init__(self, root: str | Path, definition: FlowDefinition,
                 flow: ArtifactFlow
                 ) -> None:
        self.root = Path(root)
        self.definition = definition
        self.flow = flow
        self.messages = MessageIntent(self.root, flow)
        self.projections = ProjectionService(
            flow,
            definition,
            RepairTraceStore(self.root),
        )
        self.router = RouterService(self.root, flow)
        self._control_locks: dict[str, asyncio.Lock] = {}
        self._message_locks: dict[str, asyncio.Lock] = {}
        self._auto_tasks: dict[str, asyncio.Task[None]] = {}
        self._closing = False

    @classmethod
    async def open(cls, root: str | Path, definition: FlowDefinition | None = None, *,
                   max_concurrency: int = 4, terminate_timeout: float = 1.0
                   ) -> EvoService:
        root = Path(root)
        definition = definition or evo_flow_definition()
        flow = await ArtifactFlow.open(
            root / 'artifact-runtime',
            definition,
            max_concurrency=max_concurrency,
            terminate_timeout=terminate_timeout,
        )
        service = cls(root, definition, flow)
        await asyncio.to_thread(service.router.reconcile_unpublished)
        await asyncio.to_thread(service.router.reconcile_published)
        await service._restore_auto_threads()
        return service

    async def create_thread(self, request: ThreadCreate | Mapping[str, Any]
                            ) -> dict[str, Any]:
        request = (
            request
            if isinstance(request, ThreadCreate)
            else ThreadCreate.model_validate(request)
        )
        try:
            resolve_evo_model(request.llm_config.get('evo_llm'))
        except EvoModelConfigError as exc:
            raise ServiceError(422, exc.detail()) from exc
        thread_id = await self._new_thread_id()
        seed = _seed_values(thread_id, request)
        keys = tuple(ArtifactKey.scalar(artifact_id) for artifact_id in A.SEEDS)
        commit = ArtifactCommit(
            f'create:{thread_id}',
            'user:create',
            tuple(
                ArtifactDraft(key, seed[key.artifact_id])
                for key in keys
            ),
            {key: None for key in keys},
        )
        snapshot = await self.flow.create(thread_id, commit)
        return _public_thread(request, snapshot)

    async def _new_thread_id(self) -> str:
        for _ in range(_THREAD_ID_ATTEMPTS):
            thread_id = f'thr-{uuid.uuid4().hex[:8]}'
            if not await self.flow.has_run(thread_id):
                return thread_id
        raise ServiceError(503, 'unable to allocate thread_id')

    async def list_threads(self, page_size: int, page_token: str,
                           status: str = ''
                           ) -> dict[str, Any]:
        offset = _page_offset(page_token)
        items = []
        for thread_id in sorted(await self.flow.run_ids()):
            item = await self.public_thread(thread_id, include_inputs=False)
            if not status or item['status'] == status:
                items.append(item)
        page = items[offset:offset + page_size]
        next_offset = offset + page_size
        return {
            'items': page,
            'next_page_token': str(next_offset) if next_offset < len(items) else '',
        }

    async def public_thread(self, thread_id: str, *, include_inputs: bool = True
                            ) -> dict[str, Any]:
        snapshot, config = await asyncio.gather(
            self.flow.snapshot(thread_id),
            self._run_config(thread_id),
        )
        item = {
            'thread_id': thread_id,
            'mode': str(config.get('mode') or ''),
            'title': str(config.get('title') or ''),
            **public_thread_state(snapshot),
        }
        if include_inputs:
            item['inputs'] = public_value(config.get('inputs') or {})
            item['retryable'] = snapshot.status == 'failed'
        return item

    async def start(self, thread_id: str,
                    request: CommandRequest | Mapping[str, Any]
                    ) -> dict[str, str]:
        request = _command(request)

        async def action() -> dict[str, str]:
            snapshot, mode = await asyncio.gather(
                self.flow.snapshot(thread_id),
                self._thread_mode(thread_id),
            )
            if snapshot.status == 'paused':
                raise ServiceError(409, 'paused thread requires resume')
            if snapshot.status != 'idle':
                raise ServiceError(409, 'thread has already been started')
            target = request.until_step or _STAGES[0]
            if target != _STAGES[0]:
                raise ServiceError(422, f'start runs through {_STAGES[0]}')
            await self.flow.start(thread_id)
            if mode == 'auto':
                self._ensure_auto_task(thread_id)
            return _accepted(thread_id, request.command_id, 'start')

        return await self._control(thread_id, action)

    async def continue_thread(self, thread_id: str,
                              request: CommandRequest | Mapping[str, Any]
                              ) -> dict[str, str]:
        request = _command(request)

        async def action() -> dict[str, str]:
            if await self._thread_mode(thread_id) == 'auto':
                snapshot = await self.flow.snapshot(thread_id)
                if snapshot.status == 'idle':
                    raise ServiceError(409, 'thread has not been started')
                if snapshot.status in {'cancelled', 'failed'}:
                    raise ServiceError(409, f'cannot continue thread from {snapshot.status}')
                self._ensure_auto_task(thread_id)
                return _accepted(thread_id, request.command_id, 'continue')

            snapshot = await self.flow.snapshot(thread_id)
            pending = snapshot.pending_approval
            if pending is None:
                raise ServiceError(409, 'thread is not awaiting approval')
            next_stage = _next_stage(pending.stage)
            target = request.until_step or next_stage
            if target != next_stage:
                raise ServiceError(
                    422,
                    f'continue from {pending.stage} runs through {next_stage}',
                )
            await self.flow.approve(thread_id, pending.stage)
            return _accepted(thread_id, request.command_id, 'continue')

        return await self._control(thread_id, action)

    async def retry(self, thread_id: str,
                    request: RetryRequest | Mapping[str, Any]
                    ) -> dict[str, str]:
        request = _retry_request(request)
        _validate_retry_stage(request.stage)
        command_id = (
            request.command_id.strip()
            or f'retry:{thread_id}:{time.time_ns()}'
        )

        async def action() -> dict[str, str]:
            await self.flow.retry(
                thread_id,
                stage=request.stage,
                request_id=command_id,
            )
            if await self._thread_mode(thread_id) == 'auto':
                self._ensure_auto_task(thread_id)
            return _accepted(thread_id, command_id, 'retry')

        return await self._control(thread_id, action)

    async def pause(self, thread_id: str,
                    request: ControlRequest | Mapping[str, Any]
                    ) -> dict[str, str]:
        request = _control_request(request)

        async def action() -> dict[str, str]:
            snapshot = await self.flow.snapshot(thread_id)
            if snapshot.status != 'paused':
                await self.flow.pause(thread_id)
            return _accepted(thread_id, request.command_id, 'pause')

        return await self._control(thread_id, action)

    async def resume(self, thread_id: str,
                     request: ControlRequest | Mapping[str, Any]
                     ) -> dict[str, str]:
        request = _control_request(request)

        async def action() -> dict[str, str]:
            await self.flow.resume(thread_id)
            if await self._thread_mode(thread_id) == 'auto':
                self._ensure_auto_task(thread_id)
            return _accepted(thread_id, request.command_id, 'resume')

        return await self._control(thread_id, action)

    async def cancel(self, thread_id: str,
                     request: ControlRequest | Mapping[str, Any]
                     ) -> dict[str, str]:
        request = _control_request(request)

        async def action() -> dict[str, str]:
            snapshot = await self.flow.snapshot(thread_id)
            if snapshot.status != 'cancelled':
                await self.flow.cancel(thread_id)
            return _accepted(thread_id, request.command_id, 'cancel')

        return await self._control(thread_id, action)

    async def message(self, thread_id: str,
                      request: MessageRequest
                      ) -> MessageTurnResult:
        lock = self._message_locks.setdefault(thread_id, asyncio.Lock())
        async with lock:
            result = await self.messages.run('user', thread_id, request)
        snapshot, mode = await asyncio.gather(
            self.flow.snapshot(thread_id),
            self._thread_mode(thread_id),
        )
        if mode == 'auto' and snapshot.status not in _AUTO_STOPPED:
            self._ensure_auto_task(thread_id)
        return result

    async def message_history(self, thread_id: str, page_size: int,
                              page_token: str
                              ) -> dict[str, Any]:
        await self.flow.snapshot(thread_id)
        history = await asyncio.to_thread(
            self.messages.history,
            thread_id,
            page_size,
            page_token,
        )
        return public_message_history(history)

    async def delete_thread(self, thread_id: str) -> dict[str, Any]:
        async def action() -> dict[str, Any]:
            auto = await self._thread_mode(thread_id) == 'auto'
            await self._stop_auto_task(thread_id)
            try:
                await self.flow.release(thread_id)
            except BaseException:
                if auto:
                    self._ensure_auto_task(thread_id)
                raise
            trace_ids = await _thread_trace_ids(self.flow, thread_id)
            lock = self._message_locks.setdefault(thread_id, asyncio.Lock())
            async with lock:
                await self.router.delete_thread(thread_id)
                cleanup = await asyncio.to_thread(
                    _delete_thread_files,
                    self.root,
                    thread_id,
                    trace_ids,
                )
                await self.messages.delete_thread(thread_id)
                await self.flow.delete_run(thread_id)
            return {
                'thread_id': thread_id,
                'deleted': True,
                'message': 'thread deleted',
                'cleanup': cleanup,
            }

        return await self._control(thread_id, action)

    async def close(self) -> None:
        self._closing = True
        tasks = tuple(self._auto_tasks.values())
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        self._auto_tasks.clear()
        await self.flow.close()

    async def _control(self, thread_id: str,
                       action: Callable[[], Awaitable[T]]
                       ) -> T:
        lock = self._control_locks.setdefault(thread_id, asyncio.Lock())
        async with lock:
            return await action()

    async def _run_config(self, thread_id: str) -> Mapping[str, Any]:
        record = await self.flow.head(thread_id, ArtifactKey.scalar(A.RUN_CONFIG))
        if record is None:
            return {}
        value = await self.flow.read(thread_id, record.ref)
        return value if isinstance(value, Mapping) else {}

    async def _thread_mode(self, thread_id: str) -> str:
        return str((await self._run_config(thread_id)).get('mode') or '')

    async def _restore_auto_threads(self) -> None:
        for thread_id in await self.flow.run_ids():
            snapshot, mode = await asyncio.gather(
                self.flow.snapshot(thread_id),
                self._thread_mode(thread_id),
            )
            if mode == 'auto' and snapshot.status not in _AUTO_STOPPED:
                self._ensure_auto_task(thread_id)

    def _ensure_auto_task(self, thread_id: str) -> None:
        if self._closing:
            return
        current = self._auto_tasks.get(thread_id)
        if current is not None and not current.done():
            return
        task = asyncio.create_task(
            self._drive_auto(thread_id),
            name=f'evo-auto:{thread_id}',
        )
        self._auto_tasks[thread_id] = task
        task.add_done_callback(
            lambda completed, run_id=thread_id: self._auto_task_done(
                run_id,
                completed,
            )
        )

    async def _drive_auto(self, thread_id: str) -> None:
        while not self._closing:
            lock = self._control_locks.setdefault(thread_id, asyncio.Lock())
            async with lock:
                snapshot = await self.flow.snapshot(thread_id)
                if snapshot.status in _AUTO_STOPPED:
                    return
                if snapshot.status == 'paused':
                    await self.flow.resume(thread_id)
                    continue
                if snapshot.status == 'awaiting_approval':
                    pending = snapshot.pending_approval
                    if pending is None:
                        raise RuntimeError('awaiting approval without pending stage')
                    await self.flow.approve(thread_id, pending.stage)
                    continue

            try:
                await self.flow.wait_until_boundary(
                    thread_id,
                    timeout=_AUTO_WAIT_TIMEOUT,
                )
            except TimeoutError:
                continue

    def _auto_task_done(self, thread_id: str,
                        task: asyncio.Task[None]
                        ) -> None:
        if self._auto_tasks.get(thread_id) is task:
            del self._auto_tasks[thread_id]
        if task.cancelled():
            return
        error = task.exception()
        if error is not None:
            logger.error(
                'auto runner failed for %s',
                thread_id,
                exc_info=(type(error), error, error.__traceback__),
            )

    async def _stop_auto_task(self, thread_id: str) -> None:
        task = self._auto_tasks.pop(thread_id, None)
        if task is None:
            return
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)


def _seed_values(thread_id: str, request: ThreadCreate) -> dict[str, object]:
    target_config = {
        'router_chat_url': request.inputs.router_chat_url,
        'router_admin_url': request.inputs.router_admin_url,
        'algorithm_id': request.inputs.algorithm_id,
        'case_deadline_seconds': request.inputs.case_deadline_seconds,
        'first_frame_timeout_seconds': _FIRST_FRAME_TIMEOUT,
    }
    return {
        A.RUN_CONFIG: {
            'thread_id': thread_id,
            'mode': request.mode,
            'title': request.title,
            'inputs': request.inputs.model_dump(),
            'num_case': request.inputs.num_case,
            'llm_config': dict(request.llm_config),
        },
        A.CORPUS_SOURCE_CONFIG: {
            'kb_id': request.inputs.kb_id,
            'csv_data': request.inputs.csv_data,
            'target_case_count': request.inputs.num_case,
            'min_case_count': request.inputs.num_case,
        },
        A.EVAL_TARGET_CONFIG: target_config,
        A.EVAL_POLICY: {},
        A.REPAIR_POLICY: {
            'workspace_namespace': thread_id,
        },
        A.ABTEST_CANDIDATE_CONFIG: {
            'router_chat_url': request.inputs.router_chat_url,
            'router_admin_url': request.inputs.router_admin_url,
            'case_deadline_seconds': request.inputs.case_deadline_seconds,
            'first_frame_timeout_seconds': _FIRST_FRAME_TIMEOUT,
        },
    }


def _public_thread(request: ThreadCreate, snapshot: FlowSnapshot) -> dict[str, Any]:
    return {
        'thread_id': snapshot.run_id,
        'mode': request.mode,
        'title': request.title,
        **public_thread_state(snapshot),
    }


def _command(request: CommandRequest | Mapping[str, Any]) -> CommandRequest:
    return request if isinstance(request, CommandRequest) else CommandRequest.model_validate(request)


def _control_request(request: ControlRequest | Mapping[str, Any]) -> ControlRequest:
    return request if isinstance(request, ControlRequest) else ControlRequest.model_validate(request)


def _retry_request(request: RetryRequest | Mapping[str, Any]) -> RetryRequest:
    return request if isinstance(request, RetryRequest) else RetryRequest.model_validate(request)


def _accepted(thread_id: str, command_id: str, command: str) -> dict[str, str]:
    return {
        'status': 'accepted',
        'thread_id': thread_id,
        'command_id': command_id.strip() or f'{command}:{thread_id}:{time.time_ns()}',
    }


def _page_offset(token: str) -> int:
    if not str(token or '0').isdigit():
        raise ServiceError(422, 'page_token must be a non-negative integer offset')
    return int(token or 0)


def _validate_stage(stage: str) -> None:
    if stage and stage not in _STAGES:
        raise ServiceError(422, f'until_step must be one of: {", ".join(_STAGES)}')


def _validate_retry_stage(stage: str) -> None:
    if stage and stage not in _STAGES:
        raise ServiceError(422, f'stage must be one of: {", ".join(_STAGES)}')


def _next_stage(stage: str) -> str:
    index = _STAGES.index(stage)
    return _STAGES[index + 1] if index + 1 < len(_STAGES) else ''


async def _thread_trace_ids(flow: ArtifactFlow, thread_id: str) -> tuple[str, ...]:
    snapshot = await flow.snapshot(thread_id)
    refs = tuple(
        ref
        for key, ref in snapshot.runtime.completed_artifacts.items()
        if any(token in key.artifact_id for token in ('answer', 'judge', 'trace', 'analysis', 'repair'))
    )
    values = await asyncio.gather(
        *(flow.read(thread_id, ref) for ref in refs),
        return_exceptions=True,
    )
    trace_ids: set[str] = set()
    for value in values:
        if isinstance(value, BaseException):
            continue
        _collect_trace_ids(value, trace_ids)
    return tuple(sorted(trace_ids))


def _collect_trace_ids(value: object, target: set[str]) -> None:
    if isinstance(value, Mapping):
        for key, child in value.items():
            if str(key) == 'trace_id' and _TRACE_ID.fullmatch(str(child or '')):
                target.add(str(child))
            _collect_trace_ids(child, target)
    elif isinstance(value, (list, tuple)):
        for child in value:
            _collect_trace_ids(child, target)


def _delete_thread_files(root: Path, thread_id: str, trace_ids: tuple[str, ...]) -> dict[str, Any]:
    workspace = root / 'work' / 'repair' / thread_id
    workspace_deleted = workspace.exists()
    if workspace_deleted:
        shutil.rmtree(workspace, ignore_errors=False)

    folder = root / 'repair-traces'
    for suffix in ('.jsonl', '.lock', '.fallback.log'):
        (folder / f'{thread_id}{suffix}').unlink(missing_ok=True)

    deleted_traces = 0
    trace_root = Path(os.getenv('LAZYLLM_TRACE_LOCAL_STORAGE_DIR') or '')
    if trace_root.is_dir():
        candidates = {
            path
            for trace_id in trace_ids
            for path in trace_root.rglob(f'*_{trace_id}.jsonl')
        }
        for path in trace_root.rglob('*.jsonl'):
            if path in candidates:
                continue
            try:
                if not _file_contains(path, thread_id.encode()):
                    continue
            except OSError:
                continue
            candidates.add(path)
        for path in candidates:
            path.unlink(missing_ok=True)
            deleted_traces += 1

    return {
        'repair_workspace_deleted': workspace_deleted,
        'local_trace_files_deleted': deleted_traces,
    }


def _file_contains(path: Path, needle: bytes) -> bool:
    with path.open('rb') as handle:
        tail = b''
        while chunk := handle.read(1024 * 1024):
            data = tail + chunk
            if needle in data:
                return True
            tail = data[-max(0, len(needle) - 1):]
    return False


__all__ = ['EvoService']
