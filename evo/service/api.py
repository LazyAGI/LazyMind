from __future__ import annotations

import asyncio
import json
import os
import re
import shutil
import threading
import time
import uuid
from pathlib import Path
from typing import Any

from fastapi import Body, FastAPI, HTTPException, Request, Response
from sse_starlette.sse import EventSourceResponse

from evo import normalize_chat_stream_url, normalize_chat_target_url, normalize_http_origin, validate_id
from evo.artifacts import ArtifactRef
from evo.internal_ids import stage_group
from evo.projections import rebuild_frontend_state
from evo.service.flow import EvoFlowService, FlowMessageResult, result_dict
from evo.store import Event, StoreRunLifecycle

BODY_REQUIRED = Body(...)
BODY_DEFAULT = Body(default_factory=dict)
RUN_ID = 'run_1'
MAX_CASES = 1000
MAX_WORKERS = 32
TERMINAL = {'ended', 'failed', 'cancelled'}
BACKGROUND_DISPATCH_ACTIONS = {'resume_checkpointed', 'retry_thread', 'retry_stage', 'retry_operation',
                               'ensure_stage', 'restart_from_stage'}
RESULTS = {
    'datasets': ('eval_dataset',),
    'eval-reports': ('eval_report', 'candidate_eval_report'),
    'analysis-reports': ('classification_report', 'repair_loop_plan'),
    'abtests': ('abtest_comparison', 'candidate_algorithm_cutover'),
}


def create_app() -> FastAPI:
    hub = EvoMessageHub(Path(os.getenv('LAZYMIND_EVO_BASE_DIR') or '/var/lib/lazymind/evo'))
    app = FastAPI(title='evo flow service', version='refactor')
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
    async def create_thread(request: Request, body: dict = BODY_REQUIRED) -> dict:
        return await asyncio.to_thread(hub.create_thread, body, _user_context(request))

    @app.get('/v1/evo/threads')
    def list_threads() -> list[dict]:
        return hub.list_threads()

    @app.get('/v1/evo/threads/statuses')
    def statuses() -> dict:
        rows = [hub.flow_status(row['id']) | {'title': row.get('title', '')} for row in hub.list_threads()]
        counts: dict[str, int] = {}
        for row in rows:
            counts[row['status']] = counts.get(row['status'], 0) + 1
        return {'total': len(rows), 'counts': counts, 'threads': rows}

    @app.get('/v1/evo/threads/{thread_id}')
    def get_thread(thread_id: str) -> dict:
        return hub.get_thread(thread_id)

    @app.delete('/v1/evo/threads/{thread_id}')
    def delete_thread(thread_id: str) -> dict:
        return hub.delete_thread(thread_id)

    @app.get('/v1/evo/threads/{thread_id}/history')
    def history(thread_id: str) -> dict:
        return hub.history(thread_id)

    @app.get('/v1/evo/threads/{thread_id}/flow-status')
    def flow_status(thread_id: str) -> dict:
        return hub.flow_status(thread_id)

    @app.post('/v1/evo/threads/{thread_id}:messages')
    @app.post('/v1/evo/threads/{thread_id}/messages')
    async def post_message(thread_id: str, request: Request, body: dict = BODY_REQUIRED):
        if 'text/event-stream' in request.headers.get('accept', ''):
            return EventSourceResponse(hub.post_message_stream(thread_id, body))
        return await asyncio.to_thread(hub.post_message, thread_id, body)

    @app.post('/v1/evo/threads/{thread_id}/start')
    async def start(thread_id: str, body: dict = BODY_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.start, thread_id, body)

    @app.post('/v1/evo/threads/{thread_id}/pause')
    async def pause(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.pause, thread_id)

    @app.post('/v1/evo/threads/{thread_id}/cancel')
    async def cancel(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.cancel, thread_id)

    @app.post('/v1/evo/threads/{thread_id}/retry')
    @app.post('/v1/evo/threads/{thread_id}/continue')
    async def continue_thread(thread_id: str, body: dict = BODY_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.continue_thread, thread_id, body)

    @app.post('/v1/evo/threads/{thread_id}/auto/step')
    async def auto_step(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.start, thread_id, {'force_auto': True})

    @app.post('/v1/evo/threads/{thread_id}/auto/start')
    async def auto_start(thread_id: str, request: Request, body: dict = BODY_DEFAULT):
        if 'text/event-stream' in request.headers.get('accept', ''):
            payload = {'thread_id': thread_id, **hub.start(thread_id, body)}
            return EventSourceResponse(_single_sse('auto_start', payload))
        return await asyncio.to_thread(hub.start, thread_id, body)

    @app.post('/v1/evo/threads/{thread_id}/auto/stop')
    def auto_stop(thread_id: str) -> dict:
        return hub.pause(thread_id)

    @app.get('/v1/evo/threads/{thread_id}:events')
    @app.get('/v1/evo/threads/{thread_id}/events')
    def events(thread_id: str, request: Request, since: int = 0) -> EventSourceResponse:
        last = request.headers.get('last-event-id') or ''
        return EventSourceResponse(hub.events(thread_id, int(last) if last.isdigit() else since))

    @app.get('/v1/evo/threads/{thread_id}/results/{kind}')
    def results(thread_id: str, kind: str) -> list[dict]:
        return hub.results(thread_id, kind)

    @app.get('/v1/evo/threads/{thread_id}/artifacts/{artifact_id}')
    def artifact(thread_id: str, artifact_id: str) -> dict:
        return hub.artifact(thread_id, artifact_id)

    @app.get('/v1/evo/threads/{thread_id}/reports/{report_id}/content')
    def thread_report_content(thread_id: str, report_id: str, fmt: str = ''):
        content = hub.report_content(thread_id, report_id)
        if fmt in {'md', 'markdown', 'text'}:
            return Response(content, media_type='text/markdown; charset=utf-8')
        return {'thread_id': thread_id, 'report_id': report_id, 'content': content}

    @app.get('/v1/evo/reports/{report_id}/content')
    def report_content(report_id: str, fmt: str = ''):
        thread_id, artifact = _scoped_report_id(report_id)
        content = hub.report_content(thread_id, artifact)
        if fmt in {'md', 'markdown', 'text'}:
            return Response(content, media_type='text/markdown; charset=utf-8')
        return {'thread_id': thread_id, 'report_id': artifact, 'content': content}

    @app.get('/v1/evo/diffs/{apply_id}/{filename:path}')
    def diff_content(apply_id: str, filename: str) -> Response:
        return Response(hub.diff_content(apply_id, filename), media_type='text/x-diff; charset=utf-8')

    return app


def get_app() -> FastAPI:
    return create_app()


class ThreadDispatchGate:
    def __init__(self, hub: 'EvoMessageHub', thread_id: str):
        self.hub, self.thread_id = hub, thread_id

    def can_dispatch(self, run_id: str) -> bool:
        del run_id
        try:
            status = str(self.hub._meta(self.thread_id).get('status') or '')
        except HTTPException:
            return False
        if status in {'cancelled', 'deleting'}:
            return False
        return status != 'paused' or self.hub._message_dispatch_active(self.thread_id)


class EvoMessageHub:
    def __init__(self, base_dir: Path):
        self.base_dir = base_dir
        self.threads_dir = base_dir / 'state' / 'threads'
        self._services: dict[str, EvoFlowService] = {}
        self._tasks: dict[str, threading.Thread] = {}
        self._message_locks: dict[str, threading.RLock] = {}
        self._message_conditions: dict[str, threading.Condition] = {}
        self._active_messages: set[str] = set()
        self._pending_messages: dict[str, list[str]] = {}
        self._message_results: dict[str, dict] = {}
        self._lock = threading.RLock()

    def create_thread(self, payload: dict[str, Any], user_context: dict[str, str] | None = None) -> dict:
        mode = str(payload.get('mode') or 'interactive').strip()
        if mode not in {'auto', 'interactive'}:
            raise HTTPException(400, f'bad mode {mode!r}')
        thread_id, now = f'thr-{uuid.uuid4().hex[:8]}', time.time()
        try:
            inputs = _normalize_inputs(dict(payload.get('inputs') or {}))
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        meta = {'id': thread_id, 'thread_id': thread_id, 'mode': mode, 'title': str(payload.get('title') or ''),
                'inputs': inputs, 'model_config': payload.get('llm_config') or {}, 'status': 'idle',
                'user_context': user_context or {}, 'created_at': now, 'updated_at': now}
        self._write_meta(thread_id, meta)
        if mode == 'auto' and payload.get('start_auto'):
            self.start(thread_id, payload)
        return _redact(meta)

    def list_threads(self) -> list[dict]:
        rows = [_read_json(path) for path in self.threads_dir.glob('*/thread.json')]
        return sorted([_redact(row) for row in rows if row], key=lambda row: row.get('updated_at') or 0, reverse=True)

    def get_thread(self, thread_id: str) -> dict:
        return _redact(self._meta(thread_id))

    def delete_thread(self, thread_id: str) -> dict:
        service = self._services.pop(thread_id, None)
        if self._task_alive(thread_id):
            self.cancel(thread_id)
            self._tasks[thread_id].join(timeout=5)
        run_root, thread_dir = self.base_dir / 'dev-runs' / thread_id, self._thread_dir(thread_id)
        run_deleted = service.delete() if service else (
            EvoFlowService.delete_run(run_root=run_root, run_id=RUN_ID) if run_root.exists() else False)
        shutil.rmtree(thread_dir, ignore_errors=True)
        return {'thread_id': thread_id, 'deleted_run': run_deleted, 'deleted_thread': thread_dir.exists()}

    def history(self, thread_id: str) -> dict:
        return {'thread_id': thread_id, 'messages': _read_messages(self._thread_dir(thread_id) / 'messages.jsonl')}

    def start(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        del payload
        self._meta(thread_id)
        with self._lock:
            if not self._task_alive(thread_id):
                self._update_meta(thread_id, status='running', updated_at=time.time())
                self._start_flow_task_locked(thread_id, self._resume_start_stage(thread_id))
        return {'status': 'running', 'thread_id': thread_id, 'task_id': thread_id}

    def pause(self, thread_id: str) -> dict:
        service = self._service(thread_id)
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        StoreRunLifecycle(service.store, RUN_ID).mark_paused(thread_id=thread_id, reason='user_intervention')
        self._update_meta(thread_id, status='paused', updated_at=time.time())
        return {'status': 'paused', 'thread_id': thread_id, 'paused': True}

    def cancel(self, thread_id: str) -> dict:
        service = self._service(thread_id)
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        service.checkpoints.cancel_active(RUN_ID, thread_id=thread_id)
        StoreRunLifecycle(service.store, RUN_ID).mark_cancelled(thread_id=thread_id)
        self._update_meta(thread_id, status='cancelled', pending_checkpoint=None, updated_at=time.time())
        return {'status': 'cancelled', 'thread_id': thread_id}

    def continue_thread(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        del payload
        if self._task_alive(thread_id):
            return {'status': 'running', 'thread_id': thread_id, 'resumed': False}
        if self._meta(thread_id).get('status') not in {'paused', 'failed'}:
            raise HTTPException(409, 'thread has no paused work to continue')
        self._update_meta(thread_id, status='running', pending_checkpoint=None, updated_at=time.time())
        self._start_flow_task_locked(thread_id, self._resume_start_stage(thread_id))
        return {'status': 'running', 'thread_id': thread_id, 'resumed': True}

    def post_message(self, thread_id: str, payload: dict[str, Any]) -> dict:
        content = str(payload.get('content') or payload.get('message') or '').strip()
        if not content:
            raise HTTPException(400, 'message content required')
        message_id = str(payload.get('message_id') or f'msg_{thread_id}_{uuid.uuid4().hex[:8]}')
        response = None
        condition = self._message_condition(thread_id)
        with condition:
            self._append_message(thread_id, 'user', content)
            if thread_id in self._active_messages:
                self._pending_messages.setdefault(thread_id, []).append(content)
                self._pending_message_event(thread_id, message_id, content)
                while thread_id in self._active_messages:
                    condition.wait()
                result = self._message_results.get(thread_id)
                if result:
                    return _with_message_id(result, message_id)
                raise HTTPException(500, 'message processing finished without result')
            self._active_messages.add(thread_id)
            self._message_results.pop(thread_id, None)
        try:
            if self._task_alive(thread_id):
                self._pause_running_task_for_message(thread_id, message_id)
            service = self._service(thread_id)
            loop_id, loop_content, result = message_id, content, None
            while True:
                while loop_content:
                    send = service.send_checkpoint_message if service.checkpoints.active_checkpoint(RUN_ID) \
                        else service.send_message
                    result = send(loop_id, loop_content, allowed_capabilities=payload.get('allowed_capabilities'),
                                  dispatch=bool(payload.get('dispatch', True)),
                                  max_dispatch=int(payload.get('max_dispatch') or 1),
                                  pending_reader=lambda: self._drain_pending_message(thread_id))
                    loop_content = self._drain_pending_message(thread_id)
                    loop_id = f'{message_id}_pending_{uuid.uuid4().hex[:6]}'
                if result is None:
                    raise HTTPException(500, 'message processing produced no result')
                with condition:
                    late = self._pending_messages.pop(thread_id, [])
                    if late:
                        loop_content = '\n'.join(item for item in late if item.strip())
                        continue
                    response = self._message_response(thread_id, message_id, _intent_reply(service, result), result)
                    self._message_results[thread_id] = response
                    self._active_messages.discard(thread_id)
                    condition.notify_all()
                    self._start_background_dispatch_if_needed(thread_id, message_id, result)
                    return response
        finally:
            with condition:
                if response:
                    self._message_results[thread_id] = response
                else:
                    self._message_results.pop(thread_id, None)
                    self._pending_messages.pop(thread_id, None)
                if thread_id in self._active_messages:
                    self._active_messages.discard(thread_id)
                    condition.notify_all()

    async def post_message_stream(self, thread_id: str, payload: dict[str, Any]):
        message_id = str(payload.get('message_id') or f'msg_{thread_id}_{uuid.uuid4().hex[:8]}')
        yield _sse('intent_start', {'thread_id': thread_id, 'message_id': message_id})
        try:
            result = await asyncio.to_thread(self.post_message, thread_id, {**payload, 'message_id': message_id})
            yield _sse('answer_delta', {'thread_id': thread_id, 'message_id': message_id, 'delta': result['reply']})
            yield _sse('done', {'thread_id': thread_id, 'message_id': message_id,
                                'requires_confirm': result.get('requires_confirm', False)})
        except Exception as exc:
            yield _sse('error', {'thread_id': thread_id, 'message_id': message_id, 'message': str(exc)})

    def flow_status(self, thread_id: str) -> dict:
        meta, task = self._meta(thread_id), self._tasks.get(thread_id)
        active = [thread_id] if task and task.is_alive() else []
        if not self._has_run(thread_id):
            return _flow_status_row(thread_id, 'running' if active else str(meta.get('status') or 'idle'), active)
        run_dir = self._run_dir(thread_id)
        projection = _read_json(run_dir / 'projections' / 'current.json')
        if thread_id in self._services:
            projection = projection or rebuild_frontend_state(self._services[thread_id].store, RUN_ID)
        return _status_from_projection(thread_id, run_dir, projection, str(meta.get('status') or 'idle'), active)

    async def events(self, thread_id: str, since: int = 0):
        self._meta(thread_id)
        cursor = max(0, since)
        while True:
            run_dir = self._run_dir(thread_id)
            projector = FrontendEventProjector(run_dir)
            events = self._service(thread_id).store.read_events(RUN_ID) if thread_id in self._services \
                else _stored_events(run_dir)
            for seq, event in _event_rows(events):
                if seq <= cursor:
                    continue
                cursor = seq
                frame = projector.frame(event, seq)
                if frame:
                    yield frame
            status = self.flow_status(thread_id)['status']
            if status in TERMINAL:
                yield _sse('done', {'thread_id': thread_id, 'status': status}, str(cursor + 1))
                return
            if status in {'idle', 'paused', 'waiting_checkpoint'}:
                return
            await asyncio.sleep(0.5)

    def results(self, thread_id: str, kind: str) -> list[dict]:
        rows = [_stored_artifact_row(self._run_dir(thread_id), artifact_id)
                for artifact_id in RESULTS.get(kind, ())]
        if not rows and kind not in RESULTS:
            raise HTTPException(404, f'unknown result kind: {kind}')
        return [row for row in rows if row]

    def artifact(self, thread_id: str, artifact_id: str) -> dict:
        row = _stored_artifact_row(self._run_dir(thread_id), artifact_id)
        if not row:
            raise HTTPException(404, f'artifact not found: {artifact_id}')
        return row

    def report_content(self, thread_id: str, report_id: str) -> str:
        data = _stored_artifact_payload(self._run_dir(thread_id), report_id)
        if not isinstance(data, dict):
            return str(data)
        for key in ('markdown', 'report', 'content', 'text', 'summary'):
            value = data.get(key)
            if isinstance(value, str) and value.strip():
                return value
        return json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True, default=str)

    def diff_content(self, apply_id: str, filename: str) -> str:
        del filename
        for meta in self.list_threads():
            try:
                data = _stored_artifact_payload(self._run_dir(str(meta['id'])), apply_id)
            except Exception:
                continue
            diff = data.get('diff') or data.get('patch') or data.get('content') if isinstance(data, dict) else data
            if isinstance(diff, str) and diff.strip():
                return diff
        raise HTTPException(404, f'diff content not found: {apply_id}')

    def _run_full_flow(self, thread_id: str, start_stage: str = 'dataset') -> None:
        self._update_meta(thread_id, status='running', updated_at=time.time())
        service = self._service(thread_id)
        lifecycle = StoreRunLifecycle(service.store, RUN_ID)
        try:
            service.run_full_flow(start_stage=start_stage)
            lifecycle.mark_ended(outcome='success')
            self._update_meta(thread_id, status='ended', pending_checkpoint=None, error=None,
                              updated_at=time.time())
        except Exception as exc:
            current_status = str(self._meta(thread_id).get('status') or '')
            if current_status == 'cancelled':
                lifecycle.mark_cancelled(error_type=exc.__class__.__name__, message=str(exc))
                self._update_meta(thread_id, status='cancelled',
                                  error={'type': exc.__class__.__name__, 'message': str(exc)}, updated_at=time.time())
                return
            if current_status == 'paused':
                lifecycle.mark_paused(thread_id=thread_id, reason='user_intervention')
                self._update_meta(thread_id, status='paused', error=None, updated_at=time.time())
                return
            lifecycle.mark_failed(error_type=exc.__class__.__name__, message=str(exc))
            self._update_meta(thread_id, status='failed',
                              error={'type': exc.__class__.__name__, 'message': str(exc)}, updated_at=time.time())

    def _run_background_dispatch(self, thread_id: str, message_id: str) -> None:
        self._update_meta(thread_id, status='running', pending_checkpoint=None, updated_at=time.time())
        service = self._service(thread_id)
        lifecycle = StoreRunLifecycle(service.store, RUN_ID)
        try:
            service.recover_stale_running()
            service.emit_ready_stage_progress(message_id)
            service.dispatch(message_id=f'{message_id}_background', max_dispatch=None)
            run = _read_json(service.store.run_dir(RUN_ID) / 'run.json')
            status = str(run.get('status') or 'running')
            if status in TERMINAL:
                self._update_meta(thread_id, status=status, pending_checkpoint=None, error=None,
                                  updated_at=time.time())
        except Exception as exc:
            current_status = str(self._meta(thread_id).get('status') or '')
            if current_status in {'paused', 'cancelled'}:
                return
            lifecycle.mark_failed(error_type=exc.__class__.__name__, message=str(exc))
            self._update_meta(thread_id, status='failed',
                              error={'type': exc.__class__.__name__, 'message': str(exc)}, updated_at=time.time())

    def _start_flow_task_locked(self, thread_id: str, start_stage: str = 'dataset') -> None:
        task = threading.Thread(target=self._run_full_flow, args=(thread_id, start_stage), daemon=True)
        self._tasks[thread_id] = task
        task.start()

    def _start_background_dispatch_if_needed(self, thread_id: str, message_id: str,
                                             result: FlowMessageResult) -> None:
        if result.action not in BACKGROUND_DISPATCH_ACTIONS or result.requires_confirmation:
            return
        with self._lock:
            if self._task_alive(thread_id):
                return
            task = threading.Thread(target=self._run_background_dispatch, args=(thread_id, message_id), daemon=True)
            self._tasks[thread_id] = task
            task.start()

    def _resume_start_stage(self, thread_id: str) -> str:
        if not self._has_run(thread_id):
            return 'dataset'
        try:
            self._service(thread_id).artifacts.latest_ref('eval_dataset')
            return 'eval'
        except KeyError:
            return 'dataset'

    def _task_alive(self, thread_id: str) -> bool:
        task = self._tasks.get(thread_id)
        return bool(task and task.is_alive())

    def _message_lock(self, thread_id: str) -> threading.RLock:
        with self._lock:
            lock = self._message_locks.get(thread_id)
            if lock is None:
                lock = threading.RLock()
                self._message_locks[thread_id] = lock
            return lock

    def _message_condition(self, thread_id: str) -> threading.Condition:
        with self._lock:
            condition = self._message_conditions.get(thread_id)
            if condition is None:
                condition = threading.Condition(self._message_lock(thread_id))
                self._message_conditions[thread_id] = condition
            return condition

    def _message_dispatch_active(self, thread_id: str) -> bool:
        with self._lock:
            return thread_id in self._active_messages

    def _drain_pending_message(self, thread_id: str) -> str:
        condition = self._message_condition(thread_id)
        with condition:
            items = self._pending_messages.pop(thread_id, [])
        return '\n'.join(item for item in items if item.strip())

    def _pending_message_event(self, thread_id: str, message_id: str, content: str) -> None:
        self._service(thread_id).store.append_event(Event('intent.message_loop.pending_merged', RUN_ID, {
            'thread_id': thread_id, 'message_id': message_id, 'message_preview': content[:200],
            'timestamp': time.time(),
        }))

    def _pause_running_task_for_message(self, thread_id: str, message_id: str) -> None:
        service = self._service(thread_id)
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        StoreRunLifecycle(service.store, RUN_ID).mark_paused(
            thread_id=thread_id, reason='message_intervention', message_id=message_id,
        )
        self._update_meta(thread_id, status='paused', updated_at=time.time())
        task = self._tasks.get(thread_id)
        if task and task.is_alive():
            task.join(timeout=float(os.getenv('EVO_MESSAGE_INTERRUPT_TIMEOUT', '120')))
        if task and task.is_alive():
            raise HTTPException(409, 'flow is still pausing; retry message shortly')

    def _has_run(self, thread_id: str) -> bool:
        return self._run_dir(thread_id).exists()

    def _service(self, thread_id: str) -> EvoFlowService:
        with self._lock:
            if thread_id in self._services:
                return self._services[thread_id]
            run_root = self.base_dir / 'dev-runs' / thread_id
            kwargs = self._service_kwargs(thread_id, run_root)
            service = EvoFlowService.resume(**kwargs) if (run_root / 'store' / 'runs' / RUN_ID).exists() \
                else EvoFlowService(**kwargs)
            self._services[thread_id] = service
            return service

    def _service_kwargs(self, thread_id: str, run_root: Path) -> dict[str, Any]:
        meta = self._meta(thread_id)
        inputs = _normalize_inputs(dict(meta.get('inputs') or {}))
        if inputs != meta.get('inputs'):
            self._update_meta(thread_id, inputs=inputs, updated_at=time.time())
        return {'run_root': run_root, 'run_id': RUN_ID, 'thread_id': thread_id, 'dataset_id': _dataset_id(inputs),
                'target_chat_url': str(inputs['target_chat_url']),
                'candidate_chat_url': str(inputs.get('candidate_chat_url') or ''),
                'router_admin_url': str(inputs.get('router_admin_url') or ''),
                'case_count': int(inputs.get('num_cases') or os.getenv('EVO_FLOW_CASE_COUNT', '20')),
                'max_workers': int(inputs.get('max_workers') or os.getenv('EVO_FLOW_WORKERS', '2')),
                'model_config': meta.get('model_config') or None,
                'user_context': meta.get('user_context') or {},
                'dispatch_gate': ThreadDispatchGate(self, thread_id)}

    def _message_response(self, thread_id: str, message_id: str, reply: str, result: FlowMessageResult) -> dict:
        self._append_message(thread_id, 'assistant', reply)
        status = self.flow_status(thread_id)['status']
        patch = {'status': status, 'updated_at': time.time()}
        if status not in {'failed', 'cancelled'}:
            patch['error'] = None
        self._update_meta(thread_id, **patch)
        return {'intent_id': message_id, 'reply': reply, 'thinking': '',
                'requires_confirm': result.requires_confirmation,
                'confirmation_checkpoint_id': result.confirmation_checkpoint_id,
                'preview': _preview(result), 'warnings': [], 'result': result_dict(result)}

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

    def _append_message(self, thread_id: str, role: str, content: str) -> None:
        path = self._thread_dir(thread_id) / 'messages.jsonl'
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('a', encoding='utf-8') as handle:
            handle.write(json.dumps({'role': role, 'content': content, 'ts': time.time()}, ensure_ascii=False) + '\n')

    def _thread_dir(self, thread_id: str) -> Path:
        return self.threads_dir / thread_id

    def _run_dir(self, thread_id: str) -> Path:
        return self.base_dir / 'dev-runs' / thread_id / 'store' / 'runs' / RUN_ID


def _single_sse(event: str, payload: dict[str, Any]):
    async def gen():
        yield _sse(event, payload)
    return gen()


def _sse(event: str, payload: dict[str, Any], event_id: str | None = None) -> dict:
    row = {'event': event, 'data': json.dumps({'type': event, **payload}, ensure_ascii=False, default=str)}
    if event_id:
        row['id'] = event_id
    return row


def _event_rows(events: list[Event]) -> list[tuple[int, Event]]:
    rows = [(index, event.sequence or index + 1, event) for index, event in enumerate(events)]
    return [(sequence, event) for index, sequence, event in sorted(rows, key=lambda item: (item[1], item[0]))]


UI_STAGE_ALIASES = {
    'dataset': 'dataset',
    'dataset_gen': 'dataset',
    'dataset_corpus': 'dataset',
    'eval': 'eval',
    'eval_retry': 'eval',
    'analysis': 'analysis',
    'classify': 'analysis',
    'repair': 'repair',
    'repair_loop': 'repair',
    'apply': 'repair',
    'abtest': 'abtest',
    'candidate_eval': 'abtest',
    'candidate_service_start': 'abtest',
    'candidate_service_stop': 'abtest',
    'candidate_cutover': 'abtest',
    'abtest_compare': 'abtest',
}

FLOW_KIND_ALIASES = {
    ('dataset_gen', 'load_corpus'): 'dataset.load_corpus',
    ('dataset_gen', 'build_corpus_snapshot'): 'dataset.build_corpus_snapshot',
    ('dataset_gen', 'prepare_case'): 'dataset_gen.prepare_case',
    ('dataset_gen', 'generate_case'): 'dataset_gen.generate_case',
    ('dataset_gen', 'assemble'): 'dataset.assemble',
    ('eval', 'rag_answer'): 'eval.rag_answer',
    ('eval', 'judge_answer'): 'eval.judge_answer',
    ('eval', 'aggregate'): 'eval.aggregate',
    ('candidate_eval', 'rag_answer'): 'eval.rag_answer',
    ('candidate_eval', 'judge_answer'): 'eval.judge_answer',
    ('candidate_eval', 'aggregate'): 'eval.aggregate',
    ('analysis', 'coarse_classify'): 'analysis.coarse_classify',
    ('analysis', 'fine_classify'): 'analysis.fine_classify',
    ('analysis', 'classification_report'): 'analysis.classification_report',
    ('repair', 'plan'): 'repair.plan',
    ('repair', 'repair_loop'): 'repair.loop',
    ('repair', 'candidate_workspace'): 'repair.candidate_workspace',
    ('abtest', 'candidate_service_start'): 'abtest.candidate_service.start',
    ('abtest', 'candidate_service_stop'): 'abtest.candidate_service.stop',
    ('abtest', 'compare'): 'abtest.compare',
    ('abtest', 'candidate_cutover'): 'abtest.candidate_cutover',
}


class FrontendEventProjector:
    def __init__(self, run_dir: Path):
        self.run_dir = run_dir
        self._operations: dict[str, Any] | None = None
        self._totals: dict[str, int] = {}
        self._last_stage = ''

    def frame(self, event: Event, seq: int) -> dict | None:
        payload = dict(event.payload or {})
        if event.event_type.startswith('checkpoint.'):
            return _sse(event.event_type, {'seq': seq, 'event_id': event.event_id, **payload}, str(seq))
        if event.event_type.startswith('operation.'):
            return self._operation_frame(event.event_type, payload, seq, event.event_id)
        if event.event_type == 'evo_flow.progress':
            return self._flow_frame(payload, seq, event.event_id)
        if event.event_type.startswith('run.') or event.event_type.startswith('thread_control.'):
            return self._lifecycle_frame(event.event_type, payload, seq, event.event_id)
        return None

    def _operation_frame(self, event_type: str, payload: dict[str, Any],
                         seq: int, event_id: str) -> dict | None:
        kind = event_type.split('.', 1)[1]
        if kind not in {'started', 'ended', 'checkpointed', 'progress'}:
            return None
        operation_id = str(payload.get('operation_run_id') or '')
        record = payload.get('after') if isinstance(payload.get('after'), dict) else None
        if record:
            operation_id = str(record.get('operation_run_id') or operation_id)
        record = record or self._operation(operation_id)
        if str(record.get('category') or '') == 'intent':
            return None
        data = self._operation_payload(operation_id, record, payload)
        stage = _ui_stage(str(data.get('flow_tag') or data.get('stage') or ''))
        if not stage:
            stage = _operation_stage(operation_id, str(data.get('phase') or data.get('stage_tag') or ''))
        if not stage:
            return None
        self._last_stage = stage
        action = _operation_action(kind, data)
        return _sse('message', {'stage': stage, 'action': action, 'seq': seq, 'event_id': event_id,
                                'operation_run_id': operation_id,
                                'message': str(data.get('message') or ''), 'payload': data}, str(seq))

    def _flow_frame(self, payload: dict[str, Any], seq: int, event_id: str) -> dict | None:
        stage = _ui_stage(str(payload.get('stage') or ''))
        if not stage:
            return None
        self._last_stage = stage
        data = {key: value for key, value in payload.items() if value not in (None, '')}
        data['stage'] = stage
        return _sse('message', {'stage': stage, 'action': _action(str(payload.get('status') or 'running')),
                                'seq': seq, 'event_id': event_id, 'payload': data}, str(seq))

    def _lifecycle_frame(self, event_type: str, payload: dict[str, Any],
                         seq: int, event_id: str) -> dict | None:
        if event_type not in {'run.started', 'run.dispatch_blocked', 'run.dispatch_opened',
                              'run.paused', 'run.cancelled', 'run.failed', 'run.ended',
                              'thread_control.paused', 'thread_control.cancelled'}:
            return None
        stage = _ui_stage(str(payload.get('stage') or payload.get('next_stage') or ''))
        stage = stage or self._stage_from_refs(payload) or self._last_stage or self._active_stage()
        if not stage:
            return None
        self._last_stage = stage
        data = {key: value for key, value in payload.items() if value not in (None, '')}
        data['stage'] = stage
        action = _lifecycle_action(event_type, payload)
        return _sse('message', {'stage': stage, 'action': action, 'seq': seq,
                                'event_id': event_id, 'payload': data}, str(seq))

    def _operation_payload(self, operation_id: str, record: dict[str, Any],
                           event_payload: dict[str, Any]) -> dict[str, Any]:
        progress = event_payload if 'operation_run_id' in event_payload else {}
        if isinstance(record.get('progress'), dict):
            progress = {**record['progress'], **progress}
        tags = record.get('tags') if isinstance(record.get('tags'), dict) else {}
        params = record.get('params') if isinstance(record.get('params'), dict) else {}
        data = {key: value for key, value in progress.items() if value not in (None, '')}
        for key in ('operation_run_id', 'operation_id', 'operation_type', 'flow_tag', 'stage_tag',
                    'status', 'outcome'):
            if key not in data and record.get(key) not in (None, ''):
                data[key] = record[key]
        data['flow_kind'] = str(tags.get('evo_step') or _flow_kind(record, data))
        if tags.get('writes_artifact_id'):
            data['writes_artifact_id'] = tags['writes_artifact_id']
        current_item = str(data.get('current_item') or '')
        case_candidates = [params.get('case_id'), params.get('output_case_id'),
                           _operation_case_id(operation_id), current_item if _is_case_id(current_item) else '']
        case_id = str(next((item for item in case_candidates if item), ''))
        if case_id:
            data['case_id'] = case_id
            data['current_item'] = data.get('current_item') or case_id
            index = _case_index(case_id)
            if index:
                data['case_index'] = index
        total = self._case_total(str(data.get('flow_kind') or ''), operation_id)
        if total:
            data['total'] = data.get('total') or total
            data['case_count'] = data.get('case_count') or total
        if isinstance(event_payload.get('after'), dict):
            data['after'] = event_payload['after']
        if isinstance(event_payload.get('before'), dict):
            data['before'] = event_payload['before']
        return data

    def _operation(self, operation_id: str) -> dict[str, Any]:
        operations = self._operation_map()
        record = operations.get(operation_id)
        return record if isinstance(record, dict) else {'operation_run_id': operation_id}

    def _stage_from_refs(self, payload: dict[str, Any]) -> str:
        refs = []
        for key in ('operation_refs', 'next_operations', 'blocked_operations'):
            if payload.get(key):
                refs = payload[key]
                break
        for item in refs if isinstance(refs, list) else []:
            operation_id = str(item.get('operation_run_id') if isinstance(item, dict) else item)
            stage = _record_stage(self._operation(operation_id), operation_id)
            if stage:
                return stage
        return ''

    def _active_stage(self) -> str:
        for operation_id, record in self._operation_map().items():
            if isinstance(record, dict) and record.get('status') in {'running', 'checkpointed'}:
                stage = _record_stage(record, str(operation_id))
                if stage:
                    return stage
        return ''

    def _operation_map(self) -> dict[str, Any]:
        if self._operations is None:
            operations = _read_json(self.run_dir / 'operations.json')
            self._operations = operations if isinstance(operations, dict) else {}
        return self._operations

    def _case_total(self, flow_kind: str, operation_id: str) -> int:
        if flow_kind not in {'dataset_gen.prepare_case', 'dataset_gen.generate_case',
                             'eval.rag_answer', 'eval.judge_answer'}:
            return 0
        key = f'{operation_id.split(".", 1)[0]}:{flow_kind}'
        if key in self._totals:
            return self._totals[key]
        operations = self._operation_map()
        cases = set()
        for ref, record in operations.items():
            if not isinstance(record, dict):
                continue
            candidate_kind = str((record.get('tags') or {}).get('evo_step') or _flow_kind(record, {}))
            if candidate_kind != flow_kind:
                continue
            if flow_kind.startswith('eval.') and str(ref).split('.', 1)[0] != operation_id.split('.', 1)[0]:
                continue
            params = record.get('params') or {}
            case_id = str(params.get('case_id') or params.get('output_case_id') or _operation_case_id(str(ref)) or '')
            if case_id:
                cases.add(case_id)
        self._totals[key] = len(cases)
        return self._totals[key]


def _flow_kind(record: dict[str, Any], data: dict[str, Any]) -> str:
    flow = str(record.get('flow_tag') or data.get('flow_tag') or '')
    stage = str(record.get('stage_tag') or data.get('stage_tag') or data.get('phase') or '')
    return FLOW_KIND_ALIASES.get((flow, stage), f'{flow}.{stage}' if flow and stage else stage)


def _operation_case_id(operation_id: str) -> str:
    match = re.search(r'case_\d+', operation_id)
    return match.group(0) if match else ''


def _is_case_id(value: str) -> bool:
    return bool(re.fullmatch(r'case_\d+', value))


def _case_index(case_id: str) -> int:
    if not _is_case_id(case_id):
        return 0
    match = re.search(r'\d+', case_id)
    return int(match.group(0)) if match else 0


def _operation_action(kind: str, payload: dict[str, Any]) -> str:
    if kind == 'started':
        return 'progress'
    if kind == 'checkpointed':
        return 'pause'
    if kind == 'ended':
        return _action(str(payload.get('outcome') or 'success'))
    return _action(str(payload.get('status') or 'running'))


def _lifecycle_action(event_type: str, payload: dict[str, Any]) -> str:
    if event_type == 'run.dispatch_blocked':
        return 'pause'
    if event_type in {'run.started', 'run.dispatch_opened'}:
        return 'progress'
    return _action(str(payload.get('outcome') or payload.get('status') or 'running'))


def _ui_stage(value: str) -> str:
    return UI_STAGE_ALIASES.get(value, '') or stage_group(value)


def _record_stage(record: dict[str, Any], operation_id: str = '') -> str:
    return _ui_stage(str(record.get('flow_tag') or '')) or \
        _operation_stage(operation_id or str(record.get('operation_run_id') or ''),
                         str(record.get('stage_tag') or ''))


def _operation_stage(operation_id: str, phase: str) -> str:
    head = operation_id.split('.', 1)[0]
    if operation_id.startswith('eval_retry_'):
        return 'eval'
    if head in {'dataset', 'eval'}:
        return head
    if head == 'candidate_eval':
        return 'abtest'
    if head in {'analysis', 'classify'} or phase in {
        'coarse_classify', 'fine_classify', 'assemble_classification_report',
    }:
        return 'analysis'
    if head in {'repair', 'apply'} or phase.startswith(('repair_', 'opencode')):
        return 'repair'
    if head == 'abtest' or phase.startswith('abtest'):
        return 'abtest'
    return ''


def _action(status: str) -> str:
    return {'running': 'progress', 'success': 'finish', 'failed': 'failed', 'checkpointed': 'pause',
            'cancelled': 'cancel', 'ended': 'finish', 'paused': 'pause',
            'skipped': 'finish'}.get(status, 'progress')


def _intent_reply(service: EvoFlowService, result: FlowMessageResult) -> str:
    for item in reversed(result.results):
        for ref in reversed(item.output_refs):
            payload = _artifact_payload(service, ref)
            if _is_intent_answer_payload(payload):
                return _reply_value(payload['answer'])
    reasons = result.raw.get('reasons') if isinstance(result.raw.get('reasons'), list) else []
    if reasons:
        return _reply_value(reasons)
    issues = result.raw.get('issues') if isinstance(result.raw.get('issues'), list) else []
    if issues:
        return _reply_value(issues)
    next_task = result.raw.get('next_task') if isinstance(result.raw.get('next_task'), dict) else {}
    for key in ('answer', 'message', 'content'):
        if next_task.get(key):
            return _reply_value(next_task[key])
    semantic = next_task.get('semantic_params') if isinstance(next_task.get('semantic_params'), dict) else {}
    if semantic.get('answer'):
        return _reply_value(semantic['answer'])
    return _reply_value(result.raw)


def _artifact_payload(service: EvoFlowService, ref: Any) -> Any:
    parsed = ref if isinstance(ref, ArtifactRef) else ArtifactRef.parse(str(ref))
    try:
        return service.artifacts.get(parsed)
    except KeyError:
        return {}


def _is_intent_answer_payload(payload: Any) -> bool:
    return isinstance(payload, dict) and 'answer' in payload and 'query_intent_id' in payload


def _reply_value(value: Any) -> str:
    if isinstance(value, str):
        return value
    return json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)


def _with_message_id(response: dict, message_id: str) -> dict:
    out = dict(response)
    out['intent_id'] = message_id
    return out


def _dataset_id(inputs: dict[str, Any]) -> str:
    ids = {str(inputs.get(key) or '').strip() for key in ('kb_id', 'dataset_id') if str(inputs.get(key) or '').strip()}
    if len(ids) > 1:
        raise ValueError('dataset id aliases must match')
    if ids:
        return validate_id(ids.pop(), 'dataset_id')
    legacy = str(inputs.get('dataset_name') or '').strip()
    return validate_id(legacy, 'dataset_id') if legacy else 'algo'


def _user_context(request: Request) -> dict[str, str]:
    keys = ('authorization', 'x-user-id', 'x-user-name', 'x-request-id')
    return {key: value for key in keys if (value := request.headers.get(key))}


def _normalize_inputs(inputs: dict[str, Any]) -> dict[str, Any]:
    out = dict(inputs)
    out['kb_id'] = out['dataset_id'] = _dataset_id(out)
    target = str(out.get('target_chat_url') or os.getenv('LAZYMIND_EVO_CORE_CHAT_URL')
                 or os.getenv('LAZYMIND_CORE_SERVICE_URL')
                 or os.getenv('LAZYMIND_CORE_API_URL') or 'http://core:8000')
    if target.rstrip('/').endswith(':8000'):
        target = target.rstrip('/') + '/conversations:chat'
    out['target_chat_url'] = normalize_chat_target_url(target.replace('http://evo-chat:', 'http://chat:'),
                                                       'target_chat_url')
    candidate = str(out.get('candidate_chat_url') or '').strip()
    out['candidate_chat_url'] = normalize_chat_stream_url(candidate, 'candidate_chat_url') if candidate else ''
    if out['candidate_chat_url'] and out['candidate_chat_url'] == out['target_chat_url']:
        raise ValueError('candidate_chat_url must differ from target_chat_url')
    router = str(out.get('router_admin_url') or os.getenv('LAZYMIND_EVO_ROUTER_ADMIN_URL') or '').strip()
    out['router_admin_url'] = normalize_http_origin(router, 'router_admin_url') if router else ''
    out['num_cases'] = _bounded_int(out.get('num_cases', out.get('case_count', '20')), 'num_cases', MAX_CASES)
    out['max_workers'] = _bounded_int(out.get('max_workers', os.getenv('EVO_FLOW_WORKERS', '2')),
                                      'max_workers', MAX_WORKERS)
    out.pop('case_count', None)
    return out


def _bounded_int(value: Any, field: str, maximum: int) -> int:
    try:
        out = int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f'{field} must be a positive integer') from exc
    if out < 1 or out > maximum:
        raise ValueError(f'{field} must be between 1 and {maximum}')
    return out


def _scoped_report_id(value: str) -> tuple[str, str]:
    message = 'global report content requires {thread_id}:{artifact_ref}'
    if ':' not in str(value):
        raise HTTPException(400, message)
    thread_id, artifact = (part.strip() for part in str(value).split(':', 1))
    if not thread_id or not artifact:
        raise HTTPException(400, message)
    return thread_id, artifact


def _preview(result: FlowMessageResult) -> list[dict]:
    return [{'op': ref, 'intent': result.action, 'humanized': result.action.replace('_', ' '), 'safety': 'normal',
             'params_summary': {}} for ref in result.operation_refs]


def _read_messages(path: Path) -> list[dict]:
    if not path.exists():
        return []
    rows = []
    for index, line in enumerate(path.read_text(encoding='utf-8').splitlines()):
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        if row.get('role') in {'user', 'assistant'} and row.get('content'):
            rows.append({'id': f'msg-{index + 1}', 'role': row['role'], 'content': row['content'], 'ts': row.get('ts')})
    return rows


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


def _redact(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _redact(item) for key, item in value.items()
                if key.lower() not in {'api_key', 'authorization', 'password', 'secret', 'token'}}
    if isinstance(value, list):
        return [_redact(item) for item in value]
    return value


def _flow_status_row(thread_id: str, status: str, active: list[str]) -> dict:
    return {'thread_id': thread_id, 'status': status, 'active_task_ids': active, 'pending_checkpoint': None,
            'latest_abtest_id': None, 'latest_abtest_status': None, 'report_ready': False}


def _status_from_projection(thread_id: str, run_dir: Path, projection: dict, meta_status: str,
                            active: list[str]) -> dict:
    run = projection.get('run') if isinstance(projection.get('run'), dict) else _read_json(run_dir / 'run.json')
    status = str(run.get('status') or meta_status or 'idle')
    if meta_status in {'cancelled', 'deleting'}:
        status = meta_status
    if status == 'running' and not active and run.get('dispatch_block_reason'):
        status = 'waiting_checkpoint'
    if status == 'running' and not active:
        active = _running_operation_ids(run_dir)
    return _flow_status_row(thread_id, status, active if status == 'running' else [])


def _running_operation_ids(run_dir: Path) -> list[str]:
    operations = _read_json(run_dir / 'operations.json')
    if not isinstance(operations, dict):
        return []
    active = []
    for operation_id, operation in operations.items():
        if not isinstance(operation, dict) or operation.get('status') != 'running':
            continue
        if str(operation.get('category') or '') == 'intent':
            continue
        active.append(str(operation.get('operation_run_id') or operation_id))
    return active


def _stored_events(run_dir: Path) -> list[Event]:
    path = run_dir / 'events.jsonl'
    if not path.exists():
        return []
    rows = []
    for line in path.read_text(encoding='utf-8').splitlines():
        try:
            if line.strip():
                rows.append(Event(**json.loads(line)))
        except (TypeError, json.JSONDecodeError):
            continue
    return rows


def _stored_artifact_row(run_dir: Path, artifact_id: str) -> dict:
    try:
        data = _stored_artifact_payload(run_dir, artifact_id)
    except Exception:
        return {}
    ref = artifact_id if '@v' in artifact_id else _latest_ref_text(run_dir, artifact_id)
    manifest = _read_json(run_dir / 'artifacts' / 'manifests' / f'{artifact_id.split("@v", 1)[0]}.json')
    return {'artifact_id': artifact_id.split('@v', 1)[0], 'artifact_ref': ref,
            'schema': manifest.get('schema_name', ''), 'data': data}


def _stored_artifact_payload(run_dir: Path, artifact: str) -> Any:
    artifact_id, version = _stored_artifact_target(artifact)
    manifest = _read_json(run_dir / 'artifacts' / 'manifests' / f'{artifact_id}.json')
    versions = manifest.get('versions') if isinstance(manifest.get('versions'), list) else []
    if not versions:
        raise KeyError(artifact)
    target = int(manifest.get('latest_version') or 0) if version is None else version
    selected = next((item for item in versions if isinstance(item, dict) and int(item.get('version') or 0) == target),
                    None)
    if not selected or not selected.get('payload_ref'):
        raise KeyError(artifact)
    return json.loads((run_dir / str(selected['payload_ref'])).read_text(encoding='utf-8'))


def _stored_artifact_target(artifact: str) -> tuple[str, int | None]:
    if '@v' not in artifact:
        return artifact, None
    ref = ArtifactRef.parse(artifact)
    return ref.artifact_id, ref.version


def _latest_ref_text(run_dir: Path, artifact_id: str) -> str:
    manifest = _read_json(run_dir / 'artifacts' / 'manifests' / f'{artifact_id}.json')
    return f"{artifact_id}@v{int(manifest.get('latest_version') or 1)}"
