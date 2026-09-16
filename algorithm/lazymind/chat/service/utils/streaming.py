from __future__ import annotations

import json
from typing import Any, Dict
from lazyllm import LOG

from fastapi.responses import StreamingResponse
from lazymind.chat.runtime_events import runtime_event


def response_payload(code: int, msg: str, data: Any, cost: float) -> Dict[str, Any]:
    return {'code': code, 'msg': msg, 'data': data, 'cost': cost}


FRAME_BYTES = 64 * 1024


def sse_line(payload: Dict[str, Any]) -> str:
    """Split text only between complete JSON frames, counting encoded bytes."""
    def encode(value):
        return json.dumps(value, ensure_ascii=False, default=str) + '\n\n'

    encoded = encode(payload)
    if len(encoded.encode('utf-8')) <= FRAME_BYTES:
        return encoded
    data = payload.get('data')
    if not isinstance(data, dict):
        raise ValueError('non-chat frame exceeds byte budget')
    data = dict(data)
    text_parts = [(key, data.pop(key)) for key in ('text', 'think') if isinstance(data.get(key), str)]
    if len(encode({**payload, 'data': data}).encode('utf-8')) > FRAME_BYTES:
        # Sources are display metadata, not runtime/approval controls. Keep a
        # bounded view and a durable locator for the complete source list.
        if data.get('sources'):
            from lazymind.chat.engine.tools.infra.tool_result_budget import bound_tool_result
            bounded = bound_tool_result('citation_sources', data['sources'])
            data['sources'] = bounded if isinstance(bounded, list) else bounded.get('items', [])
        if len(encode({**payload, 'data': data}).encode('utf-8')) > FRAME_BYTES:
            raise ValueError('non-text frame metadata exceeds byte budget')
    frames = []
    if any(v for k, v in data.items() if k not in ('sources',)) or data.get('sources'):
        frames.append(encode({**payload, 'data': data}))
    for key, text in text_parts:
        while text:
            lo, hi = 1, len(text)
            while lo < hi:
                mid = (lo + hi + 1) // 2
                candidate = encode({**payload, 'data': {key: text[:mid], 'sources': []}})
                if len(candidate.encode('utf-8')) <= FRAME_BYTES:
                    lo = mid
                else:
                    hi = mid - 1
            frames.append(encode({**payload, 'data': {key: text[:lo], 'sources': []}}))
            text = text[lo:]
    return ''.join(frames)


def log_and_emit_frame(frame: Any, cost: float, query: str, session_id: str, tag: str = 'FRAME') -> str:
    LOG.debug(
        f'[ChatServer] [KB_CHAT_STREAM_{tag}] '
        f'[session_id={session_id}] '
        f'[cost={cost}] [frame_bytes={len(json.dumps(frame, ensure_ascii=False, default=str).encode())}]'
    )
    return sse_line(response_payload(200, 'success', frame, cost))


def single_event_stream_response(
    payload: Dict[str, Any],
    *,
    run_id: str,
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
                    'status': 'completed',
                    'reason': 'normal',
                    'partial_output': True,
                    'model_invoked': False,
                }),
            },
            0.0,
        ))

    return StreamingResponse(_stream(), media_type='text/event-stream')
