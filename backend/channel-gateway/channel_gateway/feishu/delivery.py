from dataclasses import replace
from typing import Any

from channel_gateway.common.domain.channel import ClaimedOutbound
from channel_gateway.common.domain.outbound import (
    OutboundRenderer,
    inline_artifact_bytes,
)
from channel_gateway.common.errors import InvalidStaticAssetError
from channel_gateway.common.ports.core import StaticAssetClient
from channel_gateway.common.ports.providers import RuntimeCredentialStore
from channel_gateway.feishu.domain import FeishuRuntimeError
from channel_gateway.feishu.ports import (
    FeishuOutboundFactory,
)
from channel_gateway.feishu.presentation import (
    FeishuPresentationRenderer,
)


_MAX_FEISHU_IMAGE_BYTES = 10 * 1024 * 1024
_MAX_FEISHU_FILE_BYTES = 30 * 1024 * 1024


class FeishuDeliveryProvider:
    def __init__(
        self,
        *,
        credentials: RuntimeCredentialStore,
        channels: FeishuOutboundFactory,
        renderer: OutboundRenderer,
        lazymind: StaticAssetClient,
    ):
        self._credentials = credentials
        self._channels = channels
        self._renderer = FeishuPresentationRenderer(renderer)
        self._lazymind = lazymind

    def render(
        self,
        message: ClaimedOutbound,
    ) -> list[dict[str, Any]]:
        parts = self._renderer.render(message)
        sources = [
            str(part.get('source') or '')
            for part in parts
            if part.get('kind') in {'image', 'file'}
            and part.get('source')
        ]
        if not sources:
            return parts
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        try:
            for source in sources:
                self._lazymind.validate_static_asset(
                    source=source,
                    owner_user_id=str(account['owner_user_id']),
                )
        except InvalidStaticAssetError:
            return self._renderer.render(
                replace(
                    message,
                    text=(
                        'LazyMind 没有返回可读取的图片或文件。'
                        '它可能未实际生成，或临时链接已经失效；'
                        '请重新生成。'
                    ),
                    intent_kind='failed',
                    metadata={},
                )
            )
        return parts

    def prepare_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        saved_state: dict[str, Any],
    ) -> dict[str, Any]:
        return saved_state

    def send_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        idempotency_key: str,
        saved_state: dict[str, Any],
    ) -> None:
        chat_id = str(
            message.provider_context.get('chat_id')
            or message.recipient_id
        )
        if not chat_id:
            raise FeishuRuntimeError(
                'Feishu destination chat is missing'
            )
        reply_to = str(
            message.provider_context.get('message_id')
            or ''
        )
        reply_in_thread = (
            message.provider_context.get('reply_in_thread') is True
        )
        kind = str(part.get('kind') or '')
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        sender = self._channels.create_sender(
            account['credentials']
        )
        try:
            if kind == 'text':
                sender.send_markdown(
                    chat_id=chat_id,
                    text=str(part.get('text') or ''),
                    reply_to=reply_to or None,
                    reply_in_thread=reply_in_thread,
                    idempotency_key=idempotency_key,
                )
                return
            if kind == 'card':
                card = part.get('card')
                if not isinstance(card, dict):
                    raise FeishuRuntimeError(
                        'Feishu card payload is invalid'
                    )
                sender.send_card(
                    chat_id=chat_id,
                    card=card,
                    reply_to=reply_to or None,
                    reply_in_thread=reply_in_thread,
                    idempotency_key=idempotency_key,
                )
                return
            source = str(part.get('source') or '')
            if kind == 'image':
                try:
                    content = self._lazymind.download_static_image(
                        source=source,
                        owner_user_id=str(account['owner_user_id']),
                    )
                except InvalidStaticAssetError:
                    self._send_asset_failure(
                        sender=sender,
                        chat_id=chat_id,
                        reply_to=reply_to,
                        reply_in_thread=reply_in_thread,
                        idempotency_key=idempotency_key,
                        kind='图片',
                    )
                    return
                if len(content) > _MAX_FEISHU_IMAGE_BYTES:
                    raise FeishuRuntimeError(
                        '飞书图片不能超过 10 MB'
                    )
                sender.send_image(
                    chat_id=chat_id,
                    content=content,
                    caption='',
                    reply_to=reply_to or None,
                    reply_in_thread=reply_in_thread,
                    idempotency_key=idempotency_key,
                )
                return
            if kind == 'file':
                artifact_index = str(
                    part.get('artifact_index') or ''
                )
                if artifact_index:
                    content = inline_artifact_bytes(
                        message.metadata,
                        artifact_index,
                    )
                    if content is None:
                        raise FeishuRuntimeError(
                            'LazyMind inline artifact is invalid'
                        )
                else:
                    try:
                        content = self._lazymind.download_static_file(
                            source=source,
                            owner_user_id=str(account['owner_user_id']),
                        )
                    except InvalidStaticAssetError:
                        self._send_asset_failure(
                            sender=sender,
                            chat_id=chat_id,
                            reply_to=reply_to,
                            reply_in_thread=reply_in_thread,
                            idempotency_key=idempotency_key,
                            kind='文件',
                        )
                        return
                if len(content) > _MAX_FEISHU_FILE_BYTES:
                    raise FeishuRuntimeError(
                        '飞书文件不能超过 30 MB'
                    )
                sender.send_file(
                    chat_id=chat_id,
                    content=content,
                    filename=str(
                        part.get('filename')
                        or 'lazymind-output'
                    ),
                    reply_to=reply_to or None,
                    reply_in_thread=reply_in_thread,
                    idempotency_key=idempotency_key,
                )
                return
            raise FeishuRuntimeError(
                'Unsupported Feishu outbound part'
            )
        finally:
            sender.close()

    @staticmethod
    def _send_asset_failure(
        *,
        sender,
        chat_id: str,
        reply_to: str,
        reply_in_thread: bool,
        idempotency_key: str,
        kind: str,
    ) -> None:
        sender.send_markdown(
            chat_id=chat_id,
            text=(
                f'⚠️ LazyMind 没有返回可读取的{kind}文件。'
                '它可能未实际生成，或临时链接已经失效；请重新生成。'
            ),
            reply_to=reply_to or None,
            reply_in_thread=reply_in_thread,
            idempotency_key=idempotency_key,
        )
