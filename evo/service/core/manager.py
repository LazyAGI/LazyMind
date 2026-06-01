from __future__ import annotations
import json
import logging
import shutil
import subprocess
import threading
import time
import uuid
from pathlib import Path
from typing import Any
from evo.abtest import VerdictPolicy
from evo.apply.errors import classify
from evo.apply.runner import ApplyOptions
from evo.chat_runner import ChatRegistry, ChatRunner, SubprocessChatRunner
from evo.runtime.config import (
    EVO_CANDIDATE_CHAT_HEALTH_PATH,
    EVO_CANDIDATE_CHAT_STARTUP_TIMEOUT_S,
    EVO_TARGET_CHAT_URL,
    EvoConfig,
)
from evo.service.core import state as thread_state, store
from evo.service.executors import EXECUTORS, ExecCtx
from evo.service.executors import apply as apply_exec
from evo.service.threads.workspace import EventLog, ThreadWorkspace
log = logging.getLogger('evo.service.core.manager')


class TaskRegistry:
    def __init__(self) -> None:
        self.threads: dict[str, threading.Thread] = {}
        self.procs: dict[str, list[subprocess.Popen]] = {}
        self.abtest_policy: dict[str, VerdictPolicy] = {}
        self.lock = threading.Lock()

    def register_proc(self, tid: str, proc: subprocess.Popen) -> None:
        with self.lock:
            self.procs.setdefault(tid, []).append(proc)

    def kill_procs(self, tid: str) -> None:
        with self.lock:
            procs = self.procs.pop(tid, [])
        for proc in procs:
            if proc.poll() is None:
                proc.terminate()


class JobManager:
    def __init__(
        self,
        st: store.FsStateStore,
        config: EvoConfig,
        *,
        apply_opts: ApplyOptions | None = None,
        chat_runner: ChatRunner | None = None,
        chat_registry: ChatRegistry | None = None,
    ) -> None:
        self._store = st
        self._cfg = config
        self._apply_opts = apply_opts
        self._chat_runner = chat_runner or _default_chat_runner(config)
        self._chat_registry = chat_registry or ChatRegistry(config.storage.base_dir)
        self._registry = TaskRegistry()
        self._recover_interrupted_tasks()

    @property
    def store(self) -> store.FsStateStore:
        return self._store

    @property
    def conn(self) -> store.FsStateStore:
        return self._store

    @property
    def config(self) -> EvoConfig:
        return self._cfg

    @property
    def chat_registry(self) -> ChatRegistry:
        return self._chat_registry

    def signals(self, tid: str) -> dict:
        return store.signals(self._store, tid)

    def list_recent(self, flow: str, limit: int = 50) -> list[dict]:
        return store.list_recent(self._store, flow, limit)

    def list_rounds(self, apply_id: str) -> list[dict]:
        return store.list_rounds(self._store, apply_id)

    def apply_commits_for_thread(self, thread_id: str) -> list[dict]:
        out = []
        for row in store.list_flow_tasks_by_thread(self._store, 'apply', thread_id):
            rounds = store.list_rounds(self._store, row['id'])
            out.append({'apply_id': row['id'], 'status': row.get('status'), 'commits': [
                       r for r in rounds if r.get('commit_sha')], 'rounds': rounds})
        return out

    def submit_dataset_gen(self, **payload: Any) -> str:
        return self._submit('dataset_gen', payload, thread_id=payload.pop('thread_id', None))

    def submit_eval(self, **payload: Any) -> str:
        return self._submit('eval', payload, thread_id=payload.pop('thread_id', None))

    def submit_run(self, **payload: Any) -> str:
        thread_id = payload.pop('thread_id', None)
        payload = {k: v for k, v in payload.items() if v is not None}
        payload.setdefault('eval_id', self._latest_thread_eval(thread_id))
        return self._submit('run', {k: v for k, v in payload.items() if v is not None}, thread_id=thread_id)

    def submit_apply(self, *, report_id: str | None = None, thread_id: str
                     | None = None, extra_instructions: str | None = None) -> str:
        rid, parent_run_id, _ = apply_exec.resolve_report(self._make_ctx(), report_id, thread_id=thread_id)
        return self._submit('apply', {'extra_instructions': extra_instructions} if extra_instructions else {
        }, thread_id=thread_id, report_id=rid, parent_run_id=parent_run_id)

    def submit_abtest(
            self,
            *,
            thread_id: str,
            apply_id: str,
            baseline_eval_id: str,
            dataset_id: str,
            policy: VerdictPolicy | dict | None = None,
            **payload: Any) -> str:
        apply_row = store.must_get(self._store, apply_id)
        _require_apply_ready_for_abtest(apply_row)
        verdict_policy = _coerce_policy(policy)
        worktree = payload.get('apply_worktree') or apply_exec.resolve_worktree(self._make_ctx(), apply_id)
        data = {'apply_id': apply_id, 'baseline_eval_id': baseline_eval_id, 'dataset_id': dataset_id,
                'apply_worktree': str(worktree), 'policy': verdict_policy.__dict__, **payload}
        tid = self._submit('abtest', data, thread_id=thread_id)
        self._registry.abtest_policy[tid] = verdict_policy
        return tid

    def stop(self, tid: str) -> dict:
        return store.transition(self._store, tid, 'stop')

    def cancel(self, tid: str) -> dict:
        row = store.transition(self._store, tid, 'cancel')
        self._registry.kill_procs(tid)
        if row['flow'] == 'run':
            shutil.rmtree(self._cfg.storage.runs_dir / tid, ignore_errors=True)
        if row['flow'] == 'apply':
            self._stop_apply_candidate(row)
            apply_exec.cleanup(self._make_ctx(), tid, drop_logs=False, drop_diffs=True)
        return row

    def cont(self, tid: str) -> dict:
        row = store.must_get(self._store, tid)
        fields: dict[str, Any] = {'error_code': None, 'error_kind': None}
        if row.get('flow') in {'dataset_gen', 'eval', 'run'}:
            fields['payload'] = self._continue_payload(row)
        store.transition(self._store, tid, 'continue', **fields)
        if row.get('status') != 'stopping':
            self._spawn(tid, row['flow'])
        return store.must_get(self._store, tid)

    def _continue_payload(self, row: dict) -> dict:
        payload = dict(row.get('payload') or {})
        payload['resume'] = True
        if row.get('flow') == 'dataset_gen' and not payload.get('num_cases') and row.get('thread_id'):
            ws = ThreadWorkspace(self._cfg.storage.base_dir, row['thread_id'], create=False)
            inputs = thread_state.read_json(ws.thread_meta_path) or {}
            if num_cases := (inputs.get('inputs') or {}).get('num_cases'):
                payload['num_cases'] = num_cases
        if row.get('flow') == 'run' and row.get('thread_id'):
            ws = ThreadWorkspace(self._cfg.storage.base_dir, row['thread_id'], create=False)
            if not payload.get('eval_id') or not ws.eval_path(payload['eval_id']).is_file():
                payload['eval_id'] = self._latest_thread_eval(row['thread_id'])
        return payload

    def accept(self, tid: str, auto_next: str | bool = 'none') -> dict:
        row = store.transition(self._store, tid, 'accept')
        final_commit = row.get('final_commit') or ((row.get('payload') or {}).get('result') or {}).get('final_commit')
        if final_commit and row.get('thread_id'):
            ThreadWorkspace(self._cfg.storage.base_dir, row['thread_id']
                            ).attach_artifact('apply_commit_ids', final_commit)
        return row

    def reject(self, tid: str) -> dict:
        row = store.transition(self._store, tid, 'reject')
        self._stop_apply_candidate(row)
        apply_exec.cleanup(self._make_ctx(), tid, drop_logs=False, drop_diffs=True)
        return row

    def cancel_all(self, flow: str, *, thread_id: str | None = None) -> list[dict]:
        return store.transition_many(self._store, [r['id'] for r in store.list_active(
            self._store, flow, scope='thread' if thread_id else 'global', thread_id=thread_id)], 'cancel')

    def stop_all(self, flow: str, *, thread_id: str | None = None) -> list[dict]:
        return store.transition_many(self._store, [r['id'] for r in store.list_active(
            self._store, flow, scope='thread' if thread_id else 'global', thread_id=thread_id)], 'stop')

    def join(self, tid: str, timeout: float = 30.0) -> None:
        if thread := self._registry.threads.get(tid):
            thread.join(timeout=timeout)

    def _submit(self, flow: str, payload: dict, *, thread_id: str | None = None, **fields: Any) -> str:
        tid = store.create_task(self._store, flow, thread_id=thread_id, payload={
                                k: v for k, v in payload.items() if v is not None}, **fields)
        _attach(self._cfg.storage.base_dir, thread_id, _artifact_kind(flow), tid)
        self._spawn(tid, flow)
        return tid

    def _spawn(self, tid: str, flow: str) -> None:
        thread = threading.Thread(target=self._run_executor, args=(tid, flow), daemon=True, name=f'evo-job-{tid}')
        self._registry.threads[tid] = thread
        thread.start()

    def _run_executor(self, tid: str, flow: str) -> None:
        ctx = self._make_ctx()
        try:
            row = store.get(self._store, tid)
            if row and row.get('thread_id'):
                from evo.runtime.model_config import activate_thread_model_config

                activate_thread_model_config(self._cfg.storage.base_dir, row.get('thread_id'), session_id=f'evo:{tid}')
            EXECUTORS[flow](ctx, tid)
        except Exception as exc:
            log.exception('executor %s failed for %s: %s', flow, tid, exc)
            ctx.on_failure(tid, exc)
            ctx.pop_thread(tid)
            ctx.pop_procs(tid)

    def _make_ctx(self) -> ExecCtx:
        return ExecCtx(
            store=self._store,
            cfg=self._cfg,
            is_cancelled=lambda tid: any(store.signals(self._store, tid).values()),
            register_proc=self._registry.register_proc,
            chat_runner_factory=lambda: self._chat_runner,
            chat_registry=self._chat_registry,
            apply_opts=self._apply_opts,
            abtest_policy=self._registry.abtest_policy,
            on_stop=self._on_stop,
            on_failure=self._on_failure,
            on_success=self._on_success,
            pop_thread=lambda tid: self._registry.threads.pop(tid, None),
            pop_procs=lambda tid: self._registry.procs.pop(tid, None),
        )

    def _recover_interrupted_tasks(self) -> None:
        for flow in store.FLOWS:
            for row in store.list_active(self._store, flow):
                if row.get('status') not in {'queued', 'running', 'stopping'}:
                    continue
                try:
                    recovered = store.transition(
                        self._store,
                        row['id'],
                        'ack' if row.get('status') == 'stopping' else 'fail_transient',
                        error_code='SERVICE_RESTARTED',
                        error_kind='transient')
                    if recovered.get('thread_id') and recovered.get('status') == 'failed_transient':
                        thread_state.save_thread(
                            self._cfg.storage.base_dir,
                            thread_state.ThreadRecord(
                                id=recovered['thread_id'],
                                state=thread_state.THREAD_FAILED,
                                current_flow=recovered.get('flow'),
                                active_task_id=recovered.get('id'),
                                error={
                                    'code': 'SERVICE_RESTARTED',
                                    'kind': 'transient',
                                    'message': 'service restarted while task was running',
                                },
                            ),
                        )
                except Exception as exc:
                    log.warning('recover task %s failed: %s', row.get('id'), exc)

    def _latest_thread_eval(self, thread_id: str | None) -> str | None:
        if not thread_id:
            return None
        vals = ThreadWorkspace(self._cfg.storage.base_dir, thread_id).load_artifacts().get('eval_ids') or []
        return vals[-1] if vals else None

    def _on_stop(self, tid: str, at: str | None) -> None:
        if (row := store.get(self._store, tid)) and row.get('status') == 'stopping':
            row = store.transition(
                self._store, tid, 'ack', **({'current_step': at} if row.get('flow') == 'run' else {}))
            if row.get('thread_id'):
                EventLog(ThreadWorkspace(self._cfg.storage.base_dir, row['thread_id']).events_path).append_event(
                    f"{row['flow']}.pause", task_id=tid, payload={'at': at})

    def _on_failure(self, tid: str, exc: Exception) -> None:
        code = getattr(exc, 'code', type(exc).__name__)
        kind = getattr(exc, 'kind', None) or classify(code)
        if (row := store.get(self._store, tid)) and row.get('status') in {'running', 'stopping'}:
            row = store.transition(self._store, tid, 'fail_permanent' if kind
                                   == 'permanent' else 'fail_transient', error_code=code, error_kind=kind)
            if row.get('thread_id'):
                thread_state.save_thread(
                    self._cfg.storage.base_dir,
                    thread_state.ThreadRecord(
                        id=row['thread_id'],
                        state=thread_state.THREAD_FAILED,
                        current_flow=row.get('flow'),
                        error={
                            'code': code,
                            'kind': kind,
                            'message': str(exc)}))
            if row.get('thread_id'):
                flow = row['flow']
                tag = f'{flow}.failed'
                EventLog(
                    ThreadWorkspace(
                        self._cfg.storage.base_dir,
                        row['thread_id']).events_path).append_event(
                    tag,
                    task_id=tid,
                    payload={
                        'status': 'failed',
                        'terminal_status': row.get('status'),
                        'error_code': code,
                        'error_kind': kind,
                        'message': str(exc)})
                ws = ThreadWorkspace(self._cfg.storage.base_dir, row['thread_id'])
                meta = thread_state.read_json(ws.thread_meta_path) or {}
                if meta.get('mode') == 'auto':
                    flow_label = _flow_label(row.get('flow'))
                    content = f'AutoOperator：{flow_label}执行失败，已停止自动推进：{str(exc)}'
                    _append_thread_message(ws.messages_path, 'assistant', content)
                    EventLog(ws.events_path).append_event('message.assistant', payload={'content': content})

    def _on_success(self, tid: str, final_action: str = 'finish') -> None:
        if (row := store.get(self._store, tid)) and row.get('status') in {'running', 'stopping'}:
            row = store.transition(self._store, tid, final_action, error_code=None, error_kind=None)
            if row.get('thread_id'):
                checkpoint = _checkpoint_after_success(self._cfg.storage.base_dir, row)
                terminal = bool(checkpoint and checkpoint.get('terminal'))
                state = thread_state.THREAD_SUCCEEDED if terminal else (
                    thread_state.THREAD_WAITING if checkpoint else thread_state.THREAD_IDLE
                )
                thread_state.save_thread(self._cfg.storage.base_dir, thread_state.ThreadRecord(
                    id=row['thread_id'], state=state, current_flow=row.get('flow'), checkpoint=checkpoint))
                if checkpoint:
                    EventLog(ThreadWorkspace(self._cfg.storage.base_dir, row['thread_id']).events_path).append_event(
                        'checkpoint.wait', task_id=tid, payload=checkpoint)
                if terminal or not checkpoint:
                    ws = ThreadWorkspace(self._cfg.storage.base_dir, row['thread_id'])
                    meta = thread_state.read_json(ws.thread_meta_path) or {}
                    if meta.get('mode') == 'auto':
                        content = _auto_done_message(row)
                        _append_thread_message(ws.messages_path, 'assistant', content)
                        EventLog(ws.events_path).append_event('message.assistant', payload={'content': content})

    def _stop_apply_candidate(self, row: dict) -> None:
        chat_id = (((row.get('payload') or {}).get('result') or {}).get('candidate_chat_id'))
        if chat_id:
            self._chat_runner.stop(chat_id)
            self._chat_registry.purge(chat_id)


def build_manager(config: EvoConfig) -> JobManager:
    return JobManager(store.open_db(config.storage.state_db_path), config)


def _default_chat_runner(cfg: EvoConfig) -> ChatRunner:
    return SubprocessChatRunner(
        log_dir=cfg.storage.base_dir / 'state' / 'chats',
        health_path=EVO_CANDIDATE_CHAT_HEALTH_PATH,
        startup_timeout_s=EVO_CANDIDATE_CHAT_STARTUP_TIMEOUT_S)


def _attach(base_dir: Path, thread_id: str | None, kind: str | None, value: str) -> None:
    if thread_id and kind:
        ThreadWorkspace(base_dir, thread_id).attach_artifact(kind, value)


def _artifact_kind(flow: str) -> str | None:
    return {
        'eval': 'eval_ids',
        'run': 'run_ids',
        'apply': 'apply_ids',
        'abtest': 'abtest_ids',
    }.get(flow)


def _checkpoint_after_success(base_dir: Path, row: dict) -> dict | None:
    thread_id = row.get('thread_id')
    if not thread_id:
        return None
    ws = ThreadWorkspace(base_dir, thread_id)
    next_op = _next_op(base_dir, ws, row)
    flow = row.get('flow')
    terminal = not bool(next_op)
    return {
        'checkpoint_id': f'ckpt_{uuid.uuid4().hex[:8]}',
        'completed_flow': flow,
        'completed_task_id': row.get('id'),
        'next_op': next_op,
        'terminal': terminal,
        'allowed_stages': list(store.FLOWS),
        'message': f'{_flow_label(flow)}已完成，{"当前流程已结束。" if terminal else "是否继续执行下一步？"}',
    }


def _next_op(base_dir: Path, ws: ThreadWorkspace, row: dict) -> dict | None:
    payload = row.get('payload') or {}
    inputs = (thread_state.read_json(ws.thread_meta_path) or {}).get('inputs') or {}
    artifacts = ws.load_artifacts()
    flow = row.get('flow')
    if flow == 'dataset_gen':
        dataset_id = payload.get('eval_name') or _latest_existing_dataset(base_dir, artifacts)
        if not dataset_id:
            return None
        args: dict[str, Any] = {'dataset_id': dataset_id, 'target_chat_url': EVO_TARGET_CHAT_URL}
        if inputs.get('dataset_name'):
            args['options'] = {'dataset_name': inputs['dataset_name']}
        return {'op': 'eval.run', 'args': args}
    if flow == 'eval':
        eval_id = payload.get('eval_id') or row.get('id')
        return {'op': 'run.start', 'args': {'eval_id': eval_id}}
    if flow == 'run':
        report_id = payload.get('report_id') or row.get('report_id')
        return {'op': 'apply.start', 'args': {'report_id': report_id}} if report_id else None
    if flow == 'apply':
        if not (row.get('final_commit') or ((row.get('payload') or {}).get('result') or {}).get('final_commit')):
            return None
        dataset_id = _latest_existing_dataset(base_dir, artifacts)
        eval_id = _latest_value(artifacts, 'eval_ids')
        if not dataset_id or not eval_id:
            return None
        args: dict[str, Any] = {
            'apply_id': row.get('id'),
            'baseline_eval_id': eval_id,
            'dataset_id': dataset_id,
            'target_chat_url': EVO_TARGET_CHAT_URL,
        }
        if inputs.get('dataset_name'):
            args['eval_options'] = {'dataset_name': inputs['dataset_name']}
        return {'op': 'abtest.create', 'args': args}
    return None


def _append_thread_message(path: Path, role: str, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('a', encoding='utf-8') as f:
        f.write(json.dumps({'role': role, 'content': content, 'ts': time.time()}, ensure_ascii=False) + '\n')


def _auto_done_message(row: dict) -> str:
    flow = row.get('flow')
    result = (row.get('payload') or {}).get('result') or {}
    if flow == 'apply' and not (row.get('final_commit') or result.get('final_commit')):
        return 'AutoOperator：分析报告未产生可执行代码修改，已跳过代码修改。'
    return f'AutoOperator：{_flow_label(flow)}已完成，暂无下一步。'


def _latest_existing_dataset(base_dir: Path, artifacts: dict) -> str | None:
    for dataset_id in reversed(artifacts.get('dataset_ids') or []):
        if (base_dir / 'datasets' / str(dataset_id) / 'eval_data.json').is_file():
            return str(dataset_id)
    return None


def _latest_value(artifacts: dict, kind: str) -> str | None:
    vals = artifacts.get(kind) or []
    return str(vals[-1]) if vals else None


def _flow_label(flow: str | None) -> str:
    return {
        'dataset_gen': '评测集生成',
        'eval': '评测',
        'run': '分析',
        'apply': '代码修改',
        'abtest': 'ABTest',
    }.get(str(flow), '当前步骤')


def _coerce_policy(policy: VerdictPolicy | dict | None) -> VerdictPolicy:
    if policy is None:
        return VerdictPolicy()
    if isinstance(policy, VerdictPolicy):
        return policy
    data = dict(policy)
    if isinstance(data.get('guard_metrics'), list):
        data['guard_metrics'] = tuple(data['guard_metrics'])
    return VerdictPolicy(**data)


def _require_apply_ready_for_abtest(row: dict) -> None:
    result = (row.get('payload') or {}).get('result') or {}
    if row.get('status') not in {'succeeded', 'accepted'} or not (
            row.get('final_commit') or result.get('final_commit')):
        raise store.StateError('APPLY_NOT_READY_FOR_ABTEST',
                               f"apply {row.get('id')} must finish before abtest", {'status': row.get('status')})
