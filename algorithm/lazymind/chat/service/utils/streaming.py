from __future__ import annotations

import base64
import json
import uuid
from typing import Any, Dict
from lazyllm import LOG

from fastapi.responses import StreamingResponse
from lazymind.chat.runtime_events import runtime_event


def response_payload(code: int, msg: str, data: Any, cost: float) -> Dict[str, Any]:
    return {'code': code, 'msg': msg, 'data': data, 'cost': cost}


# Keep each encoded line below Core's 512 KiB Scanner limit, including envelope.
MAX_LINE_BYTES = 256 * 1024
CHUNK_BYTES = 180 * 1024
MAX_MESSAGE_BYTES = 32 * 1024 * 1024


def sse_line(payload: Dict[str, Any]) -> str:
    encoded = json.dumps(payload, ensure_ascii=False, default=str)
    raw = encoded.encode('utf-8')
    if len(raw) > MAX_MESSAGE_BYTES:
        raise ValueError('algorithm stream frame exceeds 32 MiB reassembly limit')
    if len(raw) <= MAX_LINE_BYTES:
        return encoded + '\n\n'
    message_id = uuid.uuid4().hex
    total = (len(raw) + CHUNK_BYTES - 1) // CHUNK_BYTES
    return ''.join(
        json.dumps({'stream_chunk': {
            'version': 1, 'id': message_id, 'index': index, 'total': total,
            'size': len(raw),
            'payload': base64.b64encode(raw[offset:offset + CHUNK_BYTES]).decode('ascii'),
        }}, separators=(',', ':')) + '\n\n'
        for index, offset in enumerate(range(0, len(raw), CHUNK_BYTES))
    )


def log_and_emit_frame(frame: Any, cost: float, query: str, session_id: str, tag: str = 'FRAME') -> str:
    LOG.debug(
        f'[ChatServer] [KB_CHAT_STREAM_{tag}] '
        f'[query_length={len(query)}] [session_id={session_id}] '
        f'[cost={cost}] [frame_type={type(frame).__name__}]'
    )
    return sse_line(response_payload(200, 'success', frame, cost))


def single_event_stream_response(
    payload: Dict[str, Any],
    *,
    run_id: str,
    error_code: str | None = None,
) -> StreamingResponse:
    """Complete a static response without claiming a successful model call."""
    async def _stream():
        yield sse_line(payload)
        yield sse_line(response_payload(
            200,
            'success',
            {
                'think': None,
                'text': None,
                'sources': [],
                'runtime_event': runtime_event('run_finished', run_id, {
                    'status': 'failed' if error_code else 'completed',
                    'reason': 'runtime_failure' if error_code else 'normal',
                    **({'code': error_code} if error_code else {}),
                    'partial_output': True,
                    'model_invoked': False,
                }),
            },
            0.0,
        ))

    return StreamingResponse(_stream(), media_type='text/event-stream')
