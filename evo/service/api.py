from __future__ import annotations

import asyncio
import json
import os
import sqlite3
from pathlib import Path
from typing import Annotated, Any, Mapping

from fastapi import (
    BackgroundTasks,
    Body,
    FastAPI,
    HTTPException,
    Query,
    Request,
)
from pydantic import BaseModel, ConfigDict, Field
from sse_starlette.sse import EventSourceResponse
from starlette.responses import FileResponse

from evo.message_intent.schemas import MessageRequest
from evo.message_intent.storage import MessageConflictError, MessageInProgressError, message_history
from evo.message_intent.turn import run_turn

from .projections import ProjectionService
from .router_api import build_router_api
from .threads import ThreadService


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid')


class ThreadInputs(StrictModel):
    kb_id: list[str] = Field(default_factory=list)
    csv_data: list[dict[str, str]] = Field(default_factory=list)
    router_chat_url: str
    router_admin_url: str
    algorithm_id: str
    num_case: int = Field(gt=0)
    case_deadline_seconds: float = Field(default=300.0, gt=0)


class ThreadCreate(StrictModel):
    mode: str
    title: str = ''
    inputs: ThreadInputs
    llm_config: dict[str, Any]


class CommandRequest(StrictModel):
    command_id: str = ''
    until_step: str = ''


class EmptyCommandRequest(StrictModel):
    command_id: str = ''


class MessageBody(StrictModel):
    message_id: str = Field(default='', max_length=160)
    text: str = Field(default='', max_length=20000)
    content: str = Field(default='', max_length=20000)


CommandBody = Annotated[CommandRequest, Body()]
EmptyCommandBody = Annotated[EmptyCommandRequest, Body()]
MessageRequestBody = Annotated[MessageBody, Body()]
STEP_STREAM_TERMINAL_EVENTS = {'step.finish', 'step.pause', 'step.failed', 'step.cancel'}


class EvoService:
    def __init__(self, root: Path) -> None:
        self.root = root
        self.threads = ThreadService(root)
        self.projections = ProjectionService(root, self.threads.runtime)


def create_app() -> FastAPI:
    root = _service_root()
    service = EvoService(root)
    app = FastAPI(title='evo service', version='v0.3.0')
    app.state.service = service

    @app.get('/healthz')
    def healthz() -> dict[str, bool]:
        return {'ok': True}

    @app.post('/threads')
    def create_thread(payload: ThreadCreate) -> dict[str, Any]:
        return service.threads.create(payload.model_dump())

    @app.get('/threads')
    def list_threads(
        page_size: Annotated[int, Query(ge=1, le=200)] = 10,
        page_token: str = '',
        status: str = '',
    ) -> dict[str, Any]:
        return service.threads.list(page_size, page_token, status)

    @app.get('/threads/{thread_id}')
    def get_thread(thread_id: str) -> dict[str, Any]:
        return service.threads.public_thread(thread_id)

    @app.delete('/threads/{thread_id}')
    def delete_thread(thread_id: str) -> dict[str, Any]:
        return service.threads.delete(thread_id)

    @app.post('/threads/{thread_id}/start')
    def start_thread(
        thread_id: str,
        payload: CommandBody,
        background_tasks: BackgroundTasks,
    ) -> dict[str, str]:
        return service.threads.start(thread_id, payload.model_dump(), background_tasks.add_task)

    @app.post('/threads/{thread_id}/continue')
    def continue_thread(
        thread_id: str,
        payload: CommandBody,
        background_tasks: BackgroundTasks,
    ) -> dict[str, str]:
        return service.threads.continue_thread(thread_id, payload.model_dump(), background_tasks.add_task)

    @app.post('/threads/{thread_id}/retry')
    def retry_thread(
        thread_id: str,
        payload: CommandBody,
        background_tasks: BackgroundTasks,
    ) -> dict[str, str]:
        return service.threads.retry(thread_id, payload.model_dump(), background_tasks.add_task)

    @app.post('/threads/{thread_id}/pause')
    def pause_thread(
        thread_id: str,
        payload: EmptyCommandBody,
    ) -> dict[str, str]:
        return service.threads.pause(thread_id, payload.model_dump())

    @app.post('/threads/{thread_id}/cancel')
    def cancel_thread(
        thread_id: str,
        payload: EmptyCommandBody,
    ) -> dict[str, str]:
        return service.threads.cancel(thread_id, payload.model_dump())

    @app.get('/threads/{thread_id}/gates')
    def gates(thread_id: str) -> dict[str, Any]:
        return service.projections.gates(thread_id)

    @app.get('/threads/{thread_id}/steps')
    def steps(thread_id: str) -> dict[str, Any]:
        return service.projections.steps(thread_id)

    @app.get('/threads/{thread_id}/gates/{step}/versions/{version}:download')
    def gate_download(thread_id: str, step: str, version: int, format: str = 'json') -> FileResponse:  # noqa: A002
        path = service.projections.gate_download(thread_id, step, version, format)
        return FileResponse(path, filename=path.name, media_type='application/octet-stream')

    @app.get('/threads/{thread_id}/gates/{step}/versions/{version}')
    def gate_content(thread_id: str, step: str, version: int) -> dict[str, Any]:
        return service.projections.gate_content(thread_id, step, version)

    @app.get('/threads/{thread_id}/events:stream')
    def event_stream(thread_id: str, request: Request, step_id: str = '') -> EventSourceResponse:
        unsupported = set(request.query_params) - {'step_id'}
        if unsupported:
            raise HTTPException(422, f'unsupported query param: {sorted(unsupported)[0]}')
        return _event_stream(
            service,
            thread_id,
            step_id,
            request,
            service.projections.events,
            step_terminal_done=True,
        )

    @app.get('/threads/{thread_id}/event-trace:stream')
    def event_trace_stream(thread_id: str, request: Request, step_id: str = '') -> EventSourceResponse:
        unsupported = set(request.query_params) - {'step_id'}
        if unsupported:
            raise HTTPException(422, f'unsupported query param: {sorted(unsupported)[0]}')
        if not step_id:
            raise HTTPException(422, 'step_id is required')
        return _event_stream(service, thread_id, step_id, request, service.projections.event_trace)

    @app.get('/threads/{thread_id}/gates/eval/versions/{version}/bad-cases')
    def eval_report_bad_cases(
        thread_id: str,
        version: int,
        page_size: Annotated[int, Query(ge=1, le=200)] = 50,
        page_token: str = '',
        keyword: str = '',
        failure_type: str = '',
    ) -> dict[str, Any]:
        return service.projections.eval_bad_cases(thread_id, version, page_size, page_token, keyword, failure_type)

    @app.get('/threads/{thread_id}/gates/abtest/versions/{version}/case-details')
    def abtest_case_details(
        thread_id: str,
        version: int,
        page_size: Annotated[int, Query(ge=1, le=200)] = 50,
        page_token: str = '',
        keyword: str = '',
        outcome: str = '',
    ) -> dict[str, Any]:
        return service.projections.abtest_case_details(thread_id, version, page_size, page_token, keyword, outcome)

    @app.get('/threads/{thread_id}/results/traces/{trace_id}')
    def trace_detail(thread_id: str, trace_id: str) -> dict[str, Any]:
        if service.threads.runtime.run_config(thread_id) is None:
            raise HTTPException(404, f'thread not found: {thread_id}')
        from evo.traces import build_trace_detail_view

        return build_trace_detail_view(trace_id)

    @app.get('/threads/{thread_id}/results/traces:compare')
    def trace_compare(
        thread_id: str,
        a: Annotated[str, Query(min_length=1)],
        b: Annotated[str, Query(min_length=1)],
    ) -> dict[str, Any]:
        if service.threads.runtime.run_config(thread_id) is None:
            raise HTTPException(404, f'thread not found: {thread_id}')
        left = a.strip()
        right = b.strip()
        if not left or not right:
            raise HTTPException(422, 'a and b trace ids are required')
        from evo.traces import build_trace_compare_view

        return build_trace_compare_view(left, right)

    @app.get('/threads/{thread_id}/messages')
    def message_history_api(
        thread_id: str,
        page_size: Annotated[int, Query(ge=1, le=200)] = 50,
        page_token: str = '',
    ) -> dict[str, Any]:
        if service.threads.runtime.run_config(thread_id) is None:
            raise HTTPException(404, f'thread not found: {thread_id}')
        try:
            return message_history(service.root, thread_id, page_size, page_token).model_dump()
        except ValueError as exc:
            raise HTTPException(422, str(exc)) from exc

    @app.post('/threads/{thread_id}/messages')
    def messages(
        thread_id: str,
        payload: MessageRequestBody,
        request: Request,
        background_tasks: BackgroundTasks,
    ) -> Any:
        text = payload.text or payload.content
        if not text.strip():
            raise HTTPException(422, 'text or content is required')
        msg = MessageRequest(message_id=payload.message_id, text=text)
        result = _message_result(service, thread_id, msg, background_tasks)
        if 'text/event-stream' in request.headers.get('accept', ''):
            return _message_stream(result)
        return result.model_dump()

    @app.get('/candidates')
    def candidates(
        thread_id: str = '',
        status: str = '',
        page_size: Annotated[int, Query(ge=1, le=200)] = 20,
        page_token: str = '',
    ) -> dict[str, Any]:
        return service.projections.candidates(thread_id, status, page_size, page_token)

    @app.get('/candidates/{candidate_id:path}')
    def candidate(candidate_id: str) -> dict[str, Any]:
        return service.projections.candidate(candidate_id)

    app.include_router(build_router_api(service))

    return app


def get_app() -> FastAPI:
    return create_app()


def _service_root() -> Path:
    configured = os.getenv('LAZYMIND_EVO_BASE_DIR')
    if configured:
        return Path(configured)
    root = Path('/var/lib/lazymind/evo')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _message_result(service: EvoService, thread_id: str, request: MessageRequest, background_tasks: BackgroundTasks):
    try:
        return run_turn(
            'user', service.root, service.threads.runtime,
            lambda tid, config, command: service.threads.submit_message_command(
                tid, config, command, background_tasks.add_task,
            ),
            thread_id, request,
        )
    except MessageConflictError as exc:
        raise HTTPException(409, str(exc)) from exc
    except MessageInProgressError as exc:
        raise HTTPException(409, str(exc)) from exc
    except sqlite3.OperationalError as exc:
        if 'locked' in str(exc).lower():
            raise HTTPException(409, 'message store is busy; retry the same message_id') from exc
        raise
    except ValueError as exc:
        message = str(exc)
        if message.startswith('thread not found'):
            raise HTTPException(404, message) from exc
        raise HTTPException(422, message) from exc


def _message_stream(result) -> EventSourceResponse:
    async def events():
        payload = {
            'type': 'assistant_response',
            'thread_id': result.thread_id,
            'turn_id': result.turn_id,
            'message_id': result.message_id,
            'turn_decision': result.turn_decision,
            'content': result.assistant_text,
            'text': result.assistant_text,
        }
        yield {'event': 'assistant_response', 'data': json.dumps(payload, ensure_ascii=False)}
        for event, ref in (
            ('observation', result.observation_ref),
            ('action_receipt', result.action_receipt_ref),
            ('pending_approval', result.pending_approval_ref),
        ):
            if ref is not None:
                yield {'event': event, 'data': ref.model_dump_json()}
        yield {'event': 'message_result', 'data': result.model_dump_json()}
        yield {'data': '[DONE]'}

    return EventSourceResponse(events())


def _event_stream(
    service: EvoService,
    thread_id: str,
    step_id: str,
    request: Request,
    snapshot_fn,
    *,
    step_terminal_done: bool = False,
) -> EventSourceResponse:
    async def events():
        last_event_id = request.headers.get('last-event-id', '').strip()
        pending_step_done = False
        while True:
            snapshot = await asyncio.to_thread(snapshot_fn, thread_id, step_id, last_event_id)
            for item in snapshot['items']:
                last_event_id = str(item['event_id'])
                event_type = str(item['event_type'])
                yield {
                    'id': last_event_id,
                    'event': event_type,
                    'data': json.dumps(_sse_payload(event_type, item), ensure_ascii=False),
                }
                if step_terminal_done and _step_stream_terminal_event(snapshot, item):
                    pending_step_done = True
            if pending_step_done and await asyncio.to_thread(_thread_events_done, service, thread_id):
                payload = _done_payload(thread_id, last_event_id, snapshot)
                yield {
                    'id': last_event_id,
                    'event': 'done',
                    'data': json.dumps(_sse_payload('done', payload), ensure_ascii=False),
                }
                break
            if (
                (not snapshot.get('step_id') or not step_terminal_done)
                and not snapshot['items']
                and await asyncio.to_thread(_thread_events_done, service, thread_id)
            ):
                public = await asyncio.to_thread(service.threads.public_thread, thread_id, include_inputs=False)
                payload = _done_payload(thread_id, last_event_id, snapshot)
                payload.update({
                    'current_step': public['current_step'],
                    'checkpoint_state': public['checkpoint_state'],
                    'first_missing_step': public['first_missing_step'],
                    'last_released_step': public['last_released_step'],
                    'retry_from_step': public['retry_from_step'],
                    'last_error': public['last_error'],
                })
                yield {
                    'id': last_event_id,
                    'event': 'done',
                    'data': json.dumps(_sse_payload('done', payload), ensure_ascii=False),
                }
                break
            if await request.is_disconnected():
                break
            await asyncio.sleep(1.0)

    return EventSourceResponse(events())


def _done_payload(thread_id: str, last_event_id: str, snapshot: Mapping[str, Any]) -> dict[str, Any]:
    payload = {'thread_id': thread_id, 'last_event_id': last_event_id}
    if snapshot.get('step_id'):
        payload['step_id'] = snapshot['step_id']
    return payload


def _step_stream_terminal_event(snapshot: Mapping[str, Any], item: Mapping[str, Any]) -> bool:
    step_id = str(snapshot.get('step_id') or '')
    return bool(
        step_id
        and str(item.get('step_id') or '') == step_id
        and str(item.get('event_type') or '') in STEP_STREAM_TERMINAL_EVENTS
    )


def _sse_payload(event_type: str, payload: Mapping[str, Any]) -> dict[str, Any]:
    data = dict(payload)
    if event_type == 'done':
        data.pop('status', None)
        data.pop('thread_status', None)
    data.setdefault('event_type', event_type)
    data.setdefault('type', data['event_type'])
    return data


def _thread_events_done(service: EvoService, thread_id: str) -> bool:
    return service.threads.public_thread(thread_id, include_inputs=False)['status'] != 'running'
