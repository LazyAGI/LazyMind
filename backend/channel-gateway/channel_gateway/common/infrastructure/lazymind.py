from __future__ import annotations

import concurrent.futures
import datetime as dt
import json
import logging
import re
import time
import uuid
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any
from urllib.parse import quote, urlsplit

import httpx

from channel_gateway.common.domain.chat import (
    ChatOptions,
    CoreEvent,
    CoreRunTerminal,
    CoreStreamUpdate,
    CoreTurnResult,
)
from channel_gateway.common.domain.channel import sanitize_channel_text
from channel_gateway.common.errors import (
    InvalidStaticAssetError,
    LazyMindError,
    LazyMindHTTPError,
    RetryableLazyMindError,
)


_logger = logging.getLogger(__name__)
_CHAT_SEMANTIC_IDLE_SECONDS = 180
_CHAT_RESUME_ATTEMPTS = 8
_CHAT_RESUME_DELAY_SECONDS = 0.5
_LATEST_ANSWER_TIMEOUT_SECONDS = 3.0
_TASK_SNAPSHOT_TIMEOUT_SECONDS = 3.0
_TURN_ENRICHMENT_TIMEOUT_SECONDS = 10.0
_AUXILIARY_WORKER_COUNT = 2
_SYSTEM_TOOL_NAMES = {
    'ask_user',
    'schedule',
    'skill',
    'workflow',
    'subagent',
    'task',
    'task_center',
}
_MAX_CHANNEL_IMAGE_BYTES = 20 * 1024 * 1024
_MAX_CHANNEL_FILE_BYTES = 100 * 1024 * 1024
_CHAT_EVENT_FIELDS = (
    'task_created',
    'artifact_created',
    'ask_pending',
    'tool_limit_pending',
    'intent_updated',
)
_TP_CLOSE_TAG = '</tp>'
_TRP_CLOSE_TAG = '</trp>'
_TOOL_PAYLOAD_PAIR_RE = re.compile(
    r'(?s)<(tool_call|tool_result)>.*?</\1>'
)
_UNFINISHED_TOOL_PAYLOAD_RE = re.compile(
    r'(?s)<(?:tool_call|tool_result)>.*$'
)
_ORPHAN_TOOL_PAYLOAD_TAG_RE = re.compile(
    r'</?(?:tool_call|tool_result)>'
)
_THINKING_BLOCK_BREAK_RE = re.compile(
    r'</(?:tp|trp)>\s*<(?:tp|trp)\b[^>]*>'
)
_TP_PAIR_RE = re.compile(r'(?s)<tp\b[^>]*>(.*?)</tp>')
_TRP_PAIR_RE = re.compile(r'(?s)<trp\b[^>]*>(.*?)</trp>')
_ORPHAN_PREVIEW_TAG_RE = re.compile(r'</?(?:tp|trp)\b[^>]*>')


@dataclass
class _ChatStreamState:
    conversation_id: str
    history_id: str = ''
    deltas: list[str] = field(default_factory=list)
    last_message: str = ''
    run_terminal: CoreRunTerminal | None = None
    sources: list[Any] = field(default_factory=list)
    events: list[CoreEvent] = field(default_factory=list)
    last_stream_update: CoreStreamUpdate | None = None
    external_event_sequence: int = 0


def _runtime_terminal(value: Any) -> CoreRunTerminal | None:
    if value is None:
        return None
    if not isinstance(value, dict):
        raise LazyMindError('LazyMind runtime event is invalid')
    if (
        value.get('schema_version') != 1
        or not str(value.get('event_id') or '')
        or not str(value.get('run_id') or '')
    ):
        raise LazyMindError('LazyMind runtime event envelope is invalid')
    event_type = str(value.get('type') or '')
    if event_type not in {
        'model_retry_scheduled', 'model_call_finished', 'run_finished',
    }:
        raise LazyMindError('LazyMind runtime event type is unsupported')
    if event_type != 'run_finished':
        return None
    data = value.get('data')
    if not isinstance(data, dict):
        raise LazyMindError('LazyMind run terminal is invalid')
    status = str(data.get('status') or '')
    reason = str(data.get('reason') or '')
    partial_output = data.get('partial_output')
    if (
        status not in {'completed', 'interrupted', 'failed', 'cancelled'}
        or not reason
        or not isinstance(partial_output, bool)
    ):
        raise LazyMindError('LazyMind run terminal fields are invalid')
    return CoreRunTerminal(
        status=status,
        reason=reason,
        code=str(data.get('code') or ''),
        partial_output=partial_output,
    )


def _terminal_text(terminal: CoreRunTerminal) -> str:
    if terminal.status == 'cancelled':
        return 'LazyMind 已停止当前任务。'
    code = f'（{terminal.code}）' if terminal.code else ''
    if terminal.status == 'interrupted':
        return f'LazyMind 未能完整完成当前任务{code}。'
    return f'LazyMind 执行失败{code}。'


def _strip_tool_payloads(value: str) -> str:
    cleaned = _TOOL_PAYLOAD_PAIR_RE.sub('', value)
    cleaned = _UNFINISHED_TOOL_PAYLOAD_RE.sub('', cleaned)
    return _ORPHAN_TOOL_PAYLOAD_TAG_RE.sub('', cleaned)


def _last_thinking_boundary(value: str) -> int:
    last_trp = value.rfind(_TRP_CLOSE_TAG)
    if last_trp >= 0:
        return last_trp + len(_TRP_CLOSE_TAG)
    last_tp = value.rfind(_TP_CLOSE_TAG)
    return last_tp + len(_TP_CLOSE_TAG) if last_tp >= 0 else -1


def _format_thinking(value: str) -> str:
    if not value:
        return ''
    cleaned = _strip_tool_payloads(value)
    cleaned = _THINKING_BLOCK_BREAK_RE.sub('\n\n', cleaned)
    cleaned = _TP_PAIR_RE.sub(
        lambda match: match.group(1).strip(),
        cleaned,
    )
    cleaned = _TRP_PAIR_RE.sub(
        lambda match: match.group(1).strip(),
        cleaned,
    )
    cleaned = _ORPHAN_PREVIEW_TAG_RE.sub('\n\n', cleaned)
    return re.sub(r'\n{3,}', '\n\n', cleaned).strip()


def _optional_seconds(value: Any) -> int | None:
    try:
        seconds = int(float(value))
    except (TypeError, ValueError):
        return None
    return max(0, seconds)


class LazyMindClient:
    def __init__(self, base_url: str, chat_timeout_seconds: int):
        self._base_url = base_url.rstrip('/')
        self._timeout = httpx.Timeout(
            connect=20.0,
            read=float(chat_timeout_seconds),
            write=30.0,
            pool=20.0,
        )
        self._auxiliary = concurrent.futures.ThreadPoolExecutor(
            max_workers=_AUXILIARY_WORKER_COUNT,
            thread_name_prefix='channel-core-aux',
        )

    @staticmethod
    def _headers(
        owner_user_id: str,
        request_id: str,
        *,
        accept: str = 'application/json',
    ) -> dict[str, str]:
        return {
            'Accept': accept,
            'Content-Type': 'application/json',
            'X-User-Id': owner_user_id,
            'X-User-Name': owner_user_id,
            'X-Request-Id': request_id,
            'Idempotency-Key': request_id,
        }

    def chat(
        self,
        *,
        owner_user_id: str,
        text: str,
        conversation_id: str,
        request_id: str,
        options: ChatOptions | None = None,
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> CoreTurnResult:
        options = options or ChatOptions()
        new_conversation = not conversation_id
        tracked_task_ids, task_snapshot = self._start_task_snapshot(
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=request_id,
        )
        if new_conversation:
            conversation_id = str(uuid.uuid4())
        state = self._consume_chat_stream(
            owner_user_id=owner_user_id,
            request_id=request_id,
            payload=self._chat_payload(
                text=text,
                conversation_id=conversation_id,
                options=options,
                initial_settings=new_conversation,
            ),
            conversation_id=conversation_id,
            on_stream=on_stream,
        )
        if task_snapshot is not None:
            tracked_task_ids = self._finished_task_snapshot(
                task_snapshot,
                conversation_id,
            )
        return self._complete_chat_turn(
            owner_user_id=owner_user_id,
            request_id=request_id,
            state=state,
            tracked_task_ids=tracked_task_ids,
        )

    def _start_task_snapshot(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> tuple[
        set[str] | None,
        concurrent.futures.Future[list[dict[str, Any]]] | None,
    ]:
        if not conversation_id:
            return set(), None
        return None, self._auxiliary.submit(
            self._conversation_tasks,
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=f'{request_id}_tasks_before',
        )

    @staticmethod
    def _finished_task_snapshot(
        snapshot: concurrent.futures.Future[list[dict[str, Any]]],
        conversation_id: str,
    ) -> set[str] | None:
        try:
            return {
                str(task.get('task_id') or '')
                for task in snapshot.result(timeout=0)
                if task.get('task_id')
            }
        except concurrent.futures.TimeoutError:
            snapshot.cancel()
            _logger.warning(
                'channel_turn_task_snapshot_skipped '
                'conversation_id=%s reason=still_running',
                conversation_id,
            )
            return None
        except LazyMindError as exc:
            _logger.warning(
                'channel_turn_task_snapshot_failed '
                'conversation_id=%s error=%s',
                conversation_id,
                exc.__class__.__name__,
            )
            return None

    def _chat_payload(
        self,
        *,
        text: str,
        conversation_id: str,
        options: ChatOptions,
        initial_settings: bool,
    ) -> dict[str, Any]:
        conversation: dict[str, Any] = {}
        if options.search_config is not None:
            conversation['search_config'] = options.search_config
        payload: dict[str, Any] = {
            'conversation': conversation,
            'stream': True,
            'input': [
                {'text': text, 'input_type': 'text'},
                *[
                    dict(item)
                    for item in options.inputs
                    if isinstance(item, dict)
                    and item.get('input_type') in {'image', 'file'}
                ],
            ],
            'mode': 'auto',
            'create_time': dt.datetime.now(dt.timezone.utc).isoformat(),
        }
        if options.enable_workflow is not None:
            payload['enable_workflow'] = options.enable_workflow
        if options.disabled_tools:
            payload['disabled_tools'] = self._unique(options.disabled_tools)
        if options.mentions:
            payload['mentions'] = options.mentions
        if options.workflow_mode is not None:
            payload['workflow_mode'] = options.workflow_mode
        if options.use_memory is not None:
            payload['use_memory'] = options.use_memory
        if options.filters is not None:
            payload['filters'] = options.filters
        if options.ask_answers_structured is not None:
            payload['ask_answers_structured'] = (
                options.ask_answers_structured
            )
        if options.thinking_depth is not None:
            payload['thinking_depth'] = options.thinking_depth
        payload['conversation_id'] = conversation_id
        if initial_settings:
            initial_conversation_settings: dict[str, Any] = {}
            if options.enable_workflow is not None:
                initial_conversation_settings['enable_workflow'] = (
                    options.enable_workflow
                )
            if options.workflow_mode is not None:
                initial_conversation_settings['workflow_mode'] = options.workflow_mode
            if initial_conversation_settings:
                payload['initial_conversation_settings'] = (
                    initial_conversation_settings
                )
        return payload

    def _consume_chat_stream(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        payload: dict[str, Any],
        conversation_id: str,
        on_stream: Callable[[CoreStreamUpdate], None] | None,
        _state: _ChatStreamState | None = None,
        _endpoint: str = '',
        _resume_attempt: int = 0,
    ) -> _ChatStreamState:
        state = _state or _ChatStreamState(conversation_id=conversation_id)
        endpoint = _endpoint or f'{self._base_url}/conversations:chat'
        semantic_progress_at = time.monotonic()
        completed = False
        try:
            with httpx.stream(
                'POST',
                endpoint,
                json=payload,
                headers=self._headers(
                    owner_user_id,
                    request_id,
                    accept='text/event-stream',
                ),
                timeout=self._timeout,
            ) as response:
                self._raise_for_status(response, 'chat')
                for line in response.iter_lines():
                    normalized = line.strip()
                    if not normalized.startswith('data:'):
                        continue
                    data = normalized[5:].strip()
                    if not data:
                        continue
                    if data == '[DONE]':
                        continue
                    try:
                        frame = json.loads(data)
                    except json.JSONDecodeError as exc:
                        raise LazyMindError('LazyMind chat returned invalid SSE JSON') from exc
                    result = frame.get('result', frame)
                    if not isinstance(result, dict):
                        continue
                    event_sequence = int(
                        result.get('external_event_sequence') or 0
                    )
                    if (
                        event_sequence
                        and event_sequence <= state.external_event_sequence
                    ):
                        continue
                    if event_sequence:
                        state.external_event_sequence = event_sequence
                    semantic_progress = False
                    current_id = result.get('conversation_id')
                    if current_id:
                        state.conversation_id = str(current_id)
                    current_history_id = result.get('history_id')
                    if current_history_id:
                        state.history_id = str(current_history_id)
                    current_sources = result.get('sources')
                    if isinstance(current_sources, list) and current_sources:
                        state.sources = list(current_sources)
                        semantic_progress = True
                    task_created = None
                    for event_type in _CHAT_EVENT_FIELDS:
                        event_payload = result.get(event_type)
                        if event_payload:
                            semantic_progress = True
                            state.events.append(
                                CoreEvent(
                                    source='chat',
                                    type=event_type,
                                    payload=(
                                        dict(event_payload)
                                        if isinstance(event_payload, dict)
                                        else {'value': event_payload}
                                    ),
                                )
                            )
                            if (
                                event_type == 'task_created'
                                and isinstance(event_payload, dict)
                            ):
                                task_created = dict(event_payload)
                            if (
                                event_type == 'tool_limit_pending'
                                and isinstance(event_payload, dict)
                            ):
                                self._auto_summarize_tool_limit(
                                    owner_user_id=owner_user_id,
                                    conversation_id=state.conversation_id,
                                    request_id=request_id,
                                    payload=event_payload,
                                )
                    runtime_event = result.get('runtime_event')
                    terminal = _runtime_terminal(runtime_event)
                    if isinstance(runtime_event, dict):
                        state.events.append(CoreEvent(
                            source='chat',
                            type='runtime_event',
                            payload=dict(runtime_event),
                        ))
                        semantic_progress = True
                    delta = result.get('delta')
                    if isinstance(delta, str) and delta:
                        state.deltas.append(delta)
                        semantic_progress = True
                    message = result.get('message')
                    if isinstance(message, str) and message:
                        if message != state.last_message:
                            state.last_message = message
                            semantic_progress = True
                    if semantic_progress:
                        semantic_progress_at = time.monotonic()
                    elif (
                        time.monotonic() - semantic_progress_at
                        > _CHAT_SEMANTIC_IDLE_SECONDS
                    ):
                        raise LazyMindError(
                            'LazyMind chat made no progress for 180 seconds'
                        )
                    if on_stream is not None:
                        update = self._stream_update(
                            ''.join(state.deltas)
                            or state.last_message,
                            result.get('thinking_duration_s'),
                            conversation_id=state.conversation_id,
                            history_id=state.history_id,
                            task_created=task_created,
                        )
                        if update != state.last_stream_update:
                            on_stream(update)
                            state.last_stream_update = update
                    if terminal is not None:
                        state.run_terminal = terminal
                        completed = True
                        break
        except LazyMindHTTPError as exc:
            if exc.status_code not in {
                404, 408, 425, 429, 500, 502, 503, 504,
            }:
                raise
            return self._recover_chat_stream(
                owner_user_id=owner_user_id,
                request_id=request_id,
                initial_payload=payload,
                state=state,
                on_stream=on_stream,
                attempt=_resume_attempt,
                cause=exc,
            )
        except LazyMindError:
            raise
        except httpx.HTTPError as exc:
            return self._recover_chat_stream(
                owner_user_id=owner_user_id,
                request_id=request_id,
                initial_payload=payload,
                state=state,
                on_stream=on_stream,
                attempt=_resume_attempt,
                cause=exc,
            )
        if not completed:
            return self._recover_chat_stream(
                owner_user_id=owner_user_id,
                request_id=request_id,
                initial_payload=payload,
                state=state,
                on_stream=on_stream,
                attempt=_resume_attempt,
                cause=LazyMindError(
                    'LazyMind chat stream ended before completion'
                ),
            )
        return state

    def _recover_chat_stream(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        initial_payload: dict[str, Any],
        state: _ChatStreamState,
        on_stream: Callable[[CoreStreamUpdate], None] | None,
        attempt: int,
        cause: Exception,
    ) -> _ChatStreamState:
        if attempt >= _CHAT_RESUME_ATTEMPTS:
            raise LazyMindError(
                'Cannot resume LazyMind external Chat after Core disconnect'
            ) from cause
        time.sleep(_CHAT_RESUME_DELAY_SECONDS)
        endpoint = f'{self._base_url}/conversations:chat'
        payload = initial_payload
        if state.conversation_id and state.history_id:
            endpoint = f'{self._base_url}/conversations:resumeChat'
            payload = {
                'conversation_id': state.conversation_id,
                'history_id': state.history_id,
                'after_sequence': state.external_event_sequence,
            }
        _logger.warning(
            'channel_chat_stream_recovering conversation_id=%s '
            'history_id=%s attempt=%s',
            state.conversation_id,
            state.history_id,
            attempt + 1,
        )
        return self._consume_chat_stream(
            owner_user_id=owner_user_id,
            request_id=request_id,
            payload=payload,
            conversation_id=state.conversation_id,
            on_stream=on_stream,
            _state=state,
            _endpoint=endpoint,
            _resume_attempt=attempt + 1,
        )

    @staticmethod
    def _stream_update(
        raw_text: str,
        raw_thinking_seconds: Any,
        *,
        conversation_id: str = '',
        history_id: str = '',
        task_created: dict[str, Any] | None = None,
    ) -> CoreStreamUpdate:
        text = _strip_tool_payloads(raw_text)
        boundary = _last_thinking_boundary(text)
        if boundary >= 0:
            thinking_raw = text[:boundary]
            answer_raw = text[boundary:]
        elif '<tp' in text or '<trp' in text:
            thinking_raw = text
            answer_raw = ''
        else:
            thinking_raw = ''
            answer_raw = text
        return CoreStreamUpdate(
            thinking=_format_thinking(thinking_raw),
            answer=sanitize_channel_text(answer_raw),
            thinking_seconds=_optional_seconds(raw_thinking_seconds),
            conversation_id=conversation_id,
            history_id=history_id,
            task_created=task_created,
        )

    def _complete_chat_turn(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        state: _ChatStreamState,
        tracked_task_ids: set[str] | None,
    ) -> CoreTurnResult:
        answer = (
            state.last_message.strip()
            or ''.join(state.deltas).strip()
        )
        if not state.conversation_id:
            raise LazyMindError('LazyMind did not return a conversation id')
        if state.run_terminal is None:
            raise LazyMindError('LazyMind chat stream ended before completion')
        latest_item: dict[str, Any] = {}
        if (not answer and not state.events) or state.external_event_sequence > 0:
            try:
                latest_item = self._latest_history_item(
                    owner_user_id=owner_user_id,
                    conversation_id=state.conversation_id,
                    request_id=request_id,
                )
                if (
                    str(latest_item.get('id') or '') == state.history_id
                    and latest_item.get('result')
                ):
                    answer = str(latest_item['result']).strip()
            except LazyMindError:
                pass
        answer = sanitize_channel_text(answer)
        if state.run_terminal.status != 'completed':
            terminal_text = _terminal_text(state.run_terminal)
            answer = f'{answer}\n\n{terminal_text}'.strip()
        self._append_turn_artifacts(
            owner_user_id=owner_user_id,
            request_id=request_id,
            state=state,
        )
        if not answer and not state.events:
            raise LazyMindError('LazyMind returned no answer')
        if tracked_task_ids is not None:
            self._append_new_tasks(
                owner_user_id=owner_user_id,
                request_id=request_id,
                state=state,
                tracked_task_ids=tracked_task_ids,
            )
        execution = latest_item.get('execution')
        if isinstance(execution, dict):
            state.events.append(CoreEvent(
                source='conversation',
                type='execution_projection',
                payload=dict(execution),
            ))
        return CoreTurnResult(
            conversation_id=state.conversation_id,
            history_id=state.history_id,
            answer=answer,
            run_terminal=state.run_terminal,
            sources=tuple(state.sources),
            events=tuple(state.events),
        )

    def _append_turn_artifacts(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        state: _ChatStreamState,
    ) -> None:
        if not state.history_id:
            return
        try:
            existing_artifact_ids = {
                str(event.payload.get('artifact_id') or '')
                for event in state.events
                if event.type == 'artifact_created'
            }
            for artifact in self._auxiliary_result(
                lambda: self._turn_artifacts(
                    owner_user_id=owner_user_id,
                    conversation_id=state.conversation_id,
                    history_id=state.history_id,
                    request_id=request_id,
                ),
                timeout_seconds=_TURN_ENRICHMENT_TIMEOUT_SECONDS,
                error_label='turn artifacts',
            ):
                if (
                    str(artifact.get('artifact_id') or '')
                    in existing_artifact_ids
                ):
                    continue
                state.events.append(
                    CoreEvent(
                        source='conversation',
                        type='artifact_created',
                        payload=artifact,
                    )
                )
        except LazyMindError as exc:
            _logger.warning(
                'channel_turn_artifact_load_failed '
                'conversation_id=%s error=%s',
                state.conversation_id,
                exc.__class__.__name__,
            )

    def _append_new_tasks(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        state: _ChatStreamState,
        tracked_task_ids: set[str],
    ) -> None:
        try:
            task_event_indexes = {
                str(event.payload.get('task_id') or ''): index
                for index, event in enumerate(state.events)
                if event.type == 'task_created'
                and event.payload.get('task_id')
            }
            for task in self._auxiliary_result(
                lambda: self._conversation_tasks(
                    owner_user_id=owner_user_id,
                    conversation_id=state.conversation_id,
                    request_id=f'{request_id}_tasks_after',
                ),
                timeout_seconds=_TASK_SNAPSHOT_TIMEOUT_SECONDS,
                error_label='conversation tasks',
            ):
                task_id = str(task.get('task_id') or '')
                if not task_id or task_id in tracked_task_ids:
                    continue
                event_index = task_event_indexes.get(task_id)
                if event_index is not None:
                    current = state.events[event_index]
                    state.events[event_index] = CoreEvent(
                        source=current.source,
                        type=current.type,
                        payload={**current.payload, **task},
                    )
                    continue
                task_event_indexes[task_id] = len(state.events)
                state.events.append(
                    CoreEvent(
                        source='conversation',
                        type='task_created',
                        payload=task,
                    )
                )
        except LazyMindError as exc:
            _logger.warning(
                'channel_turn_task_load_failed '
                'conversation_id=%s error=%s',
                state.conversation_id,
                exc.__class__.__name__,
            )

    def _auxiliary_result(
        self,
        operation: Callable[[], list[dict[str, Any]]],
        *,
        timeout_seconds: float,
        error_label: str,
    ) -> list[dict[str, Any]]:
        future = self._auxiliary.submit(operation)
        try:
            return future.result(timeout=timeout_seconds)
        except concurrent.futures.TimeoutError as exc:
            future.cancel()
            raise LazyMindError(
                f'LazyMind {error_label} timed out'
            ) from exc

    def _conversation_tasks(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        summary_only: bool = False,
    ) -> list[dict[str, Any]]:
        query = '?summary_only=true' if summary_only else ''
        payload = self._request_json(
            'GET',
            (
                f'{self._base_url}/conversations/'
                f'{quote(conversation_id, safe="")}/tasks{query}'
            ),
            owner_user_id=owner_user_id,
            request_id=request_id,
            error_label='conversation tasks',
            timeout_seconds=_TASK_SNAPSHOT_TIMEOUT_SECONDS,
        )
        data = payload.get('data')
        raw_tasks = (
            data.get('tasks')
            if isinstance(data, dict)
            else payload.get('tasks')
        )
        return [
            dict(task)
            for task in (
                raw_tasks
                if isinstance(raw_tasks, list)
                else []
            )
            if isinstance(task, dict)
        ]

    def list_conversation_tasks(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        summary_only: bool = False,
    ) -> list[dict[str, Any]]:
        return self._conversation_tasks(
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=request_id,
            summary_only=summary_only,
        )

    def _turn_artifacts(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        history_id: str,
        request_id: str,
    ) -> list[dict[str, Any]]:
        payload = self._request_json(
            'GET',
            (
                f'{self._base_url}/conversations/'
                f'{quote(conversation_id, safe="")}/artifacts'
            ),
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_artifacts',
            error_label='conversation artifacts',
            timeout_seconds=_TURN_ENRICHMENT_TIMEOUT_SECONDS,
        )
        data = payload.get('data')
        raw_artifacts = (
            data.get('artifacts')
            if isinstance(data, dict)
            else payload.get('artifacts')
        )
        return [
            dict(artifact)
            for artifact in (
                raw_artifacts
                if isinstance(raw_artifacts, list)
                else []
            )
            if isinstance(artifact, dict)
            and str(artifact.get('history_id') or '') == history_id
        ]

    def _auto_summarize_tool_limit(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        payload: dict[str, Any],
    ) -> None:
        decision_id = str(payload.get('decision_id') or '').strip()
        if not conversation_id or not decision_id:
            return
        try:
            response = httpx.post(
                (
                    f'{self._base_url}/conversations/'
                    f'{quote(conversation_id, safe="")}:toolLimitDecision'
                ),
                json={
                    'decision_id': decision_id,
                    'action': 'summarize',
                },
                headers=self._headers(
                    owner_user_id,
                    f'{request_id}_tool_limit',
                ),
                timeout=10.0,
            )
            self._raise_for_status(
                response,
                'automatic tool-limit summary',
            )
        except (LazyMindError, httpx.HTTPError) as exc:
            _logger.warning(
                'channel_tool_limit_auto_summary_failed '
                'conversation_id=%s error=%s',
                conversation_id,
                exc.__class__.__name__,
            )

    def download_static_image(
        self,
        *,
        source: str,
        owner_user_id: str,
    ) -> bytes:
        """Refresh the user's signed URL immediately before downloading."""
        static_path = self._refresh_static_path(
            source,
            owner_user_id,
        )
        status_code, content = self._download_static_image(static_path)
        if status_code == 403:
            raise InvalidStaticAssetError(
                'LazyMind static image access was denied'
            )
        return content

    def validate_static_asset(
        self,
        *,
        source: str,
        owner_user_id: str,
    ) -> None:
        self._refresh_static_path(source, owner_user_id)

    def download_static_file(
        self,
        *,
        source: str,
        owner_user_id: str,
    ) -> bytes:
        static_path = self._refresh_static_path(
            source,
            owner_user_id,
        )
        try:
            with httpx.stream(
                'GET',
                f'{self._base_url}{static_path}',
                timeout=120.0,
            ) as response:
                self._raise_for_status(response, 'static file')
                raw_length = str(
                    response.headers.get('content-length') or ''
                ).strip()
                if (
                    raw_length.isdigit()
                    and int(raw_length) > _MAX_CHANNEL_FILE_BYTES
                ):
                    raise LazyMindError(
                        'LazyMind file is too large for the channel'
                    )
                content = bytearray()
                for chunk in response.iter_bytes():
                    content.extend(chunk)
                    if len(content) > _MAX_CHANNEL_FILE_BYTES:
                        raise LazyMindError(
                            'LazyMind file is too large for the channel'
                        )
                if not content:
                    raise LazyMindError('LazyMind file is empty')
                return bytes(content)
        except httpx.HTTPError as exc:
            raise LazyMindError(
                'Cannot download LazyMind static file'
            ) from exc

    def stop_chat_generation(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> None:
        self._request_json(
            'POST',
            f'{self._base_url}/conversations:stopChatGeneration',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'conversation_id': conversation_id},
            error_label='conversation stop',
        )

    def list_conversations(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        page_size: int = 100,
        page_token: str = '',
        assistant: str = '',
    ) -> dict[str, Any]:
        params: dict[str, Any] = {'page_size': page_size}
        if page_token:
            params['page_token'] = page_token
        if assistant:
            params['assistant'] = assistant
        return self._request_json(
            'GET',
            f'{self._base_url}/conversations',
            owner_user_id=owner_user_id,
            request_id=request_id,
            params=params,
            error_label='conversation list',
        )

    def list_external_agent_sessions(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        page_size: int = 100,
        page_token: str = '',
    ) -> dict[str, Any]:
        params: dict[str, Any] = {'page_size': page_size}
        if page_token:
            params['page_token'] = page_token
        payload = self._request_json(
            'GET',
            f'{self._base_url}/external-chat/providers/'
            f'{quote(provider, safe="")}/sessions',
            owner_user_id=owner_user_id,
            request_id=request_id,
            params=params,
            error_label='external Agent session catalog',
        )
        data = payload.get('data')
        return dict(data) if isinstance(data, dict) else payload

    def bind_external_agent_session(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        host_id: str,
        provider_thread_id: str,
    ) -> dict[str, Any]:
        payload = self._request_json(
            'POST',
            f'{self._base_url}/external-chat/providers/'
            f'{quote(provider, safe="")}/sessions/'
            f'{quote(provider_thread_id, safe="")}/binding',
            owner_user_id=owner_user_id,
            request_id=request_id,
            params={'host_id': host_id},
            error_label='external Agent session binding',
        )
        data = payload.get('data')
        if not isinstance(data, dict):
            raise LazyMindError('LazyMind external Agent binding response is invalid')
        return dict(data)

    def get_conversation_detail(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> dict[str, Any]:
        payload = self._request_json(
            'GET',
            f'{self._base_url}/conversations/{quote(conversation_id, safe="")}:detail',
            owner_user_id=owner_user_id,
            request_id=request_id,
            error_label='conversation detail',
        )
        conversation = payload.get('conversation')
        if not isinstance(conversation, dict):
            raise LazyMindError('LazyMind conversation detail is invalid')
        return conversation

    def get_conversation_history(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        page_size: int = 3,
        page_token: str = '',
    ) -> dict[str, Any]:
        params: dict[str, Any] = {'page_size': page_size}
        if page_token:
            params['page_token'] = page_token
        return self._request_json(
            'GET',
            f'{self._base_url}/conversations/{quote(conversation_id, safe="")}:history',
            owner_user_id=owner_user_id,
            request_id=request_id,
            params=params,
            error_label='conversation history',
        )

    def update_conversation_search_config(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        dataset_ids: list[str],
    ) -> dict[str, Any]:
        payload = self._request_json(
            'PATCH',
            f'{self._base_url}/conversations/{quote(conversation_id, safe="")}:search-config',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'dataset_ids': dataset_ids},
            error_label='conversation knowledge-base configuration',
        )
        data = payload.get('data')
        if not isinstance(data, dict):
            raise LazyMindError('LazyMind conversation configuration response is invalid')
        return data

    def list_chat_executors(
        self,
        *,
        owner_user_id: str,
        request_id: str,
    ) -> list[dict[str, Any]]:
        payload = self._request_json(
            'GET',
            f'{self._base_url}/chat/executors',
            owner_user_id=owner_user_id,
            request_id=request_id,
            error_label='chat executor catalog',
        )
        data = payload.get('data')
        values = data.get('executors') if isinstance(data, dict) else None
        if not isinstance(values, list):
            raise LazyMindError('LazyMind chat executor catalog is invalid')
        return [dict(item) for item in values if isinstance(item, dict)]

    def update_conversation_agent_settings(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        settings: dict[str, Any],
    ) -> None:
        self._request_json(
            'PATCH',
            f'{self._base_url}/conversations/'
            f'{quote(conversation_id, safe="")}/settings',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body=settings,
            error_label='conversation agent configuration',
        )

    def get_capability_catalog(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        kinds: set[str],
    ) -> dict[str, Any]:
        requests: dict[str, tuple[str, str, dict[str, Any]]] = {}
        if 'knowledge_base' in kinds:
            requests['datasets'] = (
                '/datasets',
                'knowledge bases',
                {'page_size': 100},
            )
        if 'skill' in kinds:
            requests['skills'] = ('/skills', 'skills', {'page_size': 100})
        if 'tool' in kinds:
            requests['tools'] = ('/tools', 'tools', {})
        if 'personalization' in kinds:
            requests['personalization'] = (
                '/personalization-setting',
                'personalization setting',
                {},
            )
        if 'workflow' in kinds:
            requests['workflows'] = (
                '/chat/settings/workflows',
                'workflows',
                {},
            )
        if 'prompt' in kinds:
            requests['prompts'] = (
                '/prompts',
                'prompts',
                {'page_size': 100},
            )
        if 'conversation' in kinds:
            requests['conversations'] = (
                '/conversations',
                'conversation list',
                {'page_size': 50},
            )

        def load(item: tuple[str, tuple[str, str, dict[str, Any]]]):
            key, (path, error_label, params) = item
            return key, self._request_json(
                'GET',
                f'{self._base_url}{path}',
                owner_user_id=owner_user_id,
                request_id=f'{request_id}_{key}',
                params=params or None,
                error_label=error_label,
            )

        payloads: dict[str, dict[str, Any]] = {}
        if requests:
            with concurrent.futures.ThreadPoolExecutor(
                max_workers=len(requests)
            ) as executor:
                for key, payload in executor.map(load, requests.items()):
                    payloads[key] = payload
        datasets_payload = payloads.get('datasets', {})
        skills_payload = payloads.get('skills', {})
        tools_payload = payloads.get('tools', {})
        personalization_payload = payloads.get('personalization', {})
        workflows_payload = payloads.get('workflows', {})
        prompts_payload = payloads.get('prompts', {})
        conversations_payload = payloads.get('conversations', {})

        datasets = datasets_payload.get('datasets')
        skill_data = skills_payload.get('data')
        skills = skill_data.get('items') if isinstance(skill_data, dict) else None
        tool_data = tools_payload.get('data')
        tools = tool_data.get('tool_groups') if isinstance(tool_data, dict) else None
        personalization_data = personalization_payload.get('data')
        personalization_enabled = (
            bool(personalization_data.get('enabled', True))
            if isinstance(personalization_data, dict)
            else True
        )
        workflow_data = workflows_payload.get('data')
        workflows = (
            workflow_data.get('workflows')
            if isinstance(workflow_data, dict)
            else None
        )
        prompts = prompts_payload.get('prompts')
        conversations = conversations_payload.get('conversations')
        workflow_items = [
            {
                'id': str(item.get('workflow_ref') or ''),
                'workflow_id': str(item.get('workflow_id') or ''),
                'name': str(item.get('name') or '').strip(),
                'description': str(item.get('description') or '').strip(),
                'enabled': bool(item.get('enabled', False)),
            }
            for item in (workflows if isinstance(workflows, list) else [])
            if isinstance(item, dict)
            and item.get('workflow_ref')
            and str(item.get('name') or '').strip()
        ]
        return {
            'knowledge_base': [
                {
                    'id': str(item.get('dataset_id') or ''),
                    'name': str(item.get('display_name') or '').strip(),
                    'enabled': bool(item.get('default_dataset', False)),
                    'default': bool(item.get('default_dataset', False)),
                }
                for item in (datasets if isinstance(datasets, list) else [])
                if isinstance(item, dict)
                and item.get('dataset_id')
                and str(item.get('display_name') or '').strip()
            ],
            'skill': [
                {
                    'id': str(item.get('id') or item.get('skill_id') or ''),
                    'name': str(item.get('name') or item.get('skill_name') or '').strip(),
                    'enabled': bool(item.get('is_enabled', True)),
                    'category': str(item.get('category') or ''),
                }
                for item in (skills if isinstance(skills, list) else [])
                if isinstance(item, dict)
                and item.get('head_revision_id')
                and (item.get('id') or item.get('skill_id'))
            ],
            'tool': [
                {
                    'id': str(item.get('name') or ''),
                    'name': str(item.get('label') or item.get('name') or '').strip(),
                    'enabled': not bool(item.get('disabled', False)),
                    'can_disable': bool(item.get('can_disable', False)),
                }
                for item in (tools if isinstance(tools, list) else [])
                if isinstance(item, dict)
                and bool(item.get('active', False))
                and str(item.get('name') or '') not in _SYSTEM_TOOL_NAMES
            ],
            'personalization': [
                {
                    'id': 'personalization',
                    'name': '个人习惯',
                    'enabled': personalization_enabled,
                }
            ],
            'workflow': workflow_items,
            'prompt': [
                {
                    'id': str(item.get('id') or ''),
                    'name': str(
                        item.get('display_name')
                        or item.get('name')
                        or ''
                    ).strip(),
                    'description': str(item.get('description') or '').strip(),
                    'content': str(item.get('content') or ''),
                    'enabled': True,
                }
                for item in (prompts if isinstance(prompts, list) else [])
                if isinstance(item, dict)
                and item.get('id')
                and str(
                    item.get('display_name')
                    or item.get('name')
                    or ''
                ).strip()
            ],
            'conversation': [
                {
                    'id': str(item.get('conversation_id') or ''),
                    'name': str(
                        item.get('display_name')
                        or item.get('conversation_id')
                        or ''
                    ).strip(),
                    'enabled': True,
                }
                for item in (
                    conversations if isinstance(conversations, list) else []
                )
                if isinstance(item, dict)
                and item.get('conversation_id')
            ],
        }

    def set_default_dataset(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        dataset_id: str,
        name: str,
        enabled: bool,
    ) -> None:
        action = 'setDefault' if enabled else 'unsetDefault'
        self._request_json(
            'POST',
            f'{self._base_url}/datasets/{quote(dataset_id, safe="")}:{action}',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'name': name},
            error_label='default knowledge base',
        )

    def set_tool_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        tool_name: str,
        enabled: bool,
    ) -> None:
        action = 'enable' if enabled else 'disable'
        self._request_json(
            'POST',
            f'{self._base_url}/tools/{quote(tool_name, safe="")}:{action}',
            owner_user_id=owner_user_id,
            request_id=request_id,
            error_label='tool setting',
        )

    def set_skill_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        skill_id: str,
        enabled: bool,
    ) -> None:
        self._request_json(
            'PATCH',
            f'{self._base_url}/skills/'
            f'{quote(skill_id, safe="")}',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'is_enabled': enabled},
            error_label='Skill setting',
        )

    def set_workflow_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        workflow_ref: str,
        enabled: bool,
    ) -> None:
        self._request_json(
            'PATCH',
            f'{self._base_url}/chat/settings/workflows/'
            f'{quote(workflow_ref, safe="")}',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'enabled': enabled},
            error_label='workflow setting',
        )

    def set_personalization_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        enabled: bool,
    ) -> None:
        self._request_json(
            'PUT',
            f'{self._base_url}/personalization-setting',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'enabled': enabled},
            error_label='personalization setting',
        )

    def _request_json(
        self,
        method: str,
        endpoint: str,
        *,
        owner_user_id: str,
        request_id: str,
        error_label: str,
        params: dict[str, Any] | None = None,
        json_body: dict[str, Any] | None = None,
        timeout_seconds: float | None = None,
    ) -> dict[str, Any]:
        try:
            response = httpx.request(
                method,
                endpoint,
                params=params,
                json=json_body,
                headers=self._headers(owner_user_id, request_id),
                timeout=(
                    timeout_seconds
                    if timeout_seconds is not None
                    else self._timeout
                ),
            )
        except httpx.HTTPError as exc:
            raise RetryableLazyMindError(
                f'Cannot load LazyMind {error_label}'
            ) from exc
        self._raise_for_status(response, error_label)
        try:
            payload = response.json()
        except ValueError as exc:
            raise LazyMindError(
                f'LazyMind {error_label} returned invalid JSON'
            ) from exc
        if not isinstance(payload, dict):
            raise LazyMindError(f'LazyMind {error_label} returned an invalid payload')
        return payload

    @staticmethod
    def _raise_for_status(response: httpx.Response, error_label: str) -> None:
        if 200 <= response.status_code < 300:
            return
        body = response.read().decode('utf-8', errors='replace')
        raise LazyMindHTTPError(
            response.status_code,
            f'LazyMind {error_label} returned HTTP {response.status_code}: {body[:300]}',
        )

    def _latest_history_item(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> dict[str, Any]:
        payload = self._request_json(
            'GET',
            (
                f'{self._base_url}/conversations/'
                f'{quote(conversation_id, safe="")}:history'
            ),
            owner_user_id=owner_user_id,
            request_id=request_id,
            params={'page_size': 1},
            error_label='latest conversation answer',
            timeout_seconds=_LATEST_ANSWER_TIMEOUT_SECONDS,
        )
        history = payload.get('history')
        if not isinstance(history, list) or not history:
            return {}
        item = history[0]
        if not isinstance(item, dict):
            return {}
        return dict(item)

    def _download_static_image(self, static_path: str) -> tuple[int, bytes]:
        try:
            with httpx.stream(
                'GET',
                f'{self._base_url}{static_path}',
                timeout=60.0,
            ) as response:
                if response.status_code == 403:
                    return response.status_code, b''
                self._raise_for_status(response, 'static image')
                content_type = str(
                    response.headers.get('content-type') or ''
                ).lower()
                if not content_type.startswith('image/'):
                    raise LazyMindError('LazyMind static file is not an image')
                raw_length = str(
                    response.headers.get('content-length') or ''
                ).strip()
                if raw_length.isdigit() and int(raw_length) > _MAX_CHANNEL_IMAGE_BYTES:
                    raise LazyMindError('LazyMind image is too large for the channel')
                content = bytearray()
                for chunk in response.iter_bytes():
                    content.extend(chunk)
                    if len(content) > _MAX_CHANNEL_IMAGE_BYTES:
                        raise LazyMindError('LazyMind image is too large for the channel')
                if not content:
                    raise LazyMindError('LazyMind image is empty')
                return response.status_code, bytes(content)
        except httpx.HTTPError as exc:
            raise LazyMindError('Cannot download LazyMind static image') from exc

    def _refresh_static_path(
        self,
        source: str,
        owner_user_id: str,
    ) -> str:
        original = str(source or '').strip()
        self._static_file_path(original)
        payload = self._request_json(
            'POST',
            f'{self._base_url}/static-files:sign',
            owner_user_id=owner_user_id,
            request_id=f'channel_asset_{uuid.uuid4().hex}',
            json_body={'paths': [original]},
            error_label='static file signing',
        )
        urls = payload.get('urls')
        if not isinstance(urls, dict):
            data = payload.get('data')
            urls = data.get('urls') if isinstance(data, dict) else None
        refreshed = (
            str(urls.get(original) or '').strip()
            if isinstance(urls, dict)
            else ''
        )
        if not refreshed:
            raise InvalidStaticAssetError(
                'LazyMind static file could not be refreshed'
            )
        return self._static_file_path(refreshed)

    @staticmethod
    def _static_file_path(source: str) -> str:
        parsed = urlsplit(str(source or '').strip())
        if not parsed.path.startswith('/static-files/'):
            raise InvalidStaticAssetError(
                'Only LazyMind static files can be sent to a channel'
            )
        suffix = f'?{parsed.query}' if parsed.query else ''
        return f'{parsed.path}{suffix}'

    @staticmethod
    def mention(resource_type: str, item: dict[str, Any]) -> dict[str, str]:
        return {
            'mention_id': f'channel_{uuid.uuid4().hex}',
            'type': resource_type,
            'resource_id': str(item.get('id') or ''),
            'display_name': str(item.get('name') or ''),
        }

    @staticmethod
    def _unique(values: list[str]) -> list[str]:
        return list(dict.fromkeys(value for value in values if value))
