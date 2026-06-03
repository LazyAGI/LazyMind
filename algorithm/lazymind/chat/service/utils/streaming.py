from __future__ import annotations

import json
from typing import Any, Dict
from lazyllm import LOG

from fastapi.responses import StreamingResponse


def response_payload(code: int, msg: str, data: Any, cost: float) -> Dict[str, Any]:
    return {'code': code, 'msg': msg, 'data': data, 'cost': cost}


def sse_line(payload: Dict[str, Any]) -> str:
    return json.dumps(payload, ensure_ascii=False, default=str) + '\n\n'


def log_and_emit_frame(frame: Any, cost: float, query: str, session_id: str, tag: str = 'FRAME') -> str:
    LOG.debug(
        f'[ChatServer] [KB_CHAT_STREAM_{tag}] '
        f'[query={query}] [session_id={session_id}] '
        f'[cost={cost}] [data={json.dumps(frame, ensure_ascii=False, default=str)}]'
    )
    return sse_line(response_payload(200, 'success', frame, cost))


def single_event_stream_response(payload: Dict[str, Any]) -> StreamingResponse:
    async def _stream():
        yield sse_line(payload)
        yield sse_line(response_payload(200, 'success', {'status': 'FINISHED'}, 0.0))

    return StreamingResponse(_stream(), media_type='text/event-stream')
