"""Bounded synchronous maintenance execution and cooperative cancellation.

Slots belong to concurrent futures, never to the HTTP waiters. Tool worker
threads find the cancellation event through the run ID in their LazyLLM session.
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
from threading import Event, Lock
from typing import Callable

import lazyllm
from fastapi import HTTPException, Request
from lazyllm.common import ThreadPoolExecutor, new_session

from lazymind.config import config


class MaintenanceCancelled(RuntimeError):
    pass


@dataclass
class Execution:
    task_id: str
    run_id: str
    stopped: Event = field(default_factory=Event)


_executions: dict[str, Execution] = {}
_lock = Lock()
_pool = None
_active = 0


def check_cancelled(*_args) -> bool:
    cfg = lazyllm.globals.get('agentic_config') or {}
    run_id = cfg.get('run_id') if isinstance(cfg, dict) else None
    with _lock:
        execution = _executions.get(run_id)
    if execution and execution.stopped.is_set():
        raise MaintenanceCancelled('maintenance execution was cancelled or lost its lease')
    return False


def identity_headers() -> dict[str, str]:
    check_cancelled()
    cfg = lazyllm.globals.get('agentic_config') or {}
    if not isinstance(cfg, dict) or not cfg.get('run_id'):
        return {}
    return {'X-LazyMind-Task-Id': cfg['task_id'], 'X-LazyMind-Run-Id': cfg['run_id']}


def check_response(response) -> None:
    if getattr(response, 'status_code', 0) == 409 and 'task_lease_lost' in str(getattr(response, 'text', '')):
        cfg = lazyllm.globals.get('agentic_config') or {}
        with _lock:
            execution = _executions.get(cfg.get('run_id'))
            if execution:
                execution.stopped.set()
        raise MaintenanceCancelled('task_lease_lost')
    check_cancelled()


def initialize_context(task_id: str, run_id: str, user_id: str) -> None:
    """Called in the executor thread, including direct service invocations."""
    sid = f'{task_id}:{run_id}'
    lazyllm.globals._init_sid(sid=sid)
    lazyllm.locals._init_sid(sid=sid)
    lazyllm.globals['agentic_config'] = {
        'task_id': task_id,
        'run_id': run_id,
        'session_id': sid,
        'user_id': user_id,
    }
    check_cancelled()


async def execute(request: Request | None, function: Callable, *, timeout: float, **kwargs):
    global _pool, _active
    execution = Execution(kwargs['task_id'], kwargs['run_id'])
    with _lock:
        capacity = max(1, int(config['memory_maintenance_workers']))
        if _active >= capacity or execution.run_id in _executions:
            raise HTTPException(503, detail={'code': 'maintenance_busy'})
        if _pool is None:
            _pool = ThreadPoolExecutor(max_workers=capacity, thread_name_prefix='memory-maintenance')
        _active += 1
        _executions[execution.run_id] = execution

    def work():
        with new_session(f'{execution.task_id}:{execution.run_id}'):
            initialize_context(execution.task_id, execution.run_id, kwargs['user_id'])
            return function(**kwargs)

    def finished(_future):
        global _active
        with _lock:
            _executions.pop(execution.run_id, None)
            _active -= 1

    try:
        future = _pool.submit(work)
    except BaseException:
        finished(None)
        raise
    future.add_done_callback(finished)
    wrapped = asyncio.wrap_future(future)
    # Consume late errors after an HTTP waiter has disconnected.
    wrapped.add_done_callback(lambda f: f.exception() if not f.cancelled() else None)
    deadline = asyncio.get_running_loop().time() + timeout
    try:
        while True:
            remaining = deadline - asyncio.get_running_loop().time()
            if remaining <= 0:
                raise HTTPException(504, detail={'code': 'maintenance_timeout'})
            done, _ = await asyncio.wait([wrapped], timeout=min(0.25, remaining))
            if done:
                return wrapped.result()
            if request is not None and await request.is_disconnected():
                raise HTTPException(499, detail={'code': 'maintenance_cancelled'})
    finally:
        if not future.done():
            execution.stopped.set()
