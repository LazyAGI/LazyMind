from __future__ import annotations

import asyncio
import json

from fastapi import Request
from sse_starlette.sse import EventSourceResponse

from .projections import ProjectionService


async def execution_stream(projections: ProjectionService, thread_id: str,
                           step_id: str, request: Request, *, trace: bool
                           ) -> EventSourceResponse:
    cursor = request.headers.get('last-event-id', '').strip()
    projection = projections.event_trace if trace else projections.events
    initial = await projection(thread_id, step_id, cursor)

    async def stream():
        nonlocal initial, cursor
        while True:
            snapshot = initial
            initial = None
            if snapshot is None:
                snapshot = await projection(thread_id, step_id, cursor)
            for event in snapshot['items']:
                cursor = str(event['event_id'])
                payload = dict(event, type=event['event_type'])
                yield {
                    'id': cursor,
                    'event': event['event_type'],
                    'data': json.dumps(payload, ensure_ascii=False),
                }
            if snapshot['terminal']:
                done = {
                    key: snapshot.get(key)
                    for key in (
                        'thread_id', 'step_id', 'status', 'reason', 'current_step',
                        'checkpoint_state', 'first_missing_step',
                        'last_released_step', 'retry_from_step', 'last_error',
                    )
                }
                done['last_event_id'] = cursor
                yield {
                    'event': 'done',
                    'data': json.dumps(done, ensure_ascii=False),
                }
                return
            if await request.is_disconnected():
                return
            await asyncio.sleep(0.1)

    return EventSourceResponse(stream())


def message_stream(result: dict[str, object]) -> EventSourceResponse:
    async def stream():
        response = {
            'type': 'assistant_response',
            'thread_id': result['thread_id'],
            'turn_id': result['turn_id'],
            'message_id': result['message_id'],
            'turn_decision': result['turn_decision'],
            'content': result.get('assistant_text', ''),
            'text': result.get('assistant_text', ''),
        }
        yield {
            'event': 'assistant_response',
            'data': json.dumps(response, ensure_ascii=False),
        }
        for event, key in (
            ('observation', 'observation_ref'),
            ('action_receipt', 'action_receipt_ref'),
            ('pending_approval', 'pending_approval_ref'),
        ):
            if result.get(key) is not None:
                yield {
                    'event': event,
                    'data': json.dumps(result[key], ensure_ascii=False),
                }
        yield {
            'event': 'message_result',
            'data': json.dumps(result, ensure_ascii=False),
        }
        yield {'data': '[DONE]'}

    return EventSourceResponse(stream())


__all__ = ['execution_stream', 'message_stream']
