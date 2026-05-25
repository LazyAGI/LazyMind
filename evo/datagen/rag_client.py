from __future__ import annotations
import json
import logging
import time
import uuid
import urllib.error
import urllib.request
from typing import Any
from evo.runtime.config import EVO_RAG_MAX_RETRIES, EVO_RAG_RETRY_BACKOFF_S, EVO_RAG_TIMEOUT_S

_log = logging.getLogger('evo.datagen.rag_client')


class RAGTargetRequiredError(RuntimeError):
    pass


class RAGCallFailed(RuntimeError):
    code = 'RAG_CALL_FAILED'
    kind = 'transient'


class RAGTraceMissing(RuntimeError):
    code = 'RAG_TRACE_MISSING'
    kind = 'permanent'


def call_rag_chat(question: str, target_chat_url: str, dataset_name: str = '', filters: dict[str, Any] | None = None,
                  *, require_trace: bool = True, model_config: dict[str, Any] | None = None,
                  ) -> dict[str, Any]:
    if not target_chat_url:
        raise RAGTargetRequiredError('target_chat_url is required for RAG evaluation')
    target_chat_url = _stream_chat_url(target_chat_url)
    session_id = f'evo-eval-{uuid.uuid4().hex}'
    payload = {'query': question, 'trace': require_trace, 'session_id': session_id}
    if dataset_name:
        payload['dataset'] = dataset_name
    if filters:
        payload['filters'] = filters
    if model_config:
        payload['llm_config'] = model_config
    data = json.dumps(payload).encode('utf-8')
    req = urllib.request.Request(
        target_chat_url, data=data, headers={'Content-Type': 'application/json'}, method='POST'
    )
    attempts = max(1, EVO_RAG_MAX_RETRIES)
    backoff_s = EVO_RAG_RETRY_BACKOFF_S
    last_error: Exception | None = None
    for attempt in range(1, attempts + 1):
        try:
            result = _open_rag_stream(req)
            if not isinstance(result, dict):
                raise RAGCallFailed(f'RAG_CALL_FAILED: invalid response {type(result).__name__}')
            code = result.get('code')
            if code in (None, 200):
                return _normalize_rag_result(result, require_trace=require_trace)
            message = result.get('msg') or result
            if not _is_retryable_chat_error(code, message) or attempt == attempts:
                raise RAGCallFailed(f'RAG_CALL_FAILED: {message}')
            last_error = RAGCallFailed(f'RAG_CALL_FAILED: {message}')
        except (TimeoutError, urllib.error.URLError, json.JSONDecodeError, RAGCallFailed) as exc:
            last_error = exc
            if isinstance(exc, RAGCallFailed) and not _is_retryable_exception(exc):
                raise
            if attempt == attempts:
                break
        _log.warning(
            'RAG callback attempt %s/%s failed for %s: %s',
            attempt, attempts, target_chat_url, last_error,
        )
        time.sleep(backoff_s * attempt)
    _log.warning('RAG callback failed for %s: %s', target_chat_url, last_error)
    raise RAGCallFailed(f'RAG_CALL_FAILED: {last_error}') from last_error


def _stream_chat_url(url: str) -> str:
    url = url.rstrip('/')
    if url.endswith('/api/chat/stream'):
        return url
    if url.endswith('/api/chat'):
        return url + '/stream'
    return url + '/api/chat/stream'


def _open_rag_stream(req: urllib.request.Request) -> dict[str, Any]:
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    text_parts: list[str] = []
    sources: list[Any] = []
    trace_id = ''
    with opener.open(req, timeout=EVO_RAG_TIMEOUT_S) as resp:
        for raw_line in resp:
            line = raw_line.decode('utf-8', errors='replace').strip()
            if not line:
                continue
            if line.startswith('data:'):
                line = line[5:].strip()
            if line == '[DONE]':
                break
            body = json.loads(line)
            if not isinstance(body, dict):
                continue
            data = body.get('data') if isinstance(body.get('data'), dict) else {}
            if body.get('code') not in (None, 200) or data.get('status') == 'FAILED':
                raise RAGCallFailed(f'RAG_CALL_FAILED: {body.get("msg") or body}')
            text = data.get('text')
            if isinstance(text, str):
                text_parts.append(text)
            if isinstance(data.get('trace_id'), str):
                trace_id = data['trace_id']
            if isinstance(data.get('sources'), list):
                sources = data['sources']
    return {
        'code': 200,
        'msg': 'success',
        'data': {'text': ''.join(text_parts), 'sources': sources, 'trace_id': trace_id},
    }


def _normalize_rag_result(result: dict[str, Any], *, require_trace: bool) -> dict[str, Any]:
    if not isinstance(result, dict):
        raise RAGCallFailed(f'RAG_CALL_FAILED: invalid response {type(result).__name__}')
    data_obj = result.get('data') if isinstance(result.get('data'), dict) else {}
    sources = result.get('sources') or data_obj.get('sources') or data_obj.get('recall') or []
    trace = data_obj.get('trace') if isinstance(data_obj.get('trace'), dict) else None
    trace_id = result.get('trace_id') or data_obj.get('trace_id') or (trace or {}).get('trace_id') or (trace or {}).get('id') or ''  # noqa: E501
    if require_trace and not trace_id and not trace:
        raise RAGTraceMissing('target chat did not return trace_id or inline trace')
    answer = result.get('answer') or data_obj.get('answer') or data_obj.get('text') or data_obj.get('data') or ''
    return {
        'answer': answer,
        'contexts': result.get('contexts') or _pluck_any(sources, ('context', 'content')),
        'docs': result.get('docs') or _pluck_any(sources, ('doc', 'file_name')),
        'raw': result,
        'chunk_ids': result.get('chunk_ids') or _pluck_any(sources, ('chunk_id', 'segment_id', 'segement_id')),
        'doc_ids': result.get('doc_ids') or _pluck_any(sources, ('doc_id', 'document_id')),
        'trace_id': trace_id,
        'trace': trace,
    }


def _is_retryable_chat_error(code: Any, message: Any) -> bool:
    if isinstance(code, int) and code >= 500:
        return True
    return _is_retryable_message(str(message))


def _is_retryable_exception(exc: Exception) -> bool:
    return _is_retryable_message(str(exc))


def _is_retryable_message(message: str) -> bool:
    text = message.lower()
    return any(token in text for token in ('timed out', 'ssleoferror', 'eof occurred', 'service of servermodule'))


def _pluck_any(sources: Any, keys: tuple[str, ...]) -> list[Any]:
    if not isinstance(sources, list):
        return []
    values: list[Any] = []
    for item in sources:
        if not isinstance(item, dict):
            continue
        for key in keys:
            if item.get(key) is not None:
                values.append(item[key])
                break
    return values
