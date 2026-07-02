from __future__ import annotations

import asyncio
import json
import os
import shutil
import time
import uuid
from collections import Counter
from collections.abc import Callable
from concurrent.futures import Future, ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from threading import RLock
from typing import Any, Mapping

from fastapi import Body, FastAPI, HTTPException, Request, Response
from sse_starlette.sse import EventSourceResponse

from evo import normalize_chat_stream_url, normalize_http_origin, validate_id
from evo.artifact_flow import EvoFlowRuntime, FlowStepState
from evo.artifact_flow.runtime import FOLLOW_UP_STEP_RUN_PREFIX, STEPS
from evo.artifact_runtime import ArtifactKey, ArtifactRef
from evo.auto_agent import ActiveApproval, AutoAgentRunner
from evo.message_intent import MessageSessionStore
from evo.message_intent.store import MessageLeaseError, MessageStoreConflict
from evo.message_intent.planner import LazyLLMPlannerClient, StructuredJSONNextIntentPlanner
from evo.message_intent.service import MessageIntentService
from evo.service.auto_ports import HubAutoAgentPorts
from evo.service.message_runtime_ports import EvoMessageRuntimePorts, auto_intervention_frame
from evo.traces import build_trace_compare_view, build_trace_detail_view

BODY_REQUIRED = Body(...)
BODY_DEFAULT = Body(default_factory=dict)
RUN_ID = 'run_1'
MAX_CREATE_THREAD_CASES = 1000
MAX_CREATE_THREAD_WORKERS = 32
MAX_BACKGROUND_COMMAND_WORKERS = 4
RESULT_ARTIFACTS = {
    'datasets': ('eval.dataset',),
    'eval-reports': ('eval.summary', 'abtest.candidate_eval_summary'),
    'analysis-reports': ('analysis.summary',),
    'abtests': ('abtest.comparison',),
    'diffs': ('repair.verified_patch',),
}
STAGE_LABELS = {
    'dataset': '数据集生成',
    'eval': '评测',
    'analysis': '分析',
    'repair': '修复',
    'abtest': 'ABTest',
}
RESULT_ARTIFACT_ALIASES = {
    'eval.dataset': 'eval_dataset',
    'eval.summary': 'eval_report',
    'abtest.candidate_eval_summary': 'candidate_eval_report',
    'analysis.summary': 'classification_report',
    'repair.verified_patch': 'repair_loop_plan',
    'abtest.comparison': 'abtest_comparison',
}
ARTIFACT_ID_ALIASES = {value: key for key, value in RESULT_ARTIFACT_ALIASES.items()} | {
    'eval_dataset': 'eval.dataset',
    'eval_report': 'eval.summary',
    'candidate_eval_report': 'abtest.candidate_eval_summary',
    'classification_report': 'analysis.summary',
    'repair_loop_plan': 'repair.verified_patch',
    'abtest_comparison': 'abtest.comparison',
}


def create_app(*, planner_factory: Any | None = None) -> FastAPI:
    hub = EvoMessageHub(Path(os.getenv('LAZYMIND_EVO_BASE_DIR') or '/var/lib/lazymind/evo'),
                        planner_factory=planner_factory)
    app = FastAPI(title='evo flow service', version='artifact-runtime')
    app.state.hub = hub

    @app.get('/healthz')
    def healthz() -> dict:
        return {'ok': True, 'service': 'evo-flow'}

    @app.get('/livez')
    def livez() -> dict:
        return {'alive': True}

    @app.get('/readyz')
    def readyz() -> dict:
        return {'ready': True}

    @app.post('/v1/evo/threads')
    async def create_thread(body: dict = BODY_REQUIRED) -> dict:
        return await asyncio.to_thread(hub.create_thread, body)

    @app.get('/v1/evo/threads/statuses')
    def list_thread_statuses() -> dict:
        rows = [
            hub.flow_status(meta['id']) | {
                'title': meta.get('title', ''),
                'mode': meta.get('mode', 'interactive'),
                'created_at': meta.get('created_at'),
                'updated_at': meta.get('updated_at'),
            }
            for meta in hub.list_threads()
        ]
        counts: dict[str, int] = {}
        for row in rows:
            counts[row['status']] = counts.get(row['status'], 0) + 1
        return {'total': len(rows), 'counts': counts, 'threads': rows}

    @app.delete('/v1/evo/threads/{thread_id}')
    def delete_thread(thread_id: str) -> dict:
        return hub.delete_thread(thread_id)

    @app.get('/v1/evo/threads/{thread_id}/flow-status')
    def flow_status(thread_id: str) -> dict:
        return hub.flow_status(thread_id)

    @app.post('/v1/evo/threads/{thread_id}/messages')
    async def post_message(thread_id: str, body: dict = BODY_REQUIRED) -> EventSourceResponse:
        return EventSourceResponse(hub.post_message_stream(thread_id, body))

    @app.post('/v1/evo/threads/{thread_id}/start')
    async def start(thread_id: str, response: Response, body: dict = BODY_DEFAULT) -> dict:
        result = await asyncio.to_thread(hub.start, thread_id, body)
        _set_accepted_status(response, result)
        return result

    @app.post('/v1/evo/threads/{thread_id}/pause')
    async def pause(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.pause, thread_id)

    @app.post('/v1/evo/threads/{thread_id}/cancel')
    async def cancel(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.cancel, thread_id)

    @app.post('/v1/evo/threads/{thread_id}/retry')
    async def retry(thread_id: str, response: Response, body: dict = BODY_DEFAULT) -> dict:
        result = await asyncio.to_thread(hub.retry, thread_id, body)
        _set_accepted_status(response, result)
        return result

    @app.post('/v1/evo/threads/{thread_id}/continue')
    async def continue_thread(thread_id: str, response: Response, body: dict = BODY_DEFAULT) -> dict:
        result = await asyncio.to_thread(hub.continue_thread, thread_id, body)
        _set_accepted_status(response, result)
        return result

    @app.get('/v1/evo/threads/{thread_id}/events')
    def events(thread_id: str, request: Request, since: int = 0) -> EventSourceResponse:
        hub.get_thread(thread_id)
        last = request.headers.get('last-event-id') or ''
        return EventSourceResponse(hub.events(thread_id, int(last) if last.isdigit() else since))

    @app.get('/v1/evo/threads/{thread_id}/events/{step_run_id}')
    def step_events(thread_id: str, step_run_id: str, request: Request, since: int = 0) -> EventSourceResponse:
        hub.get_thread(thread_id)
        last = request.headers.get('last-event-id') or ''
        return EventSourceResponse(hub.events(thread_id, int(last) if last.isdigit() else since, step_run_id))

    @app.get('/v1/evo/threads/{thread_id}/results/traces/{trace_id}')
    def trace_detail(thread_id: str, trace_id: str) -> dict:
        return hub.trace_detail(thread_id, trace_id)

    @app.get('/v1/evo/threads/{thread_id}/results/traces-compare')
    def trace_compare(thread_id: str, a: str, b: str) -> dict:
        return hub.trace_compare(thread_id, a, b)

    @app.get('/v1/evo/threads/{thread_id}/results/{kind}')
    def results(thread_id: str, kind: str) -> list[dict]:
        return hub.results(thread_id, kind)

    @app.get('/v1/evo/threads/{thread_id}/artifacts/{artifact_id}')
    def artifact(thread_id: str, artifact_id: str) -> dict:
        return hub.artifact(thread_id, artifact_id)

    @app.get('/v1/evo/reports/{report_id}/content')
    def report_content(report_id: str, fmt: str = ''):
        thread_id, artifact_id = _scoped_report_id(report_id)
        content = hub.report_content(thread_id, artifact_id)
        if fmt in {'md', 'markdown', 'text'}:
            return Response(content, media_type='text/markdown; charset=utf-8')
        return {'thread_id': thread_id, 'report_id': artifact_id, 'content': content}

    @app.get('/v1/evo/diffs/{apply_id}/{filename:path}')
    def diff_content(apply_id: str, filename: str) -> Response:
        return Response(hub.diff_content(apply_id, filename), media_type='text/x-diff; charset=utf-8')

    return app


def get_app() -> FastAPI:
    return create_app()


@dataclass(frozen=True)
class RunningCommand:
    thread_id: str
    command_id: str
    operation: str
    submitted_at: float
    future: Future


class BackgroundCommandDispatcher:
    def __init__(self, *, max_workers: int) -> None:
        self._executor = ThreadPoolExecutor(max_workers=max_workers, thread_name_prefix='evo-command')
        self._lock = RLock()
        self._running: dict[str, RunningCommand] = {}

    def submit(
        self,
        thread_id: str,
        command_id: str,
        operation: str,
        work: Callable[[], Any],
        on_accept: Callable[[], None] | None = None,
    ) -> dict[str, Any]:
        with self._lock:
            current = self._running.get(thread_id)
            if current is not None and not current.future.done():
                status = 'accepted_existing' if current.command_id == command_id else 'conflict'
                return _accepted_command_response(
                    thread_id,
                    current.command_id,
                    current.operation,
                    status=status,
                    existing_command_id=current.command_id,
                )
            if current is not None:
                self._running.pop(thread_id, None)
            if on_accept is not None:
                on_accept()
            future = self._executor.submit(work)
            running = RunningCommand(thread_id, command_id, operation, time.time(), future)
            self._running[thread_id] = running
            future.add_done_callback(lambda done, tid=thread_id: self._complete(tid, done))
            return _accepted_command_response(thread_id, command_id, operation)

    def running(self, thread_id: str) -> RunningCommand | None:
        with self._lock:
            current = self._running.get(thread_id)
            if current is None:
                return None
            if current.future.done():
                self._running.pop(thread_id, None)
                return None
            return current

    def _complete(self, thread_id: str, future: Future) -> None:
        with self._lock:
            current = self._running.get(thread_id)
            if current is not None and current.future is future:
                self._running.pop(thread_id, None)


class EvoMessageHub:
    def __init__(self, base_dir: Path, *, planner_factory: Any | None = None):
        self.base_dir = base_dir
        self.threads_dir = base_dir / 'state' / 'threads'
        self._artifact_flows: dict[str, EvoFlowRuntime] = {}
        self._flow_operation_locks: dict[str, RLock] = {}
        self._pending_flow_controls: dict[str, tuple[str, str]] = {}
        self._message_services: dict[str, MessageIntentService] = {}
        self._lifecycle_lock = RLock()
        self._message_service_lock = RLock()
        self._background_commands = BackgroundCommandDispatcher(
            max_workers=_bounded_positive_int(
                os.getenv('LAZYMIND_EVO_BACKGROUND_WORKERS', str(MAX_BACKGROUND_COMMAND_WORKERS)),
                'LAZYMIND_EVO_BACKGROUND_WORKERS',
                MAX_CREATE_THREAD_WORKERS,
            )
        )
        self._planner_factory = planner_factory
        self._auto_agent = AutoAgentRunner(base_dir, HubAutoAgentPorts(self))

    def create_thread(self, payload: dict[str, Any]) -> dict:
        mode = str(payload.get('mode') or 'interactive').strip()
        if mode not in {'auto', 'interactive'}:
            raise HTTPException(400, f'bad mode {mode!r}')
        try:
            inputs = _normalize_inputs(dict(payload.get('inputs') or {}))
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        thread_id, now = f'thr-{uuid.uuid4().hex[:8]}', time.time()
        meta = {
            'id': thread_id,
            'thread_id': thread_id,
            'mode': mode,
            'title': str(payload.get('title') or ''),
            'inputs': inputs,
            'llm_config': _llm_config_payload(payload),
            'status': 'idle',
            'created_at': now,
            'updated_at': now,
        }
        self._write_meta(thread_id, meta)
        if mode == 'auto' and payload.get('start_auto'):
            self.auto_start(thread_id, payload)
        return self._meta(thread_id)

    def list_threads(self) -> list[dict]:
        rows = [_read_json(path) for path in self.threads_dir.glob('*/thread.json')]
        return sorted([row for row in rows if row], key=lambda row: row.get('updated_at') or 0, reverse=True)

    def get_thread(self, thread_id: str) -> dict:
        return self._meta(thread_id)

    def delete_thread(self, thread_id: str) -> dict:
        with self._lifecycle_lock:
            self._meta(thread_id)
            running = self._background_commands.running(thread_id)
            if running is not None:
                raise HTTPException(409, _accepted_command_response(
                    thread_id,
                    running.command_id,
                    running.operation,
                    status='conflict',
                    existing_command_id=running.command_id,
                ))
            operation_lock = self._flow_operation_lock(thread_id)
            if not operation_lock.acquire(blocking=False):
                raise HTTPException(409, _accepted_command_response(
                    thread_id,
                    'delete',
                    'delete',
                    status='conflict',
                ))
            try:
                self._close_flow(thread_id)
                self._close_message_service(thread_id)
                run_root, thread_dir = self._run_root(thread_id), self._thread_dir(thread_id)
                run_deleted, thread_deleted = run_root.exists(), thread_dir.exists()
                shutil.rmtree(run_root, ignore_errors=True)
                shutil.rmtree(thread_dir, ignore_errors=True)
            finally:
                operation_lock.release()
        return {
            'thread_id': thread_id,
            'deleted_run': run_deleted,
            'deleted_thread': thread_deleted,
            'cancelled': False,
        }

    def start(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        payload = payload or {}
        self._meta(thread_id)
        command_id = str(payload.get('command_id') or f'start:{thread_id}')
        return self._submit_background_flow_command(
            thread_id,
            command_id,
            'start',
            lambda: self._start_flow_now(thread_id, payload, command_id),
        )

    def pause(self, thread_id: str, command_id: str | None = None) -> dict:
        self._meta(thread_id)
        command_id = command_id or f'pause:{uuid.uuid4().hex}'
        if not self._has_artifact_flow(thread_id):
            if self._background_commands.running(thread_id) is not None:
                return self._request_busy_flow_control(thread_id, 'pause', command_id)
            self._update_meta(thread_id, status='paused', pending_checkpoint=None, updated_at=time.time())
            return {'status': 'paused', 'thread_id': thread_id}
        state = self._run_sync_flow_operation(
            thread_id,
            'pause',
            lambda: self._artifact_flow(thread_id).pause_flow(
                command_id=command_id,
                run_id=RUN_ID,
            ),
        )
        response = self._artifact_flow_response(thread_id, state)
        return response | {'status': 'paused', 'pending_checkpoint': None}

    def cancel(self, thread_id: str, command_id: str | None = None) -> dict:
        self._meta(thread_id)
        command_id = command_id or f'cancel:{uuid.uuid4().hex}'
        if not self._has_artifact_flow(thread_id):
            if self._background_commands.running(thread_id) is not None:
                return self._request_busy_flow_control(thread_id, 'cancel', command_id)
            self._update_meta(thread_id, status='cancelled', pending_checkpoint=None, updated_at=time.time())
            return {'status': 'cancelled', 'thread_id': thread_id}
        state = self._run_sync_flow_operation(
            thread_id,
            'cancel',
            lambda: self._artifact_flow(thread_id).cancel_flow(
                command_id=command_id,
                run_id=RUN_ID,
            ),
        )
        return self._artifact_flow_response(thread_id, state)

    def retry(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        payload = payload or {}
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            raise HTTPException(409, 'thread has no flow to retry')
        command_id = str(payload.get('command_id') or f'retry:{uuid.uuid4().hex}')
        result = self._submit_background_flow_command(
            thread_id,
            command_id,
            'retry',
            lambda: self._retry_flow_now(thread_id, payload, command_id),
        )
        return result | {'retried': True}

    def continue_thread(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        payload = payload or {}
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            raise HTTPException(409, 'thread has no flow to continue')
        command_id = str(payload.get('command_id') or f'continue:{uuid.uuid4().hex}')
        result = self._submit_background_flow_command(
            thread_id,
            command_id,
            'continue',
            lambda: self._continue_flow_now(thread_id, payload, command_id),
        )
        return result | {'resumed': True}

    def auto_start(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        self._meta(thread_id)
        return self._auto_agent.start(thread_id, payload or {})

    def active_approval(self, thread_id: str) -> ActiveApproval | None:
        self._meta(thread_id)
        if not self._message_store_path(thread_id).exists():
            return None
        approval = self._message_service(thread_id).active_approval(thread_id)
        if approval is None:
            return None
        return ActiveApproval(
            approval_token=approval.approval_token,
            intent_kind=approval.intent_kind,
            risk_level=approval.risk_level,
            status=approval.status,
            expected_refs=approval.expected_refs,
            expires_at=approval.expires_at,
        )

    def resolve_approval(self, thread_id: str, *, action: str, approval_token: str, command_id: str = '') -> dict:
        self._meta(thread_id)
        command = command_id or f'approval:{thread_id}:{action}:{approval_token}'
        try:
            result = self._message_service(thread_id).resolve_pending_structured(
                thread_id,
                action=action,
                approval_token=approval_token,
                command_id=command,
            )
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        except (MessageLeaseError, MessageStoreConflict, RuntimeError) as exc:
            raise HTTPException(409, str(exc)) from exc
        return {
            'status': result.status,
            'thread_id': result.thread_id,
            'turn_id': result.turn_id,
            'message_id': result.message_id,
            'response': result.response,
            'message_event_cursor': result.message_event_cursor,
            'pending_approval': result.pending_approval,
        }

    def probe_resolving_approval(self, thread_id: str, *, approval_token: str) -> dict:
        self._meta(thread_id)
        try:
            result = self._message_service(thread_id).probe_resolving_approval(
                thread_id,
                approval_token=approval_token,
            )
        except (MessageLeaseError, MessageStoreConflict, RuntimeError) as exc:
            raise HTTPException(409, str(exc)) from exc
        return {'thread_id': thread_id, **result}

    def post_message(self, thread_id: str, payload: dict[str, Any]) -> dict:
        with self._message_service_lock:
            self._update_llm_config(thread_id, payload)
            self._meta(thread_id)
            try:
                result = self._message_service(thread_id).handle(thread_id, payload)
            except ValueError as exc:
                raise HTTPException(400, str(exc)) from exc
            except RuntimeError as exc:
                raise HTTPException(409, str(exc)) from exc
        return {
            'status': result.status,
            'thread_id': result.thread_id,
            'turn_id': result.turn_id,
            'message_id': result.message_id,
            'response': result.response,
            'message_event_cursor': result.message_event_cursor,
            'pending_approval': result.pending_approval,
        }

    def execute_auto_intervention(
        self,
        thread_id: str,
        intervention: Mapping[str, Any],
        *,
        command_id: str,
    ) -> dict:
        with self._message_service_lock:
            self._meta(thread_id)
            try:
                result = self._message_service(thread_id).handle_typed_intervention(
                    thread_id,
                    {'kind': intervention.get('kind'), '_frame': auto_intervention_frame(intervention)},
                    command_id=command_id,
                )
            except ValueError as exc:
                raise HTTPException(400, str(exc)) from exc
            except RuntimeError as exc:
                raise HTTPException(409, str(exc)) from exc
        return {
            'status': result.status,
            'thread_id': result.thread_id,
            'turn_id': result.turn_id,
            'message_id': result.message_id,
            'response': result.response,
            'message_event_cursor': result.message_event_cursor,
            'pending_approval': result.pending_approval,
        }

    async def post_message_stream(self, thread_id: str, payload: dict[str, Any]):
        try:
            result = self.post_message(thread_id, payload)
            with self._message_service_lock:
                rows = self._message_service(thread_id).turn_events(
                    thread_id,
                    str(result.get('turn_id') or ''),
                    str(result.get('message_id') or ''),
                )
            for row in rows:
                yield _sse(str(row['event']), {'thread_id': thread_id, **row['data']}, str(row['id']))
            if not any(str(row['event']) == 'done' for row in rows):
                yield _sse(
                    'done',
                    {'thread_id': thread_id, 'status': result['status']},
                    str(result.get('message_event_cursor') or 0),
                )
        except HTTPException as exc:
            yield _sse('error', {'thread_id': thread_id, 'code': exc.status_code, 'message': exc.detail})

    async def events(self, thread_id: str, since: int = 0, step_run_id: str = ''):
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            return
        cursor, idle_ticks = max(0, since), 0
        flow = self._artifact_flow(thread_id)
        seen_step_event = False
        while True:
            events = flow.runtime.controller.event_log.scan_since(cursor, limit=100)
            for event in events:
                cursor = max(cursor, event.seq)
                if step_run_id and str((event.payload or {}).get('step_run_id') or '') != step_run_id:
                    continue
                seen_step_event = bool(step_run_id) or seen_step_event
                yield _sse('message', _frontend_event_payload(event, flow), str(event.seq))
            status = self.flow_status(thread_id)['status']
            if step_run_id and cursor < flow.runtime.controller.event_log.max_seq():
                continue
            if step_run_id and _step_run_stream_done(flow, step_run_id, status, seen_step_event):
                yield _sse('done', _step_run_done_payload(thread_id, status, flow, step_run_id), str(cursor + 1))
                return
            if status in {'ended', 'failed', 'cancelled'} and not events:
                yield _sse('done', {'thread_id': thread_id, 'status': status}, str(cursor + 1))
                return
            idle_ticks = idle_ticks + 1 if not events else 0
            if status in {'idle', 'paused', 'waiting_checkpoint'} and idle_ticks > 4:
                return
            await asyncio.sleep(0.5)

    def flow_status(self, thread_id: str) -> dict:
        meta = self._meta(thread_id)
        running = self._background_commands.running(thread_id)
        active_task_ids = [] if running is None else [running.command_id]
        if not self._has_artifact_flow(thread_id):
            status = 'running' if active_task_ids else str(meta.get('status') or 'idle')
            return _flow_status_row(thread_id, status, active_task_ids)
        flow = self._artifact_flow(thread_id)
        state = flow.step_store.get(RUN_ID)
        controller_state = flow.runtime.controller.state(RUN_ID)
        run_status = controller_state.run.status if controller_state.run_exists else ''
        status = _artifact_flow_http_status(state, run_status)
        if active_task_ids and status in {'idle', 'paused', 'waiting_checkpoint'}:
            status = 'running'
        return _flow_status_row(
            thread_id,
            status,
            active_task_ids,
            latest_abtest_status=_abtest_status(flow),
            report_ready=self._artifact_runtime_row(thread_id, 'eval.summary') is not None,
            pending_checkpoint=_artifact_checkpoint_payload(state),
        ) | {
            'current_step': '' if state is None else state.current_step,
            'completed_steps': [] if state is None else list(state.completed_steps),
            'stale_steps': [] if state is None else list(state.stale_steps),
        }

    def results(self, thread_id: str, kind: str) -> list[dict]:
        self._meta(thread_id)
        if kind not in RESULT_ARTIFACTS:
            raise HTTPException(404, f'unknown result kind: {kind}')
        rows = [row for artifact_id in RESULT_ARTIFACTS[kind] if (
            row := self._artifact_runtime_row(thread_id, artifact_id))]
        return _frontend_result_rows(kind, rows)

    def trace_detail(self, thread_id: str, trace_id: str) -> dict:
        self._meta(thread_id)
        return build_trace_detail_view(
            trace_id,
            lambda artifact_id: self._thread_artifact_payload(thread_id, artifact_id),
        )

    def trace_compare(self, thread_id: str, a: str, b: str) -> dict:
        self._meta(thread_id)
        return build_trace_compare_view(
            a,
            b,
            lambda artifact_id: self._thread_artifact_payload(thread_id, artifact_id),
        )

    def artifact(self, thread_id: str, artifact_id: str) -> dict:
        row = self._artifact_runtime_row(thread_id, ARTIFACT_ID_ALIASES.get(artifact_id, artifact_id))
        if row is None:
            raise HTTPException(404, f'artifact not found: {artifact_id}')
        return _frontend_artifact_row(row)

    def report_content(self, thread_id: str, artifact_id: str) -> str:
        data = self.artifact(thread_id, artifact_id)['data']
        if isinstance(data, dict):
            for key in ('markdown', 'report', 'content', 'text', 'summary'):
                value = data.get(key)
                if isinstance(value, str) and value.strip():
                    return value
        return json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True, default=str)

    def diff_content(self, apply_id: str, filename: str) -> str:
        del filename
        thread_id, artifact_id = _scoped_report_id(apply_id) if ':' in apply_id else self._find_artifact(apply_id)
        data = self.artifact(thread_id, artifact_id)['data']
        if isinstance(data, dict):
            for key in ('diff', 'patch', 'content'):
                value = data.get(key)
                if isinstance(value, str) and value.strip():
                    return value
            if data.get('status') in {'skipped', 'skipped_no_bad_case'}:
                return 'No code changes were produced for this repair step.\n'
        raise HTTPException(404, f'diff content not found: {apply_id}')

    def _artifact_flow(self, thread_id: str) -> EvoFlowRuntime:
        with self._lifecycle_lock:
            if thread_id not in self._artifact_flows:
                inputs = self._artifact_flow_config(thread_id)
                path = self._artifact_runtime_path(thread_id)
                path.parent.mkdir(parents=True, exist_ok=True)
                self._artifact_flows[thread_id] = EvoFlowRuntime.open(
                    path,
                    case_count=int(inputs['num_cases']),
                    llm_config=inputs.get('llm_config') or {},
                )
            return self._artifact_flows[thread_id]

    def _message_service(self, thread_id: str) -> MessageIntentService:
        with self._message_service_lock:
            if thread_id not in self._message_services:
                llm = self._message_llm(thread_id)
                self._message_services[thread_id] = MessageIntentService(
                    MessageSessionStore(self._message_store_path(thread_id)),
                    has_flow=self._has_artifact_flow,
                    flow_status=self.flow_status,
                    case_count_getter=lambda tid: int(self._artifact_flow_config(tid)['num_cases']),
                    runtime_port=EvoMessageRuntimePorts(
                        flow_getter=self._artifact_flow,
                        artifact_reader=lambda tid, artifact_id: self._artifact_runtime_row(tid, artifact_id),
                        background_submitter=self.submit_background_message_command,
                        sync_runner=self.run_sync_message_command,
                    ),
                    planner=self._message_planner(thread_id, llm=llm),
                    response_llm=llm,
                )
            return self._message_services[thread_id]

    def _message_llm(self, thread_id: str) -> LazyLLMPlannerClient:
        return LazyLLMPlannerClient(llm_config=_llm_config_payload(self._meta(thread_id)))

    def _message_planner(
        self,
        thread_id: str,
        *,
        llm: LazyLLMPlannerClient | None = None,
    ) -> StructuredJSONNextIntentPlanner:
        llm_config = _llm_config_payload(self._meta(thread_id))
        if self._planner_factory is not None:
            return self._planner_factory(thread_id, llm_config)
        return StructuredJSONNextIntentPlanner(llm or self._message_llm(thread_id))

    def _close_flow(self, thread_id: str) -> None:
        with self._lifecycle_lock:
            flow = self._artifact_flows.pop(thread_id, None)
            self._flow_operation_locks.pop(thread_id, None)
            if flow is not None:
                flow.close()

    def _has_artifact_flow(self, thread_id: str) -> bool:
        return thread_id in self._artifact_flows or self._artifact_runtime_path(thread_id).exists()

    def _artifact_runtime_row(self, thread_id: str, artifact_id: str) -> dict | None:
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            return None
        flow = self._artifact_flow(thread_id)
        key, requested_version = _artifact_selector(artifact_id)
        ref = ArtifactRef(
            key, requested_version) if requested_version is not None else flow.runtime.stores.artifact_store.latest(key)
        if ref is None:
            return None
        record = flow.runtime.stores.artifact_store.get(ref)
        if record is None:
            return None
        return {
            'artifact_id': key.artifact_id,
            'partition': key.partition,
            'ref': str(ref),
            'schema': record.value.schema,
            'data': record.value.payload,
        }

    def _find_artifact(self, artifact_id: str) -> tuple[str, str]:
        for meta in self.list_threads():
            thread_id = str(meta.get('id') or '')
            if thread_id and self._artifact_runtime_row(thread_id, artifact_id) is not None:
                return thread_id, artifact_id
        raise HTTPException(404, f'artifact not found: {artifact_id}')

    def _artifact_flow_config(self, thread_id: str) -> dict[str, Any]:
        meta = self._meta(thread_id)
        raw_inputs = dict(meta.get('inputs') or {})
        try:
            inputs = _normalize_inputs(raw_inputs)
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        if inputs != raw_inputs:
            self._update_meta(thread_id, inputs=inputs, updated_at=time.time())
        return inputs | {'llm_config': _llm_config_payload(meta)}

    def _update_llm_config(self, thread_id: str, payload: dict[str, Any]) -> None:
        llm_config = _llm_config_payload(payload)
        if not llm_config:
            return
        with self._lifecycle_lock:
            self._update_meta(thread_id, llm_config=llm_config, updated_at=time.time())
        self._close_message_service(thread_id)

    def _close_message_service(self, thread_id: str) -> None:
        with self._message_service_lock:
            service = self._message_services.pop(thread_id, None)
        if service is not None:
            service.store.close()

    def _artifact_flow_response(self, thread_id: str, state: FlowStepState) -> dict:
        controller_state = self._artifact_flow(thread_id).runtime.controller.state(state.run_id)
        run_status = controller_state.run.status if controller_state.run_exists else ''
        status = _artifact_flow_http_status(state, run_status)
        checkpoint = _artifact_checkpoint_payload(state)
        self._update_meta(thread_id, status=status, pending_checkpoint=checkpoint, updated_at=time.time())
        return {
            'status': status,
            'thread_id': thread_id,
            'run_id': state.run_id,
            'current_step': state.current_step,
            'completed_steps': list(state.completed_steps),
            'stale_steps': list(state.stale_steps),
            'gate_status': state.gate_status,
            'gate_artifact_ref': '' if state.gate_artifact_ref is None else str(state.gate_artifact_ref),
            'pending_checkpoint': checkpoint,
        }

    def _start_flow_now(self, thread_id: str, payload: Mapping[str, Any], command_id: str) -> FlowStepState:
        self._update_llm_config(thread_id, dict(payload))
        config = self._artifact_flow_config(thread_id)
        return self._artifact_flow(thread_id).start_full_flow(command_id=command_id, run_id=RUN_ID, config=config)

    def _retry_flow_now(self, thread_id: str, payload: Mapping[str, Any], command_id: str) -> FlowStepState:
        self._update_llm_config(thread_id, dict(payload))
        self._apply_current_flow_llm_config(thread_id)
        return self._artifact_flow(thread_id).retry_failed_flow(command_id=command_id, run_id=RUN_ID)

    def _continue_flow_now(self, thread_id: str, payload: Mapping[str, Any], command_id: str) -> FlowStepState:
        self._update_llm_config(thread_id, dict(payload))
        self._apply_current_flow_llm_config(thread_id)
        return self._artifact_flow(thread_id).continue_flow(command_id=command_id, run_id=RUN_ID)

    def submit_background_message_command(
        self,
        thread_id: str,
        command_id: str,
        operation: str,
        work: Callable[[], dict[str, Any]],
    ) -> dict[str, Any]:
        with self._lifecycle_lock:
            result = self._background_commands.submit(
                thread_id,
                command_id,
                operation,
                lambda: self._run_locked_flow_operation(
                    thread_id,
                    lambda: self._run_background_message_command(thread_id, work),
                ),
                on_accept=lambda: self._update_meta(thread_id, status='running', updated_at=time.time()),
            )
        if result['status'] == 'conflict':
            return result | {'reason': 'thread already has a running long command'}
        return result

    def run_sync_message_command(
        self,
        thread_id: str,
        operation: str,
        work: Callable[[], dict[str, Any]],
    ) -> dict[str, Any]:
        try:
            return self._run_sync_flow_operation(thread_id, operation, work)
        except HTTPException as exc:
            detail = exc.detail if isinstance(exc.detail, Mapping) else {}
            return dict(detail) if detail else {'status': 'conflict', 'reason': str(exc.detail)}

    def _run_background_message_command(self, thread_id: str, work: Callable[[], dict[str, Any]]) -> dict[str, Any]:
        try:
            self._apply_current_flow_llm_config(thread_id)
            result = dict(work())
            control_result = self._apply_pending_flow_control(thread_id)
            if control_result is not None:
                return control_result
            self._sync_flow_status_from_background_result(thread_id, result)
            return result
        except Exception:
            self._update_meta(thread_id, status='failed', updated_at=time.time())
            raise

    def _sync_flow_status_from_background_result(self, thread_id: str, result: Mapping[str, Any]) -> None:
        status = str(result.get('status') or '')
        if status:
            self._update_meta(thread_id, status=_message_background_status(status), updated_at=time.time())

    def _submit_background_flow_command(
        self,
        thread_id: str,
        command_id: str,
        operation: str,
        work: Callable[[], FlowStepState],
    ) -> dict:
        with self._lifecycle_lock:
            result = self._background_commands.submit(
                thread_id,
                command_id,
                operation,
                lambda: self._run_locked_flow_operation(
                    thread_id,
                    lambda: self._run_background_flow_command(thread_id, work),
                ),
                on_accept=lambda: self._update_meta(thread_id, status='running', updated_at=time.time()),
            )
        if result['status'] == 'conflict':
            raise HTTPException(409, result)
        return result

    def _run_background_flow_command(self, thread_id: str, work: Callable[[], FlowStepState]) -> dict:
        try:
            result = self._artifact_flow_response(thread_id, work())
            control_result = self._apply_pending_flow_control(thread_id)
            return control_result or result
        except Exception:
            self._update_meta(thread_id, status='failed', updated_at=time.time())
            raise

    def _flow_operation_lock(self, thread_id: str) -> RLock:
        with self._lifecycle_lock:
            lock = self._flow_operation_locks.get(thread_id)
            if lock is None:
                lock = RLock()
                self._flow_operation_locks[thread_id] = lock
            return lock

    def _run_locked_flow_operation(self, thread_id: str, work: Callable[[], Any]) -> Any:
        with self._flow_operation_lock(thread_id):
            return work()

    def _run_sync_flow_operation(self, thread_id: str, operation: str, work: Callable[[], Any]) -> Any:
        lock = self._flow_operation_lock(thread_id)
        if not lock.acquire(blocking=False):
            if operation in {'pause', 'cancel'} and self._background_commands.running(thread_id) is not None:
                return self._request_busy_flow_control(
                    thread_id,
                    operation,
                    f'{operation}:{uuid.uuid4().hex}',
                )
            running = self._background_commands.running(thread_id)
            command_id = '' if running is None else running.command_id
            raise HTTPException(409, _accepted_command_response(
                thread_id,
                command_id or operation,
                operation,
                status='conflict',
                existing_command_id=command_id,
            ))
        try:
            self._apply_current_flow_llm_config(thread_id)
            return work()
        finally:
            lock.release()

    def _apply_current_flow_llm_config(self, thread_id: str) -> None:
        self._artifact_flow(thread_id).set_llm_config(_llm_config_payload(self._meta(thread_id)))

    def _request_busy_flow_control(self, thread_id: str, operation: str, command_id: str) -> dict:
        with self._lifecycle_lock:
            self._pending_flow_controls[thread_id] = (operation, command_id)
        return _accepted_command_response(thread_id, command_id, operation) | {'control_requested': True}

    def _apply_pending_flow_control(self, thread_id: str) -> dict | None:
        with self._lifecycle_lock:
            pending = self._pending_flow_controls.pop(thread_id, None)
        if pending is None:
            return None
        operation, command_id = pending
        flow = self._artifact_flow(thread_id)
        if operation == 'pause':
            state = flow.pause_flow(command_id=command_id, run_id=RUN_ID)
            return self._artifact_flow_response(thread_id, state) | {'status': 'paused', 'pending_checkpoint': None}
        if operation == 'cancel':
            state = flow.cancel_flow(command_id=command_id, run_id=RUN_ID)
            return self._artifact_flow_response(thread_id, state)
        return None

    def _thread_dir(self, thread_id: str) -> Path:
        return self.threads_dir / thread_id

    def _run_root(self, thread_id: str) -> Path:
        return self.base_dir / 'dev-runs' / thread_id

    def _artifact_runtime_path(self, thread_id: str) -> Path:
        return self._run_root(thread_id) / 'artifact-runtime.sqlite'

    def _message_store_path(self, thread_id: str) -> Path:
        return self._thread_dir(thread_id) / 'message-session.sqlite'

    def _meta(self, thread_id: str) -> dict:
        meta = _read_json(self._thread_dir(thread_id) / 'thread.json')
        if not meta:
            raise HTTPException(404, f'thread {thread_id} not found')
        return meta

    def _write_meta(self, thread_id: str, meta: dict) -> None:
        _write_json(self._thread_dir(thread_id) / 'thread.json', meta)

    def _update_meta(self, thread_id: str, **patch: Any) -> None:
        meta = self._meta(thread_id)
        meta.update(patch)
        self._write_meta(thread_id, meta)


def _sse(event: str, payload: dict[str, Any], event_id: str | None = None) -> dict:
    row = {'event': event, 'data': json.dumps({'type': event, **payload}, ensure_ascii=False, default=str)}
    if event_id:
        row['id'] = event_id
    return row


def _step_run_done_payload(thread_id: str, status: str, flow: EvoFlowRuntime, step_run_id: str) -> dict[str, Any]:
    return {
        'thread_id': thread_id,
        'status': status,
        'step_run_id': step_run_id,
        'next_step_run_id': flow.step_store.next_step_run_id_for(RUN_ID, step_run_id),
    }


def _step_run_stream_done(
    flow: EvoFlowRuntime,
    step_run_id: str,
    status: str,
    seen_step_event: bool,
) -> bool:
    step = flow.step_store.step_for_step_run_id(RUN_ID, step_run_id)
    if not step:
        return False
    if step.startswith(FOLLOW_UP_STEP_RUN_PREFIX):
        return seen_step_event and status not in {'running', 'idle'}
    state = flow.step_store.get(RUN_ID)
    if state is None or step not in STEPS:
        return False
    if step in state.completed_steps:
        return True
    if state.current_step == step and state.gate_status in {'paused', 'completed', 'cancelled', 'stale'}:
        return True
    if state.current_step in STEPS and STEPS.index(state.current_step) > STEPS.index(step):
        return True
    return status in {'failed', 'cancelled'} and seen_step_event


def _set_accepted_status(response: Response, result: Mapping[str, Any]) -> None:
    if str(result.get('status') or '') in {'accepted', 'accepted_existing'}:
        response.status_code = 202


def _accepted_command_response(
    thread_id: str,
    command_id: str,
    operation: str,
    *,
    status: str = 'accepted',
    existing_command_id: str = '',
) -> dict[str, Any]:
    return {
        'status': status,
        'accepted': status in {'accepted', 'accepted_existing'},
        'running': status in {'accepted', 'accepted_existing', 'conflict'},
        'thread_id': thread_id,
        'command_id': command_id,
        'existing_command_id': existing_command_id,
        'operation': operation,
        'run_id': RUN_ID,
        'events_url': f'/v1/evo/threads/{thread_id}/events',
        'message': (
            '已有长任务正在运行。' if status == 'conflict'
            else '命令已在后台运行。' if status == 'accepted_existing'
            else '命令已受理，正在后台运行。'
        ),
    }


def _flow_status_row(
    thread_id: str,
    status: str,
    active_task_ids: list[str],
    *,
    latest_abtest_status: str | None = None,
    report_ready: bool = False,
    pending_checkpoint: dict | None = None,
) -> dict:
    return {
        'thread_id': thread_id,
        'status': status,
        'active_task_ids': active_task_ids,
        'latest_abtest_id': 'abtest.comparison' if latest_abtest_status else None,
        'latest_abtest_status': latest_abtest_status,
        'report_ready': report_ready,
        'pending_checkpoint': pending_checkpoint,
    }


def _frontend_event_payload(event: Any, flow: EvoFlowRuntime | None = None) -> dict:
    payload = dict(event.payload or {})
    derived = _derive_frontend_event(event.event_type, payload)
    display_payload = _compact_event_payload(event.event_type, payload)
    step_run = _event_step_run_fields(event, flow)
    raw_event = {
        'event_type': event.event_type,
        'run_id': event.run_id,
        'payload_keys': sorted(str(key) for key in payload),
    }
    return {
        'seq': event.seq,
        'event_id': f'artifact:{event.seq}',
        'type': derived.get('type') or event.event_type,
        'stage': derived.get('stage'),
        'action': derived.get('action'),
        'event_type': event.event_type,
        'run_id': event.run_id,
        'payload': {**display_payload, **derived, **step_run, 'raw_event': raw_event},
        **step_run,
        **{key: value for key, value in derived.items() if value not in (None, '')},
    }


def _event_step_run_fields(event: Any, flow: EvoFlowRuntime | None = None) -> dict[str, str]:
    payload = dict(event.payload or {})
    step_run_id = str(payload.get('step_run_id') or '')
    if not step_run_id:
        return {}
    next_step_run_id = str(payload.get('next_step_run_id') or '')
    if not next_step_run_id and flow is not None:
        next_step_run_id = flow.step_store.next_step_run_id_for(str(event.run_id), step_run_id)
    return {'step_run_id': step_run_id, 'next_step_run_id': next_step_run_id}


def _compact_event_payload(event_type: str, payload: dict) -> dict:
    return {
        key: value
        for key, value in {
            'command_id': payload.get('command_id'),
            'attempt_id': payload.get('attempt_id') or _nested(payload, 'attempt', 'attempt_id'),
            'reason': payload.get('reason'),
            'event_type': event_type,
        }.items()
        if value not in (None, '')
    }


def _derive_frontend_event(event_type: str, payload: dict) -> dict:
    if event_type.startswith('run.'):
        return {
            'type': f'artifact.{event_type}',
            'stage': '',
            'action': '',
            'operation_run_id': '',
            'flow_kind': '',
            'case_id': '',
            'artifact_id': '',
            'writes_artifact_id': '',
            'runtime_artifact_id': '',
            'status': '',
        }
    attempt_id = str(payload.get('attempt_id') or _nested(payload, 'attempt', 'attempt_id') or '')
    stage, op_id, case_id = _attempt_parts(attempt_id)
    artifact_id = _first_output_artifact_id(payload.get('output_refs'))
    if not artifact_id:
        artifact_id = _first_artifact_key_id(_nested(payload, 'attempt', 'output_artifact_keys'))
    artifact_alias = RESULT_ARTIFACT_ALIASES.get(artifact_id, artifact_id)
    flow_kind = _frontend_flow_kind(op_id, artifact_id)
    action = _frontend_event_action(event_type)
    if event_type == 'plan.submitted':
        reason = _nested(payload, 'plan', 'reason')
        stage = str(reason).removeprefix('step:') if isinstance(reason, str) and reason.startswith('step:') else stage
        if not stage:
            stage = _stage_from_command_id(str(payload.get('command_id') or ''))
        action = 'start'
    if event_type == 'run.completed':
        stage = ''
        action = 'finish'
    return {
        'type': f'{stage}.{action}' if stage and action else event_type,
        'stage': stage,
        'action': action,
        'operation_run_id': op_id,
        'flow_kind': flow_kind,
        'case_id': case_id,
        'artifact_id': artifact_alias,
        'writes_artifact_id': artifact_alias,
        'runtime_artifact_id': artifact_id,
        'status': 'success' if action == 'finish' else 'running' if action in {'start', 'progress'} else action,
    }


def _frontend_event_action(event_type: str) -> str:
    if event_type == 'attempt.completed':
        return 'finish'
    if event_type == 'attempt.failed':
        return 'failed'
    if event_type == 'attempt.cancelled':
        return 'cancel'
    if event_type in {'attempt.created', 'attempt.claimed', 'attempt.heartbeat'}:
        return 'progress'
    if event_type == 'run.cancelled':
        return 'cancel'
    if event_type == 'run.failed':
        return 'failed'
    return ''


def _attempt_parts(attempt_id: str) -> tuple[str, str, str]:
    parts = attempt_id.split(':')
    op_id = parts[1] if len(parts) > 1 else ''
    case_id = ''
    if op_id.endswith(']') and '[' in op_id:
        op_id, case_id = op_id[:-1].split('[', 1)
    return _stage_from_op(op_id), op_id, case_id


def _stage_from_op(op_id: str) -> str:
    if op_id.startswith('candidate_eval.') or op_id.startswith('abtest.'):
        return 'abtest'
    if op_id.startswith('analysis.'):
        return 'analysis'
    if op_id.startswith('repair.'):
        return 'repair'
    if op_id.startswith('eval.'):
        return 'eval'
    if op_id.startswith('dataset.'):
        return 'dataset'
    return ''


def _frontend_flow_kind(op_id: str, artifact_id: str) -> str:
    if op_id == 'dataset.build_corpus_snapshot':
        return 'dataset.build_corpus_snapshot'
    if op_id == 'eval.summary' or artifact_id == 'eval.summary':
        return 'eval.aggregate'
    if op_id == 'analysis.classify_case':
        return 'analysis.fine_classify'
    if op_id == 'abtest.candidate_rag_answer':
        return 'abtest.candidate_rag_answer'
    if op_id == 'abtest.candidate_judge':
        return 'abtest.candidate_judge'
    if op_id == 'abtest.candidate_eval_summary' or artifact_id == 'abtest.candidate_eval_summary':
        return 'eval.aggregate'
    if op_id == 'abtest.candidate_service':
        return 'abtest.candidate_service.start'
    return op_id


def _first_output_artifact_id(value: Any) -> str:
    if isinstance(value, dict):
        items = value.get('items')
        if isinstance(items, list) and items:
            first = items[0]
            if isinstance(first, list) and first:
                return str(
                    _artifact_id_value(
                        first[0]) or _nested(
                        first[1] if len(first) > 1 else {},
                        'key',
                        'artifact_id') or '')
    if isinstance(value, Mapping) and value:
        first_key = next(iter(value))
        return _artifact_id_value(first_key)
    return ''


def _first_artifact_key_id(value: Any) -> str:
    items = _nested(value, 'items')
    if isinstance(items, list) and items:
        return _artifact_id_value(items[0])
    if isinstance(value, (list, tuple)) and value:
        return _artifact_id_value(value[0])
    return ''


def _artifact_id_value(value: Any) -> str:
    if isinstance(value, dict):
        return str(value.get('artifact_id') or '')
    return str(getattr(value, 'artifact_id', '') or '')


def _stage_from_command_id(command_id: str) -> str:
    parts = command_id.split(':')
    return parts[2] if len(parts) > 3 and parts[2] in STAGE_LABELS else ''


def _nested(value: Any, *keys: str) -> Any:
    current = value
    for key in keys:
        if isinstance(current, dict):
            current = current.get(key)
        else:
            current = getattr(current, key, None)
    return current


def _frontend_result_rows(kind: str, rows: list[dict]) -> list[dict]:
    if kind == 'analysis-reports':
        adapted = []
        for row in rows:
            adapted.extend(_analysis_result_rows(row))
        return adapted
    return [_frontend_artifact_row(row) for row in rows]


def _frontend_artifact_row(row: dict) -> dict:
    data = row.get('data') if isinstance(row.get('data'), dict) else {}
    artifact_id = str(row.get('artifact_id') or '')
    alias = RESULT_ARTIFACT_ALIASES.get(artifact_id, artifact_id)
    adapted_data = _frontend_data(artifact_id, data)
    return {
        **row,
        'artifact_id': alias,
        'runtime_artifact_id': artifact_id,
        'source_artifact_id': artifact_id,
        'data': adapted_data,
        **_frontend_top_level_fields(alias, adapted_data),
    }


def _analysis_result_rows(row: dict) -> list[dict]:
    base = _frontend_artifact_row(row)
    data = base.get('data') if isinstance(base.get('data'), dict) else {}
    repair_plan = _repair_loop_plan_data(data)
    return [
        {**base, 'artifact_id': 'classification_report', 'data': data,
            ** _frontend_top_level_fields('classification_report', data)},
        {
            **base,
            'artifact_id': 'repair_loop_plan',
            'runtime_artifact_id': 'analysis.summary',
            'source_artifact_id': 'analysis.summary',
            'data': repair_plan,
            **_frontend_top_level_fields('repair_loop_plan', repair_plan),
        },
    ]


def _frontend_data(artifact_id: str, data: dict) -> dict:
    if artifact_id in {'eval.summary', 'abtest.candidate_eval_summary'}:
        return _eval_report_data(data)
    if artifact_id == 'analysis.summary':
        return _classification_report_data(data)
    if artifact_id == 'repair.verified_patch':
        return _diff_data(data)
    if artifact_id == 'abtest.comparison':
        return _abtest_data(data)
    return data


def _frontend_top_level_fields(alias: str, data: dict) -> dict:
    if alias == 'eval_dataset':
        return {'total_nums': data.get('size') or data.get('total_nums'), 'cases': data.get('cases') or []}
    if alias in {'eval_report', 'candidate_eval_report'}:
        return {'metrics': data.get('metrics') or {}, 'total_cases': data.get('total') or data.get('case_count')}
    if alias == 'classification_report':
        return {'cases': data.get('cases') or [], 'summary': data.get('summary') or {}}
    if alias == 'repair_loop_plan':
        return {'target': data.get('target') or {}, 'priorities': data.get('priorities') or []}
    return {}


def _eval_report_data(data: dict) -> dict:
    metrics = dict(data.get('metrics') or {})
    rows = [dict(item) for item in data.get('rows') or [] if isinstance(item, dict)]
    case_details = _case_details_from_eval_rows(rows, data.get('case_details'))
    total_cases = data.get('total') or data.get('case_count') or data.get(
        'total_cases') or len(case_details) or len(rows)
    return data | {
        'case_details': case_details,
        'total_cases': total_cases,
        'case_details_summary': _case_details_summary(case_details, total_cases, metrics),
    }


def _classification_report_data(data: dict) -> dict:
    rows = [dict(item) for item in data.get('rows') or [] if isinstance(item, dict)]
    category_counts = dict(data.get('category_counts') or {})
    priorities = [
        {
            'rank': index + 1,
            'fine_category': category,
            'case_count': count,
            'priority_score': round(float(count or 0) / max(int(data.get('total') or 1), 1), 4),
        }
        for index, (category, count) in enumerate(
            sorted(category_counts.items(), key=lambda item: (-int(item[1] or 0), item[0])),
        )
    ]
    target = priorities[0] | {'badcase_ids': [row.get('case_id')
                                              for row in rows if row.get('case_id')]} if priorities else {}
    return data | {
        'bad_case_count': len(rows),
        'classified_case_count': len(rows),
        'cases': rows,
        'priorities': priorities,
        'target': target,
        'summary': {
            'fine_category_counts': category_counts,
            'coarse_category_counts': category_counts,
            'confidence_counts': dict(Counter(str(row.get('confidence') or 'medium') for row in rows)),
        },
    }


def _repair_loop_plan_data(analysis_data: dict) -> dict:
    report = _classification_report_data(analysis_data)
    return {
        'id': 'repair_loop_plan',
        'classification_report_ref': 'analysis.summary',
        'target': report.get('target') or {},
        'priorities': report.get('priorities') or [],
        'cases': report.get('cases') or [],
        'summary': report.get('summary') or {},
    }


def _diff_data(data: dict) -> dict:
    content = str(data.get('diff') or data.get('patch') or data.get('content') or '')
    return data | {'content': content, 'diff': str(data.get('diff') or content), 'files': data.get('files') or []}


def _abtest_data(data: dict) -> dict:
    raw_summary = dict(data.get('summary') or {})
    case_deltas = [dict(item) for item in data.get('case_deltas')
                   or raw_summary.get('case_deltas') or [] if isinstance(item, dict)]
    metrics = data.get('metrics') or raw_summary.get('metrics') or {}
    policy = data.get('policy') or raw_summary.get('policy') or {}
    decision = data.get('decision') or raw_summary.get('decision') or {}
    case_details = _case_details_from_abtest_deltas(case_deltas, data.get('case_details'))
    summary = raw_summary | {
        'metrics': metrics,
        'case_deltas': case_deltas,
        'goodcase_guard': data.get('goodcase_guard') or raw_summary.get('goodcase_guard') or {},
        'decision': decision,
        'policy': policy,
        'case_count': data.get('case_count') or raw_summary.get('case_count') or len(case_deltas),
        'reasons': data.get('reasons') or raw_summary.get('reasons') or decision.get('reasons') or [],
        'missing_metrics': data.get('missing_metrics') or raw_summary.get('missing_metrics') or [],
    }
    return data | {
        'abtest_id': data.get('id') or 'abtest_comparison',
        'case_details': case_details,
        'case_details_summary': _case_details_summary(case_details, summary['case_count']),
        'summary': summary,
    }


def _case_details_summary(
        case_details: list[dict],
        total_cases: Any | None = None,
        fallback_metrics: dict | None = None) -> dict:
    buckets: dict[str, dict[str, Any]] = {}
    for row in case_details:
        category = str(row.get('question_type') or row.get('category') or '总体')
        bucket = buckets.setdefault(category, {'count': 0, 'totals': {}})
        bucket['count'] += 1
        for key in (
            'answer_score',
            'answer_correctness',
            'answer_relevance',
            'completeness',
            'format_compliance',
            'chunk_recall',
            'chunk_precision',
            'doc_recall',
            'doc_precision',
            'retrieval_score',
        ):
            bucket['totals'][key] = float(bucket['totals'].get(key, 0.0)) + float(row.get(key) or 0.0)
    if not buckets:
        buckets['总体'] = {
            'count': int(
                total_cases or 0), 'totals': {
                key.removesuffix('_avg'): float(
                    value or 0.0) for key, value in (
                    fallback_metrics or {}).items() if key.endswith('_avg')}, 'already_average': True, }
    return {
        'total_count': total_cases or len(case_details),
        'question_types': [
            {
                'question_type_key': category,
                'question_type_name': category,
                'count': int(bucket['count'] or 0),
                'averages': {
                    key: round(float(value or 0.0) if bucket.get('already_average')
                               else float(value or 0.0) / max(int(bucket['count'] or 0), 1), 4)
                    for key, value in bucket['totals'].items()
                },
            }
            for category, bucket in buckets.items()
        ],
    }


def _case_details_from_eval_rows(rows: list[dict], existing: Any) -> list[dict]:
    if isinstance(existing, list):
        return [dict(item) for item in existing if isinstance(item, dict)]
    return [
        {
            'case_id': row.get('case_id') or row.get('id'),
            'question_type': row.get('question_type') or row.get('category') or '总体',
            'answer_score': row.get('answer_score'),
            'answer_correctness': row.get('answer_correctness'),
            'answer_relevance': row.get('answer_relevance'),
            'completeness': row.get('completeness'),
            'format_compliance': row.get('format_compliance'),
            'chunk_recall': row.get('chunk_recall'),
            'chunk_precision': row.get('chunk_precision'),
            'doc_recall': row.get('doc_recall'),
            'doc_precision': row.get('doc_precision'),
            'retrieval_score': row.get('retrieval_score'),
            'retrieval_failure_type': row.get('retrieval_failure_type'),
            'is_correct': row.get('is_correct'),
            'reason': row.get('reason'),
            'trace_id': row.get('trace_id'),
            'quality': row.get('quality_label') or row.get('quality'),
            'failure_type': row.get('failure_type'),
        }
        | {
            key: row[key]
            for key in (
                'question',
                'key_points',
                'ground_truth',
                'rag_answer',
                'retrieve_contexts',
                'retrieve_doc',
                'reference_chunk_ids',
                'reference_doc_ids',
                'retrieve_chunk_ids',
                'retrieve_doc_ids',
                'rag_trace',
                'rag_response',
            )
            if key in row
        }
        for row in rows
    ]


def _case_details_from_abtest_deltas(case_deltas: list[dict], existing: Any) -> list[dict]:
    trace_fields = ('trace_id', 'baseline_trace_id', 'candidate_trace_id')

    def case_id(row: dict) -> str:
        return str(row.get('case_id') or row.get('case_key') or row.get('caseId') or row.get('id') or '')

    if isinstance(existing, list):
        traces_by_case = {
            case_id(row): {
                key: row[key] for key in trace_fields if row.get(key)
            }
            for row in case_deltas
            if case_id(row) and any(key in row for key in trace_fields)
        }
        return [
            item | {key: value for key, value in traces_by_case.get(case_id(item), {}).items() if not item.get(key)}
            for item in existing if isinstance(item, dict)
        ]
    return [
        {
            'case_id': case_id(row),
            'outcome': row.get('outcome'),
            'trace_id': row.get('trace_id') or '',
            'baseline_trace_id': row.get('baseline_trace_id') or '',
            'candidate_trace_id': row.get('candidate_trace_id') or '',
            **{f'baseline_{metric}': value for metric, value in _dict_items(row.get('before'))},
            **{f'candidate_{metric}': value for metric, value in _dict_items(row.get('after'))},
            **{metric: value for metric, value in _dict_items(row.get('delta'))},
        }
        for row in case_deltas
    ]


def _dict_items(value: Any):
    return value.items() if isinstance(value, dict) else ()


def _artifact_flow_http_status(state: FlowStepState | None, run_status: str = '') -> str:
    if run_status == 'failed':
        return 'failed'
    if run_status == 'cancelled':
        return 'cancelled'
    if run_status in {'running', 'cancel_requested'}:
        return 'running'
    if state is None:
        return 'idle'
    if state.gate_status == 'completed':
        return 'ended'
    if state.gate_status == 'cancelled':
        return 'cancelled'
    if state.gate_status in {'paused', 'stale'}:
        return 'waiting_checkpoint'
    return 'running'


def _message_background_status(status: str) -> str:
    if status == 'completed':
        return 'ended'
    if status in {'cancelled', 'failed', 'running'}:
        return status
    if status in {'paused', 'stale'}:
        return 'waiting_checkpoint'
    return status or 'running'


def _artifact_checkpoint_payload(state: FlowStepState | None) -> dict | None:
    if state is None or state.gate_status not in {'paused', 'stale'}:
        return None
    return {
        'checkpoint_id': f'artifact_gate:{state.current_step}',
        'checkpoint_kind': 'stage_gate',
        'stage': state.current_step,
        'next_stage': state.next_step or '',
        'message': f'{STAGE_LABELS.get(str(state.current_step), state.current_step)}已完成，请确认是否继续执行下一步。',
        'gate_artifact_ref': '' if state.gate_artifact_ref is None else str(state.gate_artifact_ref),
    }


def _llm_config_payload(payload: dict[str, Any]) -> dict[str, Any]:
    value = payload.get('llm_config') or {}
    return dict(value) if isinstance(value, dict) else {}


def _abtest_status(flow: EvoFlowRuntime) -> str | None:
    ref = flow.runtime.stores.artifact_store.latest(ArtifactKey.of('abtest.comparison'))
    if ref is None:
        return None
    record = flow.runtime.stores.artifact_store.get(ref)
    if record is None or not isinstance(record.value.payload, dict):
        return 'completed'
    return str(record.value.payload.get('status') or 'completed')


def _artifact_selector(value: str) -> tuple[ArtifactKey, int | None]:
    text = value.strip()
    if not text:
        raise HTTPException(400, 'artifact id required')
    version = None
    if '@v' in text:
        text, raw_version = text.rsplit('@v', 1)
        try:
            version = int(raw_version)
        except ValueError as exc:
            raise HTTPException(400, f'bad artifact version: {value}') from exc
    partition = ''
    if text.endswith(']') and '[' in text:
        text, partition = text[:-1].split('[', 1)
    return ArtifactKey(text, partition), version


def _scoped_report_id(value: str) -> tuple[str, str]:
    text = str(value or '').strip()
    if ':' not in text:
        raise HTTPException(400, 'global report content requires scoped id: {thread_id}:{artifact_ref}')
    thread_id, artifact_id = (part.strip() for part in text.split(':', 1))
    if not thread_id or not artifact_id:
        raise HTTPException(400, 'global report content requires scoped id: {thread_id}:{artifact_ref}')
    return thread_id, artifact_id


def _normalize_inputs(inputs: dict[str, Any]) -> dict[str, Any]:
    normalized = dict(inputs)
    dataset_id = _dataset_id(normalized)
    normalized['kb_id'] = normalized['dataset_id'] = dataset_id
    if 'dataset_name' in normalized:
        normalized['dataset_name'] = dataset_id
    normalized['target_chat_url'] = _chat_url(normalized.get('target_chat_url'))
    normalized['candidate_chat_url'] = _optional_chat_url(normalized.get('candidate_chat_url'))
    if normalized['candidate_chat_url'] and normalized['candidate_chat_url'] == normalized['target_chat_url']:
        raise ValueError('candidate_chat_url must differ from target_chat_url')
    normalized['router_admin_url'] = _admin_url(normalized.get('router_admin_url'))
    normalized['num_cases'] = _bounded_positive_int(_case_count_value(normalized), 'num_cases', MAX_CREATE_THREAD_CASES)
    normalized.pop('case_count', None)
    max_workers = inputs['max_workers'] if 'max_workers' in inputs else os.getenv('EVO_FLOW_WORKERS', '2')
    normalized['max_workers'] = _bounded_positive_int(max_workers, 'max_workers', MAX_CREATE_THREAD_WORKERS)
    return normalized


def _dataset_id(inputs: dict[str, Any]) -> str:
    ids = {str(inputs.get(key) or '').strip() for key in ('kb_id', 'dataset_id') if str(inputs.get(key) or '').strip()}
    if len(ids) > 1:
        raise ValueError('dataset id aliases must match')
    if ids:
        return validate_id(ids.pop(), 'dataset_id')
    legacy = str(inputs.get('dataset_name') or '').strip()
    return validate_id(legacy, 'dataset_id') if legacy else 'algo'


def _chat_url(value: Any) -> str:
    url = str(value or os.getenv('LAZYMIND_EVO_TARGET_CHAT_URL') or 'http://chat:8046/api/chat/stream').strip()
    return normalize_chat_stream_url(url.replace('http://evo-chat:', 'http://chat:'), 'target_chat_url')


def _optional_chat_url(value: Any) -> str:
    url = str(value or '').strip()
    return normalize_chat_stream_url(url, 'candidate_chat_url') if url else ''


def _admin_url(value: Any) -> str:
    url = str(value or os.getenv('LAZYMIND_EVO_ROUTER_ADMIN_URL') or '').strip()
    return normalize_http_origin(url, 'router_admin_url') if url else ''


def _case_count_value(inputs: dict[str, Any]) -> Any:
    values = [inputs[key] for key in ('num_cases', 'case_count') if key in inputs]
    if len(values) == 2 and str(values[0]) != str(values[1]):
        raise ValueError('num_cases and case_count must match')
    return values[0] if values else os.getenv('EVO_FLOW_CASE_COUNT', '20')


def _bounded_positive_int(value: Any, field: str, maximum: int) -> int:
    try:
        out = int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f'{field} must be a positive integer') from exc
    if out < 1:
        raise ValueError(f'{field} must be a positive integer')
    if out > maximum:
        raise ValueError(f'{field} must be <= {maximum}')
    return out


def _read_json(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError):
        return {}


def _write_json(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(f'.{os.getpid()}.{time.time_ns()}.tmp')
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True, default=str), encoding='utf-8')
    tmp.replace(path)
