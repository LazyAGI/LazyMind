"""Background maintenance for local trace files (archive old JSONL, delete expired ZIP)."""

import logging
import threading

_logger = logging.getLogger(__name__)

_MAINTAIN_LOCAL_TRACES_INTERVAL = 86400  # 24 hours

_stop_event = threading.Event()
_thread: threading.Thread | None = None
_lock = threading.Lock()


def _run_once():
    try:
        from lazyllm.tracing.backends.local import maintain_local_traces
        result = maintain_local_traces()
        if result['compressed_jsonl'] or result['deleted_zip']:
            _logger.info(
                'local trace maintenance: compressed=%d, deleted=%d',
                len(result['compressed_jsonl']),
                len(result['deleted_zip']),
            )
    except Exception:
        _logger.warning('local trace maintenance failed', exc_info=True)


def start_local_trace_maintenance(interval: int = _MAINTAIN_LOCAL_TRACES_INTERVAL):
    global _thread

    from lazyllm.configs import config
    if config['trace_backend'] != 'local':
        return

    with _lock:
        if _thread is not None and _thread.is_alive():
            return
        _stop_event.clear()

        def _loop():
            _run_once()
            while not _stop_event.wait(interval):
                _run_once()

        _thread = threading.Thread(target=_loop, name='local-trace-maintenance', daemon=True)
        _thread.start()
