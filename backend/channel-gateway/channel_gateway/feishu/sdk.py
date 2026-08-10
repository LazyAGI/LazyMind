import asyncio
import json
import logging
import queue
import threading
import uuid
from collections.abc import Callable
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
from lark_channel.event.callback.model.p2_card_action_trigger import (
    P2CardActionTriggerResponse,
)

from channel_gateway.common.errors import RetryableProviderSideEffectError
from channel_gateway.common.domain.chat import CoreStreamUpdate
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.feishu.domain import (
    FeishuAppCredentials,
    FeishuInboundAction,
    FeishuInboundMessage,
)
from channel_gateway.feishu.domain import FeishuRuntimeError
from channel_gateway.feishu.presentation import (
    parse_ask_form_submission,
    presentable_feishu_text,
    streamable_feishu_text,
)


_logger = logging.getLogger(__name__)
_STREAM_ABORT = object()
_STREAM_FINISH = object()
_STREAM_PROVIDER_TIMEOUT_SECONDS = 60
_STREAM_FINISH_TIMEOUT_SECONDS = 120


def _message_text(message_type: str, raw_content: str) -> str:
    try:
        content = json.loads(raw_content or '{}')
    except (TypeError, ValueError):
        return ''
    if not isinstance(content, dict):
        return ''
    if message_type == 'text':
        return str(content.get('text') or '').strip()
    if message_type != 'post':
        return ''
    for field in ('content_v2', 'content'):
        text = _post_text(content.get(field))
        if text:
            return text
    return ''


def _post_text(paragraphs: Any) -> str:
    if not isinstance(paragraphs, list):
        return ''
    lines: list[str] = []
    for paragraph in paragraphs:
        if not isinstance(paragraph, list):
            continue
        parts: list[str] = []
        for element in paragraph:
            if not isinstance(element, dict):
                continue
            tag = str(element.get('tag') or '')
            if tag in {'text', 'a', 'md'}:
                parts.append(str(element.get('text') or ''))
            elif tag == 'at':
                parts.append(str(element.get('user_name') or ''))
        line = ''.join(parts).strip()
        if line:
            lines.append(line)
    return '\n'.join(lines)


class _DurableFeishuChannel(FeishuChannel):
    """Waits for Gateway persistence before the SDK acknowledges an event."""

    def __init__(
        self,
        *args,
        on_durable_message: Callable[[FeishuInboundMessage], None],
        on_durable_action: Callable[[FeishuInboundAction], None] | None,
        **kwargs,
    ):
        self._on_durable_message = on_durable_message
        self._on_durable_action = on_durable_action
        super().__init__(*args, **kwargs)

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
        selection = str(value.get('selection') or '')
        text = str(value.get('text') or selection)
        command_action = value.get('command_action')
        ask_answers = value.get('ask_answers_structured')
        if action == 'ask' and not text:
            text, ask_answers = parse_ask_form_submission(
                value,
                getattr(raw_action, 'form_value', None),
            )
        if (
            action not in {'select', 'ask', 'command'}
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
        self._on_durable_action(
            FeishuInboundAction(
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
            )
        )
        return P2CardActionTriggerResponse({})


class _LarkCardReplyStream(ReplyStream):
    def __init__(
        self,
        *,
        channel: FeishuChannel,
        chat_id: str,
        initial_card: dict[str, Any],
        timeout_seconds: float,
    ):
        self._channel = channel
        self._chat_id = chat_id
        self._initial_card = initial_card
        self._timeout_seconds = timeout_seconds
        self._updates: queue.Queue[tuple[object, object]] = queue.Queue()
        self._future = None
        self._lock = threading.Lock()
        self._closed = False

    def update(self, snapshot: CoreStreamUpdate) -> None:
        with self._lock:
            if self._closed:
                return
            if self._future is None:
                self._future = self._channel.schedule(self._run())
            self._updates.put(('snapshot', snapshot))

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
            self._closed = True
            if future is None:
                return
            self._updates.put((_STREAM_ABORT, ''))
        try:
            future.result(timeout=10)
        except Exception:
            pass

    async def _run(self):
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
                while True:
                    if (
                        kind == 'snapshot'
                        and isinstance(value, CoreStreamUpdate)
                    ):
                        latest_snapshot = value
                    elif kind in {_STREAM_ABORT, _STREAM_FINISH}:
                        terminal = (kind, value)
                        break
                    try:
                        kind, value = self._updates.get_nowait()
                    except queue.Empty:
                        break
                if latest_snapshot is not None:
                    snapshot = latest_snapshot
                if terminal is not None:
                    kind, value = terminal
                if kind is _STREAM_ABORT:
                    sequence = await self._update_element(
                        card_id,
                        'lazymind_status',
                        '⚠️ **回答已中断**',
                        sequence,
                        rendered,
                    )
                    await self._provider_call(
                        self._channel.finish_streaming_card(
                            card_id,
                            sequence + 1,
                        )
                    )
                    _logger.info(
                        'feishu_card_stream_aborted '
                        'message_id=%s update_count=%s',
                        result.message_id,
                        sequence,
                    )
                    return result
                if kind is _STREAM_FINISH:
                    final_text = str(snapshot.answer or value)
                    final_snapshot = CoreStreamUpdate(
                        thinking=snapshot.thinking,
                        answer=final_text,
                        thinking_seconds=snapshot.thinking_seconds,
                    )
                    sequence = await self._render_snapshot(
                        card_id,
                        final_snapshot,
                        sequence,
                        rendered,
                        finished=True,
                    )
                    await self._provider_call(
                        self._channel.finish_streaming_card(
                            card_id,
                            sequence + 1,
                        )
                    )
                    _logger.info(
                        'feishu_card_stream_completed '
                        'message_id=%s update_count=%s',
                        result.message_id,
                        sequence,
                    )
                    return result
                if not isinstance(value, CoreStreamUpdate):
                    if latest_snapshot is None:
                        continue
                sequence = await self._render_snapshot(
                    card_id,
                    snapshot,
                    sequence,
                    rendered,
                )
        except Exception:
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
        if finished:
            status = '✅ **回答完成**'
        elif snapshot.answer:
            status = '✍️ **正在生成回答**'
        else:
            status = '⏳ **正在理解你的问题**'
        if snapshot.thinking_seconds is not None:
            status += f' · {snapshot.thinking_seconds} 秒'
        sequence = await self._update_element(
            card_id,
            'lazymind_status',
            status,
            sequence,
            rendered,
        )
        thinking = presentable_feishu_text(snapshot.thinking)
        if finished and thinking in {
            '',
            '正在分析问题...',
            '正在分析问题…',
        }:
            thinking = '分析与处理已完成。'
        if finished or not snapshot.answer:
            sequence = await self._update_element(
                card_id,
                'lazymind_thinking',
                thinking or '正在分析问题…',
                sequence,
                rendered,
            )
        answer = streamable_feishu_text(snapshot.answer)
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
        await self._provider_call(
            self._channel.update_card_element_content(
                card_id,
                element_id,
                content,
                sequence,
            )
        )
        rendered[element_id] = content
        return sequence

    async def _provider_call(self, operation):
        return await asyncio.wait_for(
            operation,
            timeout=min(
                self._timeout_seconds,
                _STREAM_PROVIDER_TIMEOUT_SECONDS,
            ),
        )


class LarkChannelClient:
    """Small synchronous boundary around the official async Feishu SDK."""

    def __init__(
        self,
        credentials: FeishuAppCredentials,
        on_message: Callable[[FeishuInboundMessage], None] | None = None,
        on_action: Callable[[FeishuInboundAction], None] | None = None,
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
            else FeishuChannel
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
                group_policy='open',
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
                    image=False,
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
            security=SecurityConfig(mode='audit'),
        )
        if on_message is not None:
            channel_kwargs['on_durable_message'] = on_message
            channel_kwargs['on_durable_action'] = on_action
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

    def send_markdown_to_user(
        self,
        *,
        open_id: str,
        text: str,
        idempotency_key: str,
    ) -> str:
        return self._send(
            chat_id=open_id,
            message={'markdown': text},
            idempotency_key=idempotency_key,
            receive_id_type='open_id',
        )

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
            raise RetryableProviderSideEffectError(
                f'Feishu card update failed: {exc}'
            ) from exc
        if not result.success:
            raise FeishuRuntimeError(
                f'Feishu card update failed: {result.error}'
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
    ) -> ReplyStream:
        return _LarkCardReplyStream(
            channel=self._channel,
            chat_id=chat_id,
            initial_card=initial_card,
            timeout_seconds=max(
                self._send_timeout_seconds,
                _STREAM_FINISH_TIMEOUT_SECONDS,
            ),
        )

    def _send(
        self,
        *,
        chat_id: str,
        message,
        idempotency_key: str,
        receive_id_type: str = 'chat_id',
    ) -> str:
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
            raise RetryableProviderSideEffectError(
                f'Feishu send failed: {exc}'
            ) from exc
        if not result.success:
            raise FeishuRuntimeError(
                f'Feishu send failed: {result.error}'
            )
        message_id = str(result.message_id or '')
        if not message_id:
            raise FeishuRuntimeError(
                'Feishu send succeeded without a message id'
            )
        return message_id
