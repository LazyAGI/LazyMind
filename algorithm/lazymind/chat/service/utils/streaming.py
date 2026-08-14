from __future__ import annotations

import json
from typing import Any, Dict, Optional
from lazyllm import LOG

from fastapi.responses import StreamingResponse


def response_payload(code: int, msg: str, data: Any, cost: float) -> Dict[str, Any]:
    return {'code': code, 'msg': msg, 'data': data, 'cost': cost}


def sse_line(payload: Dict[str, Any]) -> str:
    return json.dumps(payload, ensure_ascii=False, default=str) + '\n\n'


def _log_safe_frame(frame: Any) -> Any:
    if not isinstance(frame, dict): return frame
    safe = dict(frame)
    provider_status = safe.get('provider_status')
    if isinstance(provider_status, dict):
        safe['provider_status'] = {
            'model_call_id': provider_status.get('model_call_id'),
            'http_status': provider_status.get('http_status'),
            'finish_reason': provider_status.get('finish_reason'),
            'error_body_length': len(str(provider_status.get('error_body') or '')),
        }
    transport_error = safe.get('model_transport_error')
    if isinstance(transport_error, dict):
        safe['model_transport_error'] = {
            key: value for key, value in transport_error.items() if key != 'error_message'
        }
    return safe


def log_and_emit_frame(frame: Any, cost: float, query: str, session_id: str, tag: str = 'FRAME') -> str:
    log_frame = _log_safe_frame(frame)
    LOG.debug(
        f'[ChatServer] [KB_CHAT_STREAM_{tag}] '
        f'[query={query}] [session_id={session_id}] '
        f'[cost={cost}] [data={json.dumps(log_frame, ensure_ascii=False, default=str)}]'
    )
    return sse_line(response_payload(200, 'success', frame, cost))


def single_event_stream_response(
    payload: Dict[str, Any],
    final_data: Optional[Dict[str, Any]] = None,
) -> StreamingResponse:
    async def _stream():
        yield sse_line(payload)
        yield sse_line(response_payload(
            200,
            'success',
            {'status': 'FINISHED', **(final_data or {})},
            0.0,
        ))

    return StreamingResponse(_stream(), media_type='text/event-stream')
