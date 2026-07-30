import json
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
from lark_channel.api.im.v1.model.create_chat_request import (
    CreateChatRequest,
)
from lark_channel.api.im.v1.model.create_chat_request_body import (
    CreateChatRequestBody,
)
from lark_channel.event.callback.model.p2_card_action_trigger import (
    P2CardActionTriggerResponse,
)

from channel_gateway.common.errors import RetryableProviderSideEffectError
from channel_gateway.feishu.domain import (
    FeishuAppCredentials,
    FeishuInboundAction,
    FeishuInboundMessage,
)
from channel_gateway.feishu.domain import FeishuRuntimeError


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


def _ask_form_submission(
    value: dict[str, Any],
    form_value: Any,
) -> tuple[str, dict[str, Any] | None]:
    raw_questions = value.get('ask_form_questions')
    if not isinstance(raw_questions, list) or not isinstance(
        form_value,
        dict,
    ):
        return '', None
    answered: list[dict[str, Any]] = []
    lines: list[str] = []
    for raw_question in raw_questions:
        if not isinstance(raw_question, dict):
            return '', None
        name = str(raw_question.get('name') or '')
        text = str(raw_question.get('text') or '')
        question_type = str(raw_question.get('type') or '')
        choices = [
            str(choice)
            for choice in (
                raw_question.get('choices')
                if isinstance(raw_question.get('choices'), list)
                else []
            )
        ]
        answer = _ask_form_answer(
            question_type,
            form_value.get(name),
            str(
                form_value.get(
                    str(raw_question.get('other_name') or ''),
                    '',
                )
                or ''
            ).strip(),
        )
        if not name or not text or answer is None:
            return '', None
        answered.append(
            {
                'text': text,
                'type': question_type,
                'choices': choices,
                'custom_choices': choices,
                'answer': answer,
            }
        )
        lines.append(f'{text}: {_ask_answer_text(answer)}')
    if not answered:
        return '', None
    return (
        '\n'.join(lines),
        {
            'ask_id': str(value.get('ask_id') or ''),
            'questions': answered,
        },
    )


def _ask_form_answer(
    question_type: str,
    raw: Any,
    other_text: str,
) -> dict[str, Any] | None:
    if question_type == 'multiple':
        values = [
            str(item).strip()
            for item in (raw if isinstance(raw, list) else [])
            if str(item).strip()
        ]
        if not values:
            return None
        return {
            'type': 'multiple',
            'value': values,
            'otherText': other_text,
        }
    value = str(raw or '').strip()
    if not value:
        return None
    if question_type == 'boolean':
        return {'type': 'boolean', 'value': value}
    if question_type == 'single':
        return {
            'type': 'single',
            'value': value,
            'otherText': other_text,
        }
    if question_type == 'text':
        return {'type': 'text', 'value': value}
    return None


def _ask_answer_text(answer: dict[str, Any]) -> str:
    value = answer.get('value')
    if isinstance(value, list):
        rendered = '、'.join(str(item) for item in value)
    else:
        rendered = str(value or '')
    other_text = str(answer.get('otherText') or '').strip()
    if other_text and (
        value == '其他'
        or isinstance(value, list) and '其他' in value
    ):
        return rendered.replace('其他', other_text)
    return rendered


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
        self._on_durable_message(
            FeishuInboundMessage(
                message_id=str(
                    getattr(message, 'message_id', '') or ''
                ),
                chat_id=str(getattr(message, 'chat_id', '') or ''),
                chat_type=str(
                    getattr(message, 'chat_type', '') or ''
                ),
                message_type=message_type,
                sender_id=str(
                    getattr(sender_id, 'open_id', '') or ''
                ),
                sender_is_bot=sender_type in {'app', 'bot'},
                root_id=str(
                    getattr(message, 'root_id', '') or ''
                ),
                parent_id=str(
                    getattr(message, 'parent_id', '') or ''
                ),
                thread_id=str(
                    getattr(message, 'thread_id', '') or ''
                ),
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
        ask_answers = value.get('ask_answers_structured')
        if action == 'ask' and not text:
            text, ask_answers = _ask_form_submission(
                value,
                getattr(raw_action, 'form_value', None),
            )
        if action not in {'select', 'ask'} or not text:
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
                root_message_id=str(
                    value.get('root_message_id') or ''
                ),
                intended_chat_id=str(
                    value.get('intended_chat_id') or ''
                ),
                ask_answers_structured=(
                    dict(ask_answers)
                    if isinstance(ask_answers, dict)
                    else None
                ),
            )
        )
        return P2CardActionTriggerResponse({})


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
        reply_to: str | None,
        reply_in_thread: bool,
        idempotency_key: str,
    ) -> str:
        return self._send(
            chat_id=chat_id,
            message={'markdown': text},
            reply_to=reply_to,
            reply_in_thread=reply_in_thread,
            idempotency_key=idempotency_key,
        )

    def send_image(
        self,
        *,
        chat_id: str,
        content: bytes,
        caption: str,
        reply_to: str | None,
        reply_in_thread: bool,
        idempotency_key: str,
    ) -> None:
        self._send(
            chat_id=chat_id,
            message=OutboundImage(
                source=MediaSource(kind='buffer', buffer=content),
                caption=caption or None,
            ),
            reply_to=reply_to,
            reply_in_thread=reply_in_thread,
            idempotency_key=idempotency_key,
        )

    def send_card(
        self,
        *,
        chat_id: str,
        card: dict[str, Any],
        reply_to: str | None,
        reply_in_thread: bool,
        idempotency_key: str,
    ) -> str:
        return self._send(
            chat_id=chat_id,
            message=OutboundCard(card=card),
            reply_to=reply_to,
            reply_in_thread=reply_in_thread,
            idempotency_key=idempotency_key,
        )

    def send_file(
        self,
        *,
        chat_id: str,
        content: bytes,
        filename: str,
        reply_to: str | None,
        reply_in_thread: bool,
        idempotency_key: str,
    ) -> None:
        self._send(
            chat_id=chat_id,
            message=OutboundFile(
                source=MediaSource(kind='buffer', buffer=content),
                file_name=filename,
            ),
            reply_to=reply_to,
            reply_in_thread=reply_in_thread,
            idempotency_key=idempotency_key,
        )

    def _send(
        self,
        *,
        chat_id: str,
        message,
        reply_to: str | None,
        reply_in_thread: bool,
        idempotency_key: str,
    ) -> str:
        options = SendOpts(
            reply_to=reply_to or None,
            reply_in_thread=reply_in_thread or None,
            receive_id_type='chat_id',
            uuid=str(
                uuid.uuid5(
                    uuid.NAMESPACE_URL,
                    f'lazymind:{idempotency_key}',
                )
            ),
            reply_target_gone=(
                'fail' if reply_in_thread else 'fresh'
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

    def create_workspace(
        self,
        *,
        account_id: str,
        owner_open_id: str,
        owner_name: str,
    ) -> str:
        name = (
            f'{owner_name}的 LazyMind 工作台'
            if owner_name
            else 'LazyMind · 我的 AI 工作台'
        )
        body = (
            CreateChatRequestBody.builder()
            .name(name)
            .description(
                '每个话题对应一个独立的 LazyMind 会话。'
            )
            .owner_id(owner_open_id)
            .user_id_list([owner_open_id])
            .group_message_type('thread')
            .chat_mode('group')
            .chat_type('private')
            .build()
        )
        request = (
            CreateChatRequest.builder()
            .user_id_type('open_id')
            .uuid(
                str(
                    uuid.uuid5(
                        uuid.NAMESPACE_URL,
                        f'lazymind:feishu-workspace:{account_id}',
                    )
                )
            )
            .request_body(body)
            .build()
        )
        try:
            response = self._channel.client.im.v1.chat.create(
                request
            )
        except Exception as exc:
            raise FeishuRuntimeError(
                f'Feishu workspace creation failed: {exc}'
            ) from exc
        response_data = getattr(response, 'data', None)
        chat_id = str(
            getattr(response_data, 'chat_id', '') or ''
        )
        if (
            not response.success()
            or not chat_id
            or getattr(
                response_data,
                'group_message_type',
                None,
            )
            != 'thread'
            or getattr(response_data, 'chat_mode', None)
            != 'group'
            or getattr(response_data, 'chat_type', None)
            != 'private'
        ):
            raise FeishuRuntimeError(
                'Feishu workspace creation failed: '
                f'{getattr(response, "code", "")} '
                f'{getattr(response, "msg", "")}'
            )
        return chat_id
