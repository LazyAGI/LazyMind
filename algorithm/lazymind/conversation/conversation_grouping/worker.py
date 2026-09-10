"""One organizer call, without importing the HTTP server's lifecycle."""
from __future__ import annotations

import ctypes
import json
import os
import signal
import sys
import threading
import time


def main():
    # Reserve the protocol descriptor before importing dependencies that may print.
    protocol = os.fdopen(os.dup(sys.stdout.fileno()), 'w', buffering=1)
    os.dup2(sys.stderr.fileno(), sys.stdout.fileno())
    parent_pid = int(sys.argv[1])
    if sys.platform == 'linux':
        libc = ctypes.CDLL(None, use_errno=True)
        if libc.prctl(1, signal.SIGKILL) != 0:
            raise OSError(ctypes.get_errno(), 'PR_SET_PDEATHSIG failed')
        if os.getppid() != parent_pid:
            return
    raw = sys.stdin.buffer.readline()
    if not raw:
        return

    def watch_parent():
        # Parent exclusively owns the write end; EOF also covers abrupt crashes.
        while os.read(sys.stdin.fileno(), 1):
            pass
        os._exit(1)

    threading.Thread(target=watch_parent, name='organizer-parent-watch', daemon=True).start()
    from .schemas import GroupingRequest
    from .grouping import run_grouping
    from .grouping import _STREAM_SINK
    lock = threading.Lock()

    def emit(event):
        with lock:
            protocol.write(json.dumps(event, ensure_ascii=False) + '\n')
            protocol.flush()

    last_emit = received = 0

    def sink(event):
        nonlocal last_emit, received
        now = time.monotonic()
        runtime = event.get('runtime_event', {})
        if runtime.get('type') == 'model_call_started':
            received, last_emit = 0, 0
            emit({'type': 'progress', 'state': 'waiting', 'received_chars': 0})
        elif event.get('tag') in ('text', 'think') and event.get('delta'):
            received += len(event['delta'])
            if now - last_emit >= 0.5:
                emit({'type': 'progress', 'state': 'generating', 'received_chars': received})
                last_emit = now
        elif runtime.get('type') == 'model_call_finished':
            emit({'type': 'progress', 'state': 'validating', 'received_chars': received})

    token = _STREAM_SINK.set(sink)
    try:
        result = run_grouping(GroupingRequest.model_validate_json(raw))
        emit({'type': 'result', 'result': result.model_dump()})
    except Exception as exc:
        emit({'type': 'result', 'result': {'status': 'failed', 'task_id': '',
              'error_code': 'worker_failed', 'error': type(exc).__name__, 'retryable': False}})
    finally:
        _STREAM_SINK.reset(token)
        protocol.close()


if __name__ == '__main__':
    main()
