from __future__ import annotations

import hashlib
import fcntl
import json
import random
import re
import shutil
import threading
import time
from collections.abc import Mapping
from pathlib import Path
from typing import Any, Callable

from fastapi import HTTPException

from evo.artifact_flow.commands import CancelFlow, ContinueFlow, PauseFlow, ResumeFlow, RetryFlow
from evo.artifact_flow.commands import FlowCommand
from evo.operations.router_ledger import RouterAlgorithmLedger
from evo.operations.router_manager import RouterManager, RouterManagerError
from .runtime_port import RuntimePort

THREAD_ID = re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,127}')
STEPS = ('dataset', 'eval', 'analysis', 'repair', 'abtest')
CHAT_CASE_DEADLINE_SECONDS = 300.0
CHAT_FIRST_FRAME_TIMEOUT_SECONDS = 60.0


class ThreadService:
    def __init__(self, root: Path) -> None:
        self.root = root
        self.runtime = RuntimePort(root)
        self.download_root = root / 'downloads'
        self.repair_work_root = root / 'work' / 'repair'
        self.download_root.mkdir(parents=True, exist_ok=True)
        self._lock = threading.RLock()
        self._active: set[str] = set()

    def create(self, payload: Mapping[str, Any]) -> dict[str, Any]:
        inputs = _inputs(payload['inputs'])
        llm_config = _llm_config(payload['llm_config'])
        mode = str(payload['mode'])
        if mode not in {'auto', 'interactive'}:
            raise HTTPException(422, 'mode must be auto or interactive')
        with self._lock, (self.root / 'thread-create.lock').open('a') as lock_file:
            fcntl.flock(lock_file, fcntl.LOCK_EX)
            for _ in range(16):
                thread_id = f'thr-{random.randint(0, 99999999):08d}'
                if self.runtime.run_config(thread_id) is not None:
                    continue
                seed = _seed(thread_id, mode, str(payload.get('title') or ''), inputs, llm_config)
                try:
                    self.runtime.seed(thread_id, seed, _digest(seed))
                except Exception:
                    self.runtime.delete_run(thread_id)
                    raise
                break
            else:
                raise HTTPException(500, 'failed to allocate thread id')
        return {'thread_id': thread_id, 'mode': seed['run_config']['mode'],
                'title': seed['run_config']['title'], 'status': 'idle'}

    def list(self, page_size: int, page_token: str, status: str = '') -> dict[str, Any]:
        items = [self.public_thread(run_id, include_inputs=False) for run_id in self.runtime.run_ids()]
        if status:
            items = [item for item in items if item['status'] == status]
        start = int(page_token or 0) if str(page_token or '0').isdigit() else -1
        if start < 0:
            raise HTTPException(422, 'page_token must be an integer offset')
        page = items[start:start + page_size]
        return {'items': page, 'next_page_token': str(start + page_size) if start + page_size < len(items) else ''}

    def public_thread(self, thread_id: str, *, include_inputs: bool = True) -> dict[str, Any]:
        config = self._config(thread_id)
        status = self._status(thread_id, config)
        item = {
            'thread_id': thread_id,
            'mode': str(config.get('mode') or ''),
            'title': str(config.get('title') or ''),
            'status': status['status'],
            'current_step': status['current_step'],
            'checkpoint_state': status['checkpoint_state'],
            'first_missing_step': status['first_missing_step'],
            'last_released_step': status['last_released_step'],
            'retry_from_step': status['retry_from_step'],
            'last_error': status['last_error'],
        }
        if include_inputs:
            item['inputs'] = config.get('inputs') or {}
            item['retryable'] = status['status'] == 'failed'
        return item

    def delete(self, thread_id: str) -> dict[str, Any]:
        with self._lock:
            self._config(thread_id)
            if thread_id in self._active:
                raise HTTPException(409, 'thread has an active command; cancel before delete')
            self._stop_owned_router_algorithms(thread_id)
            self.runtime.delete_run(thread_id)
            shutil.rmtree(self.download_root / thread_id, ignore_errors=True)
            shutil.rmtree(self.repair_work_root / thread_id, ignore_errors=True)
        return {'thread_id': thread_id, 'deleted': True, 'message': 'thread deleted'}

    def start(
        self,
        thread_id: str,
        payload: Mapping[str, Any],
        schedule: Callable[[Callable[[], None]], None],
    ) -> dict[str, str]:
        command_id = _command_id(payload, 'start', thread_id)
        with self._lock:
            config, snapshot = self._ready_locked(thread_id)
            if snapshot.status != 'idle' or any(item.completed for item in snapshot.progress):
                raise HTTPException(409, 'thread has already been started')
            until = str(payload.get('until_step') or ('abtest' if config.get('mode') == 'auto' else '')).strip()
            if config.get('mode') == 'auto' and until != 'abtest':
                raise HTTPException(422, 'auto mode start requires until_step=abtest')
            _validate_step(until)
            self._active.add(thread_id)
        return self._submit(
            thread_id,
            command_id,
            schedule,
            lambda: self.runtime.flow(_num_case(config)).handle(thread_id, ContinueFlow(command_id, until)),
        )

    def continue_thread(
        self,
        thread_id: str,
        payload: Mapping[str, Any],
        schedule: Callable[[Callable[[], None]], None],
    ) -> dict[str, str]:
        command_id = _command_id(payload, 'continue', thread_id)
        with self._lock:
            config, snapshot = self._ready_locked(thread_id)
            if snapshot.status == 'failed':
                raise HTTPException(409, 'continue requires retry after failed flow')
            if not any(item.completed for item in snapshot.progress) and snapshot.status != 'paused':
                raise HTTPException(409, 'thread has not been started')
            if snapshot.progress and snapshot.progress[-1].completed:
                raise HTTPException(409, 'thread has already ended')
            until = str(payload.get('until_step') or '').strip()
            _validate_step(until)
            self._active.add(thread_id)

        def run() -> None:
            flow = self.runtime.flow(_num_case(config))
            if self.runtime.gate_state(thread_id).status == 'paused':
                flow.handle(thread_id, ResumeFlow(f'{command_id}:resume'))
            flow.handle(thread_id, ContinueFlow(f'{command_id}:continue', until))

        return self._submit(thread_id, command_id, schedule, run)

    def retry(
        self,
        thread_id: str,
        payload: Mapping[str, Any],
        schedule: Callable[[Callable[[], None]], None],
    ) -> dict[str, str]:
        command_id = _command_id(payload, 'retry', thread_id)
        with self._lock:
            config, snapshot = self._ready_locked(thread_id)
            if snapshot.status != 'failed':
                raise HTTPException(409, 'retry requires a failed thread')
            until = str(payload.get('until_step') or '').strip()
            _validate_step(until)
            self._active.add(thread_id)

        def run() -> None:
            flow = self.runtime.flow(_num_case(config))
            flow.handle(thread_id, RetryFlow(f'{command_id}:retry'))
            flow.handle(thread_id, ContinueFlow(f'{command_id}:continue', until))

        return self._submit(thread_id, command_id, schedule, run)

    def pause(self, thread_id: str, payload: Mapping[str, Any]) -> dict[str, str]:
        with self._lock:
            config, snapshot = self._ready_locked(thread_id, allow_active=True)
            if not any(item.completed for item in snapshot.progress) and thread_id not in self._active:
                raise HTTPException(409, 'thread has not been started')
            command_id = _command_id(payload, 'pause', thread_id)
            result = self.runtime.flow(_num_case(config)).handle(thread_id, PauseFlow(command_id))
        if result.command_status == 'conflict':
            raise HTTPException(409, 'command_id conflict')
        if result.command_status == 'failed':
            raise HTTPException(500, result.error or 'pause command failed')
        return _accepted(thread_id, command_id)

    def cancel(self, thread_id: str, payload: Mapping[str, Any]) -> dict[str, str]:
        with self._lock:
            config = self._config(thread_id)
            command_id = _command_id(payload, 'cancel', thread_id)
            result = self.runtime.flow(_num_case(config)).handle(thread_id, CancelFlow(command_id))
        if result.command_status == 'conflict':
            raise HTTPException(409, 'command_id conflict')
        if result.command_status == 'failed':
            raise HTTPException(500, result.error or 'cancel command failed')
        return _accepted(thread_id, command_id)

    def run_message_command(self, thread_id: str, config: Mapping[str, Any], command: FlowCommand):
        with self._lock:
            if thread_id in self._active:
                raise HTTPException(409, 'thread already has an active command')
            self._active.add(thread_id)
        try:
            return self.runtime.flow(_num_case(config)).handle(thread_id, command)
        finally:
            with self._lock:
                self._active.discard(thread_id)

    def submit_message_command(
        self,
        thread_id: str,
        config: Mapping[str, Any],
        command: FlowCommand,
        schedule: Callable[[Callable[[], None]], None],
    ):
        if isinstance(command, ContinueFlow):
            snapshot = self.runtime.query(_num_case(config)).snapshot(thread_id)
            payload = {'command_id': command.command_id, 'until_step': command.until_step}
            if not any(item.completed for item in snapshot.progress) and snapshot.status != 'paused':
                return self.start(thread_id, payload, schedule)
            return self.continue_thread(thread_id, payload, schedule)
        if isinstance(command, ResumeFlow):
            return self.continue_thread(thread_id, {'command_id': command.command_id}, schedule)
        if isinstance(command, RetryFlow):
            return self.retry(thread_id, {'command_id': command.command_id}, schedule)
        if isinstance(command, PauseFlow):
            return self.pause(thread_id, {'command_id': command.command_id})
        if isinstance(command, CancelFlow):
            return self.cancel(thread_id, {'command_id': command.command_id})
        return self.run_message_command(thread_id, config, command)

    def _config(self, thread_id: str) -> Mapping[str, Any]:
        if not THREAD_ID.fullmatch(str(thread_id or '')):
            raise HTTPException(400, 'invalid thread_id')
        config = self.runtime.run_config(thread_id)
        if config is None:
            raise HTTPException(404, f'thread not found: {thread_id}')
        return config

    def _ready_locked(self, thread_id: str, *, allow_active: bool = False):
        if not allow_active and thread_id in self._active:
            raise HTTPException(409, 'thread already has an active command')
        config = self._config(thread_id)
        snapshot = self.runtime.query(_num_case(config)).snapshot(thread_id)
        if snapshot.status == 'cancelled':
            raise HTTPException(409, 'cancelled thread cannot be executed')
        return config, snapshot

    def _submit(
        self,
        thread_id: str,
        command_id: str,
        schedule: Callable[[Callable[[], None]], None],
        action: Callable[[], None],
    ) -> dict[str, str]:
        try:
            schedule(lambda: self._background(thread_id, action))
        except Exception:
            with self._lock:
                self._active.discard(thread_id)
            raise
        return _accepted(thread_id, command_id)

    def _background(self, thread_id: str, action: Callable[[], None]) -> None:
        try:
            action()
        finally:
            with self._lock:
                self._active.discard(thread_id)

    def _status(self, thread_id: str, config: Mapping[str, Any]) -> dict[str, str]:
        snapshot = self.runtime.query(_num_case(config)).snapshot(thread_id)
        gate = self.runtime.gate_state(thread_id)
        progress = list(snapshot.progress)
        if thread_id in self._active:
            status = 'running'
        elif snapshot.status == 'cancelled':
            status = 'cancelled'
        elif snapshot.status == 'failed':
            status = 'failed'
        elif progress and progress[-1].completed:
            status = 'ended'
        elif snapshot.status == 'paused' or any(item.completed for item in progress):
            status = 'paused'
        else:
            status = 'idle'
        checkpoint = snapshot.checkpoint
        return {
            'status': status,
            'current_step': checkpoint.current_step,
            'checkpoint_state': checkpoint.checkpoint_state,
            'first_missing_step': checkpoint.first_missing_step,
            'last_released_step': checkpoint.last_released_step,
            'retry_from_step': checkpoint.retry_from_step,
            'last_error': gate.last_error,
        }

    def _stop_owned_router_algorithms(self, thread_id: str) -> None:
        ledger = RouterAlgorithmLedger(self.runtime.store_root)
        rows = [
            row for row in ledger.list_algorithms(thread_id=thread_id, expected_state='active')
            if row.get('cleanup_policy') == 'thread_delete'
        ]
        for row in rows:
            algorithm_id = str(row['algorithm_id'])
            manager = RouterManager(str(row['router_admin_url']), str(row['service_url']))
            try:
                if algorithm_id in _strategy_weights(manager.get_ab_strategy()):
                    raise HTTPException(
                        409,
                        f'router algorithm {algorithm_id} is referenced by active AB strategy',
                    )
                manager.stop_algorithm(algorithm_id)
                ledger.mark_state(algorithm_id, 'stopped')
            except RouterManagerError as exc:
                raise HTTPException(503, f'failed to stop router algorithm {algorithm_id}: {exc}') from exc


def _inputs(value: Mapping[str, Any]) -> dict[str, Any]:
    csv_data = []
    for item in value.get('csv_data') or []:
        if not isinstance(item, Mapping):
            raise HTTPException(422, 'inputs.csv_data items must be objects')
        if len(item) != 1:
            raise HTTPException(422, 'each csv_data item must be {"kb_id": "csv_path"}')
        key, path = next(iter(item.items()))
        pair = {str(key).strip(): str(path).strip()}
        if not next(iter(pair)) or not next(iter(pair.values())):
            raise HTTPException(422, 'csv_data kb_id and csv_path must be non-empty')
        csv_data.append(pair)
    inputs = {
        'kb_id': [str(item).strip() for item in value.get('kb_id') or [] if str(item).strip()],
        'csv_data': csv_data,
        'router_chat_url': str(value.get('router_chat_url') or '').strip(),
        'router_admin_url': str(value.get('router_admin_url') or '').strip(),
        'algorithm_id': str(value.get('algorithm_id') or '').strip(),
        'num_case': int(value.get('num_case') or 0),
        'case_deadline_seconds': float(value.get('case_deadline_seconds') or CHAT_CASE_DEADLINE_SECONDS),
    }
    if not inputs['kb_id'] and not inputs['csv_data']:
        raise HTTPException(422, 'inputs.kb_id or inputs.csv_data is required')
    if not inputs['router_chat_url']:
        raise HTTPException(422, 'inputs.router_chat_url is required')
    if not inputs['router_admin_url']:
        raise HTTPException(422, 'inputs.router_admin_url is required')
    if not inputs['algorithm_id']:
        raise HTTPException(422, 'inputs.algorithm_id is required')
    if inputs['num_case'] < 1:
        raise HTTPException(422, 'inputs.num_case must be positive')
    if inputs['case_deadline_seconds'] <= 0:
        raise HTTPException(422, 'inputs.case_deadline_seconds must be positive')
    return inputs


def _llm_config(value: Mapping[str, Any]) -> dict[str, Any]:
    if {'eval_policy', 'repair_policy', 'candidate_config', 'abtest_candidate_config'} & set(value):
        raise HTTPException(422, 'llm_config cannot contain stage policy keys')
    if not isinstance(value.get('llm'), Mapping) or not isinstance(value.get('evo_llm'), Mapping):
        raise HTTPException(422, 'llm_config.llm and llm_config.evo_llm are required')
    return dict(value)


def _seed(thread_id: str, mode: str, title: str, inputs: Mapping[str, Any], llm_config: Mapping[str, Any]):
    target_config = {
        'router_chat_url': inputs['router_chat_url'],
        'router_admin_url': inputs['router_admin_url'],
        'algorithm_id': inputs['algorithm_id'],
        'llm_config': dict(llm_config),
        'case_deadline_seconds': inputs['case_deadline_seconds'],
        'first_frame_timeout_seconds': CHAT_FIRST_FRAME_TIMEOUT_SECONDS,
    }
    candidate_config = {
        'router_chat_url': inputs['router_chat_url'],
        'router_admin_url': inputs['router_admin_url'],
        'llm_config': dict(llm_config),
        'case_deadline_seconds': inputs['case_deadline_seconds'],
        'first_frame_timeout_seconds': CHAT_FIRST_FRAME_TIMEOUT_SECONDS,
    }
    return {
        'run_config': {'thread_id': thread_id, 'mode': mode, 'title': title, 'inputs': dict(inputs),
                       'num_case': inputs['num_case'], 'llm_config': dict(llm_config)},
        'source_config': {'kb_id': inputs['kb_id'], 'csv_data': inputs['csv_data'],
                          'target_case_count': inputs['num_case'],
                          'min_case_count': inputs['num_case']},
        'target_config': target_config,
        'eval_policy': {'judge_llm_config': dict(llm_config)},
        'repair_policy': {'llm_config': dict(llm_config), 'thread_id': thread_id,
                          'workspace_namespace': thread_id},
        'candidate_config': candidate_config,
    }


def _command_id(payload: Mapping[str, Any], prefix: str, thread_id: str) -> str:
    return str(payload.get('command_id') or '').strip() or f'{prefix}:{thread_id}:{time.time_ns()}'


def _accepted(thread_id: str, command_id: str) -> dict[str, str]:
    return {'status': 'accepted', 'thread_id': thread_id, 'command_id': command_id}


def _validate_step(step: str) -> None:
    if step and step not in STEPS:
        raise HTTPException(422, 'until_step must be dataset, eval, analysis, repair, or abtest')


def _num_case(config: Mapping[str, Any]) -> int:
    return int(config.get('num_case') or (config.get('inputs') or {}).get('num_case') or 0)


def _digest(value: object) -> str:
    raw = json.dumps(
        value,
        sort_keys=True,
        separators=(',', ':'),
        ensure_ascii=False,
    ).encode()
    return hashlib.sha256(raw).hexdigest()


def _strategy_weights(strategy: Mapping[str, Any]) -> dict[str, Any]:
    raw = strategy.get('strategy') if isinstance(strategy.get('strategy'), Mapping) else None
    return dict((raw or {}).get('weights') or {})


__all__ = ['ThreadService']
