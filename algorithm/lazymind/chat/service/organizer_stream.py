"""A supervised organizer step: progress only leaves the worker, never reasoning text."""
from __future__ import annotations

import asyncio
import ctypes
import json
import multiprocessing
import os
import signal
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone

from fastapi import HTTPException
from fastapi.responses import StreamingResponse

from .llm_task import LLMTaskRequest, run_llm_task


def _worker(request_data, output, parent_pid):
    # Core/Chat restarts must not orphan a model caller (production runtime is Linux).
    ctypes.CDLL(None).prctl(1, signal.SIGKILL)
    if os.getppid() != parent_pid:
        return
    from .conversation_organizer import _STREAM_SINK
    last_emit = 0.0
    received = 0

    def sink(event):
        nonlocal last_emit, received
        now = time.monotonic()
        runtime = event.get('runtime_event', {})
        if runtime.get('type') == 'model_call_started':
            received, last_emit = 0, 0
            output.send({'type': 'progress', 'state': 'waiting', 'received_chars': 0})
        elif event.get('tag') in ('text', 'think') and event.get('delta'):
            received += len(event['delta'])
            if now - last_emit >= 0.5:
                output.send({'type': 'progress', 'state': 'generating', 'received_chars': received})
                last_emit = now
        elif runtime.get('type') == 'model_call_finished':
            output.send({'type': 'progress', 'state': 'validating', 'received_chars': received})

    _STREAM_SINK.set(sink)
    try:
        result = run_llm_task(LLMTaskRequest.model_validate(request_data))
        output.send({'type': 'result', 'result': result.model_dump()})
    except Exception as exc:
        output.send({'type': 'result', 'result': {'status': 'failed', 'task_id': '',
                     'error_code': 'worker_failed', 'error': type(exc).__name__, 'retryable': False}})
    finally:
        output.close()


@dataclass
class Execution:
    process: object
    pipe: object
    canceled: bool = False
    stop_lock: asyncio.Lock = field(default_factory=asyncio.Lock)

    async def stop(self):
        async with self.stop_lock:
            if self.process.is_alive():
                self.process.terminate()
            await asyncio.to_thread(self.process.join, 2)
            if self.process.is_alive():
                self.process.kill()
                await asyncio.to_thread(self.process.join, 2)
            return not self.process.is_alive()


_executions: dict[str, Execution] = {}
_cleanup_tasks: set[asyncio.Task] = set()
# Tombstones also fence a delayed POST arriving after its cancellation request.
_canceled: set[str] = set()


async def cancel_execution(execution_id: str):
    _canceled.add(execution_id)
    execution = _executions.get(execution_id)
    if execution is None:
        return {'settled': True}
    execution.canceled = True
    return {'settled': await execution.stop()}


async def stream_execution(execution_id: str, request: LLMTaskRequest):
    if request.task_type != 'conversation.organize_step':
        raise HTTPException(400, 'Only organizer steps support this endpoint')
    if execution_id in _canceled or execution_id in _executions:
        raise HTTPException(409, 'Execution already exists or was canceled')
    context = multiprocessing.get_context('spawn')
    receiver, sender = context.Pipe(duplex=False)
    process = context.Process(target=_worker, args=(request.model_dump(), sender, os.getpid()), daemon=True)
    process.start()
    sender.close()
    execution = Execution(process, receiver)
    _executions[execution_id] = execution

    async def events():
        state, received = 'waiting', 0
        first_response_at = last_activity_at = ''
        started = last_activity = last_heartbeat = time.monotonic()
        try:
            while not execution.canceled:
                now = time.monotonic()
                while receiver.poll():
                    try:
                        event = receiver.recv()
                    except EOFError:
                        break
                    if event['type'] == 'result':
                        # A terminal event means the worker has exited, not merely emitted JSON.
                        await asyncio.to_thread(process.join, 2)
                        if not await execution.stop():
                            return
                        yield json.dumps(event) + '\n'
                        return
                    if event['state'] == 'waiting':
                        started = last_activity = now
                        first_response_at = last_activity_at = ''
                    elif event['state'] == 'generating':
                        last_activity = now
                        last_activity_at = datetime.now(timezone.utc).isoformat()
                        first_response_at = first_response_at or last_activity_at
                    state, received = event['state'], event['received_chars']
                    yield json.dumps({**event, 'elapsed_seconds': int(now - started),
                                      'first_response_at': first_response_at,
                                      'last_activity_at': last_activity_at}) + '\n'
                limit = 300 if state == 'waiting' else 120
                if now - last_activity >= limit:
                    code = 'first_response_timeout' if state == 'waiting' else 'stream_idle_timeout'
                    if not await execution.stop():
                        return
                    yield json.dumps({'type': 'result', 'result': {'status': 'failed', 'task_id': '',
                                      'error_code': code, 'error': code, 'retryable': True}}) + '\n'
                    return
                if not process.is_alive():
                    yield json.dumps({'type': 'result', 'result': {'status': 'failed', 'task_id': '',
                                      'error_code': 'worker_exited', 'retryable': False}}) + '\n'
                    return
                if now - last_heartbeat >= 2:
                    # Heartbeats report liveness but never advance model activity deadlines.
                    yield json.dumps({'type': 'progress', 'state': state, 'received_chars': received,
                                      'elapsed_seconds': int(now - started),
                                      'idle_seconds': int(now - last_activity),
                                      'first_response_at': first_response_at,
                                      'last_activity_at': last_activity_at}) + '\n'
                    last_heartbeat = now
                await asyncio.sleep(0.1)
        finally:
            # Shield cleanup from HTTP disconnect cancellation before acknowledging settlement.
            async def cleanup():
                await execution.stop()
                receiver.close()
                _canceled.add(execution_id)
                _executions.pop(execution_id, None)
            cleanup_task = asyncio.create_task(cleanup())
            _cleanup_tasks.add(cleanup_task)
            cleanup_task.add_done_callback(_cleanup_tasks.discard)
            await asyncio.shield(cleanup_task)

    return StreamingResponse(events(), media_type='application/x-ndjson', headers={'X-Accel-Buffering': 'no'})
