from __future__ import annotations

import asyncio
import json
import os
import shutil
import threading
import time
import uuid
from dataclasses import asdict
from pathlib import Path
from typing import Any

from fastapi import Body, FastAPI, HTTPException, Request, Response
from sse_starlette.sse import EventSourceResponse

from evo.artifacts import ArtifactRef
from evo.flow import EvoFlowService
from evo.flow.service import FlowMessageResult, result_dict
from evo.store import Event

BODY_REQUIRED = Body(...)
BODY_DEFAULT = Body(default_factory=dict)
RUN_ID = 'run_1'
STAGE_MAP = {
    'dataset_corpus': 'dataset',
    'dataset_gen': 'dataset',
    'dataset': 'dataset',
    'eval': 'eval',
    'candidate_eval': 'abtest',
    'run': 'analysis',
    'analysis': 'analysis',
    'apply': 'repair',
    'repair_plan': 'repair',
    'candidate_workspace': 'repair',
    'repair_loop': 'repair',
    'candidate_service_start': 'abtest',
    'candidate_service_stop': 'abtest',
    'abtest_compare': 'abtest',
    'candidate_cutover': 'abtest',
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
    async def create_thread(body: dict = BODY_REQUIRED) -> dict:
        return await asyncio.to_thread(hub.create_thread, body)

    @app.get('/v1/evo/threads')
    def list_threads() -> list[dict]:
        return hub.list_threads()

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
    async def retry(thread_id: str, body: dict = BODY_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.retry, thread_id, body)

    @app.post('/v1/evo/threads/{thread_id}/auto/step')
    async def auto_step(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.start, thread_id, {'force_auto': True})

    @app.post('/v1/evo/threads/{thread_id}/auto/start')
    async def auto_start(thread_id: str, request: Request, body: dict = BODY_DEFAULT):
        if 'text/event-stream' in request.headers.get('accept', ''):
            return EventSourceResponse(
                _single_sse('auto_start', {'thread_id': thread_id, **hub.start(thread_id, body)})
            )
        return await asyncio.to_thread(hub.start, thread_id, body)

    @app.post('/v1/evo/threads/{thread_id}/auto/stop')
    def auto_stop(thread_id: str) -> dict:
        return hub.pause(thread_id)

    @app.get('/v1/evo/threads/{thread_id}:events')
    @app.get('/v1/evo/threads/{thread_id}/events')
    def events(thread_id: str, request: Request, since: int = 0) -> EventSourceResponse:
        hub.get_thread(thread_id)
        last = request.headers.get('last-event-id') or ''
        return EventSourceResponse(hub.events(thread_id, int(last) if last.isdigit() else since))

    @app.get('/v1/evo/threads/{thread_id}/results/{kind}')
    def results(thread_id: str, kind: str) -> list[dict]:
        return hub.results(thread_id, kind)

    @app.get('/v1/evo/reports/{report_id}/content')
    def report_content(report_id: str, fmt: str = ''):
        content = hub.report_content(report_id)
        if fmt in {'md', 'markdown', 'text'}:
            return Response(content, media_type='text/markdown; charset=utf-8')
        return {'report_id': report_id, 'content': content}

    @app.get('/v1/evo/diffs/{apply_id}/{filename:path}')
    def diff_content(apply_id: str, filename: str) -> Response:
        return Response(hub.diff_content(apply_id, filename), media_type='text/x-diff; charset=utf-8')

    return app


def get_app() -> FastAPI:
    return create_app()


class EvoMessageHub:
    def __init__(self, base_dir: Path):
        self.base_dir = base_dir
        self.threads_dir = base_dir / 'state' / 'threads'
        self._services: dict[str, EvoFlowService] = {}
        self._tasks: dict[str, threading.Thread] = {}
        self._checkpoint_events: dict[str, threading.Event] = {}
        self._queued_messages: dict[str, list[dict[str, Any]]] = {}
        self._lock = threading.RLock()

    def create_thread(self, payload: dict[str, Any]) -> dict:
        mode = str(payload.get('mode') or 'interactive').strip()
        if mode not in {'auto', 'interactive'}:
            raise HTTPException(400, f'bad mode {mode!r}')
        thread_id, now = f'thr-{uuid.uuid4().hex[:8]}', time.time()
        inputs = dict(payload.get('inputs') or {})
        meta = {'id': thread_id, 'thread_id': thread_id, 'mode': mode, 'title': str(payload.get('title') or ''),
                'inputs': inputs, 'model_config': payload.get('llm_config') or {}, 'status': 'idle',
                'created_at': now, 'updated_at': now}
        self._write_meta(thread_id, meta)
        if mode == 'auto' and payload.get('start_auto'):
            self.start(thread_id, payload)
        return meta

    def list_threads(self) -> list[dict]:
        rows = [_read_json(path) for path in self.threads_dir.glob('*/thread.json')]
        return sorted([row for row in rows if row], key=lambda row: row.get('updated_at') or 0, reverse=True)

    def get_thread(self, thread_id: str) -> dict:
        return self._meta(thread_id)

    def delete_thread(self, thread_id: str) -> dict:
        self._meta(thread_id)
        cancelled = False
        task = self._tasks.get(thread_id)
        if task and task.is_alive():
            self.cancel(thread_id)
            cancelled = True
            task.join(timeout=5)
            if task.is_alive():
                raise HTTPException(409, f'thread {thread_id} is still running')
        self._queued_messages.pop(thread_id, None)
        self._checkpoint_events.pop(thread_id, None)
        self._services.pop(thread_id, None)
        run_root, thread_dir = self.base_dir / 'dev-runs' / thread_id, self._thread_dir(thread_id)
        run_deleted, thread_deleted = run_root.exists(), thread_dir.exists()
        shutil.rmtree(run_root, ignore_errors=True)
        shutil.rmtree(thread_dir, ignore_errors=True)
        return {'thread_id': thread_id, 'deleted_run': run_deleted, 'deleted_thread': thread_deleted,
                'cancelled': cancelled}

    def history(self, thread_id: str) -> dict:
        return {'thread_id': thread_id, 'messages': _read_messages(self._thread_dir(thread_id) / 'messages.jsonl')}

    def start(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        self._meta(thread_id)
        with self._lock:
            if self._task_alive(thread_id):
                return {'status': 'running', 'thread_id': thread_id, 'task_id': thread_id}
            self._clear_stage_checkpoint(thread_id)
            self._start_flow_task_locked(thread_id)
        return {'status': 'running', 'thread_id': thread_id, 'task_id': thread_id}

    def pause(self, thread_id: str) -> dict:
        service = self._service(thread_id)
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        self._update_meta(thread_id, status='paused', updated_at=time.time())
        return {'status': 'paused', 'thread_id': thread_id}

    def cancel(self, thread_id: str) -> dict:
        service = self._service(thread_id)
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        self._queued_messages.pop(thread_id, None)
        self._update_meta(thread_id, status='cancelled', pending_checkpoint=None, updated_at=time.time())
        event = self._checkpoint_events.get(thread_id)
        if event:
            event.set()
        return {'status': 'cancelled', 'thread_id': thread_id}

    def retry(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        message = str((payload or {}).get('message') or '继续刚才暂停的任务。')
        result = self.post_message(thread_id, {'message': message, 'dispatch': True})
        return {'status': 'running', 'thread_id': thread_id, 'message_result': result}

    def post_message(self, thread_id: str, payload: dict[str, Any]) -> dict:
        content = str(payload.get('content') or payload.get('message') or '').strip()
        if not content:
            raise HTTPException(400, 'message content required')
        message_id = str(payload.get('message_id') or f'msg_{thread_id}_{uuid.uuid4().hex[:8]}')
        self._append_message(thread_id, 'user', content)
        task_alive = self._task_alive(thread_id)
        if task_alive and _interrupt_message(content):
            self.pause(thread_id)
        checkpoint = self._stage_checkpoint(thread_id)
        if checkpoint:
            service = self._service(thread_id)
            if _resume_message(content):
                result = FlowMessageResult(message_id, {'next_task': {'type': 'checkpoint_continue'}},
                                           'checkpoint_continue', skipped=True)
                self._resume_stage_checkpoint(thread_id, service, checkpoint, 'message')
                reply = f'已继续：{checkpoint.get("next_stage") or "下一阶段"}。'
            else:
                result = service.send_message(
                    message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                    dispatch=bool(payload.get('dispatch', True)), max_dispatch=int(payload.get('max_dispatch') or 1),
                )
                reply = _reply(result) + ' 当前仍在 checkpoint，已记录这条干预。'
            self._append_message(thread_id, 'assistant', reply)
            self._update_meta(thread_id, status=self.flow_status(thread_id)['status'], updated_at=time.time())
            return {'intent_id': message_id, 'reply': reply, 'thinking': '', 'requires_confirm': False,
                    'preview': _preview(result), 'warnings': [], 'result': result_dict(result)}
        resume_stage = self._stalled_resume_stage(thread_id) if _resume_message(content) and not task_alive else ''
        if resume_stage:
            service = self._service(thread_id)
            result = FlowMessageResult(message_id, {'next_task': {'type': 'checkpoint_continue'}},
                                       'checkpoint_continue', skipped=True)
            self._start_resume_stage(thread_id, service, resume_stage, 'message')
            reply = f'已继续：{resume_stage}。'
            self._append_message(thread_id, 'assistant', reply)
            self._update_meta(thread_id, status=self.flow_status(thread_id)['status'], updated_at=time.time())
            return {'intent_id': message_id, 'reply': reply, 'thinking': '', 'requires_confirm': False,
                    'preview': _preview(result), 'warnings': [], 'result': result_dict(result)}
        if task_alive:
            result = self._preview_message(thread_id, self._service(thread_id), message_id, content, payload)
            self._queue_message(thread_id, {
                'message_id': message_id,
                'content': content,
                'allowed_capabilities': payload.get('allowed_capabilities'),
                'dispatch': bool(payload.get('dispatch', True)),
                'max_dispatch': int(payload.get('max_dispatch') or 1),
                'action': result.action,
            })
            reply = f'流程正在执行，已解析为 {result.action}；将在下一个阶段 checkpoint 应用。'
            self._append_message(thread_id, 'assistant', reply)
            self._update_meta(thread_id, status=self.flow_status(thread_id)['status'], updated_at=time.time())
            return {'intent_id': message_id, 'reply': reply, 'thinking': '', 'requires_confirm': False,
                    'preview': _preview(result), 'warnings': [], 'result': result_dict(result)}
        dispatch = bool(payload.get('dispatch', True))
        had_run = self._has_run(thread_id)
        service = self._service(thread_id)
        if not had_run:
            service.plan_full_flow()
        result = self._preview_message(thread_id, service, message_id, content, payload) if not dispatch else (
            service.send_message(
                message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                dispatch=True, max_dispatch=int(payload.get('max_dispatch') or 1),
            )
        )
        reply = _reply(result)
        self._append_message(thread_id, 'assistant', reply)
        self._update_meta(thread_id, status=self.flow_status(thread_id)['status'], updated_at=time.time())
        return {'intent_id': message_id, 'reply': reply, 'thinking': '', 'requires_confirm': False,
                'preview': _preview(result), 'warnings': [], 'result': result_dict(result)}

    async def post_message_stream(self, thread_id: str, payload: dict[str, Any]):
        message_id = str(payload.get('message_id') or f'msg_{thread_id}_{uuid.uuid4().hex[:8]}')

        def emit(event: str, data: dict[str, Any]) -> dict:
            return _sse(event, {'thread_id': thread_id, 'message_id': message_id, **data})

        yield emit('intent_start', {})
        yield emit('thinking_delta', {'delta': '正在理解你的请求并规划下一步。'})
        try:
            result = await asyncio.to_thread(self.post_message, thread_id, {**payload, 'message_id': message_id})
            for chunk in _chunks(result['reply']):
                yield emit('answer_delta', {'delta': chunk})
            yield emit('plan_ready', {'intent_id': result['intent_id'], 'actions': result['preview'],
                                      'warnings': result['warnings']})
            for action in result['preview']:
                yield emit('action', {'intent_id': result['intent_id'], 'action': action})
            yield emit('done', {'intent_id': result['intent_id']})
        except Exception as exc:
            yield emit('error', {'code': getattr(exc, 'code', 'MESSAGE_FAILED'), 'message': str(exc)})

    def flow_status(self, thread_id: str) -> dict:
        meta, task = self._meta(thread_id), self._tasks.get(thread_id)
        status = str(meta.get('status') or 'idle')
        active_task_ids = [thread_id] if task and task.is_alive() else []
        if thread_id not in self._services and not self._has_run(thread_id):
            return _flow_status_row(thread_id, 'running' if active_task_ids else status, active_task_ids)
        if thread_id not in self._services:
            return self._stored_flow_status(thread_id, status, active_task_ids)
        service = self._service(thread_id)
        state = service.graph.schedule_state()
        stage_checkpoint = self._stage_checkpoint(thread_id)
        if status in {'cancelled', 'paused', 'failed'}:
            pass
        elif stage_checkpoint:
            status = 'waiting_checkpoint'
        elif task and task.is_alive():
            status = 'running'
        elif state.checkpointed:
            status = 'waiting_checkpoint'
        elif status == 'running':
            status = 'ended' if _has_artifact(service, 'abtest_comparison') else 'idle'
        decision = _artifact_payload(service, 'abtest_comparison').get('decision') or {}
        return {'thread_id': thread_id, 'status': status,
                'active_task_ids': active_task_ids if status == 'running' else [],
                'latest_abtest_id': 'abtest_comparison' if decision else None,
                'latest_abtest_status': decision.get('status'), 'report_ready': _has_artifact(service, 'eval_report'),
                'pending_checkpoint': _checkpoint(state.checkpointed) or stage_checkpoint}

    async def events(self, thread_id: str, since: int = 0):
        self._meta(thread_id)
        if thread_id not in self._services and not self._has_run(thread_id):
            return
        emitted = max(0, since)
        idle_ticks = 0
        while True:
            events = self._service(thread_id).store.read_events(RUN_ID) if thread_id in self._services \
                else _stored_events(self._run_dir(thread_id))
            for index, event in enumerate(events[emitted:], emitted + 1):
                emitted = index
                frame = _event_frame(event, index)
                if frame:
                    yield frame
            status = self.flow_status(thread_id)['status']
            if status in {'ended', 'failed', 'cancelled'} and emitted >= len(events):
                yield _sse('done', {'thread_id': thread_id, 'status': status}, str(emitted + 1))
                return
            idle_ticks = idle_ticks + 1 if emitted >= len(events) else 0
            if status in {'idle', 'paused', 'waiting_checkpoint'} and idle_ticks > 4:
                return
            await asyncio.sleep(0.5)

    def results(self, thread_id: str, kind: str) -> list[dict]:
        service = self._service(thread_id)
        if kind == 'datasets':
            return [_artifact_row(service, 'eval_dataset')]
        if kind == 'eval-reports':
            return [_artifact_row(service, item) for item in ('eval_report', 'candidate_eval_report')]
        if kind == 'analysis-reports':
            return [_artifact_row(service, item) for item in ('classification_report', 'repair_loop_plan')]
        if kind == 'diffs':
            return _schema_rows(service, {'CodePatchCandidate', 'VerifiedRepair'})
        if kind == 'abtests':
            return [_artifact_row(service, item) for item in ('abtest_comparison', 'candidate_algorithm_cutover')]
        raise HTTPException(404, f'unknown result kind: {kind}')

    def report_content(self, report_id: str) -> str:
        data = self._artifact_payload_any(report_id)
        for key in ('markdown', 'report', 'content', 'text', 'summary'):
            value = data.get(key)
            if isinstance(value, str) and value.strip():
                return value
        return json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True, default=str)

    def diff_content(self, apply_id: str, filename: str) -> str:
        data = self._artifact_payload_any(apply_id)
        diff = data.get('diff') or data.get('patch') or data.get('content') or ''
        if isinstance(diff, str) and diff.strip():
            return diff
        files = data.get('files') or data.get('diff_files') or []
        for item in files if isinstance(files, list) else []:
            if not isinstance(item, dict):
                continue
            path = str(item.get('path') or item.get('filename') or '')
            if path == filename or path.endswith('/' + filename) or Path(path).name == filename:
                text = item.get('diff') or item.get('patch') or item.get('content') or ''
                if isinstance(text, str):
                    return text
        raise HTTPException(404, f'diff content not found: {apply_id}/{filename}')

    def _run_full_flow(self, thread_id: str, start_stage: str = 'dataset') -> None:
        self._update_meta(thread_id, status='running', updated_at=time.time())
        try:
            service = self._service(thread_id)
            flow = service.run_full_flow(
                repair_plan_params={'target_mean_delta': 0, 'goodcase_guard_ratio': 0.5},
                start_stage=start_stage,
                after_stage=lambda stage, detail: self._after_stage(thread_id, service, stage, detail),
            )
            if self._meta(thread_id).get('status') == 'cancelled':
                return
            self._update_meta(
                thread_id,
                status='ended',
                flow={k: [asdict(item) for item in v] for k, v in flow.items()},
                pending_checkpoint=None,
                updated_at=time.time(),
            )
        except Exception as exc:
            current_status = str(self._meta(thread_id).get('status') or '')
            status = 'cancelled' if current_status == 'cancelled' else (
                'paused' if self._service(thread_id).graph.run_refs({'checkpointed'}) else 'failed'
            )
            self._update_meta(
                thread_id,
                status=status,
                error={'type': exc.__class__.__name__, 'message': str(exc)},
                updated_at=time.time(),
            )
        finally:
            self._clear_stage_checkpoint(thread_id)

    def _after_stage(self, thread_id: str, service: EvoFlowService, stage: str, detail: dict[str, Any]) -> None:
        if self._meta(thread_id).get('status') == 'cancelled':
            raise RuntimeError('flow cancelled')
        if detail.get('terminal'):
            return
        checkpoint = service.checkpoints.create_checkpoint(RUN_ID, None, f'{stage} stage finished')
        stage_checkpoint = {
            'checkpoint_id': checkpoint.checkpoint_id,
            'stage': _checkpoint_stage(stage),
            'completed_stage': _checkpoint_stage(stage),
            'completed_flow': _checkpoint_stage(stage),
            'next_stage': str(detail.get('next_stage') or _next_stage({'stage': stage})),
            'next_op': {'op': str(detail.get('next_op') or _next_operation(stage))},
            'message': str(detail.get('message') or f'{_stage_label(stage)}已完成，请确认是否继续执行下一步。'),
            'detail': detail,
        }
        event = self._checkpoint_events.setdefault(thread_id, threading.Event())
        event.clear()
        service.store.append_event(Event('checkpoint.wait', RUN_ID, stage_checkpoint))
        self._update_meta(
            thread_id, status='waiting_checkpoint', pending_checkpoint=stage_checkpoint, updated_at=time.time()
        )
        if self._meta(thread_id).get('mode') == 'auto':
            if _requires_manual_cutover(stage_checkpoint):
                self._auto_hold_stage(thread_id, service, stage_checkpoint)
            else:
                self._auto_continue_stage(thread_id, service, stage_checkpoint)
                return
        while not event.wait(1):
            if str(self._meta(thread_id).get('status') or '') in {'cancelled', 'failed'}:
                raise RuntimeError(f'{thread_id} stopped while waiting for checkpoint {checkpoint.checkpoint_id}')

    def _auto_continue_stage(self, thread_id: str, service: EvoFlowService, checkpoint: dict[str, Any]) -> None:
        self._apply_queued_messages(thread_id, service)
        message = f'AutoOperator 已分析 {checkpoint.get("stage")} checkpoint，继续执行。'
        payload = {'checkpoint_id': checkpoint.get('checkpoint_id'), 'stage': checkpoint.get('stage'),
                   'next_stage': checkpoint.get('next_stage'), 'message': message}
        service.store.append_event(Event('autooperator.analysis', RUN_ID, payload))
        self._append_message(thread_id, 'assistant', message)
        self._resume_stage_checkpoint(thread_id, service, checkpoint, 'autooperator')

    def _auto_hold_stage(self, thread_id: str, service: EvoFlowService, checkpoint: dict[str, Any]) -> None:
        self._apply_queued_messages(thread_id, service)
        message = 'AutoOperator 已完成 ABTest 分析，候选算法切流需要用户确认。'
        payload = {'checkpoint_id': checkpoint.get('checkpoint_id'), 'stage': checkpoint.get('stage'),
                   'next_stage': checkpoint.get('next_stage'), 'message': message}
        service.store.append_event(Event('autooperator.analysis', RUN_ID, payload))
        self._append_message(thread_id, 'assistant', message)

    def _apply_queued_messages(self, thread_id: str, service: EvoFlowService) -> None:
        messages = self._queued_messages.pop(thread_id, [])
        for item in messages:
            if not item.get('dispatch'):
                service.store.append_event(Event('autooperator.intervention_observed', RUN_ID, {
                    'message_id': item['message_id'], 'action': item.get('action') or 'preview',
                    'message': 'AutoOperator 已记录前端干预消息，等待 checkpoint 处理。',
                }))
                continue
            result = service.send_message(
                item['message_id'], item['content'], allowed_capabilities=item.get('allowed_capabilities'),
                dispatch=bool(item.get('dispatch')), max_dispatch=int(item.get('max_dispatch') or 1),
            )
            service.store.append_event(Event('autooperator.intervention_applied', RUN_ID, {
                'message_id': item['message_id'], 'action': result.action,
                'operation_refs': list(result.operation_refs),
                'message': f'AutoOperator 已应用前端干预：{result.action}。',
            }))

    def _queue_message(self, thread_id: str, message: dict[str, Any]) -> None:
        self._queued_messages.setdefault(thread_id, []).append(message)

    def _continue_stage_checkpoint(
        self, thread_id: str, service: EvoFlowService, checkpoint: dict[str, Any], source: str
    ) -> None:
        service.store.append_event(Event('checkpoint.continue', RUN_ID, {
            'checkpoint_id': checkpoint.get('checkpoint_id'), 'stage': checkpoint.get('stage'),
            'next_stage': checkpoint.get('next_stage'), 'source': source,
        }))
        self._clear_stage_checkpoint(thread_id)
        self._update_meta(thread_id, status='running', updated_at=time.time())

    def _resume_stage_checkpoint(
        self, thread_id: str, service: EvoFlowService, checkpoint: dict[str, Any], source: str
    ) -> None:
        start_stage = str(checkpoint.get('next_stage') or _next_stage(checkpoint) or 'dataset')
        self._apply_queued_messages(thread_id, service)
        with self._lock:
            alive = self._task_alive(thread_id)
            self._continue_stage_checkpoint(thread_id, service, checkpoint, source)
            if not alive:
                self._start_flow_task_locked(thread_id, start_stage)

    def _start_resume_stage(self, thread_id: str, service: EvoFlowService, start_stage: str, source: str) -> None:
        service.store.append_event(Event('checkpoint.continue', RUN_ID, {
            'checkpoint_id': '', 'stage': '', 'next_stage': start_stage, 'source': source, 'recovered': True,
        }))
        with self._lock:
            if not self._task_alive(thread_id):
                self._start_flow_task_locked(thread_id, start_stage)
            else:
                self._update_meta(thread_id, status='running', updated_at=time.time())

    def _start_flow_task_locked(self, thread_id: str, start_stage: str = 'dataset') -> None:
        task = threading.Thread(target=self._run_full_flow, args=(thread_id, start_stage), daemon=True)
        self._tasks[thread_id] = task
        task.start()

    def _stage_checkpoint(self, thread_id: str) -> dict | None:
        checkpoint = self._meta(thread_id).get('pending_checkpoint')
        return checkpoint if isinstance(checkpoint, dict) and checkpoint.get('checkpoint_id') else None

    def _clear_stage_checkpoint(self, thread_id: str) -> None:
        event = self._checkpoint_events.get(thread_id)
        if event:
            event.set()
        try:
            self._update_meta(thread_id, pending_checkpoint=None, updated_at=time.time())
        except HTTPException:
            pass

    def _task_alive(self, thread_id: str) -> bool:
        task = self._tasks.get(thread_id)
        return bool(task and task.is_alive())

    def _stalled_resume_stage(self, thread_id: str) -> str:
        events = self._service(thread_id).store.read_events(RUN_ID) if thread_id in self._services \
            else _stored_events(self._run_dir(thread_id))
        start_stage, offset = '', -1
        for index, event in enumerate(events):
            if event.event_type == 'checkpoint.continue':
                stage = str((event.payload or {}).get('next_stage') or '')
                if stage:
                    start_stage, offset = stage, index
        if not start_stage:
            return ''
        for event in events[offset + 1:]:
            payload = event.payload or {}
            stage = STAGE_MAP.get(str(payload.get('stage') or payload.get('phase') or ''))
            if stage == start_stage or event.event_type.startswith('checkpoint.wait'):
                return ''
        return start_stage

    def _has_run(self, thread_id: str) -> bool:
        return (self.base_dir / 'dev-runs' / thread_id / 'store' / 'runs' / RUN_ID).exists()

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
        inputs = meta.get('inputs') or {}
        return {'run_root': run_root, 'run_id': RUN_ID, 'dataset_id': _dataset_id(inputs),
                'target_chat_url': _chat_url(inputs.get('target_chat_url')),
                'case_count': int(inputs.get('num_cases') or os.getenv('EVO_FLOW_CASE_COUNT', '20')),
                'max_workers': int(inputs.get('max_workers') or os.getenv('EVO_FLOW_WORKERS', '4')),
                'model_config': meta.get('model_config') or None}

    def _preview_message(
        self, thread_id: str, service: EvoFlowService, message_id: str, content: str, payload: dict[str, Any]
    ) -> FlowMessageResult:
        root = self.base_dir / 'dev-runs' / thread_id / 'tmp' / message_id
        shutil.rmtree(root, ignore_errors=True)
        root.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(service.run_root / 'store', root / 'store', ignore=_preview_copy_ignore)
        try:
            runner = EvoFlowService.resume(**self._service_kwargs(thread_id, root))
            return runner.send_message(
                message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                dispatch=False, max_dispatch=int(payload.get('max_dispatch') or 1),
            )
        finally:
            shutil.rmtree(root, ignore_errors=True)

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

    def _stored_flow_status(self, thread_id: str, status: str, active_task_ids: list[str]) -> dict:
        run_dir = self._run_dir(thread_id)
        projection = _read_json(run_dir / 'projections' / 'current.json')
        run = _read_json(run_dir / 'run.json')
        checkpointed = _checkpointed_operation_ids(run_dir, projection)
        latest = projection.get('latest_artifacts') or {}
        if status in {'cancelled', 'paused', 'failed'}:
            pass
        elif self._stage_checkpoint(thread_id) or checkpointed:
            status = 'waiting_checkpoint'
        elif active_task_ids:
            status = 'running'
        elif status == 'running':
            status = 'ended' if 'abtest_comparison' in latest else str(run.get('status') or 'idle')
        decision = _artifact_decision(run_dir, 'abtest_comparison') if 'abtest_comparison' in latest else {}
        return _flow_status_row(
            thread_id, status, active_task_ids if status == 'running' else [],
            latest_abtest_status=decision.get('status'), report_ready='eval_report' in latest,
            pending_checkpoint=self._stage_checkpoint(thread_id) or _checkpoint_ids(checkpointed),
        )

    def _artifact_payload_any(self, artifact: str) -> dict[str, Any]:
        artifact = artifact.strip()
        if not artifact:
            raise HTTPException(400, 'artifact id required')
        errors = []
        for meta in self.list_threads():
            if not self._has_run(str(meta['id'])) and str(meta['id']) not in self._services:
                continue
            try:
                service = self._service(str(meta['id']))
                ref = ArtifactRef.parse(artifact) if '@v' in artifact else service.artifacts.latest_ref(artifact)
                data = service.artifacts.get(ref)
                return data if isinstance(data, dict) else {'content': data}
            except (KeyError, ValueError, FileNotFoundError) as exc:
                errors.append(str(exc))
        raise HTTPException(404, f'artifact not found: {artifact}; searched={len(errors)}')


def _single_sse(event: str, payload: dict[str, Any]):
    async def gen():
        yield _sse(event, payload)
    return gen()


def _sse(event: str, payload: dict[str, Any], event_id: str | None = None) -> dict:
    row = {'event': event, 'data': json.dumps({'type': event, **payload}, ensure_ascii=False, default=str)}
    if event_id:
        row['id'] = event_id
    return row


def _event_frame(event, seq: int) -> dict | None:
    payload = dict(event.payload or {})
    if event.event_type == 'evo_flow.progress':
        if str(payload.get('stage') or '') == 'full_flow':
            return None
        stage = STAGE_MAP.get(str(payload.get('stage') or ''))
    elif event.event_type == 'operation.progress':
        stage = _stage_from_operation(str(payload.get('operation_run_id') or '')) or STAGE_MAP.get(
            str(payload.get('phase') or payload.get('stage') or '')
        )
    elif event.event_type.startswith('checkpoint.'):
        return _sse(
            event.event_type,
            {'seq': seq, 'event_id': event.event_id, 'created_at': event.created_at, **payload},
            str(seq),
        )
    elif event.event_type.startswith('autooperator.'):
        return _sse(
            event.event_type,
            {'seq': seq, 'event_id': event.event_id, 'created_at': event.created_at, **payload},
            str(seq),
        )
    else:
        return None
    if not stage:
        return None
    action = _action(str(payload.get('status') or 'running'))
    data = {'type': f'{stage}.{action}', 'stage': stage, 'action': action, 'seq': seq,
            'event_id': event.event_id, 'created_at': event.created_at,
            'message': payload.get('message') or '', 'payload': payload,
            'task_id': payload.get('stage') or payload.get('phase')}
    return _sse('message', data, str(seq))


def _action(status: str) -> str:
    return {'running': 'progress', 'success': 'finish', 'failed': 'failed', 'checkpointed': 'pause',
            'cancelled': 'cancel', 'skipped': 'finish'}.get(status, 'progress')


def _dataset_id(inputs: dict[str, Any]) -> str:
    return str(inputs.get('kb_id') or inputs.get('dataset_id') or inputs.get('dataset_name') or 'algo').strip()


def _chat_url(value: Any) -> str:
    url = str(value or os.getenv('LAZYMIND_EVO_TARGET_CHAT_URL') or 'http://chat:8046/api/chat/stream').strip()
    url = url.replace('http://evo-chat:', 'http://chat:')
    return f'{url}/stream' if url.endswith('/api/chat') else url


def _intent_label(action: str) -> str:
    labels = {
        'ask_clarification': '需要补充信息',
        'no_operations': '无需新增操作',
        'reject': '未通过当前能力边界',
        'resume_checkpointed': '继续执行 checkpoint 后续流程',
        'respond_to_user': '直接回复用户',
        'read_run_status_query': '查看当前流程进度',
        'read_artifact_query': '查看产物内容',
        'read_repair_artifact': '查看修复产物',
        'read_operation_query': '查看操作状态',
    }
    return labels.get(action, action.replace('_', ' '))


def _reply(result) -> str:
    label = _intent_label(str(result.action))
    if result.action == 'ask_clarification':
        return '我还需要你补充一点信息，才能继续规划下一步。'
    if result.action == 'no_operations':
        return '收到，这条消息不需要新增自进化操作。'
    if result.action == 'reject':
        return '这条消息超出了当前 checkpoint 允许的能力边界，请调整后重试。'
    if result.action == 'resume_checkpointed':
        return '已收到继续确认，正在恢复 checkpoint 后续流程。'
    status = '已完成' if result.results else '已识别'
    return f'{status}意图：{label}。'


def _interrupt_message(message: str) -> bool:
    text = message.lower()
    words = ('立即暂停', '暂停当前', '取消当前', '停止当前', 'cancel current')
    return any(word in text for word in words)


def _resume_message(message: str) -> bool:
    text = ''.join(message.lower().split()).strip('。.!！')
    return text in {'继续', '继续执行', '恢复', '恢复执行', '确认', '确认切流', '切流', 'resume', 'continue'}


def _checkpoint_stage(stage: str) -> str:
    return {'dataset_gen': 'dataset', 'run': 'analysis', 'apply': 'repair'}.get(stage, stage)


def _next_stage(checkpoint: dict[str, Any]) -> str:
    stage = str(checkpoint.get('stage') or '')
    return {'dataset': 'eval', 'dataset_gen': 'eval', 'eval': 'analysis', 'analysis': 'repair',
            'run': 'repair', 'repair': 'abtest', 'apply': 'abtest'}.get(stage, '')


def _next_operation(stage: str) -> str:
    return {'dataset': 'eval.run', 'dataset_gen': 'eval.run', 'eval': 'analysis.run', 'analysis': 'repair.run',
            'run': 'repair.run', 'repair': 'abtest.compare', 'apply': 'abtest.compare'}.get(stage, '')


def _stage_label(stage: str) -> str:
    labels = {
        'dataset': '数据集生成', 'eval': '评测', 'analysis': '分析', 'repair': '修复', 'abtest': 'ABTest'
    }
    return labels.get(stage, stage)


def _stage_from_operation(operation_run_id: str) -> str | None:
    prefix = operation_run_id.split('.', 1)[0]
    return {'dataset': 'dataset', 'eval': 'eval', 'candidate_eval': 'abtest', 'analysis': 'analysis',
            'repair': 'repair', 'abtest': 'abtest', 'intent': None}.get(prefix)


def _preview(result) -> list[dict]:
    return [
        {'op': ref, 'intent': result.action, 'humanized': _intent_label(result.action),
         'safety': 'normal', 'params_summary': {}}
        for ref in result.operation_refs
    ]


def _preview_copy_ignore(path: str, names: list[str]) -> set[str]:
    ignored = {name for name in names if '.tmp' in name}
    if Path(path).name == RUN_ID:
        ignored |= {'candidate', 'tmp'} & set(names)
    return ignored


def _chunks(text: str, size: int = 64) -> list[str]:
    return [text[i:i + size] for i in range(0, len(text), size)] or ['']


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


def _flow_status_row(
    thread_id: str, status: str, active_task_ids: list[str], *, latest_abtest_status: str | None = None,
    report_ready: bool = False, pending_checkpoint: dict | None = None,
) -> dict:
    return {'thread_id': thread_id, 'status': status, 'active_task_ids': active_task_ids,
            'latest_abtest_id': 'abtest_comparison' if latest_abtest_status else None,
            'latest_abtest_status': latest_abtest_status, 'report_ready': report_ready,
            'pending_checkpoint': pending_checkpoint}


def _stored_events(run_dir: Path) -> list[Event]:
    path = run_dir / 'events.jsonl'
    if not path.exists():
        return []
    rows = []
    for line in path.read_text(encoding='utf-8').splitlines():
        if line.strip():
            try:
                rows.append(Event(**json.loads(line)))
            except (TypeError, json.JSONDecodeError):
                continue
    return rows


def _checkpointed_operation_ids(run_dir: Path, projection: dict) -> list[str]:
    blockers = projection.get('blockers') or []
    if blockers:
        return [str(item) for item in blockers]
    operations = _read_json(run_dir / 'operations.json')
    return [str(data.get('operation_run_id') or key) for key, data in operations.items()
            if isinstance(data, dict) and data.get('status') == 'checkpointed']


def _checkpoint_ids(operation_ids: list[str]) -> dict | None:
    return {'checkpoint_id': operation_ids[0], 'message': 'operation paused, send continue to resume'} \
        if operation_ids else None


def _artifact_decision(run_dir: Path, artifact_id: str) -> dict:
    latest = sorted((run_dir / 'artifacts' / 'blobs' / artifact_id).glob('v*.json'))
    if not latest:
        return {}
    data = _read_json(latest[-1])
    decision = data.get('decision') if isinstance(data, dict) else {}
    return decision if isinstance(decision, dict) else {}


def _has_artifact(service: EvoFlowService, artifact_id: str) -> bool:
    try:
        service.artifacts.latest_ref(artifact_id)
        return True
    except KeyError:
        return False


def _artifact_payload(service: EvoFlowService, artifact_id: str) -> dict:
    try:
        return service.artifacts.get(service.artifacts.latest_ref(artifact_id))
    except KeyError:
        return {}


def _requires_manual_cutover(checkpoint: dict[str, Any]) -> bool:
    next_op = checkpoint.get('next_op') or {}
    op = str(next_op.get('op') if isinstance(next_op, dict) else next_op)
    text = f'{op} {checkpoint.get("message") or ""}'.lower()
    return 'candidate_cutover' in text or '切流' in text


def _artifact_row(service: EvoFlowService, artifact_id: str) -> dict:
    try:
        ref = service.artifacts.latest_ref(artifact_id)
    except KeyError:
        return {}
    data = service.artifacts.get(ref)
    if artifact_id == 'eval_dataset':
        data = _eval_dataset_with_cases(service, data)
    return {'artifact_id': artifact_id, 'artifact_ref': str(ref), 'schema': service.artifacts.schema_name(ref),
            'case_count': len(data.get('case_ids') or data.get('cases') or []), 'data': data}


def _eval_dataset_with_cases(service: EvoFlowService, data: dict) -> dict:
    cases = []
    for value in data.get('case_refs') or []:
        try:
            case = service.artifacts.get(ArtifactRef.parse(str(value)))
        except (KeyError, ValueError):
            continue
        if isinstance(case, dict):
            cases.append(case)
    return {**data, 'cases': cases} if cases else data


def _schema_rows(service: EvoFlowService, schemas: set[str]) -> list[dict]:
    rows = []
    for path in sorted(service.artifacts.manifest_dir.glob('*.json')):
        try:
            ref = service.artifacts.latest_ref(path.stem)
        except KeyError:
            continue
        if service.artifacts.schema_name(ref) in schemas:
            rows.append(_artifact_row(service, path.stem))
    return rows


def _checkpoint(refs: list[ArtifactRef]) -> dict | None:
    return {'checkpoint_id': str(refs[0]), 'message': 'operation paused, send continue to resume'} if refs else None
