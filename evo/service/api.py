from __future__ import annotations

import asyncio
import json
import os
import shutil
import threading
import time
import uuid
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Callable

from fastapi import Body, FastAPI, HTTPException, Request, Response
from sse_starlette.sse import EventSourceResponse

from evo import normalize_chat_stream_url, normalize_http_origin, validate_id
from evo.artifacts import ArtifactRef
from evo.checkpoints import CheckpointState, checkpoint_state_from_run, frontend_checkpoint_from_run
from evo.checkpoints.manager import _lifecycle_payload
from evo.checkpoints.models import RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS
from evo.projections import rebuild_frontend_state
from evo.service.flow import EvoFlowService, FlowMessageResult, TARGET_MEAN_DELTA, result_dict
from evo.store import Event, StoreRunLifecycle

BODY_REQUIRED = Body(...)
BODY_DEFAULT = Body(default_factory=dict)
RUN_ID = 'run_1'
MAX_CREATE_THREAD_CASES = 1000
MAX_CREATE_THREAD_WORKERS = 32
STAGE_MAP = {
    'dataset_corpus': 'dataset', 'dataset_gen': 'dataset', 'dataset': 'dataset', 'eval': 'eval',
    'candidate_eval': 'abtest', 'run': 'analysis', 'analysis': 'analysis', 'apply': 'repair', 'repair': 'repair',
    'abtest': 'abtest', 'repair_plan': 'repair', 'candidate_workspace': 'repair', 'repair_loop': 'repair',
    'candidate_service_start': 'abtest', 'candidate_service_stop': 'abtest', 'abtest_compare': 'abtest',
    'candidate_cutover': 'abtest',
}
RUN_STATUS_STAGES = (
    ('dataset', '数据集生成', ('eval_dataset', 'corpus_snapshot')),
    ('eval', '执行评测', ('eval_report',)),
    ('analysis', '错误分析', ('classification_report', 'repair_loop_plan')),
    ('repair', '代码优化', ('verified_repair', 'opencode_run_trace_attempt_1')),
    ('abtest', 'ABTest 和切流', ('abtest_comparison', 'candidate_algorithm_cutover')),
)
RESULT_ARTIFACT_IDS = {
    'datasets': ('eval_dataset',),
    'eval-reports': ('eval_report', 'candidate_eval_report'),
    'analysis-reports': ('classification_report', 'repair_loop_plan'),
    'abtests': ('abtest_comparison', 'candidate_algorithm_cutover'),
}
RESULT_ARTIFACT_SCHEMAS = {
    'analysis-reports': {'RepairLoopPlan', 'RepairEvidencePacket', 'FaultLocalizationReport', 'DiagnosticProbePlan',
                         'DiagnosticProbeResult', 'RepairDiagnosis', 'OpenCodeInstruction'},
    'diffs': {'OpenCodeRunTrace', 'OpenCodeWorkerReport', 'CodePatchCandidate', 'CandidateServiceRun',
              'RepairEvaluation', 'PatchCorrectnessAssessment', 'PatchCritique', 'BranchDecision', 'RepairBranchState',
              'RepairStateTransition', 'RepairHypothesis', 'RepairPlan', 'CandidateClassificationReport',
              'RepairLoopDecision', 'RepairLoopMemory', 'RepairLoopState', 'VerifiedRepair'},
}


@dataclass(frozen=True)
class QueuedDrainResult:
    blocked: bool = False
    confirmation_result: FlowMessageResult | None = None
    applied_count: int = 0
    remaining_count: int = 0


class CheckpointMessageController:
    def __init__(self, hub: 'EvoMessageHub'):
        self.hub = hub

    def handle(self, thread_id: str, service: EvoFlowService, checkpoint: CheckpointState, message_id: str,
               content: str, payload: dict[str, Any]) -> dict:
        input_policy = _resume_input_policy(payload)
        allowed_capabilities = payload.get('allowed_capabilities')
        if checkpoint.is_manual_cutover:
            allowed = (
                list(allowed_capabilities) if isinstance(allowed_capabilities, list)
                else service.registry.capability_ids()
            )
            allowed_capabilities = [
                capability for capability in allowed if capability != 'cutover_candidate_algorithm'
            ]
        result = service.send_checkpoint_message(
            message_id, content, allowed_capabilities=allowed_capabilities,
            dispatch=bool(payload.get('dispatch', True)), max_dispatch=int(payload.get('max_dispatch') or 1),
        )
        return self._handle_checkpoint_result(thread_id, service, checkpoint, message_id, result, input_policy)

    def _handle_checkpoint_result(self, thread_id: str, service: EvoFlowService, checkpoint: CheckpointState,
                                  message_id: str, result: FlowMessageResult, input_policy: str) -> dict:
        resumed_checkpoint = False
        if result.action == 'confirm_intent_operation':
            parent = service.checkpoints.active_checkpoint(RUN_ID)
            if parent and parent.checkpoint_kind == 'stage_gate' and service.confirmation_succeeded(result):
                input_policy = _default_resume_input_policy(parent, input_policy)
                self.hub._apply_confirmed_intent_at_stage_gate(thread_id, service, parent, input_policy)
                reply = '修改已应用，测试集已更新。点击「继续执行」进入评测阶段。'
            else:
                self.hub._cache_active_checkpoint(thread_id, service) if parent else self.hub._clear_stage_checkpoint(
                    thread_id)
                reply = self.hub._result_reply(thread_id, service, result)
        elif result.action == 'resume_checkpointed':
            if checkpoint.is_manual_cutover:
                reply = '候选算法切流需要前端调用继续接口并显式确认切流。'
                return self._reply(thread_id, message_id, reply, result, requires_confirmation=True,
                                   confirmation_checkpoint_id=checkpoint.checkpoint_id)
            input_policy = _default_resume_input_policy(checkpoint, input_policy)
            resumed_checkpoint = self.hub._resume_stage_checkpoint(thread_id, service, checkpoint, 'message',
                                                                   input_policy)
            result = _stage_checkpoint_resumed_result(message_id, checkpoint, input_policy)
            reply = f'已继续：{checkpoint.next_stage or "下一阶段"}。' if resumed_checkpoint \
                else '已应用排队干预，当前操作需要确认。'
        elif _completed_manual_cutover(checkpoint, result, service):
            input_policy = _default_resume_input_policy(checkpoint, input_policy)
            resumed_checkpoint = self.hub._resume_stage_checkpoint(thread_id, service, checkpoint, 'message',
                                                                   input_policy)
            reply = '已完成候选算法切流，正在收尾当前流程。' if resumed_checkpoint \
                else '已完成候选算法切流，但排队干预需要确认。'
        else:
            reply = self.hub._result_reply(thread_id, service, result)
            if result.action != 'read_run_status_query': reply += ' 当前仍在 checkpoint，已记录这条干预。'
        if result.requires_confirmation: self.hub._cache_active_checkpoint(thread_id, service)
        checkpoint_requires_confirmation = (checkpoint.is_manual_cutover and not resumed_checkpoint
                                            and not _completed_manual_cutover(checkpoint, result, service))
        return self._reply(
            thread_id, message_id, reply, result,
            requires_confirmation=result.requires_confirmation or checkpoint_requires_confirmation,
            confirmation_checkpoint_id=(result.confirmation_checkpoint_id
                                        or (checkpoint.checkpoint_id if checkpoint_requires_confirmation else '')),
        )

    def _reply(self, thread_id: str, message_id: str, reply: str, result: FlowMessageResult, *,
               requires_confirmation: bool | None = None, confirmation_checkpoint_id: str | None = None) -> dict:
        return self.hub._message_response(thread_id, message_id, reply, result,
                                          requires_confirmation=requires_confirmation,
                                          confirmation_checkpoint_id=confirmation_checkpoint_id)


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
        rows = [hub.flow_status(meta['id']) | {'title': meta.get('title', ''),
                                               'mode': meta.get('mode', 'interactive'),
                                               'created_at': meta.get('created_at'),
                                               'updated_at': meta.get('updated_at')}
                for meta in hub.list_threads()]
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

    @app.post('/v1/evo/threads/{thread_id}/continue')
    async def continue_thread(thread_id: str, body: dict = BODY_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.continue_thread, thread_id, body)

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

    @app.get('/v1/evo/threads/{thread_id}/artifacts/{artifact_id}')
    def artifact(thread_id: str, artifact_id: str) -> dict:
        return hub.artifact(thread_id, artifact_id)

    @app.get('/v1/evo/threads/{thread_id}/reports/{report_id}/content')
    def thread_report_content(thread_id: str, report_id: str, fmt: str = ''):
        content = hub.report_content(thread_id, report_id)
        if fmt in {'md', 'markdown', 'text'}: return Response(content, media_type='text/markdown; charset=utf-8')
        return {'thread_id': thread_id, 'report_id': report_id, 'content': content}

    @app.get('/v1/evo/reports/{report_id}/content')
    def report_content(report_id: str, fmt: str = ''):
        thread_id, artifact = _scoped_report_id(report_id)
        content = hub.report_content(thread_id, artifact)
        if fmt in {'md', 'markdown', 'text'}: return Response(content, media_type='text/markdown; charset=utf-8')
        return {'thread_id': thread_id, 'report_id': artifact, 'content': content}

    @app.get('/v1/evo/diffs/{apply_id}/{filename:path}')
    def diff_content(apply_id: str, filename: str) -> Response:
        return Response(hub.diff_content(apply_id, filename), media_type='text/x-diff; charset=utf-8')

    return app


def get_app() -> FastAPI:
    return create_app()


class ThreadDispatchGate:
    def __init__(self, hub: 'EvoMessageHub', thread_id: str):
        self.hub = hub
        self.thread_id = thread_id

    def can_dispatch(self, run_id: str) -> bool:
        del run_id
        try:
            status = str(self.hub._meta(self.thread_id).get('status') or '')
        except HTTPException:
            return False
        return status not in {'paused', 'cancelled', 'deleting'}


class ContinuationPolicyResolver:
    @staticmethod
    def resolve(payload: dict[str, Any] | None = None, checkpoint: CheckpointState | None = None) -> str:
        del checkpoint
        payload = payload or {}
        value = str(payload.get('input_policy') or '').strip()
        if not value and payload.get('restart_from_snapshot'):
            value = RESUME_FROM_SNAPSHOT
        if not value:
            value = RESUME_WITH_INTERVENTIONS
        if value not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
            expected = f'{RESUME_WITH_INTERVENTIONS} or {RESUME_FROM_SNAPSHOT}'
            raise HTTPException(400, f'bad input_policy {value!r}; expected {expected}')
        return value


class EvoMessageHub:
    def __init__(self, base_dir: Path):
        self.base_dir = base_dir
        self.threads_dir = base_dir / 'state' / 'threads'
        self._services: dict[str, EvoFlowService] = {}
        self._tasks: dict[str, threading.Thread] = {}
        self._checkpoint_events: dict[str, threading.Event] = {}
        self._queued_messages: dict[str, list[dict[str, Any]]] = {}
        self._lock = threading.RLock()
        self._checkpoint_messages = CheckpointMessageController(self)

    def create_thread(self, payload: dict[str, Any]) -> dict:
        mode = str(payload.get('mode') or 'interactive').strip()
        if mode not in {'auto', 'interactive'}: raise HTTPException(400, f'bad mode {mode!r}')
        thread_id, now = f'thr-{uuid.uuid4().hex[:8]}', time.time()
        try:
            inputs = _normalize_inputs(dict(payload.get('inputs') or {}))
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        meta = {'id': thread_id, 'thread_id': thread_id, 'mode': mode, 'title': str(payload.get('title') or ''),
                'inputs': inputs, 'model_config': payload.get('llm_config') or {}, 'status': 'idle',
                'created_at': now, 'updated_at': now}
        self._write_meta(thread_id, meta)
        if mode == 'auto' and payload.get('start_auto'): self.start(thread_id, payload)
        return meta

    def list_threads(self) -> list[dict]:
        rows = [_read_json(path) for path in self.threads_dir.glob('*/thread.json')]
        return sorted([row for row in rows if row], key=lambda row: row.get('updated_at') or 0, reverse=True)

    def get_thread(self, thread_id: str) -> dict:
        return self._meta(thread_id)

    def delete_thread(self, thread_id: str) -> dict:
        self._meta(thread_id)
        cancelled = False
        service = self._service(thread_id) if thread_id in self._services or self._has_run(thread_id) else None
        if service and self._manual_cutover_pending(service):
            raise HTTPException(409, f'thread {thread_id} is running manual cutover')
        task = self._tasks.get(thread_id)
        if task and task.is_alive():
            self._update_meta(thread_id, status='deleting', pending_checkpoint=None, updated_at=time.time())
            self.cancel(thread_id)
            cancelled = True
            task.join(timeout=5)
            if task.is_alive(): raise HTTPException(409, f'thread {thread_id} is still running')
        self._queued_messages.pop(thread_id, None)
        self._checkpoint_events.pop(thread_id, None)
        service = self._services.pop(thread_id, None)
        run_root, thread_dir = self.base_dir / 'dev-runs' / thread_id, self._thread_dir(thread_id)
        run_deleted, thread_deleted = run_root.exists(), thread_dir.exists()
        if service:
            run_deleted = service.delete()
        elif run_deleted:
            EvoFlowService.delete_run(run_root=run_root, run_id=RUN_ID)
        shutil.rmtree(thread_dir, ignore_errors=True)
        return {'thread_id': thread_id, 'deleted_run': run_deleted, 'deleted_thread': thread_deleted,
                'cancelled': cancelled}

    def history(self, thread_id: str) -> dict:
        return {'thread_id': thread_id, 'messages': _read_messages(self._thread_dir(thread_id) / 'messages.jsonl')}

    def start(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        self._meta(thread_id)
        with self._lock:
            checkpoint = self._stage_checkpoint(thread_id)
            if checkpoint:
                return {'status': 'waiting_checkpoint', 'thread_id': thread_id, 'task_id': thread_id,
                        'checkpoint_id': checkpoint.checkpoint_id}
            if self._task_alive(thread_id):
                return {'status': 'running', 'thread_id': thread_id, 'task_id': thread_id}
            self._clear_stage_checkpoint(thread_id)
            self._start_flow_task_locked(thread_id, self._resume_start_stage(thread_id))
        return {'status': 'running', 'thread_id': thread_id, 'task_id': thread_id}

    def _resume_start_stage(self, thread_id: str) -> str:
        """Restarting a thread must not redo finished stages: once eval_dataset exists the
        flow starts at eval, where per-stage artifact-match guards skip completed work."""
        if not self._has_run(thread_id): return 'dataset'
        try:
            self._service(thread_id).artifacts.latest_ref('eval_dataset')
        except KeyError:
            return 'dataset'
        return 'eval'

    def pause(self, thread_id: str) -> dict:
        service = self._service(thread_id)
        StoreRunLifecycle(service.store, RUN_ID).mark_paused(thread_id=thread_id)
        self._update_meta(thread_id, status='paused', updated_at=time.time())
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        task = self._tasks.get(thread_id)
        if task and task.is_alive():
            task.join(timeout=5)
        if not (task and task.is_alive()):
            self._checkpoint_orphaned_running_operations(service)
        return {'status': 'paused', 'thread_id': thread_id}

    def _checkpoint_orphaned_running_operations(self, service: EvoFlowService) -> None:
        for ref in service.graph.run_refs({'running'}):
            service.graph.checkpoint_run(ref)

    def cancel(self, thread_id: str) -> dict:
        service = self._service(thread_id)
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        self._queued_messages.pop(thread_id, None)
        service.checkpoints.cancel_active(RUN_ID, thread_id=thread_id)
        StoreRunLifecycle(service.store, RUN_ID).mark_cancelled(thread_id=thread_id)
        self._update_meta(thread_id, status='cancelled', pending_checkpoint=None, updated_at=time.time())
        event = self._checkpoint_events.get(thread_id)
        if event: event.set()
        return {'status': 'cancelled', 'thread_id': thread_id}

    def retry(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        return self.continue_thread(thread_id, payload)

    def continue_thread(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        payload = payload or {}
        self._meta(thread_id)
        if self._task_alive(thread_id):
            return {'status': 'running', 'thread_id': thread_id, 'resumed': False, 'block_reason': 'flow_busy'}
        if not self._has_run(thread_id) and thread_id not in self._services:
            raise HTTPException(409, 'thread has no flow to continue')
        service = self._service(thread_id)
        checkpoint = self._stage_checkpoint(thread_id)
        if checkpoint and checkpoint.is_intent_confirmation and not payload.get('confirm_intent'):
            return {'status': 'waiting_checkpoint', 'thread_id': thread_id, 'resumed': False,
                    'block_reason': 'intent_confirmation_required'}
        if checkpoint and checkpoint.is_manual_cutover and not payload.get('confirm_cutover'):
            return {'status': 'waiting_checkpoint', 'thread_id': thread_id, 'resumed': False,
                    'block_reason': 'manual_cutover_confirmation_required'}

        policy = ContinuationPolicyResolver.resolve(payload, checkpoint)
        if checkpoint and checkpoint.is_intent_confirmation:
            result = self._execute_intent_confirmation(
                thread_id, service, checkpoint, str(payload.get('message_id') or f'continue_{uuid.uuid4().hex[:8]}'),
                input_policy=policy,
            )
            return {'status': self.flow_status(thread_id)['status'], 'thread_id': thread_id,
                    'resumed': bool(result.raw.get('parent_resumed', False)),
                    'intent_applied': bool(result.raw.get('intent_applied', False)),
                    'action': result.action}
        if checkpoint and checkpoint.is_manual_cutover:
            result = self._confirm_manual_cutover(
                thread_id, service, checkpoint, str(payload.get('message_id') or f'continue_{uuid.uuid4().hex[:8]}'),
                policy,
            )
            return {'status': self.flow_status(thread_id)['status'], 'thread_id': thread_id, 'resumed': True,
                    'action': result.action, 'input_policy': policy}
        if checkpoint:
            resumed = self._resume_stage_checkpoint(thread_id, service, checkpoint, 'continue', policy)
            return {'status': self.flow_status(thread_id)['status'], 'thread_id': thread_id, 'resumed': resumed,
                    'input_policy': policy, 'next_stage': checkpoint.next_stage}

        if service.graph.run_refs({'checkpointed'}):
            service.resume_checkpointed(input_policy=policy, dispatch=False)
            self._update_meta(thread_id, status='running', pending_checkpoint=None, updated_at=time.time())
            self._start_flow_task_locked(thread_id, self._resume_start_stage(thread_id))
            return {'status': 'running', 'thread_id': thread_id, 'resumed': True, 'input_policy': policy}

        if str(self._meta(thread_id).get('status') or '') == 'paused':
            self._update_meta(thread_id, status='running', pending_checkpoint=None, updated_at=time.time())
            self._start_flow_task_locked(thread_id, self._resume_start_stage(thread_id))
            return {'status': 'running', 'thread_id': thread_id, 'resumed': True}

        raise HTTPException(409, 'thread has no checkpoint or paused work to continue')

    def post_message(self, thread_id: str, payload: dict[str, Any]) -> dict:
        content = str(payload.get('content') or payload.get('message') or '').strip()
        if not content: raise HTTPException(400, 'message content required')
        message_id = str(payload.get('message_id') or f'msg_{thread_id}_{uuid.uuid4().hex[:8]}')
        self._append_message(thread_id, 'user', content)
        task_alive = self._task_alive(thread_id)
        checkpoint = self._stage_checkpoint(thread_id)
        if checkpoint:
            service = self._service(thread_id)
            return self._checkpoint_messages.handle(thread_id, service, checkpoint, message_id, content, payload)
        if task_alive:
            service = self._service(thread_id)
            result = self._preview_message(thread_id, service, message_id, content, payload)
            if result.action == 'read_run_status_query':
                return self._message_response(thread_id, message_id, self._result_reply(thread_id, service, result),
                                              result)
            if self._pause_running_for_message(thread_id, service):
                self._update_meta(thread_id, status='running', updated_at=time.time())
                result = service.send_message(message_id, content,
                                              allowed_capabilities=payload.get('allowed_capabilities'),
                                              dispatch=bool(payload.get('dispatch', True)),
                                              max_dispatch=int(payload.get('max_dispatch') or 1))
                if result.action == 'resume_checkpointed':
                    self._start_resumed_dispatch(thread_id)
                elif not result.requires_confirmation:
                    self._start_resumed_dispatch(thread_id)
                return self._message_response(thread_id, message_id, self._result_reply(thread_id, service, result),
                                              result)
            self._queued_messages.setdefault(thread_id, []).append({
                'message_id': message_id, 'content': content,
                'allowed_capabilities': payload.get('allowed_capabilities'),
                'dispatch': bool(payload.get('dispatch', True)),
                'max_dispatch': int(payload.get('max_dispatch') or 1), 'action': result.action,
            })
            return self._message_response(
                thread_id, message_id, '已收到你的消息，当前运行任务正在进入 checkpoint；状态就绪后会优先处理这条消息。',
                FlowMessageResult(message_id, result.raw, result.action, result.operation_refs, [], skipped=True),
                requires_confirmation=False, confirmation_checkpoint_id='',
                result_payload=_queued_preview_result_dict(result),
            )
        dispatch = bool(payload.get('dispatch', True))
        had_run = self._has_run(thread_id)
        service = self._service(thread_id)
        if not had_run: service.plan_full_flow()
        checkpoint = self._stage_checkpoint(thread_id)
        if checkpoint:
            return self._checkpoint_messages.handle(thread_id, service, checkpoint, message_id, content, payload)
        resume_stage = self._stalled_resume_stage(thread_id)
        result = self._preview_message(thread_id, service, message_id, content, payload) if not dispatch else (
            service.send_message(message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                                 dispatch=True, max_dispatch=int(payload.get('max_dispatch') or 1))
        )
        if result.action == 'resume_checkpointed' and resume_stage:
            self._start_resume_stage(thread_id, service, resume_stage, 'message')
            reply = f'已继续：{resume_stage}。'
        else:
            if result.action == 'resume_checkpointed': self._start_resumed_dispatch(thread_id)
            reply = self._result_reply(thread_id, service, result)
        return self._message_response(thread_id, message_id, reply, result)

    def _start_resumed_dispatch(self, thread_id: str) -> None:
        """resume_checkpointed only closes the checkpoint; dispatch runs in a flow task."""
        with self._lock:
            if self._task_alive(thread_id): return
            self._update_meta(thread_id, status='running', updated_at=time.time())
            self._start_flow_task_locked(thread_id, self._resume_start_stage(thread_id))

    def _pause_running_for_message(self, thread_id: str, service: EvoFlowService) -> bool:
        StoreRunLifecycle(service.store, RUN_ID).mark_paused(thread_id=thread_id, reason='message_preemption')
        self._update_meta(thread_id, status='paused', updated_at=time.time())
        refs = service.graph.run_refs({'running'})
        for ref in refs:
            service.runtime.request_interrupt(ref)
        task = self._tasks.get(thread_id)
        if task and task.is_alive():
            task.join(timeout=5)
        if task and task.is_alive():
            return False
        self._checkpoint_orphaned_running_operations(service)
        return True

    def _message_response(self, thread_id: str, message_id: str, reply: str, result: FlowMessageResult, *,
                          requires_confirmation: bool | None = None, confirmation_checkpoint_id: str | None = None,
                          result_payload: dict[str, Any] | None = None) -> dict:
        self._append_message(thread_id, 'assistant', reply)
        self._update_meta(thread_id, status=self.flow_status(thread_id)['status'], updated_at=time.time())
        return {
            'intent_id': message_id, 'reply': reply, 'thinking': '',
            'requires_confirm': result.requires_confirmation if requires_confirmation is None
            else requires_confirmation,
            'confirmation_checkpoint_id': result.confirmation_checkpoint_id if confirmation_checkpoint_id is None
            else confirmation_checkpoint_id,
            'preview': _preview(result), 'warnings': [], 'result': result_payload or result_dict(result),
        }

    def _result_reply(self, thread_id: str, service: EvoFlowService, result: FlowMessageResult) -> str:
        if result.action == 'read_run_status_query':
            return _run_status_reply(thread_id, service, self.flow_status(thread_id), self._meta(thread_id))
        return _intent_answer(service, result) or _reply(result)

    async def post_message_stream(self, thread_id: str, payload: dict[str, Any]):
        message_id = str(payload.get('message_id') or f'msg_{thread_id}_{uuid.uuid4().hex[:8]}')

        def emit(event: str, data: dict[str, Any]) -> dict:
            return _sse(event, {'thread_id': thread_id, 'message_id': message_id, **data})

        yield emit('intent_start', {})
        try:
            result = await asyncio.to_thread(self.post_message, thread_id, {**payload, 'message_id': message_id})
            for chunk in _chunks(result['reply']):
                yield emit('answer_delta', {'delta': chunk})
            yield emit('plan_ready', {'intent_id': result['intent_id'], 'actions': result['preview'],
                                      'warnings': result['warnings'],
                                      'requires_confirm': result.get('requires_confirm', False),
                                      'confirmation_checkpoint_id': result.get('confirmation_checkpoint_id', '')})
            for action in result['preview']:
                yield emit('action', {'intent_id': result['intent_id'], 'action': action})
            yield emit('done', {'intent_id': result['intent_id'],
                                'requires_confirm': result.get('requires_confirm', False),
                                'confirmation_checkpoint_id': result.get('confirmation_checkpoint_id', '')})
        except Exception as exc:
            yield emit('error', {'code': getattr(exc, 'code', 'MESSAGE_FAILED'), 'message': str(exc)})

    def flow_status(self, thread_id: str) -> dict:
        meta, task = self._meta(thread_id), self._tasks.get(thread_id)
        status = str(meta.get('status') or 'idle')
        active_task_ids = [thread_id] if task and task.is_alive() else []
        if thread_id not in self._services and not self._has_run(thread_id):
            visible = 'running' if active_task_ids else ('idle' if status == 'running' else status)
            return _flow_status_row(thread_id, visible, active_task_ids)
        if thread_id not in self._services:
            run_dir = self._run_dir(thread_id)
            projection = _read_json(run_dir / 'projections' / 'current.json')
            return _lifecycle_flow_status(thread_id, run_dir, projection, status, active_task_ids)
        service = self._service(thread_id)
        run_dir = service.store.run_dir(RUN_ID)
        projection = _read_json(run_dir / 'projections' / 'current.json') or rebuild_frontend_state(service.store,
                                                                                                    RUN_ID)
        return _lifecycle_flow_status(thread_id, run_dir, projection, status, active_task_ids)

    async def events(self, thread_id: str, since: int = 0):
        self._meta(thread_id)
        if thread_id not in self._services and not self._has_run(thread_id): return
        cursor, idle_ticks = max(0, since), 0
        while True:
            in_memory = thread_id in self._services
            events = self._service(thread_id).store.read_events(RUN_ID) if in_memory \
                else _stored_events(self._run_dir(thread_id))
            operations = _operations_by_id(self._service(thread_id)) if in_memory \
                else _read_json(self._run_dir(thread_id) / 'operations.json')
            event_rows = _event_rows(events)
            for seq, event in event_rows:
                if seq <= cursor: continue
                cursor = seq
                frame = _event_frame(event, seq, operations)
                if frame: yield frame
            status = self.flow_status(thread_id)['status']
            latest_sequence = event_rows[-1][0] if event_rows else 0
            if status in {'ended', 'failed', 'cancelled'} and cursor >= latest_sequence:
                yield _sse('done', {'thread_id': thread_id, 'status': status}, str(cursor + 1))
                return
            idle_ticks = idle_ticks + 1 if cursor >= latest_sequence else 0
            if status in {'idle', 'paused', 'waiting_checkpoint'} and idle_ticks > 4: return
            await asyncio.sleep(0.5)

    def results(self, thread_id: str, kind: str) -> list[dict]:
        self._meta(thread_id)
        rows = _stored_result_rows(self._run_dir(thread_id), kind)
        if rows is not None:
            return rows
        raise HTTPException(404, f'unknown result kind: {kind}')

    def artifact(self, thread_id: str, artifact_id: str) -> dict:
        row = _artifact_row(self._service(thread_id), artifact_id)
        if not row: raise HTTPException(404, f'artifact not found: {artifact_id}')
        return row

    def report_content(self, thread_id: str, report_id: str) -> str:
        data = self._thread_artifact_payload(thread_id, report_id)
        for key in ('markdown', 'report', 'content', 'text', 'summary'):
            value = data.get(key)
            if isinstance(value, str) and value.strip(): return value
        return json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True, default=str)

    def diff_content(self, apply_id: str, filename: str) -> str:
        data = self._artifact_payload_any(apply_id)
        diff = data.get('diff') or data.get('patch') or data.get('content') or ''
        if isinstance(diff, str) and diff.strip(): return diff
        files = data.get('files') or data.get('diff_files') or []
        for item in files if isinstance(files, list) else []:
            if not isinstance(item, dict): continue
            path = str(item.get('path') or item.get('filename') or '')
            if path == filename or path.endswith('/' + filename) or Path(path).name == filename:
                text = item.get('diff') or item.get('patch') or item.get('content') or ''
                if isinstance(text, str): return text
        raise HTTPException(404, f'diff content not found: {apply_id}/{filename}')

    def _run_full_flow(self, thread_id: str, start_stage: str = 'dataset') -> None:
        self._update_meta(thread_id, status='running', updated_at=time.time())
        try:
            service = self._service(thread_id)
            flow = service.run_full_flow(
                repair_plan_params={'target_mean_delta': TARGET_MEAN_DELTA, 'goodcase_guard_ratio': 0.5},
                start_stage=start_stage,
                after_stage=lambda stage, detail: self._after_stage(thread_id, service, stage, detail),
            )
            if self._meta(thread_id).get('status') == 'cancelled': return
            StoreRunLifecycle(service.store, RUN_ID).mark_ended(outcome='success')
            self._update_meta(thread_id, status='ended',
                              flow={k: [asdict(item) for item in v] for k, v in flow.items()},
                              pending_checkpoint=None, updated_at=time.time())
        except Exception as exc:
            current_status = str(self._meta(thread_id).get('status') or '')
            service = self._service(thread_id)
            status = 'cancelled' if current_status == 'cancelled' else (
                'paused' if service.graph.run_refs({'checkpointed'}) else 'failed')
            lifecycle = StoreRunLifecycle(service.store, RUN_ID)
            if status == 'cancelled':
                service.checkpoints.cancel_active(RUN_ID, thread_id=thread_id)
                lifecycle.mark_cancelled(error_type=exc.__class__.__name__, message=str(exc))
            elif status == 'paused':
                lifecycle.mark_paused(error_type=exc.__class__.__name__, message=str(exc))
            else:
                service.checkpoints.cancel_active(RUN_ID, thread_id=thread_id)
                lifecycle.mark_failed(error_type=exc.__class__.__name__, message=str(exc))
            self._update_meta(thread_id, status=status,
                              error={'type': exc.__class__.__name__, 'message': str(exc)}, updated_at=time.time())
        finally:
            # A paused flow keeps its pending checkpoint so the thread can be resumed
            # (even after a service restart); only terminal outcomes clear it.
            if str(self._meta(thread_id).get('status') or '') not in {'paused', 'waiting_checkpoint'}:
                self._clear_stage_checkpoint(thread_id)
            else:
                event = self._checkpoint_events.get(thread_id)
                if event: event.set()

    def _after_stage(self, thread_id: str, service: EvoFlowService, stage: str, detail: dict[str, Any]) -> None:
        if self._stopped(thread_id): raise RuntimeError('flow cancelled')
        if detail.get('terminal'): return
        checkpoint = service.checkpoints.create_checkpoint(RUN_ID, None, f'{stage} stage finished')
        if not detail.get('next_stage') or not detail.get('next_op'):
            raise RuntimeError(f'{stage} checkpoint missing next stage metadata')
        event = self._checkpoint_events.setdefault(thread_id, threading.Event())
        event.clear()
        stage_checkpoint = service.checkpoints.record_stage_wait(
            RUN_ID, checkpoint.checkpoint_id, stage=_checkpoint_stage(stage), next_stage=str(detail['next_stage']),
            message=str(detail.get('message') or f'{_stage_label(stage)}已完成，请确认是否继续执行下一步。'),
            checkpoint_kind=str(detail.get('checkpoint_kind') or 'stage_gate'), next_op=str(detail['next_op']),
            detail=detail,
        )
        self._update_meta(thread_id, status='waiting_checkpoint',
                          pending_checkpoint=stage_checkpoint.frontend_payload(), updated_at=time.time())
        if self._meta(thread_id).get('mode') == 'auto':
            if stage_checkpoint.is_manual_cutover:
                self._auto_hold_stage(thread_id, service, stage_checkpoint)
            elif self._auto_continue_stage(thread_id, service, stage_checkpoint):
                return
        while not event.wait(1):
            if self._stopped(thread_id):
                raise RuntimeError(f'{thread_id} stopped while waiting for checkpoint {checkpoint.checkpoint_id}')
        if self._stopped(thread_id):
            raise RuntimeError(f'{thread_id} stopped while waiting for checkpoint {checkpoint.checkpoint_id}')

    def _auto_continue_stage(self, thread_id: str, service: EvoFlowService, checkpoint: CheckpointState) -> bool:
        queued = self._apply_queued_messages(thread_id, service)
        if queued.blocked and queued.confirmation_result:
            self._cache_active_checkpoint(thread_id, service)
            self._append_message(thread_id, 'assistant',
                                 f'AutoOperator 已应用前端干预：{queued.confirmation_result.action}，等待用户确认。')
            return False
        message = f'AutoOperator 已分析 {checkpoint.stage} checkpoint，继续执行。'
        service.store.append_event(Event('autooperator.analysis', RUN_ID, {
            'checkpoint_id': checkpoint.checkpoint_id, 'stage': checkpoint.stage,
            'next_stage': checkpoint.next_stage, 'message': message,
        }))
        self._append_message(thread_id, 'assistant', message)
        # AutoOperator always adopts interventions: queued frontend edits were just applied above.
        self._resume_stage_checkpoint(thread_id, service, checkpoint, 'autooperator', RESUME_WITH_INTERVENTIONS)
        return True

    def _auto_hold_stage(self, thread_id: str, service: EvoFlowService, checkpoint: CheckpointState) -> None:
        self._hold_queued_messages(thread_id, service)
        message = 'AutoOperator 已完成 ABTest 分析，候选算法切流需要用户确认。'
        service.store.append_event(Event('autooperator.analysis', RUN_ID, {
            'checkpoint_id': checkpoint.checkpoint_id, 'stage': checkpoint.stage,
            'next_stage': checkpoint.next_stage, 'message': message,
        }))
        self._append_message(thread_id, 'assistant', message)

    def _hold_queued_messages(self, thread_id: str, service: EvoFlowService) -> None:
        for item in self._queued_messages.pop(thread_id, []):
            service.store.append_event(Event('autooperator.intervention_observed', RUN_ID, {
                'message_id': item['message_id'], 'action': item.get('action') or 'manual_cutover_hold',
                'message': 'AutoOperator 已记录前端干预消息，候选算法切流等待用户显式确认。',
            }))

    def _apply_queued_messages(self, thread_id: str, service: EvoFlowService) -> QueuedDrainResult:
        messages = self._queued_messages.pop(thread_id, [])
        applied_count = 0
        for index, item in enumerate(messages):
            if not item.get('dispatch'):
                service.store.append_event(Event('autooperator.intervention_observed', RUN_ID, {
                    'message_id': item['message_id'], 'action': item.get('action') or 'preview',
                    'message': 'AutoOperator 已记录前端干预消息，等待 checkpoint 处理。',
                }))
                applied_count += 1
                continue
            result = service.send_checkpoint_message(
                item['message_id'], item['content'], allowed_capabilities=item.get('allowed_capabilities'),
                dispatch=bool(item.get('dispatch')), max_dispatch=int(item.get('max_dispatch') or 1),
            )
            applied_count += 1
            service.store.append_event(Event('autooperator.intervention_applied', RUN_ID, {
                'message_id': item['message_id'], 'action': result.action,
                'operation_refs': list(result.operation_refs),
                'message': f'AutoOperator 已应用前端干预：{result.action}。',
            }))
            if result.requires_confirmation:
                remaining = messages[index + 1:] + self._queued_messages.get(thread_id, [])
                if remaining: self._queued_messages[thread_id] = remaining
                return QueuedDrainResult(blocked=True, confirmation_result=result, applied_count=applied_count,
                                         remaining_count=len(remaining))
        return QueuedDrainResult(applied_count=applied_count)

    def _resume_stage_checkpoint(self, thread_id: str, service: EvoFlowService, checkpoint: CheckpointState,
                                 source: str, input_policy: str) -> bool:
        start_stage = checkpoint.next_stage or _blocked_operations_stage(checkpoint)
        if not start_stage: raise RuntimeError('checkpoint missing next_stage')
        if checkpoint.is_manual_cutover:
            self._hold_queued_messages(thread_id, service)
        else:
            queued = self._apply_queued_messages(thread_id, service)
            if queued.blocked:
                self._cache_active_checkpoint(thread_id, service)
                return False

        if checkpoint.dispatch_block_reason == 'checkpointed':
            service.resume_checkpointed(input_policy=input_policy, dispatch=False)
        else:
            service.resume_stage_checkpoint(checkpoint, source=source, input_policy=input_policy, thread_id=thread_id)
        with self._lock:
            alive = self._task_alive(thread_id)
            self._clear_stage_checkpoint(thread_id)
            self._update_meta(thread_id, status='running', updated_at=time.time())
            if not alive: self._start_flow_task_locked(thread_id, start_stage)
        return True

    def _cache_active_checkpoint(self, thread_id: str, service: EvoFlowService) -> None:
        self._update_meta(thread_id, pending_checkpoint=service.checkpoints.frontend_checkpoint(RUN_ID),
                          status='waiting_checkpoint', updated_at=time.time())

    def _apply_confirmed_intent_at_stage_gate(self, thread_id: str, service: EvoFlowService,
                                              parent: CheckpointState, input_policy: str) -> None:
        service.apply_stage_interventions(parent.checkpoint_id, input_policy)
        run_path = service.store.run_dir(RUN_ID) / 'run.json'
        run_data = service.store.read_json(run_path) if run_path.exists() else {}
        lifecycle = StoreRunLifecycle(service.store, RUN_ID)
        if run_data.get('status') == 'ended':
            lifecycle.mark_running()
        if service.checkpoints.active_checkpoint(RUN_ID) is None:
            lifecycle.block_dispatch('checkpoint_wait', **_lifecycle_payload(parent.frontend_payload()))
        self._cache_active_checkpoint(thread_id, service)

    def _confirm_manual_cutover(self, thread_id: str, service: EvoFlowService, checkpoint: CheckpointState | None,
                                message_id: str, input_policy: str) -> FlowMessageResult:
        with self._lock:
            if _has_artifact(service, 'candidate_algorithm_cutover'):
                return self._manual_cutover_result(message_id, already_done=True)
            active = service.checkpoints.active_checkpoint(RUN_ID)
            if active and checkpoint and active.checkpoint_id == checkpoint.checkpoint_id and active.is_manual_cutover:
                resumed = self._resume_stage_checkpoint(thread_id, service, active, 'manual_cutover', input_policy)
                if not resumed:
                    raise RuntimeError('manual cutover confirmation was blocked by a queued intervention')
                return self._manual_cutover_result(message_id, already_done=False)
            if service.manual_cutover_confirmed():
                self._ensure_cutover_flow_running(thread_id, service)
                return self._manual_cutover_result(message_id, already_done=False)
        raise RuntimeError('manual cutover confirmation requires an active checkpoint')

    def _execute_intent_confirmation(
        self, thread_id: str, service: EvoFlowService, checkpoint: CheckpointState,
        message_id: str, input_policy: str = RESUME_WITH_INTERVENTIONS,
    ) -> FlowMessageResult:
        result = service.confirm_checkpoint(checkpoint.checkpoint_id, message_id)
        parent = service.checkpoints.active_checkpoint(RUN_ID)
        if parent and parent.checkpoint_kind == 'stage_gate' and service.confirmation_succeeded(result):
            self._apply_confirmed_intent_at_stage_gate(thread_id, service, parent, input_policy)
            result.raw['parent_resumed'] = False
            result.raw['intent_applied'] = True
        elif parent:
            result.raw['parent_resumed'] = False
            self._cache_active_checkpoint(thread_id, service)
        else:
            result.raw['parent_resumed'] = True
            self._clear_stage_checkpoint(thread_id)
        return result

    def _manual_cutover_result(self, message_id: str, *, already_done: bool) -> FlowMessageResult:
        return FlowMessageResult(message_id,
                                 {'next_task': {'type': 'manual_cutover_confirmation', 'already_done': already_done}},
                                 'cutover_candidate_algorithm', [], [])

    def _manual_cutover_pending(self, service: EvoFlowService) -> bool:
        return service.manual_cutover_confirmed() and not _has_artifact(service, 'candidate_algorithm_cutover')

    def _ensure_cutover_flow_running(self, thread_id: str, service: EvoFlowService) -> None:
        # Cutover is a standalone action on an accepted comparison; it never replays the abtest stage.
        with self._lock:
            if self._task_alive(thread_id):
                self._update_meta(thread_id, status='running', updated_at=time.time())
                return
            task = threading.Thread(target=self._run_cutover_task, args=(thread_id, service), daemon=True)
            self._tasks[thread_id] = task
            task.start()

    def _run_cutover_task(self, thread_id: str, service: EvoFlowService) -> None:
        self._update_meta(thread_id, status='running', updated_at=time.time())
        lifecycle = StoreRunLifecycle(service.store, RUN_ID)
        try:
            service.execute_candidate_cutover('msg_manual_cutover')
            lifecycle.mark_ended(outcome='success')
            self._update_meta(thread_id, status='ended', pending_checkpoint=None, updated_at=time.time())
        except Exception as exc:
            lifecycle.mark_failed(error_type=exc.__class__.__name__, message=str(exc))
            self._update_meta(thread_id, status='failed',
                              error={'type': exc.__class__.__name__, 'message': str(exc)}, updated_at=time.time())

    def _start_resume_stage(self, thread_id: str, service: EvoFlowService, start_stage: str, source: str) -> None:
        # Recovery of a stage whose checkpoint was already resumed (flow task died afterwards):
        # the original resume already applied its policy, so the restart replays from snapshot.
        service.checkpoints.record_resume(
            RUN_ID, validate_id(f'recovered_{start_stage or "stage"}', 'checkpoint_id'),
            input_policy=RESUME_FROM_SNAPSHOT, next_operations=[], rebound_input_refs={},
            resume_context={'kind': 'stage', 'stage': '', 'next_stage': str(start_stage or ''),
                            'source': str(source or ''), 'recovered': True},
        )
        with self._lock:
            if not self._task_alive(thread_id):
                self._start_flow_task_locked(thread_id, start_stage)
            else:
                self._update_meta(thread_id, status='running', updated_at=time.time())

    def _start_flow_task_locked(self, thread_id: str, start_stage: str = 'dataset') -> None:
        task = threading.Thread(target=self._run_full_flow, args=(thread_id, start_stage), daemon=True)
        self._tasks[thread_id] = task
        task.start()

    def _stage_checkpoint(self, thread_id: str) -> CheckpointState | None:
        if self._stopped(thread_id): return None
        if thread_id in self._services:
            return self._service(thread_id).checkpoints.active_checkpoint(RUN_ID)
        run = _read_json(self._run_dir(thread_id) / 'run.json')
        lifecycle_checkpoint = checkpoint_state_from_run(run)
        if lifecycle_checkpoint and _lifecycle_status(run) == 'waiting_checkpoint': return lifecycle_checkpoint
        return None

    def _clear_stage_checkpoint(self, thread_id: str) -> None:
        event = self._checkpoint_events.get(thread_id)
        if event: event.set()
        try:
            self._update_meta(thread_id, pending_checkpoint=None, updated_at=time.time())
        except HTTPException:
            pass

    def _task_alive(self, thread_id: str) -> bool:
        task = self._tasks.get(thread_id)
        return bool(task and task.is_alive())

    def _stopped(self, thread_id: str) -> bool:
        return str(self._meta(thread_id).get('status') or '') in {'cancelled', 'deleting', 'failed'}

    def _stalled_resume_stage(self, thread_id: str) -> str:
        events = self._service(thread_id).store.read_events(RUN_ID) if thread_id in self._services \
            else _stored_events(self._run_dir(thread_id))
        start_stage, offset = '', -1
        for index, event in enumerate(events):
            if event.event_type == 'checkpoint.continue':
                stage = str(((event.payload or {}).get('resume_context') or {}).get('next_stage') or '')
                if stage: start_stage, offset = stage, index
        if not start_stage: return ''
        for event in events[offset + 1:]:
            payload = event.payload or {}
            stage = STAGE_MAP.get(str(payload.get('stage') or payload.get('phase') or ''))
            if stage == start_stage or event.event_type.startswith('checkpoint.wait'): return ''
        return start_stage

    def _has_run(self, thread_id: str) -> bool:
        return self._run_dir(thread_id).exists()

    def _service(self, thread_id: str) -> EvoFlowService:
        with self._lock:
            if thread_id in self._services: return self._services[thread_id]
            run_root = self.base_dir / 'dev-runs' / thread_id
            kwargs = self._service_kwargs(thread_id, run_root)
            service = EvoFlowService.resume(**kwargs) if (run_root / 'store' / 'runs' / RUN_ID).exists() \
                else EvoFlowService(**kwargs)
            self._services[thread_id] = service
            return service

    def _service_kwargs(self, thread_id: str, run_root: Path) -> dict[str, Any]:
        meta = self._meta(thread_id)
        raw_inputs = dict(meta.get('inputs') or {})
        try:
            inputs = _normalize_inputs(raw_inputs)
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        if inputs != raw_inputs: self._update_meta(thread_id, inputs=inputs, updated_at=time.time())
        return {'run_root': run_root, 'run_id': RUN_ID, 'dataset_id': _dataset_id(inputs),
                'target_chat_url': str(inputs['target_chat_url']),
                'candidate_chat_url': str(inputs.get('candidate_chat_url') or ''),
                'router_admin_url': str(inputs.get('router_admin_url') or ''),
                'case_count': int(inputs.get('num_cases') or os.getenv('EVO_FLOW_CASE_COUNT', '20')),
                'max_workers': int(inputs.get('max_workers') or os.getenv('EVO_FLOW_WORKERS', '2')),
                'model_config': meta.get('model_config') or None,
                'dispatch_gate': ThreadDispatchGate(self, thread_id)}

    def _preview_message(self, thread_id: str, service: EvoFlowService, message_id: str, content: str,
                         payload: dict[str, Any]) -> FlowMessageResult:
        root = self.base_dir / 'dev-runs' / thread_id / 'tmp' / message_id
        shutil.rmtree(root, ignore_errors=True)
        root.parent.mkdir(parents=True, exist_ok=True)
        with service.store.artifact_graph(RUN_ID).snapshot_lock():
            shutil.copytree(service.run_root / 'store', root / 'store', ignore=_preview_copy_ignore)
        try:
            runner = EvoFlowService.resume(**self._service_kwargs(thread_id, root))
            StoreRunLifecycle(runner.store, RUN_ID).open_dispatch(checkpoint_close_verified=True, preview=True)
            return runner.send_message(message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                                       dispatch=False, max_dispatch=int(payload.get('max_dispatch') or 1))
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def _meta(self, thread_id: str) -> dict:
        meta = _read_json(self._thread_dir(thread_id) / 'thread.json')
        if not meta: raise HTTPException(404, f'thread {thread_id} not found')
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

    def _artifact_payload_any(self, artifact: str) -> dict[str, Any]:
        artifact = artifact.strip()
        if not artifact: raise HTTPException(400, 'artifact id required')
        errors = []
        for meta in self.list_threads():
            if not self._has_run(str(meta['id'])) and str(meta['id']) not in self._services: continue
            try:
                service = self._service(str(meta['id']))
                ref = ArtifactRef.parse(artifact) if '@v' in artifact else service.artifacts.latest_ref(artifact)
                data = service.artifacts.get(ref)
                return data if isinstance(data, dict) else {'content': data}
            except (KeyError, ValueError, FileNotFoundError) as exc:
                errors.append(str(exc))
        raise HTTPException(404, f'artifact not found: {artifact}; searched={len(errors)}')

    def _thread_artifact_payload(self, thread_id: str, artifact: str) -> dict[str, Any]:
        artifact = artifact.strip()
        if not artifact: raise HTTPException(400, 'artifact id required')
        self._meta(thread_id)
        run_dir = self._run_dir(thread_id)
        if not run_dir.exists(): raise HTTPException(404, f'run not found for thread: {thread_id}')
        try:
            data = _stored_artifact_payload(run_dir, artifact)
            return data if isinstance(data, dict) else {'content': data}
        except (KeyError, ValueError, FileNotFoundError) as exc:
            raise HTTPException(404, f'artifact not found in thread {thread_id}: {artifact}') from exc


def _single_sse(event: str, payload: dict[str, Any]):
    async def gen():
        yield _sse(event, payload)
    return gen()


def _event_rows(events: list[Event]) -> list[tuple[int, Event]]:
    rows = [(index, event.sequence or index + 1, event) for index, event in enumerate(events)]
    return [(sequence, event) for index, sequence, event in sorted(rows, key=lambda item: (item[1], item[0]))]


def _sse(event: str, payload: dict[str, Any], event_id: str | None = None) -> dict:
    row = {'event': event, 'data': json.dumps({'type': event, **payload}, ensure_ascii=False, default=str)}
    if event_id: row['id'] = event_id
    return row


def _event_frame(event, seq: int, operations: dict[str, Any] | None = None) -> dict | None:
    payload = dict(event.payload or {})
    if event.event_type.startswith('checkpoint.') or event.event_type.startswith('autooperator.'):
        return _sse(event.event_type,
                    {'seq': seq, 'event_id': event.event_id, 'created_at': event.created_at, **payload}, str(seq))
    if event.event_type == 'evo_flow.progress':
        if str(payload.get('stage') or '') == 'full_flow': return None
        stage = STAGE_MAP.get(str(payload.get('stage') or ''))
    elif event.event_type == 'operation.progress':
        payload.update(_operation_event_meta(str(payload.get('operation_run_id') or ''), operations or {}))
        stage = _stage_from_operation(payload)
    else:
        return None
    if not stage: return None
    action = _action(str(payload.get('status') or 'running'))
    data = {'type': f'{stage}.{action}', 'stage': stage, 'action': action, 'seq': seq,
            'event_id': event.event_id, 'created_at': event.created_at,
            'message': payload.get('message') or '', 'payload': payload,
            'task_id': payload.get('stage') or payload.get('phase'),
            **{key: payload[key] for key in ('flow_kind', 'case_id', 'case_index', 'artifact_id') if key in payload}}
    return _sse('message', data, str(seq))


def _action(status: str) -> str:
    return {'running': 'progress', 'success': 'finish', 'failed': 'failed', 'checkpointed': 'pause',
            'cancelled': 'cancel', 'skipped': 'finish'}.get(status, 'progress')


def _dataset_id(inputs: dict[str, Any]) -> str:
    ids = {str(inputs.get(key) or '').strip() for key in ('kb_id', 'dataset_id') if str(inputs.get(key) or '').strip()}
    if len(ids) > 1: raise ValueError('dataset id aliases must match')
    if ids: return validate_id(ids.pop(), 'dataset_id')
    # Legacy frontend threads carry dataset_name as a display name, never as an id alias;
    # it only acts as the id when no kb_id/dataset_id was provided at all.
    legacy = str(inputs.get('dataset_name') or '').strip()
    return validate_id(legacy, 'dataset_id') if legacy else 'algo'


def _scoped_report_id(value: str) -> tuple[str, str]:
    text = str(value or '').strip()
    if ':' not in text:
        raise HTTPException(400, 'global report content requires scoped id: {thread_id}:{artifact_ref}')
    thread_id, artifact = (part.strip() for part in text.split(':', 1))
    if not thread_id or not artifact:
        raise HTTPException(400, 'global report content requires scoped id: {thread_id}:{artifact_ref}')
    return thread_id, artifact


def _normalize_inputs(inputs: dict[str, Any]) -> dict[str, Any]:
    normalized = dict(inputs)
    dataset_id = _dataset_id(normalized)
    normalized['kb_id'] = normalized['dataset_id'] = dataset_id
    if 'dataset_name' in normalized: normalized['dataset_name'] = dataset_id
    normalized['target_chat_url'] = _chat_url(normalized.get('target_chat_url'))
    normalized['candidate_chat_url'] = _optional_chat_url(normalized.get('candidate_chat_url'))
    if normalized['candidate_chat_url'] and normalized['candidate_chat_url'] == normalized['target_chat_url']:
        raise ValueError('candidate_chat_url must differ from target_chat_url')
    normalized['router_admin_url'] = _admin_url(normalized.get('router_admin_url'))
    normalized['num_cases'] = _bounded_positive_int(_case_count_value(normalized), 'num_cases',
                                                    MAX_CREATE_THREAD_CASES)
    normalized.pop('case_count', None)
    max_workers = inputs['max_workers'] if 'max_workers' in inputs else os.getenv('EVO_FLOW_WORKERS', '2')
    normalized['max_workers'] = _bounded_positive_int(max_workers, 'max_workers', MAX_CREATE_THREAD_WORKERS)
    return normalized


def _chat_url(value: Any) -> str:
    url = str(value or os.getenv('LAZYMIND_EVO_TARGET_CHAT_URL') or 'http://chat:8046/api/chat/stream').strip()
    return _stream_url(url, 'target_chat_url')


def _optional_chat_url(value: Any) -> str:
    url = str(value or '').strip()
    return _stream_url(url, 'candidate_chat_url') if url else ''


def _stream_url(url: str, field: str) -> str:
    return normalize_chat_stream_url(url.replace('http://evo-chat:', 'http://chat:'), field)


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
    if out < 1: raise ValueError(f'{field} must be a positive integer')
    if out > maximum: raise ValueError(f'{field} must be <= {maximum}')
    return out


def _intent_label(action: str) -> str:
    labels = {'ask_clarification': '需要补充信息', 'no_operations': '无需新增操作', 'reject': '未通过当前能力边界',
              'resume_checkpointed': '继续执行 checkpoint 后续流程', 'respond_to_user': '直接回复用户',
              'read_run_status_query': '查看当前流程进度', 'read_artifact_query': '查看产物内容',
              'read_repair_artifact': '查看修复产物', 'read_operation_query': '查看操作状态'}
    return labels.get(action, action.replace('_', ' '))


def _reply(result) -> str:
    fixed = {'ask_clarification': '我还需要你补充一点信息，才能继续规划下一步。',
             'no_operations': '收到，这条消息不需要新增自进化操作。',
             'reject': '这条消息超出了当前 checkpoint 允许的能力边界，请调整后重试。',
             'resume_checkpointed': '已收到继续确认，正在恢复 checkpoint 后续流程。'}
    if result.action in fixed: return fixed[result.action]
    status = '已完成' if result.results else '已识别'
    return f'{status}意图：{_intent_label(str(result.action))}。'


def _intent_answer(service: EvoFlowService, result: FlowMessageResult) -> str:
    for op_result in result.results:
        for ref in op_result.output_refs:
            try:
                payload = service.artifacts.get(ref)
            except (KeyError, FileNotFoundError):
                continue
            answer = payload.get('answer') if isinstance(payload, dict) else ''
            if isinstance(answer, str) and answer.strip(): return answer.strip()
    return ''


def _run_status_reply(thread_id: str, service: EvoFlowService, flow_status: dict[str, Any],
                      meta: dict[str, Any]) -> str:
    run_dir = service.store.run_dir(RUN_ID)
    projection = _read_json(run_dir / 'projections' / 'current.json')
    run = _read_json(run_dir / 'run.json')
    operations = _read_json(run_dir / 'operations.json')
    latest = projection.get('latest_artifacts') or {}
    progress = projection.get('progress') or {}
    events = _stored_events(run_dir)
    checkpoint = flow_status.get('pending_checkpoint')
    status = str(flow_status.get('status') or meta.get('status') or run.get('status') or 'idle')
    status = str(meta.get('status')) if str(meta.get('status') or '') in {'cancelled', 'failed', 'paused'} else status
    stages = [_stage_summary(stage, label, artifacts, operations, latest, progress, events, checkpoint)
              for stage, label, artifacts in RUN_STATUS_STAGES]
    current = next((item for item in stages if item['state'] in {'running', 'waiting', 'failed'}), None)
    current = current or next((item for item in stages if item['state'] == 'pending'), stages[-1])
    lines = [f'当前线程 {thread_id}：{_thread_status_label(status)}，当前阶段：{current["label"]} - {current["text"]}。']
    if status == 'cancelled': lines.append('线程已取消，当前不会继续推进；下面是取消前已经完成和未开始的部分。')
    if checkpoint: lines.append(_checkpoint_line(checkpoint))
    lines.append('阶段状态：')
    lines.extend(f'- {item["label"]}: {item["text"]}{item["detail"]}' for item in stages)
    return '\n'.join(lines)


def _stage_summary(stage: str, label: str, artifacts: tuple[str, ...], operations: dict, latest: dict, progress: dict,
                   events: list[Event], checkpoint: dict | None) -> dict[str, str]:
    stage_ops = [row for row in operations.values() if isinstance(row, dict) and _operation_stage(row) == stage]
    flow_state = _latest_flow_stage_status(events, stage)
    state = _stage_state(stage, artifacts, stage_ops, latest, flow_state, checkpoint)
    detail = _stage_detail(stage, artifacts, latest, progress, stage_ops, checkpoint)
    return {'label': label, 'state': state, 'text': _stage_state_label(state, flow_state), 'detail': detail}


def _stage_state(stage: str, artifacts: tuple[str, ...], operations: list[dict], latest: dict, flow_state: str,
                 checkpoint: dict | None) -> str:
    if checkpoint and STAGE_MAP.get(str(checkpoint.get('next_stage') or '')) == stage: return 'waiting'
    if any(str(op.get('outcome') or op.get('status')) == 'failed' for op in operations) or flow_state == 'failed':
        return 'failed'
    if any(str(op.get('status') or '') == 'running' for op in operations) or flow_state == 'running': return 'running'
    if any(artifact in latest for artifact in artifacts) or flow_state in {'success', 'skipped'}: return 'done'
    return 'pending'


def _stage_detail(stage: str, artifacts: tuple[str, ...], latest: dict, progress: dict, operations: list[dict],
                  checkpoint: dict | None) -> str:
    if stage == 'dataset' and 'eval_dataset' in latest:
        payload = progress.get('dataset.assemble') if isinstance(progress, dict) else {}
        total = (payload or {}).get('total') or (payload or {}).get('done')
        return f'，已生成 {total} 条样本' if total else '，数据集已生成'
    if stage == 'repair':
        if any(key in latest for key in {'verified_repair', 'repair_loop_agent'}): return '，opencode 执行轨迹已生成'
        return '，opencode 尚未开始' if not operations else ''
    if stage == 'abtest':
        if 'candidate_algorithm_cutover' in latest: return '，候选算法已切流'
        if checkpoint and checkpoint.get('checkpoint_kind') == 'manual_cutover': return '，等待用户确认切流'
        return '，ABTest 结果已生成' if 'abtest_comparison' in latest else ''
    ready = next((artifact for artifact in artifacts if artifact in latest), '')
    return f'，产物 {ready} 已生成' if ready else ''


def _operation_stage(operation: dict[str, Any]) -> str | None:
    oid = str(operation.get('operation_id') or operation.get('operation_run_id') or '')
    return None if oid.startswith('intent.') else _stage_from_operation(operation)


def _latest_flow_stage_status(events: list[Event], stage: str) -> str:
    status = ''
    for event in events:
        payload = event.payload or {}
        if event.event_type == 'evo_flow.progress' and STAGE_MAP.get(str(payload.get('stage') or '')) == stage:
            status = str(payload.get('status') or status)
    return status


def _checkpoint_line(checkpoint: dict) -> str:
    next_op = checkpoint.get('next_op') or {}
    op = next_op.get('op') if isinstance(next_op, dict) else next_op
    next_stage = STAGE_MAP.get(str(checkpoint.get('next_stage') or '')) or str(checkpoint.get('next_stage') or '')
    next_label = next((label for stage, label, _ in RUN_STATUS_STAGES if stage == next_stage), next_stage or '下一阶段')
    return f'待确认：{checkpoint.get("message") or "等待确认是否继续"} 下一步是 {next_label}{f" ({op})" if op else ""}。'


def _completed_manual_cutover(checkpoint: CheckpointState, result: FlowMessageResult,
                              service: EvoFlowService) -> bool:
    if not checkpoint.is_manual_cutover or result.action != 'cutover_candidate_algorithm': return False
    cutover_done = _has_artifact(service, 'candidate_algorithm_cutover')
    return cutover_done and any(ref.status in {'ended', 'success'} for ref in result.results)


def _resume_input_policy(payload: dict[str, Any]) -> str:
    return str(payload.get('input_policy') or '').strip()


def _default_resume_input_policy(checkpoint: CheckpointState, input_policy: str) -> str:
    return ContinuationPolicyResolver.resolve({'input_policy': input_policy}, checkpoint)


def _stage_checkpoint_resumed_result(message_id: str, checkpoint: CheckpointState,
                                     input_policy: str) -> FlowMessageResult:
    return FlowMessageResult(
        message_id,
        {'next_task': {'type': 'stage_checkpoint_resumed', 'checkpoint_id': checkpoint.checkpoint_id,
                       'next_stage': checkpoint.next_stage, 'input_policy': input_policy}},
        'resume_checkpointed',
    )


def _blocked_operations_stage(checkpoint: CheckpointState) -> str:
    """Operation checkpoints carry no next_stage; derive the restart stage from blocked operations."""
    for operation in checkpoint.blocked_operations or checkpoint.next_operations or ():
        stage = STAGE_MAP.get(str(operation).split('.', 1)[0])
        if stage: return stage
    return ''


def _thread_status_label(status: str) -> str:
    return {'running': '运行中', 'waiting_checkpoint': '等待确认', 'cancelled': '已取消', 'ended': '已完成',
            'failed': '失败', 'paused': '已暂停', 'idle': '空闲'}.get(status, status)


def _stage_state_label(state: str, flow_state: str) -> str:
    if state == 'done': return '已完成' if flow_state != 'skipped' else '已跳过'
    return {'running': '进行中', 'waiting': '等待确认', 'failed': '失败', 'pending': '未开始'}.get(state, state)


def _checkpoint_stage(stage: str) -> str:
    return {'dataset_gen': 'dataset', 'run': 'analysis', 'apply': 'repair'}.get(stage, stage)


def _stage_label(stage: str) -> str:
    return {'dataset': '数据集生成', 'eval': '评测', 'analysis': '分析', 'repair': '修复',
            'abtest': 'ABTest'}.get(stage, stage)


def _operations_by_id(service: EvoFlowService) -> dict[str, Any]:
    return {str(row.get('operation_run_id') or ''): row for row in service.store.list_operations(RUN_ID)}


def _operation_event_meta(operation_run_id: str, operations: dict[str, Any]) -> dict[str, Any]:
    operation = operations.get(operation_run_id) or {}
    params, tags = operation.get('params') or {}, operation.get('tags') or {}
    return {
        'flow_tag': operation.get('flow_tag'), 'stage_tag': operation.get('stage_tag'),
        'flow_kind': tags.get('evo_step') or operation.get('stage_tag') or operation.get('flow_tag'),
        'case_id': params.get('case_id') or params.get('output_case_id'),
        'artifact_id': tags.get('writes_artifact_id'),
    } | _case_index(params.get('case_id') or params.get('output_case_id'))


def _case_index(case_id: Any) -> dict[str, int]:
    value = str(case_id or '')
    suffix = value.rsplit('_', 1)[1] if value.startswith('case_') else ''
    return {'case_index': int(suffix)} if suffix.isdigit() else {}


def _stage_from_operation(payload: dict[str, Any]) -> str | None:
    return STAGE_MAP.get(str(payload.get('flow_tag') or payload.get('stage_tag') or payload.get('phase') or ''))


def _preview(result) -> list[dict]:
    return [{'op': ref, 'intent': result.action, 'humanized': _intent_label(result.action), 'safety': 'normal',
             'params_summary': {}} for ref in result.operation_refs]


def _queued_preview_result_dict(result: FlowMessageResult) -> dict[str, Any]:
    data = result_dict(result)
    data['requires_confirmation'] = False
    data['confirmation_checkpoint_id'] = ''
    return data


def _preview_copy_ignore(path: str, names: list[str]) -> set[str]:
    ignored = {name for name in names if '.tmp' in name}
    if Path(path).name == RUN_ID: ignored |= {'candidate', 'tmp'} & set(names)
    return ignored


def _chunks(text: str, size: int = 64) -> list[str]:
    return [text[i:i + size] for i in range(0, len(text), size)] or ['']


def _read_messages(path: Path) -> list[dict]:
    if not path.exists(): return []
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


def _flow_status_row(thread_id: str, status: str, active_task_ids: list[str], *,
                     latest_abtest_status: str | None = None, report_ready: bool = False,
                     pending_checkpoint: dict | None = None) -> dict:
    return {'thread_id': thread_id, 'status': status, 'active_task_ids': active_task_ids,
            'latest_abtest_id': 'abtest_comparison' if latest_abtest_status else None,
            'latest_abtest_status': latest_abtest_status, 'report_ready': report_ready,
            'pending_checkpoint': pending_checkpoint}


def _lifecycle_flow_status(thread_id: str, run_dir: Path, projection: dict[str, Any], meta_status: str,
                           active_task_ids: list[str]) -> dict:
    run = projection.get('run') if isinstance(projection.get('run'), dict) else {}
    if not run: run = _read_json(run_dir / 'run.json')
    latest = projection.get('latest_artifacts') if isinstance(projection.get('latest_artifacts'), dict) else {}
    status = _lifecycle_status(run)
    pending_checkpoint = frontend_checkpoint_from_run(run)
    if status == 'running' and not active_task_ids:
        status, pending_checkpoint = _stalled_running_status(run_dir, projection, pending_checkpoint)
    # RunLifecycle (run.json) is the single source of run status; thread meta only
    # tracks the deletion intent, which never reaches the lifecycle.
    if meta_status == 'deleting': status = 'deleting'
    decision = _artifact_decision(run_dir, 'abtest_comparison') if 'abtest_comparison' in latest else {}
    return _flow_status_row(thread_id, status, active_task_ids if status == 'running' else [],
                            latest_abtest_status=decision.get('status'),
                            report_ready=_eval_report_ready(run_dir, latest),
                            pending_checkpoint=pending_checkpoint)


def _eval_report_ready(run_dir: Path, latest: dict[str, Any]) -> bool:
    if 'eval_report' not in latest:
        return False
    aggregate = _read_json(run_dir / 'operations.json').get('eval.aggregate', {})
    return aggregate.get('status') == 'ended' and aggregate.get('outcome') == 'success'


def _lifecycle_status(run: dict[str, Any]) -> str:
    status = str(run.get('status') or 'idle')
    return 'waiting_checkpoint' if status == 'running' and run.get('dispatch_block_reason') else status


def _stalled_running_status(run_dir: Path, projection: dict[str, Any],
                            pending_checkpoint: dict | None) -> tuple[str, dict | None]:
    if pending_checkpoint: return 'waiting_checkpoint', pending_checkpoint
    blocker_ids = _blocker_operation_ids(projection.get('blockers') or [])
    if blocker_ids: return 'waiting_checkpoint', _checkpoint_ids(blocker_ids)
    operations = _projection_operations(run_dir, projection)
    waiting = [oid for oid in (_operation_id(operation) for operation in operations
               if str(operation.get('status') or 'pending') in {'pending', 'running', 'checkpointed'}
               or str(operation.get('outcome') or '') == 'failed') if oid]
    if waiting: return 'waiting_checkpoint', _checkpoint_ids(waiting)
    return ('ended', None) if operations else ('idle', None)


def _projection_operations(run_dir: Path, projection: dict[str, Any]) -> list[dict[str, Any]]:
    operations = projection.get('operations')
    if isinstance(operations, list):
        return [operation for operation in operations if isinstance(operation, dict)]
    stored = _read_json(run_dir / 'operations.json')
    if isinstance(stored, dict):
        return [operation for operation in stored.values() if isinstance(operation, dict)]
    return []


def _operation_id(operation: dict[str, Any]) -> str:
    return str(operation.get('operation_run_id') or operation.get('operation_id') or '')


def _blocker_operation_ids(blockers: list[Any]) -> list[str]:
    out = []
    for blocker in blockers:
        operation_id = (blocker.get('operation_run_id') or blocker.get('operation_id')) if isinstance(blocker, dict) \
            else blocker
        if operation_id: out.append(str(operation_id))
    return out


def _stored_events(run_dir: Path) -> list[Event]:
    path = run_dir / 'events.jsonl'
    if not path.exists(): return []
    rows = []
    for line in path.read_text(encoding='utf-8').splitlines():
        if line.strip():
            try:
                rows.append(Event(**json.loads(line)))
            except (TypeError, json.JSONDecodeError):
                continue
    return rows


def _checkpoint_ids(operation_ids: list[str]) -> dict | None:
    return {'checkpoint_id': operation_ids[0], 'message': 'operation paused, send continue to resume'} \
        if operation_ids else None


def _artifact_decision(run_dir: Path, artifact_id: str) -> dict:
    latest = sorted((run_dir / 'artifacts' / 'blobs' / artifact_id).glob('v*.json'))
    if not latest: return {}
    data = _read_json(latest[-1])
    decision = data.get('decision') if isinstance(data, dict) else {}
    return decision if isinstance(decision, dict) else {}


def _has_artifact(service: EvoFlowService, artifact_id: str) -> bool:
    try:
        service.artifacts.latest_ref(artifact_id)
        return True
    except KeyError:
        return False


def _artifact_row(service: EvoFlowService, artifact_id: str) -> dict:
    try:
        ref = service.artifacts.latest_ref(artifact_id)
    except KeyError:
        return {}
    data = service.artifacts.get(ref)
    if artifact_id == 'eval_dataset':
        data = _eval_dataset_with_cases(data, lambda case_ref: service.artifacts.get(case_ref))
    return _artifact_result_row(artifact_id, str(ref), service.artifacts.schema_name(ref), data)


def _stored_result_rows(run_dir: Path, kind: str) -> list[dict] | None:
    if kind not in RESULT_ARTIFACT_IDS and kind not in RESULT_ARTIFACT_SCHEMAS: return None
    rows = [_stored_artifact_row(run_dir, artifact_id) for artifact_id in RESULT_ARTIFACT_IDS.get(kind, ())]
    rows += _stored_schema_rows(run_dir, RESULT_ARTIFACT_SCHEMAS.get(kind, set()))
    return _dedupe_artifact_rows(rows)


def _stored_artifact_row(run_dir: Path, artifact_id: str) -> dict:
    manifest = _read_json(run_dir / 'artifacts' / 'manifests' / f'{artifact_id}.json')
    versions = manifest.get('versions') if isinstance(manifest.get('versions'), list) else []
    latest_version = int(manifest.get('latest_version') or 0)
    version = next((item for item in versions if isinstance(item, dict) and item.get('version') == latest_version),
                   None)
    if not version: version = next((item for item in reversed(versions) if isinstance(item, dict)), None)
    payload_ref = str(version.get('payload_ref') or '') if version else ''
    data = _read_json(run_dir / payload_ref) if payload_ref else {}
    if not data and not manifest: return {}
    if artifact_id == 'eval_dataset':
        data = _eval_dataset_with_cases(
            data, lambda case_ref: _stored_artifact_row(run_dir, case_ref.artifact_id).get('data')
        )
    artifact_ref = f"{artifact_id}@v{int(version.get('version') or latest_version or 1)}" if version else artifact_id
    schema = (manifest.get('schema_name') or version.get('schema_name')) if version else ''
    return _artifact_result_row(artifact_id, artifact_ref, schema, data)


def _artifact_result_row(artifact_id: str, artifact_ref: str, schema: str, data: dict) -> dict:
    return {'artifact_id': artifact_id, 'artifact_ref': artifact_ref, 'schema': schema,
            'case_count': len(data.get('case_ids') or data.get('cases') or []), 'data': data}


def _stored_schema_rows(run_dir: Path, schemas: set[str]) -> list[dict]:
    if not schemas: return []
    rows = []
    for path in sorted((run_dir / 'artifacts' / 'manifests').glob('*.json')):
        row = _stored_artifact_row(run_dir, path.stem)
        if row and row.get('schema') in schemas: rows.append(row)
    return rows


def _dedupe_artifact_rows(rows: list[dict]) -> list[dict]:
    out, seen = [], set()
    for row in rows:
        artifact_id = str(row.get('artifact_id') or '')
        if not artifact_id or artifact_id in seen: continue
        seen.add(artifact_id)
        out.append(row)
    return out


def _stored_artifact_payload(run_dir: Path, artifact: str) -> Any:
    artifact_id, version = _stored_artifact_target(artifact)
    manifest = _read_json(run_dir / 'artifacts' / 'manifests' / f'{artifact_id}.json')
    versions = manifest.get('versions') if isinstance(manifest.get('versions'), list) else []
    if not versions: raise KeyError(artifact)
    target_version = int(manifest.get('latest_version') or 0) if version is None else version
    selected = next(
        (item for item in versions if isinstance(item, dict) and int(item.get('version') or 0) == target_version),
        None)
    if not selected: raise KeyError(artifact)
    payload_ref = str(selected.get('payload_ref') or '')
    if not payload_ref: raise FileNotFoundError(artifact)
    return json.loads((run_dir / payload_ref).read_text(encoding='utf-8'))


def _stored_artifact_target(artifact: str) -> tuple[str, int | None]:
    if '@v' not in artifact: return artifact, None
    ref = ArtifactRef.parse(artifact)
    return ref.artifact_id, ref.version


def _eval_dataset_with_cases(data: dict, load_case: Callable[[ArtifactRef], Any]) -> dict:
    cases = []
    for value in data.get('case_refs') or []:
        try:
            case = load_case(ArtifactRef.parse(str(value)))
        except (KeyError, ValueError, TypeError):
            continue
        if isinstance(case, dict): cases.append(case)
    return {**data, 'cases': cases} if cases else data
