"""A supervised organizer step: progress only leaves the worker, never reasoning text."""
from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import threading
import queue
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone

from .schemas import GroupingRequest


class ExecutionConflict(RuntimeError):
    """The execution is duplicate, expired, or already cancelled."""


@dataclass
class Execution:
    process: object
    pipe: object
    canceled: bool = False
    stop_lock: asyncio.Lock = field(default_factory=asyncio.Lock)

    async def stop(self):
        async with self.stop_lock:
            if self.process.poll() is None:
                self.process.terminate()
            try:
                await asyncio.to_thread(self.process.wait, 2)
            except subprocess.TimeoutExpired:
                pass
            if self.process.poll() is None:
                self.process.kill()
                try:
                    await asyncio.to_thread(self.process.wait, 2)
                except subprocess.TimeoutExpired:
                    pass
            return self.process.poll() is not None


_executions: dict[str, Execution] = {}
_cleanup_tasks: set[asyncio.Task] = set()
# Tombstones also fence a delayed POST arriving after its cancellation request.
# This supervisor requires one owning process/replica for both endpoints.
_canceled: dict[str, float] = {}
_started_at = time.time()


def _prune():
    now = time.monotonic()
    for key, deadline in list(_canceled.items()):
        if deadline <= now and key not in _executions:
            del _canceled[key]


def _tombstone(execution_id):
    _prune()
    _canceled[execution_id] = time.monotonic() + 300


class WorkerOutput:
    def __init__(self, stream):
        self.events = queue.Queue()
        self.stream = stream
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()

    def _read(self):
        try:
            for line in self.stream:
                self.events.put(json.loads(line))
        except (ValueError, OSError):
            pass

    def poll(self):
        return not self.events.empty()

    def recv(self):
        return self.events.get_nowait()

    def close(self):
        self.reader.join(2)
        self.stream.close()


async def cancel_execution(execution_id: str):
    _tombstone(execution_id)
    execution = _executions.get(execution_id)
    if execution is None:
        return {'settled': True}
    execution.canceled = True
    return {'settled': await execution.stop()}


async def stream_execution(execution_id: str, request: GroupingRequest):
    _prune()
    if execution_id in _canceled or execution_id in _executions:
        raise ExecutionConflict('Execution already exists or was canceled')
    _prune()
    issued_at = request.options.get('execution_issued_at')
    if (not isinstance(issued_at, (int, float)) or issued_at < _started_at
            or time.time() - issued_at > 90 or issued_at - time.time() > 30):
        raise ExecutionConflict('Execution request expired')
    process = subprocess.Popen(
        [sys.executable, '-m', 'lazymind.conversation.conversation_grouping.worker', str(os.getpid())],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=None, text=True,
    )
    receiver = WorkerOutput(process.stdout)
    execution = Execution(process, receiver)
    _executions[execution_id] = execution
    try:
        await asyncio.to_thread(process.stdin.write, request.model_dump_json() + '\n')
        await asyncio.to_thread(process.stdin.flush)
        if execution.canceled:
            await execution.stop()
    except BaseException:
        if await asyncio.shield(execution.stop()):
            process.stdin.close()
            receiver.close()
            _executions.pop(execution_id, None)
            _tombstone(execution_id)
        raise

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
                        try:
                            await asyncio.to_thread(process.wait, 2)
                        except subprocess.TimeoutExpired:
                            pass
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
                if process.poll() is not None:
                    await asyncio.to_thread(receiver.reader.join, 2)
                    if receiver.poll():
                        continue
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
                if await execution.stop():
                    process.stdin.close()
                    receiver.close()
                    _tombstone(execution_id)
                    _executions.pop(execution_id, None)
            cleanup_task = asyncio.create_task(cleanup())
            _cleanup_tasks.add(cleanup_task)
            cleanup_task.add_done_callback(_cleanup_tasks.discard)
            await asyncio.shield(cleanup_task)

    return events()
