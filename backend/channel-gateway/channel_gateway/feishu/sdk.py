import asyncio
import copy
import hashlib
import json
import logging
import math
import queue
import re
import threading
import time
import uuid
from collections.abc import Callable, Mapping
from typing import Any

from lark_channel import (
    FeishuChannel,
    InboundConfig,
    MediaCapabilities,
    MediaSource,
    OutboundConfig,
    OutboundCard,
    OutboundFile,
    OutboundImage,
    PolicyConfig,
    RetryConfig,
    SafetyConfig,
    SecurityConfig,
    SendOpts,
    TextBatchConfig,
    TransportConfig,
)
from lark_channel.api.cardkit.v1.model.id_convert_card_request import (
    IdConvertCardRequest,
)
from lark_channel.api.cardkit.v1.model.id_convert_card_request_body import (
    IdConvertCardRequestBody,
)
from lark_channel.api.cardkit.v1.model.settings_card_request import (
    SettingsCardRequest,
)
from lark_channel.api.cardkit.v1.model.settings_card_request_body import (
    SettingsCardRequestBody,
)
from lark_channel.api.cardkit.v1.model.content_card_element_request import (
    ContentCardElementRequest,
)
from lark_channel.api.cardkit.v1.model.content_card_element_request_body import (
    ContentCardElementRequestBody,
)
from lark_channel.channel.errors import (
    FeishuChannelError,
    FeishuChannelErrorCode,
)
from lark_channel.channel.normalize import flatten, parse_message_content
from lark_channel.event.callback.model.p2_card_action_trigger import (
    P2CardActionTriggerResponse,
)
from lark_channel.event.custom import CustomizedEventProcessor

from channel_gateway.common.errors import RetryableProviderSideEffectError
from channel_gateway.common.domain.chat import CoreStreamUpdate
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.feishu.domain import (
    FeishuAppCredentials,
    FeishuInboundAction,
    FeishuInboundMenu,
    FeishuInboundMessage,
    FeishuRuntimeError,
    workspace_card_expired,
)
from channel_gateway.feishu.presentation import (
    parse_ask_form_submission,
    streamable_feishu_text,
)
from channel_gateway.feishu.workspace import is_feishu_image_key


_logger = logging.getLogger(__name__)
_STREAM_ABORT = object()
_STREAM_FINISH = object()
_STREAM_PAUSE_FOR_INTERACTION = object()
_STREAM_PROVIDER_TIMEOUT_SECONDS = 60
_STREAM_FINISH_TIMEOUT_SECONDS = 120
_STREAM_MESSAGE_UPDATE_INTERVAL_SECONDS = 0.4
_STREAM_ELEMENT_RETRY_DELAYS = (0.5, 1.0, 2.0, 4.0)
_STREAM_ELEMENT_RETRY_BUDGET_SECONDS = 30.0
_STREAM_CLOSE_RETRY_BUDGET_SECONDS = 10.0
_FEISHU_RATE_LIMIT_CODES = {11020, 11021, 99991400, 99991402}


class _StreamRetryCancelled(RuntimeError):
    pass


class _StreamCloseFailed(RuntimeError):
    pass


def _retryable_side_effect_error(
    label: str,
    error: Any,
) -> RetryableProviderSideEffectError:
    return RetryableProviderSideEffectError(
        f'{label}: {error}',
        retry_after_seconds=getattr(
            error,
            'retry_after_seconds',
            None,
        ),
    )


def _response_retry_after_seconds(response: Any) -> float | None:
    raw = getattr(response, 'raw', None)
    headers = getattr(raw, 'headers', None)
    if not isinstance(headers, Mapping):
        return None
    normalized_headers = {
        str(key).lower(): header_value
        for key, header_value in headers.items()
    }
    value = normalized_headers.get(
        'x-ogw-ratelimit-reset',
        normalized_headers.get('retry-after'),
    )
    try:
        retry_after = float(value)
    except (TypeError, ValueError):
        return None
    return (
        retry_after
        if math.isfinite(retry_after) and retry_after >= 0
        else None
    )


def _cardkit_response_error(
    response: Any,
    *,
    label: str,
) -> FeishuChannelError | None:
    try:
        code = int(getattr(response, 'code', None))
    except (TypeError, ValueError):
        code = -1
    raw_response = getattr(response, 'raw', None)
    try:
        status_code = int(getattr(raw_response, 'status_code', None))
    except (TypeError, ValueError):
        status_code = -1
    rate_limited = (
        status_code == 429
        or code in _FEISHU_RATE_LIMIT_CODES
    )
    if code == 0 and not rate_limited:
        return None
    error = FeishuChannelError(
        (
            FeishuChannelErrorCode.RATE_LIMITED
            if rate_limited
            else FeishuChannelErrorCode.UNKNOWN
        ),
        f"{label}: {{'code': {code}, 'msg': "
        f"{getattr(response, 'msg', '')!r}}}",
    )
    error.raw_code = code
    error.retry_after_seconds = _response_retry_after_seconds(response)
    return error


def _message_text(message_type: str, raw_content: str) -> str:
    if message_type not in {'text', 'post'}:
        return ''
    try:
        text, _resources = flatten(
            parse_message_content(message_type, raw_content)
        )
    except (TypeError, ValueError):
        return ''
    return str(text or '').strip()


def _message_image_key(message_type: str, raw_content: str) -> str:
    if message_type != 'image':
        return ''
    try:
        content = json.loads(raw_content or '{}')
    except (TypeError, ValueError):
        return ''
    return (
        str(content.get('image_key') or '')
        if isinstance(content, dict)
        else ''
    )


class _WorkspaceFeishuChannel(FeishuChannel):
    """Closes CardKit streaming state after an in-place reply finishes."""

    def __init__(self, *args, **kwargs):
        self._card_ids: dict[str, str] = {}
        self._card_sequences: dict[str, int] = {}
        self._card_state_lock = threading.Lock()
        super().__init__(*args, **kwargs)

    async def update_card_element_content(
        self,
        card_id: str,
        element_id: str,
        content: str,
        sequence: int,
    ) -> None:
        request_uuid = str(uuid.uuid5(
            uuid.NAMESPACE_URL,
            ':'.join((
                card_id,
                element_id,
                str(sequence),
                hashlib.sha256(content.encode('utf-8')).hexdigest(),
            )),
        ))
        request_body = (
            ContentCardElementRequestBody.builder()
            .uuid(request_uuid)
            .content(content)
            .sequence(sequence)
            .build()
        )
        request = (
            ContentCardElementRequest.builder()
            .card_id(card_id)
            .element_id(element_id)
            .request_body(request_body)
            .build()
        )
        response = await (
            self._driver._client.cardkit.v1.card_element.acontent(request)
        )
        error = _cardkit_response_error(
            response,
            label='update_card_element_content failed',
        )
        if error is not None:
            raise error

    async def finish_streaming_card(
        self,
        card_id: str,
        sequence: int,
    ) -> None:
        settings = json.dumps(
            {'config': {'streaming_mode': False}},
            ensure_ascii=False,
        )
        request_uuid = str(uuid.uuid5(
            uuid.NAMESPACE_URL,
            ':'.join((
                card_id,
                str(sequence),
                hashlib.sha256(settings.encode('utf-8')).hexdigest(),
            )),
        ))
        request_body = (
            SettingsCardRequestBody.builder()
            .settings(settings)
            .uuid(request_uuid)
            .sequence(sequence)
            .build()
        )
        request = (
            SettingsCardRequest.builder()
            .card_id(card_id)
            .request_body(request_body)
            .build()
        )
        response = await self._driver._client.cardkit.v1.card.asettings(
            request
        )
        error = _cardkit_response_error(
            response,
            label='finish_streaming_card failed',
        )
        if error is not None:
            raise error

    async def finish_message_stream(self, message_id: str) -> None:
        card_id = await self._card_id_for_message(message_id)
        with self._card_state_lock:
            sequence = max(
                int(time.time()),
                self._card_sequences.get(card_id, 0) + 1,
            )
            self._card_sequences[card_id] = sequence
        await self.finish_streaming_card(card_id, sequence)

    async def _card_id_for_message(self, message_id: str) -> str:
        with self._card_state_lock:
            cached = self._card_ids.get(message_id)
        if cached:
            return cached
        request = (
            IdConvertCardRequest.builder()
            .request_body(
                IdConvertCardRequestBody.builder()
                .message_id(message_id)
                .build()
            )
            .build()
        )
        response = await self._driver._client.cardkit.v1.card.aid_convert(
            request
        )
        data = getattr(response, 'data', None)
        card_id = str(getattr(data, 'card_id', '') or '')
        if int(getattr(response, 'code', -1) or 0) != 0 or not card_id:
            raise FeishuRuntimeError(
                'Feishu CardKit id conversion failed '
                f'code={getattr(response, "code", -1)} '
                f'msg={getattr(response, "msg", "")}'
            )
        with self._card_state_lock:
            self._card_ids[message_id] = card_id
        return card_id


class _DurableFeishuChannel(_WorkspaceFeishuChannel):
    """Waits for Gateway persistence before the SDK acknowledges an event."""

    def __init__(
        self,
        *args,
        on_durable_message: Callable[[FeishuInboundMessage], None],
        on_durable_action: (
            Callable[[FeishuInboundAction], dict[str, Any] | None] | None
        ),
        on_durable_menu: Callable[[FeishuInboundMenu], None] | None,
        **kwargs,
    ):
        self._on_durable_message = on_durable_message
        self._on_durable_action = on_durable_action
        self._on_durable_menu = on_durable_menu
        super().__init__(*args, **kwargs)

    def _build_dispatcher(self):
        dispatcher = super()._build_dispatcher()
        dispatcher._processorMap[
            'p2.application.bot.menu_v6'
        ] = CustomizedEventProcessor(self._on_p2_application_bot_menu_v6)
        return dispatcher

    def _on_p2_application_bot_menu_v6(self, data: Any) -> None:
        if self._on_durable_menu is None:
            return
        event = getattr(data, 'event', None)
        raw = event if isinstance(event, dict) else {}
        operator = raw.get('operator')
        operator = operator if isinstance(operator, dict) else {}
        operator_id = operator.get('operator_id')
        operator_id = (
            operator_id if isinstance(operator_id, dict) else {}
        )
        context = getattr(data, 'header', None)
        event_id = str(
            getattr(context, 'event_id', '')
            or getattr(data, 'event_id', '')
            or ''
        )
        sender_id = str(operator_id.get('open_id') or '')
        event_key = str(raw.get('event_key') or '')
        if not event_id or not sender_id or not event_key:
            return
        self._on_durable_menu(
            FeishuInboundMenu(
                event_id=event_id,
                sender_id=sender_id,
                event_key=event_key,
            )
        )

    def _on_p2_im_message_receive_v1(self, data: Any) -> None:
        event = getattr(data, 'event', None)
        message = getattr(event, 'message', None)
        sender = getattr(event, 'sender', None)
        sender_id = getattr(sender, 'sender_id', None)
        message_type = str(
            getattr(message, 'message_type', '') or ''
        )
        text = _message_text(
            message_type,
            str(getattr(message, 'content', '') or ''),
        )
        sender_type = str(
            getattr(sender, 'sender_type', '') or ''
        ).lower()
        chat_type = str(
            getattr(message, 'chat_type', '') or ''
        )
        if chat_type != 'p2p':
            return
        self._on_durable_message(
            FeishuInboundMessage(
                message_id=str(
                    getattr(message, 'message_id', '') or ''
                ),
                chat_id=str(getattr(message, 'chat_id', '') or ''),
                sender_id=str(
                    getattr(sender_id, 'open_id', '') or ''
                ),
                sender_is_bot=sender_type in {'app', 'bot'},
                text=text,
                image_key=_message_image_key(
                    message_type,
                    str(getattr(message, 'content', '') or ''),
                ),
                message_type=message_type,
            )
        )

    def _on_p2_card_action_trigger(
        self,
        data: Any,
    ) -> P2CardActionTriggerResponse:
        event = getattr(data, 'event', None)
        raw_action = getattr(event, 'action', None)
        value = getattr(raw_action, 'value', None)
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except (TypeError, ValueError):
                value = {}
        if not isinstance(value, dict):
            return P2CardActionTriggerResponse({})
        action = str(value.get('lazymind_action') or '')
        selection = str(
            value.get('selection')
            or getattr(raw_action, 'option', '')
            or ''
        )
        text = str(value.get('text') or selection)
        command_action = value.get('command_action')
        workspace_action = value.get('workspace_action')
        if isinstance(workspace_action, dict) and selection:
            workspace_action = {
                **workspace_action,
                'selection': selection,
            }
        ask_answers = value.get('ask_answers_structured')
        if action == 'ask' and not text:
            text, ask_answers = parse_ask_form_submission(
                value,
                getattr(raw_action, 'form_value', None),
            )
        if (
            action not in {'select', 'ask', 'command', 'local'}
            or not text
            or (
                action == 'command'
                and not isinstance(command_action, dict)
            )
        ):
            return P2CardActionTriggerResponse({})
        context = getattr(event, 'context', None)
        operator = getattr(event, 'operator', None)
        if self._on_durable_action is None:
            return P2CardActionTriggerResponse({})
        card = self._on_durable_action(
            FeishuInboundAction(
                event_id=str(
                    getattr(getattr(data, 'header', None), 'event_id', '')
                    or ''
                ),
                message_id=str(
                    getattr(context, 'open_message_id', '') or ''
                ),
                chat_id=str(
                    getattr(context, 'open_chat_id', '') or ''
                ),
                sender_id=str(
                    getattr(operator, 'open_id', '')
                    or ''
                ),
                action=action,
                text=text,
                selection=selection,
                selection_id=str(
                    value.get('selection_id') or ''
                ),
                intended_chat_id=str(
                    value.get('intended_chat_id') or ''
                ),
                ask_answers_structured=(
                    dict(ask_answers)
                    if isinstance(ask_answers, dict)
                    else None
                ),
                command_action=(
                    dict(command_action)
                    if isinstance(command_action, dict)
                    else None
                ),
                workspace_action=(
                    dict(workspace_action)
                    if isinstance(workspace_action, dict)
                    else None
                ),
            )
        )
        return P2CardActionTriggerResponse(
            {
                'card': {'type': 'raw', 'data': card}
            }
            if isinstance(card, dict)
            else {}
        )


class _LarkCardReplyStream(ReplyStream):
    def __init__(
        self,
        *,
        channel: FeishuChannel,
        chat_id: str,
        initial_card: dict[str, Any],
        timeout_seconds: float,
        message_id: str = '',
        should_render: Callable[[], bool] | None = None,
        on_message_started: Callable[[str], None] | None = None,
        render_card: Callable[
            [CoreStreamUpdate, bool, bool],
            dict[str, Any],
        ] | None = None,
    ):
        self._channel = channel
        self._chat_id = chat_id
        self._initial_card = initial_card
        self._timeout_seconds = timeout_seconds
        self.message_id = message_id
        self._should_render = should_render or (lambda: True)
        self._on_message_started = on_message_started
        self._render_card = render_card
        self._updates: queue.Queue[tuple[object, object]] = queue.Queue()
        self._future = None
        self._lock = threading.Lock()
        self._closed = False
        self._abort_requested = False

    def retarget_message(self, message_id: str) -> None:
        if message_id:
            self.message_id = message_id

    def update(self, snapshot: CoreStreamUpdate) -> None:
        with self._lock:
            if self._closed:
                return
            if self._future is None:
                self._future = self._channel.schedule(self._run())
                self._future.add_done_callback(
                    self._log_background_failure
                )
            self._updates.put(('snapshot', snapshot))

    def pause_for_interaction(self) -> None:
        """Close CardKit streaming so buttons can emit action callbacks."""
        with self._lock:
            if self._closed:
                return
            if self._future is None:
                self._future = self._channel.schedule(self._run())
                self._future.add_done_callback(
                    self._log_background_failure
                )
            self._updates.put((_STREAM_PAUSE_FOR_INTERACTION, ''))

    def finish(self, final_text: str) -> bool:
        with self._lock:
            future = self._future
            if future is None or self._closed:
                self._closed = True
                return False
            self._closed = True
            self._updates.put((_STREAM_FINISH, final_text))
        try:
            result = future.result(timeout=self._timeout_seconds)
        except Exception:
            _logger.exception('feishu_reply_stream_finish_failed')
            return False
        return bool(result.success and result.message_id)

    def abort(self) -> None:
        with self._lock:
            future = self._future
            if self._closed:
                return
            self._abort_requested = True
            self._closed = True
            if future is None:
                return
            self._updates.put((_STREAM_ABORT, ''))
        try:
            future.result(timeout=10)
        except Exception:
            pass

    async def _run(self):
        if self.message_id:
            return await self._run_message_updates()
        card_id = await self._provider_call(
            self._channel.create_card_instance(self._initial_card)
        )
        result = await self._provider_call(
            self._channel.send_card_by_reference(
                self._chat_id,
                card_id,
                receive_id_type='chat_id',
            )
        )
        if not result.success or not result.message_id:
            raise FeishuRuntimeError(
                f'Feishu stream card send failed: {result.error}'
            )
        _logger.info(
            'feishu_card_stream_started message_id=%s',
            result.message_id,
        )
        self.message_id = str(result.message_id)
        if self._on_message_started is not None:
            await asyncio.to_thread(
                self._on_message_started,
                self.message_id,
            )
        sequence = 0
        rendered: dict[str, str] = {}
        snapshot = CoreStreamUpdate()
        try:
            while True:
                kind, value = await asyncio.to_thread(
                    self._updates.get
                )
                latest_snapshot = None
                terminal = None
                pause_for_interaction = False
                while True:
                    if (
                        kind == 'snapshot'
                        and isinstance(value, CoreStreamUpdate)
                    ):
                        latest_snapshot = value
                    elif kind in {_STREAM_ABORT, _STREAM_FINISH}:
                        terminal = (kind, value)
                        break
                    elif kind is _STREAM_PAUSE_FOR_INTERACTION:
                        pause_for_interaction = True
                    try:
                        kind, value = self._updates.get_nowait()
                    except queue.Empty:
                        break
                if latest_snapshot is not None:
                    snapshot = latest_snapshot
                if terminal is not None:
                    kind, value = terminal
                if kind is _STREAM_ABORT:
                    if not self._should_render():
                        await self._finish_streaming_card(
                            card_id,
                            sequence + 1,
                        )
                        await self._replace_message_card(
                            result.message_id,
                            self._message_snapshot_card(
                                snapshot,
                                finished=True,
                                aborted=True,
                            ),
                        )
                        return result
                    sequence = await self._update_element(
                        card_id,
                        'lazymind_status',
                        '⚠️ **回答已中断**',
                        sequence,
                        rendered,
                    )
                    await self._finish_streaming_card(
                        card_id,
                        sequence + 1,
                    )
                    await self._replace_message_card(
                        result.message_id,
                        self._message_snapshot_card(
                            snapshot,
                            finished=True,
                            aborted=True,
                        ),
                    )
                    _logger.info(
                        'feishu_card_stream_aborted '
                        'message_id=%s update_count=%s',
                        result.message_id,
                        sequence,
                    )
                    return result
                if kind is _STREAM_FINISH:
                    if not self._should_render():
                        await self._finish_streaming_card(
                            card_id,
                            sequence + 1,
                        )
                        await self._replace_message_card(
                            result.message_id,
                            self._message_snapshot_card(
                                snapshot,
                                finished=True,
                                aborted=True,
                            ),
                        )
                        return result
                    final_text = str(value or snapshot.answer)
                    final_snapshot = CoreStreamUpdate(
                        thinking=snapshot.thinking,
                        answer=final_text,
                        thinking_seconds=snapshot.thinking_seconds,
                        task_progress=snapshot.task_progress,
                    )
                    sequence = await self._render_snapshot(
                        card_id,
                        final_snapshot,
                        sequence,
                        rendered,
                        finished=True,
                    )
                    await self._finish_streaming_card(
                        card_id,
                        sequence + 1,
                    )
                    await self._replace_message_card(
                        result.message_id,
                        self._message_snapshot_card(
                            final_snapshot,
                            finished=True,
                        ),
                    )
                    _logger.info(
                        'feishu_card_stream_completed '
                        'message_id=%s update_count=%s',
                        result.message_id,
                        sequence,
                    )
                    return result
                if pause_for_interaction:
                    await self._finish_streaming_card(
                        card_id,
                        sequence + 1,
                    )
                    if self._should_render():
                        card = self._message_snapshot_card(snapshot)
                        await self._replace_message_card(
                            result.message_id,
                            self._message_update_card(card),
                        )
                    continue
                if not isinstance(value, CoreStreamUpdate):
                    if latest_snapshot is None:
                        continue
                sequence = await self._render_snapshot(
                    card_id,
                    snapshot,
                    sequence,
                    rendered,
                )
        except _StreamRetryCancelled:
            try:
                await self._finish_streaming_card(
                    card_id,
                    sequence + 1,
                )
            except Exception:
                _logger.warning(
                    'feishu_card_stream_close_after_retry_failed '
                    'card_id=%s',
                    card_id,
                    exc_info=True,
                )
                raise
            return result
        except Exception as exc:
            if _stream_element_retry_delay(
                exc,
                len(_STREAM_ELEMENT_RETRY_DELAYS),
            ) is not None:
                try:
                    await self._finish_streaming_card(
                        card_id,
                        sequence + 1,
                    )
                except Exception:
                    _logger.warning(
                        'feishu_card_stream_close_after_rate_limit_failed '
                        'card_id=%s',
                        card_id,
                        exc_info=True,
                    )
            _logger.exception(
                'feishu_card_stream_failed card_id=%s',
                card_id,
            )
            raise

    async def _render_snapshot(
        self,
        card_id: str,
        snapshot: CoreStreamUpdate,
        sequence: int,
        rendered: dict[str, str],
        *,
        finished: bool = False,
    ) -> int:
        status, answer = self._snapshot_values(
            snapshot,
            finished=finished,
        )
        sequence = await self._update_element(
            card_id,
            'lazymind_status',
            status,
            sequence,
            rendered,
        )
        return await self._update_element(
            card_id,
            'lazymind_answer',
            answer
            or (
                '本次没有生成可展示的回答。'
                if finished
                else '<font color="grey">正在准备回答…</font>'
            ),
            sequence,
            rendered,
        )

    async def _run_message_updates(self):
        result = await self._replace_message_card(
            self.message_id,
            self._message_update_card(self._initial_card),
        )
        last_update_at = time.monotonic()
        snapshot = CoreStreamUpdate()
        while True:
            kind, value = await asyncio.to_thread(self._updates.get)
            latest_snapshot = None
            terminal = None
            pause_for_interaction = False
            while True:
                if kind == 'snapshot' and isinstance(value, CoreStreamUpdate):
                    latest_snapshot = value
                elif kind in {_STREAM_ABORT, _STREAM_FINISH}:
                    terminal = (kind, value)
                    break
                elif kind is _STREAM_PAUSE_FOR_INTERACTION:
                    pause_for_interaction = True
                try:
                    kind, value = self._updates.get_nowait()
                except queue.Empty:
                    break
            if latest_snapshot is not None:
                snapshot = latest_snapshot
            if terminal is not None:
                kind, value = terminal
            if kind is _STREAM_ABORT:
                if self._should_render():
                    card = self._message_snapshot_card(
                        snapshot,
                        finished=True,
                        aborted=True,
                    )
                    await self._replace_message_card(
                        self.message_id,
                        self._message_update_card(card),
                    )
                else:
                    await self._replace_message_card(
                        self.message_id,
                        self._message_update_card(
                            self._message_snapshot_card(
                                snapshot,
                                finished=True,
                                aborted=True,
                            )
                        ),
                    )
                await self._finish_message_stream()
                return result
            if kind is _STREAM_FINISH:
                final_snapshot = CoreStreamUpdate(
                    thinking=snapshot.thinking,
                    answer=str(value or snapshot.answer),
                    thinking_seconds=snapshot.thinking_seconds,
                    task_progress=snapshot.task_progress,
                )
                if self._should_render():
                    card = self._message_snapshot_card(
                        final_snapshot,
                        finished=True,
                    )
                    await self._replace_message_card(
                        self.message_id,
                        self._message_update_card(card),
                    )
                else:
                    await self._replace_message_card(
                        self.message_id,
                        self._message_update_card(
                            self._message_snapshot_card(
                                final_snapshot,
                                finished=True,
                                aborted=True,
                            )
                        ),
                    )
                await self._finish_message_stream()
                return result
            if pause_for_interaction:
                await self._finish_message_stream()
                if self._should_render():
                    card = self._message_snapshot_card(snapshot)
                    await self._replace_message_card(
                        self.message_id,
                        self._message_update_card(card),
                    )
                continue
            if latest_snapshot is None and not isinstance(
                value,
                CoreStreamUpdate,
            ):
                continue
            if self._should_render():
                card = self._message_snapshot_card(snapshot)
                delay = (
                    _STREAM_MESSAGE_UPDATE_INTERVAL_SECONDS
                    - (time.monotonic() - last_update_at)
                )
                if delay > 0:
                    await asyncio.sleep(delay)
                await self._replace_message_card(
                    self.message_id,
                    self._message_update_card(card),
                )
                last_update_at = time.monotonic()

    @staticmethod
    def _message_update_card(card: dict[str, Any]) -> dict[str, Any]:
        """Existing messages use full-card patches, not CardKit streaming."""
        value = copy.deepcopy(card)
        config = value.get('config')
        if isinstance(config, dict):
            config['streaming_mode'] = False
            config.pop('streaming_config', None)
        return value

    @staticmethod
    def _log_background_failure(future) -> None:
        if future.cancelled():
            return
        try:
            error = future.exception()
        except Exception:
            _logger.exception('feishu_reply_stream_future_check_failed')
            return
        if error is not None:
            _logger.error(
                'feishu_reply_stream_background_failed error=%s',
                error,
            )

    async def _replace_message_card(
        self,
        message_id: str,
        card: dict[str, Any],
    ):
        try:
            result = await self._provider_call(
                self._channel.update_card(message_id, card)
            )
            if result.success:
                return result
            error: Exception = FeishuRuntimeError(
                f'Feishu workspace stream update failed: {result.error}'
            )
        except Exception as exc:
            error = exc
        if workspace_card_expired(error) and self._should_render():
            replacement_message_id = self.message_id
            if replacement_message_id and replacement_message_id != message_id:
                result = await self._provider_call(
                    self._channel.update_card(replacement_message_id, card)
                )
                if result.success:
                    return result
                error = FeishuRuntimeError(
                    'Feishu recovered workspace stream update failed: '
                    f'{result.error}'
                )
        raise error

    async def _finish_message_stream(self) -> None:
        finish = getattr(self._channel, 'finish_message_stream', None)
        if callable(finish):
            await self._provider_call(finish(self.message_id))

    def _message_snapshot_card(
        self,
        snapshot: CoreStreamUpdate,
        *,
        finished: bool = False,
        aborted: bool = False,
    ) -> dict[str, Any]:
        if self._render_card is not None:
            return self._render_card(snapshot, finished, aborted)
        status, answer = self._snapshot_values(
            snapshot,
            finished=finished,
        )
        if aborted:
            status = '⚠️ **回答已中断**'
        card = copy.deepcopy(self._initial_card)
        config = card.get('config')
        if isinstance(config, dict):
            config['streaming_mode'] = not finished
            if finished:
                config.pop('streaming_config', None)
        if finished:
            _remove_card_element(card, name='cancel_generation')
        replacements = {
            'lazymind_status': status,
            'lazymind_answer': answer or (
                '本次没有生成可展示的回答。'
                if finished
                else '<font color="grey">正在准备回答…</font>'
            ),
        }
        _replace_card_element_content(card, replacements)
        return card

    @staticmethod
    def _snapshot_values(
        snapshot: CoreStreamUpdate,
        *,
        finished: bool,
    ) -> tuple[str, str]:
        if finished:
            status = '✅ **回答完成**'
        elif snapshot.answer:
            status = '✍️ **正在生成回答**'
        else:
            status = '⏳ **正在理解你的问题**'
        if snapshot.thinking_seconds is not None:
            status += f' · {snapshot.thinking_seconds} 秒'
        answer = streamable_feishu_text(snapshot.answer)
        progress = streamable_feishu_text(snapshot.task_progress)
        if progress:
            answer = f'{answer}\n\n---\n{progress}'.strip()
        return status, answer

    async def _update_element(
        self,
        card_id: str,
        element_id: str,
        content: str,
        sequence: int,
        rendered: dict[str, str],
    ) -> int:
        if rendered.get(element_id) == content:
            return sequence
        sequence += 1
        await self._retry_element_update(
            lambda: self._channel.update_card_element_content(
                card_id,
                element_id,
                content,
                sequence,
            ),
        )
        rendered[element_id] = content
        return sequence

    async def _retry_element_update(self, operation) -> None:
        deadline = (
            time.monotonic() + _STREAM_ELEMENT_RETRY_BUDGET_SECONDS
        )
        for attempt in range(len(_STREAM_ELEMENT_RETRY_DELAYS) + 1):
            try:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise asyncio.TimeoutError(
                        'Feishu CardKit element retry budget exhausted'
                    )
                await self._provider_call(
                    operation(),
                    timeout_seconds=remaining,
                )
                return
            except Exception as exc:
                delay = _stream_element_retry_delay(exc, attempt)
                if (
                    delay is None
                    or attempt == len(_STREAM_ELEMENT_RETRY_DELAYS)
                ):
                    raise
                if delay > max(0.0, deadline - time.monotonic()):
                    raise
                await self._wait_for_element_retry(delay)

    async def _finish_streaming_card(
        self,
        card_id: str,
        sequence: int,
    ) -> None:
        deadline = (
            time.monotonic() + _STREAM_CLOSE_RETRY_BUDGET_SECONDS
        )
        for attempt in range(len(_STREAM_ELEMENT_RETRY_DELAYS) + 1):
            try:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise asyncio.TimeoutError(
                        'Feishu CardKit close retry budget exhausted'
                    )
                await self._provider_call(
                    self._channel.finish_streaming_card(card_id, sequence),
                    timeout_seconds=remaining,
                )
                return
            except Exception as exc:
                delay = _stream_element_retry_delay(exc, attempt)
                if (
                    delay is None
                    or attempt == len(_STREAM_ELEMENT_RETRY_DELAYS)
                    or delay > max(0.0, deadline - time.monotonic())
                ):
                    raise _StreamCloseFailed(
                        'Feishu CardKit stream close failed'
                    ) from exc
                await asyncio.sleep(delay)

    async def _wait_for_element_retry(self, delay: float) -> None:
        remaining = delay
        if self._abort_requested or not self._should_render():
            raise _StreamRetryCancelled(
                'Feishu CardKit stream is no longer current'
            )
        while remaining > 0:
            interval = min(0.5, remaining)
            await asyncio.sleep(interval)
            remaining -= interval
            if self._abort_requested:
                raise _StreamRetryCancelled(
                    'Feishu CardKit stream retry was cancelled'
                )
            if not self._should_render():
                raise _StreamRetryCancelled(
                    'Feishu CardKit stream is no longer current'
                )

    async def _provider_call(
        self,
        operation,
        *,
        timeout_seconds: float | None = None,
    ):
        timeout = min(
            self._timeout_seconds,
            _STREAM_PROVIDER_TIMEOUT_SECONDS,
        )
        if timeout_seconds is not None:
            timeout = min(timeout, max(0.001, timeout_seconds))
        return await asyncio.wait_for(
            operation,
            timeout=timeout,
        )


def _stream_element_retry_delay(
    error: Exception,
    attempt: int,
) -> float | None:
    error_code = getattr(error, 'code', None)
    error_code = str(getattr(error_code, 'value', error_code) or '')
    retryable = error_code == 'rate_limited'
    raw_codes = {
        int(value)
        for value in re.findall(
            r"['\"]?code['\"]?\s*:\s*(\d+)",
            str(error),
        )
    }
    retryable = retryable or bool(raw_codes & _FEISHU_RATE_LIMIT_CODES)
    if not retryable:
        return None
    delay = _STREAM_ELEMENT_RETRY_DELAYS[
        min(attempt, len(_STREAM_ELEMENT_RETRY_DELAYS) - 1)
    ]
    retry_after = getattr(error, 'retry_after_seconds', None)
    if (
        isinstance(retry_after, (int, float))
        and math.isfinite(retry_after)
        and retry_after >= 0
    ):
        delay = max(delay, float(retry_after))
    return delay


class LarkChannelClient:
    """Small synchronous boundary around the official async Feishu SDK."""

    def __init__(
        self,
        credentials: FeishuAppCredentials,
        on_message: Callable[[FeishuInboundMessage], None] | None = None,
        on_action: (
            Callable[[FeishuInboundAction], dict[str, Any] | None] | None
        ) = None,
        on_menu: Callable[[FeishuInboundMenu], None] | None = None,
        *,
        send_timeout_seconds: float = 60,
        connect_timeout_seconds: float = 30,
    ):
        self._send_timeout_seconds = send_timeout_seconds
        self._connect_timeout_seconds = connect_timeout_seconds
        self._stopped = threading.Event()
        channel_type = (
            _DurableFeishuChannel
            if on_message is not None
            else _WorkspaceFeishuChannel
        )
        channel_kwargs = dict(
            app_id=credentials.app_id,
            app_secret=credentials.app_secret,
            transport=TransportConfig(
                kind='ws',
                auto_reconnect=True,
                trust_env_proxy=True,
                handshake_timeout_seconds=20,
            ),
            policy=PolicyConfig(
                dm_policy='open',
                group_policy='disabled',
                require_mention=False,
            ),
            safety=SafetyConfig(
                text_batch=TextBatchConfig(
                    delay_ms=0,
                    long_delay_ms=0,
                    max_messages=1,
                ),
                stale_message_window_ms=7 * 24 * 60 * 60 * 1000,
            ),
            inbound=InboundConfig(
                media_capabilities=MediaCapabilities(
                    image=True,
                    audio=False,
                    video=False,
                    file=False,
                    sticker=False,
                ),
            ),
            outbound=OutboundConfig(
                text_chunk_limit=3500,
                chunk_mode='none',
                retry=RetryConfig(max_attempts=1),
            ),
            security=SecurityConfig(
                mode='strict',
                max_ws_fragment_parts=64,
                max_ws_fragment_bytes=16 * 1024 * 1024,
                max_concurrent_ws_handlers=32,
                resource_overflow_policy='drop',
            ),
        )
        if on_message is not None:
            channel_kwargs['on_durable_message'] = on_message
            channel_kwargs['on_durable_action'] = on_action
            channel_kwargs['on_durable_menu'] = on_menu
        self._channel = channel_type(**channel_kwargs)

    def start(self) -> None:
        future = self._channel.schedule(
            self._channel.start_background(
                timeout=self._connect_timeout_seconds,
            )
        )
        try:
            future.result(
                timeout=self._connect_timeout_seconds + 5,
            )
        except Exception:
            if self._stopped.is_set():
                return
            raise
        self._stopped.wait()

    def start_blocking(self) -> None:
        self._channel.start()

    def stop(self) -> None:
        self._stopped.set()
        self._channel.stop()

    def is_ready(self) -> bool:
        return (
            self._channel.is_ready
            or self._transport_connected()
        )

    def connection_state(self) -> str:
        snapshot = str(
            self._channel.connection_snapshot().state
        )
        if self._transport_connected():
            return 'connected'
        return snapshot

    def _transport_connected(self) -> bool:
        transport = getattr(self._channel, '_ws_client', None)
        return (
            transport is not None
            and getattr(transport, '_conn', None) is not None
        )

    def close(self) -> None:
        self.stop()

    def send_markdown(
        self,
        *,
        chat_id: str,
        text: str,
        idempotency_key: str,
    ) -> str:
        return self._send(
            chat_id=chat_id,
            message={'markdown': text},
            idempotency_key=idempotency_key,
        )

    def send_card_to_user(
        self,
        *,
        open_id: str,
        card: dict[str, Any],
        idempotency_key: str,
    ) -> str:
        return self._send(
            chat_id=open_id,
            message=OutboundCard(card=card),
            idempotency_key=idempotency_key,
            receive_id_type='open_id',
        )

    def send_card_to_user_with_chat(
        self,
        *,
        open_id: str,
        card: dict[str, Any],
        idempotency_key: str,
    ) -> tuple[str, str]:
        result = self._send_result(
            chat_id=open_id,
            message=OutboundCard(card=card),
            idempotency_key=idempotency_key,
            receive_id_type='open_id',
        )
        raw = result.raw if isinstance(result.raw, dict) else {}
        data = raw.get('data') if isinstance(raw, dict) else {}
        data = data if isinstance(data, dict) else {}
        return str(result.message_id or ''), str(data.get('chat_id') or '')

    def send_image(
        self,
        *,
        chat_id: str,
        content: bytes,
        caption: str,
        idempotency_key: str,
    ) -> None:
        self._send(
            chat_id=chat_id,
            message=OutboundImage(
                source=MediaSource(kind='buffer', buffer=content),
                caption=caption or None,
            ),
            idempotency_key=idempotency_key,
        )

    def upload_image(self, *, content: bytes) -> str:
        future = self._channel.schedule(
            self._channel.upload_media(
                MediaSource(kind='buffer', buffer=content),
                kind='image',
            )
        )
        image_key = str(
            future.result(timeout=self._send_timeout_seconds) or ''
        ).strip()
        if not is_feishu_image_key(image_key):
            raise FeishuRuntimeError(
                '飞书图片上传返回了无效的 image_key'
            )
        return image_key

    def download_image(
        self,
        *,
        image_key: str,
        message_id: str,
    ) -> bytes:
        future = self._channel.schedule(
            self._channel.download_resource(
                image_key,
                resource_type='image',
                message_id=message_id,
            )
        )
        content = future.result(timeout=self._send_timeout_seconds)
        return bytes(content or b'')

    def send_card(
        self,
        *,
        chat_id: str,
        card: dict[str, Any],
        idempotency_key: str,
    ) -> str:
        return self._send(
            chat_id=chat_id,
            message=OutboundCard(card=card),
            idempotency_key=idempotency_key,
        )

    def update_card(
        self,
        *,
        message_id: str,
        card: dict[str, Any],
    ) -> None:
        try:
            future = self._channel.schedule(
                self._channel.update_card(message_id, card)
            )
            result = future.result(
                timeout=self._send_timeout_seconds,
            )
        except Exception as exc:
            raise _retryable_side_effect_error(
                'Feishu card update failed',
                exc,
            ) from exc
        if not result.success:
            error = result.error
            if bool(getattr(error, 'retryable', False)):
                raise _retryable_side_effect_error(
                    'Feishu card update failed',
                    error,
                )
            raise FeishuRuntimeError(
                f'Feishu card update failed: {error}'
            )

    def send_file(
        self,
        *,
        chat_id: str,
        content: bytes,
        filename: str,
        idempotency_key: str,
    ) -> None:
        self._send(
            chat_id=chat_id,
            message=OutboundFile(
                source=MediaSource(kind='buffer', buffer=content),
                file_name=filename,
            ),
            idempotency_key=idempotency_key,
        )

    def start_card_stream(
        self,
        *,
        chat_id: str,
        initial_card: dict[str, Any],
        message_id: str = '',
        should_render: Callable[[], bool] | None = None,
        on_message_started: Callable[[str], None] | None = None,
        render_card: Callable[
            [CoreStreamUpdate, bool, bool],
            dict[str, Any],
        ] | None = None,
    ) -> ReplyStream:
        return _LarkCardReplyStream(
            channel=self._channel,
            chat_id=chat_id,
            initial_card=initial_card,
            timeout_seconds=max(
                self._send_timeout_seconds,
                _STREAM_FINISH_TIMEOUT_SECONDS,
            ),
            message_id=message_id,
            should_render=should_render,
            on_message_started=on_message_started,
            render_card=render_card,
        )

    def _send(
        self,
        *,
        chat_id: str,
        message,
        idempotency_key: str,
        receive_id_type: str = 'chat_id',
    ) -> str:
        result = self._send_result(
            chat_id=chat_id,
            message=message,
            idempotency_key=idempotency_key,
            receive_id_type=receive_id_type,
        )
        return str(result.message_id or '')

    def _send_result(
        self,
        *,
        chat_id: str,
        message,
        idempotency_key: str,
        receive_id_type: str = 'chat_id',
    ):
        options = SendOpts(
            receive_id_type=receive_id_type,
            uuid=str(
                uuid.uuid5(
                    uuid.NAMESPACE_URL,
                    f'lazymind:{idempotency_key}',
                )
            ),
        )
        try:
            future = self._channel.schedule(
                self._channel.send(chat_id, message, options)
            )
            result = future.result(
                timeout=self._send_timeout_seconds,
            )
        except Exception as exc:
            raise _retryable_side_effect_error(
                'Feishu send failed',
                exc,
            ) from exc
        if not result.success:
            error = result.error
            if bool(getattr(error, 'retryable', False)):
                raise _retryable_side_effect_error(
                    'Feishu send failed',
                    error,
                )
            raise FeishuRuntimeError(
                f'Feishu send failed: {error}'
            )
        message_id = str(result.message_id or '')
        if not message_id:
            raise FeishuRuntimeError(
                'Feishu send succeeded without a message id'
            )
        return result


def _replace_card_element_content(
    value: Any,
    replacements: dict[str, str],
) -> None:
    if isinstance(value, list):
        for item in value:
            _replace_card_element_content(item, replacements)
        return
    if not isinstance(value, dict):
        return
    element_id = str(value.get('element_id') or '')
    if element_id in replacements and 'content' in value:
        value['content'] = replacements[element_id]
    for child in value.values():
        _replace_card_element_content(child, replacements)


def _remove_card_element(value: Any, *, name: str) -> None:
    if isinstance(value, list):
        value[:] = [
            item
            for item in value
            if not (
                isinstance(item, dict)
                and str(item.get('name') or '') == name
            )
        ]
        for item in value:
            _remove_card_element(item, name=name)
        return
    if not isinstance(value, dict):
        return
    for child in value.values():
        _remove_card_element(child, name=name)
