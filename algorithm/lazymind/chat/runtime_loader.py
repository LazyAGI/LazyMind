from __future__ import annotations

import importlib
import json
import logging
import threading
import time
import urllib.error
import urllib.request

from types import ModuleType

from lazymind.config import config


logger = logging.getLogger(__name__)

_condition = threading.Condition()
_chat_service: ModuleType | None = None
_loading = False
_error: BaseException | None = None
_background_started = False
_rag_condition = threading.Condition()
_rag_module: ModuleType | None = None
_rag_loading = False
_rag_error: BaseException | None = None


def ensure_chat_runtime() -> ModuleType:
    """Load the base Chat runtime once without importing the RAG stack."""
    global _chat_service, _loading, _error

    with _condition:
        if _chat_service is not None:
            return _chat_service
        if _loading:
            while _loading:
                _condition.wait()
            if _chat_service is not None:
                return _chat_service
            assert _error is not None
            raise RuntimeError('Chat runtime initialization failed') from _error
        _loading = True
        _error = None

    started = time.monotonic()
    try:
        module = importlib.import_module('lazymind.chat.service.chat_service')
    except BaseException as exc:
        with _condition:
            _loading = False
            _error = exc
            _condition.notify_all()
        raise

    with _condition:
        _chat_service = module
        _loading = False
        _condition.notify_all()
    logger.info('Chat runtime warmup completed in %.3fs', time.monotonic() - started)
    return module


def chat_runtime_status() -> str:
    with _condition:
        if _chat_service is not None:
            return 'ready'
        if _loading:
            return 'loading'
        if _error is not None:
            return 'failed'
        return 'unloaded'


def ensure_rag_runtime() -> ModuleType:
    """Load the concrete KB, temp retriever, and document reader stack once."""
    global _rag_module, _rag_loading, _rag_error
    with _rag_condition:
        if _rag_module is not None:
            return _rag_module
        if _rag_loading:
            while _rag_loading:
                _rag_condition.wait()
            if _rag_module is not None:
                return _rag_module
            assert _rag_error is not None
            raise RuntimeError('RAG runtime initialization failed') from _rag_error
        _rag_loading = True
        _rag_error = None

    started = time.monotonic()
    try:
        module = importlib.import_module('lazymind.chat.engine.tools.kb')
    except BaseException as exc:
        with _rag_condition:
            _rag_loading = False
            _rag_error = exc
            _rag_condition.notify_all()
        raise

    with _rag_condition:
        _rag_module = module
        _rag_loading = False
        _rag_condition.notify_all()
    logger.info('RAG runtime warmup completed in %.3fs', time.monotonic() - started)
    return module


def rag_runtime_status() -> str:
    with _rag_condition:
        if _rag_module is not None:
            return 'ready'
        if _rag_loading:
            return 'loading'
        if _rag_error is not None:
            return 'failed'
        return 'unloaded'


def start_background_chat_runtime_warmup() -> None:
    """Warm Chat after KB services are ready without delaying HTTP readiness."""
    global _background_started
    with _condition:
        if _background_started or _chat_service is not None:
            return
        _background_started = True
    threading.Thread(
        target=_wait_and_warm,
        name='chat-runtime-warmup',
        daemon=True,
    ).start()


def _wait_and_warm() -> None:
    try:
        _wait_for_kb_runtime()
        ensure_chat_runtime()
        ensure_rag_runtime()
    except Exception:
        logger.exception('Background Chat runtime warmup failed')


def _wait_for_kb_runtime() -> None:
    algo_url = config['agentic_kb_url'].rstrip('/')
    processor_url = config['document_processor_url'].rstrip('/')
    algo_id = config['algo_id']
    timeout = float(config['startup_timeout'])
    interval = float(config['startup_retry_interval'])
    deadline = time.monotonic() + timeout if timeout > 0 else None

    while True:
        if _http_ok(f'{algo_url}/docs') and _algorithm_registered(processor_url, algo_id):
            return
        if deadline is not None and time.monotonic() >= deadline:
            raise TimeoutError('timed out waiting to warm Chat runtime')
        time.sleep(interval)


def _http_ok(url: str) -> bool:
    try:
        with urllib.request.urlopen(url, timeout=2) as response:
            return 200 <= response.status < 300
    except (urllib.error.URLError, TimeoutError, ConnectionError):
        return False


def _algorithm_registered(processor_url: str, algo_id: str) -> bool:
    try:
        with urllib.request.urlopen(f'{processor_url}/algo/list', timeout=2) as response:
            if not 200 <= response.status < 300:
                return False
            payload = json.loads(response.read().decode('utf-8'))
    except (urllib.error.URLError, TimeoutError, ConnectionError, ValueError):
        return False
    return any(item.get('algo_id') == algo_id for item in payload.get('data', []))
