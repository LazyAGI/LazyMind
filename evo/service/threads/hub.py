from __future__ import annotations
import asyncio
import json
import threading
import time
import uuid
from typing import TYPE_CHECKING
from fastapi import APIRouter, Body, HTTPException, Request
from sse_starlette.sse import EventSourceResponse
from evo.runtime.fs import atomic_write_json
from evo.runtime.model_config import (
    activate_thread_model_config,
    extract_model_config,
    require_evo_llm,
    require_thread_model_config,
    save_thread_model_config,
)
from evo.service.core import schemas, state as thread_state, store
from evo.service.core.intent_store import IntentStore
from evo.service.core.ops_executor import Op, OpsExecutor
from evo.service.threads.view import ThreadView
from evo.service.threads.workspace import EventLog, ThreadWorkspace
if TYPE_CHECKING:
    from evo.orchestrator.planner import Planner
    from evo.service.core.manager import JobManager

BODY_REQUIRED = Body(...)
BODY_DICT_DEFAULT = Body(default_factory=dict)


class ThreadHub:
    def __init__(self, *, jm: 'JobManager', planner: 'Planner', intent_store: IntentStore, ops: OpsExecutor) -> None:
        self.jm = jm
        self.planner = planner
        self.intents = intent_store
        self.ops = ops
        self.view = ThreadView(base_dir=jm.config.storage.base_dir, store_=jm.store)
        self._message_cancels: dict[str, threading.Event] = {}
        self._auto_stops: dict[str, threading.Event] = {}
        self._auto_checkpoint_ids: dict[str, str] = {}
        self._lock = threading.Lock()

    def create_thread(self, payload: dict) -> dict:
        mode = payload.get('mode', 'interactive')
        if mode not in {'interactive', 'auto'}:
            raise HTTPException(400, f'bad mode {mode!r}')
        model_config = extract_model_config(payload)
        if model_config or (mode == 'auto' and payload.get('start_auto', True)):
            require_evo_llm(model_config, self.jm.config.model_config.llm_role)
        tid = f'thr-{uuid.uuid4().hex[:8]}'
        ws = ThreadWorkspace(self.jm.config.storage.base_dir, tid)
        now = time.time()
        inputs = dict(payload.get('inputs') or {})
        inputs['target_chat_url'] = self.jm.config.eval_run.target_chat_url
        meta = {
            'id': tid,
            'mode': mode,
            'title': payload.get('title', ''),
            'inputs': inputs,
            'status': 'active',
            'state': thread_state.THREAD_IDLE,
            'created_at': now,
            'updated_at': now,
        }
        atomic_write_json(ws.thread_meta_path, meta)
        if model_config:
            save_thread_model_config(self.jm.config.storage.base_dir, tid, model_config)
        if mode == 'auto' and payload.get('start_auto', True):
            self.start(tid)
            self.auto_start(tid)
        return dict(meta)

    def list_threads(self) -> list[dict]:
        return self.view.list_threads()

    def list_thread_statuses(self) -> dict:
        return self.view.statuses()

    def get_thread(self, thread_id: str) -> dict:
        meta = self.view.get_thread(thread_id)
        if meta is None:
            raise HTTPException(404, f'thread {thread_id} not found')
        meta['pending_intents'] = self.intents.list_pending(thread_id)
        return meta

    def history(self, thread_id: str) -> dict:
        ws = self._workspace(thread_id)
        return {'thread_id': thread_id, 'messages': _read_messages(ws.messages_path)}

    def flow_status(self, thread_id: str) -> dict:
        return self.view.flow_status(thread_id)

    def start(self, thread_id: str, payload: dict | None = None) -> dict:
        if model_config := extract_model_config(payload):
            require_evo_llm(model_config, self.jm.config.model_config.llm_role)
            save_thread_model_config(self.jm.config.storage.base_dir, thread_id, model_config)
        require_thread_model_config(self.jm.config.storage.base_dir, thread_id, self.jm.config.model_config.llm_role)
        ws = self._workspace(thread_id)
        active = [row for row in self._active_tasks(thread_id) if row.get('flow') == 'dataset_gen']
        if active:
            return {'status': 'submitted', 'thread_id': thread_id, 'task_id': active[-1]['id']}
        inputs = (thread_state.read_json(ws.thread_meta_path) or {}).get('inputs') or {}
        return self._run_ops(
            thread_id,
            [
                {
                    'op': 'dataset_gen.start',
                    'args': {
                        'kb_id': inputs.get('kb_id'),
                        'algo_id': inputs.get('algo_id') or 'general_algo',
                        'eval_name': inputs.get('eval_name') or f'{thread_id}_eval',
                        **({'num_cases': inputs['num_cases']} if inputs.get('num_cases') else {}),
                    },
                }
            ],
        )

    def pause(self, thread_id: str) -> dict:
        task = self._active_task(thread_id)
        if task:
            self.jm.stop(task['id'])
        self._save_record(thread_id, thread_state.THREAD_PAUSED, active_task_id=(task or {}).get('id'))
        return {'status': 'paused', 'thread_id': thread_id}

    def cancel(self, thread_id: str) -> dict:
        for task in self._active_tasks(thread_id):
            try:
                self.jm.cancel(task['id'])
            except Exception:
                pass
        self._save_record(thread_id, thread_state.THREAD_CANCELLED)
        return {'status': 'cancelled', 'thread_id': thread_id}

    def retry(self, thread_id: str, payload: dict | None = None) -> dict:
        if model_config := extract_model_config(payload):
            require_evo_llm(model_config, self.jm.config.model_config.llm_role)
            save_thread_model_config(self.jm.config.storage.base_dir, thread_id, model_config)
        require_thread_model_config(self.jm.config.storage.base_dir, thread_id, self.jm.config.model_config.llm_role)
        task = self._latest_resumable(thread_id)
        if not task:
            raise store.StateError('NO_RESUMABLE_TASK', f'thread {thread_id} has no resumable task')
        self.jm.cont(task['id'])
        self._save_record(thread_id, thread_state.THREAD_RUNNING, active_task_id=task['id'])
        return {'status': 'running', 'thread_id': thread_id, 'active_task_id': task['id']}

    def post_message(self, thread_id: str, content: str, model_config: dict | None = None) -> dict:
        if model_config:
            require_evo_llm(model_config, self.jm.config.model_config.llm_role)
            save_thread_model_config(self.jm.config.storage.base_dir, thread_id, model_config)
        require_thread_model_config(self.jm.config.storage.base_dir, thread_id, self.jm.config.model_config.llm_role)
        ws = self._workspace(thread_id)
        elog = EventLog(ws.events_path)
        _append_message(ws.messages_path, 'user', content)
        elog.append_event('message.user', payload={'content': content})
        ctx = self.view.planner_context(thread_id, ws.messages_path, ws.load_artifacts())
        activate_thread_model_config(self.jm.config.storage.base_dir, thread_id, session_id=f'evo:{thread_id}:planner')
        intent = self.planner.draft(content, ctx)
        plan = self.planner.materialize(intent, ctx)
        if intent.suggested_ops_preview and not plan.ops and plan.warnings:
            intent.reply = f"无法执行：{plan.warnings[0].removeprefix('validation failed: ')}。"
            intent.suggested_ops_preview = []
        self.intents.save(intent)
        _append_message(ws.messages_path, 'assistant', intent.reply)
        self.intents.transition(intent.intent_id, 'confirm')
        self.intents.transition(intent.intent_id, 'materialize')
        if plan.ops:
            self._run_ops(thread_id, plan.ops)
        elog.append_event('message.assistant', payload={'content': intent.reply})
        return {
            'intent_id': intent.intent_id,
            'reply': intent.reply,
            'thinking': intent.thinking,
            'requires_confirm': False,
            'preview': [
                {'op': p.op, 'humanized': p.humanized, 'safety': p.safety, 'params_summary': p.params_summary}
                for p in intent.suggested_ops_preview
            ],
            'warnings': plan.warnings,
        }

    async def post_message_stream(self, thread_id: str, content: str, model_config: dict | None = None):
        message_id = f'msg_{thread_id}_{uuid.uuid4().hex[:8]}'
        seq = 0

        def emit(event: str, payload: dict) -> dict:
            nonlocal seq
            seq += 1
            return _sse(event, {'thread_id': thread_id, 'message_id': message_id, **payload}, f'{message_id}:{seq}')

        yield emit('intent_start', {})
        yield emit('thinking_delta', {'delta': '正在理解你的请求并规划下一步。'})
        try:
            result = await asyncio.to_thread(self.post_message, thread_id, content, model_config)
            for chunk in _chunks(result['reply']):
                yield emit('answer_delta', {'delta': chunk})
            yield emit(
                'plan_ready',
                {'intent_id': result['intent_id'], 'actions': result['preview'], 'warnings': result['warnings']},
            )
            for action in result['preview']:
                yield emit('action', {'intent_id': result['intent_id'], 'action': action})
            yield emit('done', {'intent_id': result['intent_id']})
        except Exception as exc:
            yield emit(
                'error',
                {'code': getattr(exc, 'code', 'MESSAGE_FAILED'), 'message': str(exc)},
            )

    def cancel_message(self, thread_id: str, message_id: str | None = None) -> dict:
        prefix = f'{thread_id}:'
        with self._lock:
            keys = [f'{thread_id}:{message_id}'] if message_id else [
                k for k in self._message_cancels if k.startswith(prefix)]
            for key in keys:
                if ev := self._message_cancels.get(key):
                    ev.set()
        return {'status': 'cancelled', 'thread_id': thread_id, 'message_id': message_id, 'count': len(keys)}

    def auto_step(self, thread_id: str) -> dict:
        status = self.flow_status(thread_id)
        msg = _auto_message(status)
        if not msg:
            return {'status': status.get('status', 'idle'), 'thread_id': thread_id}
        checkpoint = status.get('pending_checkpoint') or {}
        checkpoint_id = checkpoint.get('checkpoint_id') or checkpoint.get('completed_task_id')
        with self._lock:
            if checkpoint_id and self._auto_checkpoint_ids.get(thread_id) == checkpoint_id:
                return {'status': 'idle', 'thread_id': thread_id, 'message': msg}
            if checkpoint_id:
                self._auto_checkpoint_ids[thread_id] = checkpoint_id
        try:
            result = self.post_message(thread_id, msg)
        except Exception:
            with self._lock:
                if checkpoint_id and self._auto_checkpoint_ids.get(thread_id) == checkpoint_id:
                    self._auto_checkpoint_ids.pop(thread_id, None)
            raise
        return {'status': 'running', 'thread_id': thread_id, 'message': msg, 'intent_id': result.get('intent_id')}

    def auto_start(self, thread_id: str, interval_s: float = 5.0) -> dict:
        stop = self._auto_stops.get(thread_id)
        if stop and not stop.is_set():
            return {'status': 'running'}
        stop = threading.Event()
        self._auto_stops[thread_id] = stop
        threading.Thread(target=self._auto_loop, args=(thread_id, interval_s, stop), daemon=True).start()
        return {'status': 'started'}

    async def auto_start_stream(self, thread_id: str, interval_s: float = 5.0):
        yield _sse('auto_start', {'thread_id': thread_id, 'interval_s': interval_s})
        self.auto_start(thread_id, interval_s)

    def auto_stop(self, thread_id: str) -> dict:
        if stop := self._auto_stops.get(thread_id):
            stop.set()
        return {'status': 'stopped'}

    def _auto_loop(self, thread_id: str, interval_s: float, stop: threading.Event) -> None:
        try:
            while not stop.is_set():
                if self.auto_step(thread_id).get('status') in {'ended', 'cancelled', 'failed'}:
                    break
                stop.wait(interval_s)
        finally:
            stop.set()

    def _run_ops(self, thread_id: str, ops: list[dict]) -> dict:
        is_checkpoint_only = all(str(o.get('op') or '').startswith('checkpoint.') for o in ops)
        if not is_checkpoint_only:
            self._save_record(thread_id, thread_state.THREAD_RUNNING)
        results = self.ops.execute([Op(op=o['op'], args=o.get('args', {})) for o in ops], thread_id=thread_id)
        task_id = next((r.task_id for r in results if r.task_id), None)
        statuses = {r.status for r in results}
        if statuses & {'stopped'}:
            self._save_record(thread_id, thread_state.THREAD_PAUSED, active_task_id=task_id)
        elif statuses & {'cancelled'}:
            self._save_record(thread_id, thread_state.THREAD_CANCELLED)
        elif task_id and statuses & {'submitted', 'continued'}:
            self._save_record(thread_id, thread_state.THREAD_RUNNING, active_task_id=task_id)
        if any(r.error for r in results):
            self._save_record(thread_id, thread_state.THREAD_FAILED, error=next(r.error for r in results if r.error))
        return {'status': 'submitted' if task_id else 'done', 'thread_id': thread_id, 'task_id': task_id}

    def _workspace(self, thread_id: str) -> ThreadWorkspace:
        ws = ThreadWorkspace(self.jm.config.storage.base_dir, thread_id, create=False)
        if not ws.thread_meta_path.exists():
            raise HTTPException(404, f'thread {thread_id} not found')
        return ws

    def _active_tasks(self, thread_id: str) -> list[dict]:
        return [row for flow in store.FLOWS for row in store.list_active(
            self.jm.store, flow, scope='thread', thread_id=thread_id)]

    def _active_task(self, thread_id: str) -> dict | None:
        tasks = self._active_tasks(thread_id)
        tasks.sort(key=lambda row: row.get('created_at', 0.0), reverse=True)
        return tasks[0] if tasks else None

    def _latest_resumable(self, thread_id: str) -> dict | None:
        rows = [
            row
            for flow in store.FLOWS
            for row in store.list_flow_tasks_by_thread(self.jm.store, flow, thread_id)
            if row.get('status') in thread_state.RESUMABLE_TASK_STATUSES
        ]
        rows.sort(key=lambda row: row.get('updated_at', 0.0), reverse=True)
        return rows[0] if rows else None

    def _save_record(
            self,
            thread_id: str,
            state: str,
            *,
            active_task_id: str | None = None,
            error: dict | None = None) -> None:
        thread_state.save_thread(
            self.jm.config.storage.base_dir,
            thread_state.ThreadRecord(id=thread_id, state=state, active_task_id=active_task_id,
                                      error=error, updated_at=time.time()),
        )


def _auto_message(status: dict) -> str | None:
    if status.get('status') in {'running', 'cancelled', 'ended'}:
        return None
    checkpoint = status.get('pending_checkpoint') or {}
    return '继续执行' if checkpoint and not checkpoint.get('terminal') else None


def _append_message(path, role: str, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('a', encoding='utf-8') as f:
        f.write(json.dumps({'role': role, 'content': content, 'ts': time.time()}, ensure_ascii=False) + '\n')


def _read_messages(path) -> list[dict]:
    if not path.exists():
        return []
    rows = []
    for index, line in enumerate(path.read_text(encoding='utf-8').splitlines()):
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        role = row.get('role')
        content = row.get('content')
        if role in {'user', 'assistant'} and content:
            rows.append({'id': f'msg-{index + 1}', 'role': role, 'content': content, 'ts': row.get('ts')})
    return rows


def _chunks(text: str, size: int = 64) -> list[str]:
    return [text[i: i + size] for i in range(0, len(text), size)] or ['']


def _sse(event: str, payload: dict, event_id: str | None = None) -> dict:
    row = {'event': event, 'data': json.dumps({'type': event, **payload}, ensure_ascii=False, default=str)}
    if event_id:
        row['id'] = event_id
    return row


def build_router(hub: ThreadHub) -> APIRouter:
    router = APIRouter(prefix='/v1/evo')

    @router.post('/threads')
    async def create_thread(req: dict = BODY_REQUIRED) -> dict:
        return await asyncio.to_thread(hub.create_thread, req)

    @router.get('/threads')
    async def list_threads() -> list[dict]:
        return hub.list_threads()

    @router.get('/threads/statuses', response_model=schemas.ThreadStatusList)
    @router.get('/threads/statuse', response_model=schemas.ThreadStatusList, include_in_schema=False)
    async def list_thread_statuses() -> dict:
        return hub.list_thread_statuses()

    @router.get('/threads/{thread_id}:events')
    async def tail_events_colon(thread_id: str, since: int = 0) -> EventSourceResponse:
        return _tail_events_response(hub, thread_id, since)

    @router.get('/threads/{thread_id}')
    async def get_thread(thread_id: str) -> dict:
        return hub.get_thread(thread_id)

    @router.get('/threads/{thread_id}/history')
    async def get_thread_history(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.history, thread_id)

    @router.get('/threads/{thread_id}/flow-status', response_model=schemas.ThreadFlowStatus)
    async def flow_status(thread_id: str) -> dict:
        return hub.flow_status(thread_id)

    @router.post('/threads/{thread_id}:messages', operation_id='post_thread_message_colon')
    @router.post('/threads/{thread_id}/messages', operation_id='post_thread_message')
    async def post_message(thread_id: str, request: Request, body: dict = BODY_REQUIRED):
        content = body.get('content') or body.get('message') or ''
        model_config = extract_model_config(body)
        if 'text/event-stream' in request.headers.get('accept', ''):
            return EventSourceResponse(hub.post_message_stream(thread_id, content, model_config))
        return await asyncio.to_thread(hub.post_message, thread_id, content, model_config)

    @router.post('/threads/{thread_id}:messages:cancel', operation_id='cancel_active_thread_message_colon')
    @router.post('/threads/{thread_id}/messages:cancel', operation_id='cancel_active_thread_message')
    async def cancel_active_message(thread_id: str) -> dict:
        return hub.cancel_message(thread_id)

    @router.post('/threads/{thread_id}/messages/{message_id}/cancel')
    async def cancel_message(thread_id: str, message_id: str) -> dict:
        return hub.cancel_message(thread_id, message_id)

    @router.post('/threads/{thread_id}/start')
    async def start_thread(thread_id: str, body: dict = BODY_DICT_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.start, thread_id, body)

    @router.post('/threads/{thread_id}/pause')
    async def pause_thread(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.pause, thread_id)

    @router.post('/threads/{thread_id}/cancel')
    async def cancel_thread(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.cancel, thread_id)

    @router.post('/threads/{thread_id}/retry')
    async def retry_thread(thread_id: str, body: dict = BODY_DICT_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.retry, thread_id, body)

    @router.post('/threads/{thread_id}/auto/step')
    async def auto_step(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.auto_step, thread_id)

    @router.post('/threads/{thread_id}/auto/start')
    async def auto_start(thread_id: str, request: Request, body: dict = BODY_DICT_DEFAULT):
        interval_s = float(body.get('interval_s', 5.0))
        if 'text/event-stream' in request.headers.get('accept', ''):
            return EventSourceResponse(hub.auto_start_stream(thread_id, interval_s))
        return await asyncio.to_thread(hub.auto_start, thread_id, interval_s)

    @router.post('/threads/{thread_id}/auto/stop')
    async def auto_stop(thread_id: str) -> dict:
        return hub.auto_stop(thread_id)

    @router.get('/threads/{thread_id}/events')
    async def tail_events(thread_id: str, since: int = 0) -> EventSourceResponse:
        return _tail_events_response(hub, thread_id, since)
    return router


def _tail_events_response(hub: ThreadHub, thread_id: str, since: int = 0) -> EventSourceResponse:
    if since < 0:
        raise HTTPException(400, 'since must be >= 0')
    path = ThreadWorkspace(hub.jm.config.storage.base_dir, thread_id, create=False).events_path

    async def gen():
        offset = since
        while True:
            if path.exists() and path.stat().st_size > offset:
                hidden_tasks = _hidden_event_task_ids(hub, thread_id)
                with path.open('rb') as f:
                    f.seek(offset)
                    chunk = f.read()
                for line in chunk.splitlines():
                    offset += len(line) + 1
                    if text := line.decode('utf-8', 'replace').strip():
                        if _event_hidden(text, hidden_tasks):
                            continue
                        yield {'event': 'message', 'data': text, 'id': str(offset)}
            await asyncio.sleep(0.5)
    return EventSourceResponse(gen())


def _hidden_event_task_ids(hub: ThreadHub, thread_id: str) -> set[str]:
    rows = store.list_flow_tasks_by_thread(hub.jm.store, 'run', thread_id)
    has_report = any(r.get('status') == 'succeeded' and (r.get('payload') or {}).get('report_id') for r in rows)
    if not has_report:
        return set()
    return {
        r['id'] for r in rows
        if r.get('status') == 'failed_transient' and not (r.get('payload') or {}).get('report_id')
    }


def _event_hidden(text: str, hidden_tasks: set[str]) -> bool:
    if not hidden_tasks:
        return False
    try:
        obj = json.loads(text)
    except json.JSONDecodeError:
        return False
    return obj.get('task_id') in hidden_tasks and str(obj.get('tag') or '').startswith('run.')


def mount(app, hub: ThreadHub) -> None:
    app.state.thread_hub = hub
    app.include_router(build_router(hub))
