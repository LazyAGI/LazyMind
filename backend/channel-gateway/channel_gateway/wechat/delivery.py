from __future__ import annotations

import uuid
from typing import Any

from channel_gateway.common.domain.channel import ClaimedInbound, ClaimedOutbound
from channel_gateway.common.domain.outbound import (
    inline_artifact_bytes,
)
from channel_gateway.common.ports.core import StaticAssetClient
from channel_gateway.wechat.credentials import WeChatCredentialStore
from channel_gateway.wechat.domain import WeChatError
from channel_gateway.wechat.interaction import (
    WeChatPresentationRenderer,
    WeChatReplyStream,
)
from channel_gateway.wechat.ports import WeChatDeliveryClient


class WeChatDeliveryProvider:
    def __init__(
        self,
        *,
        client: WeChatDeliveryClient,
        credentials: WeChatCredentialStore,
        renderer: WeChatPresentationRenderer,
        lazymind: StaticAssetClient,
    ):
        self._client = client
        self._credentials = credentials
        self._renderer = renderer
        self._lazymind = lazymind

    def open_stream(self, message: ClaimedInbound) -> WeChatReplyStream:
        account = self._credentials.load_runtime_account(message.account_id)
        return WeChatReplyStream(
            message=message,
            client=self._client,
            credentials=dict(account['credentials']),
        )

    def render(self, message: ClaimedOutbound) -> list[dict[str, Any]]:
        return self._renderer.render(message)

    def prepare_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        saved_state: dict[str, Any],
    ) -> dict[str, Any]:
        kind = str(part.get('kind') or '')
        if kind not in ('image', 'file'):
            return saved_state
        source = str(part.get('source') or '')
        artifact_index = str(part.get('artifact_index') or '')
        state_key = source or f'artifact:{artifact_index}'
        ciphertext = str(saved_state.get('ciphertext') or '')
        if saved_state.get('source') == state_key and ciphertext:
            return saved_state
        account = self._credentials.load_runtime_account(message.account_id)
        credentials = dict(account['credentials'])
        if kind == 'image':
            content = self._lazymind.download_static_image(
                source=source,
                owner_user_id=str(account['owner_user_id']),
            )
            item = self._client.upload_image(
                base_url=credentials['base_url'],
                token=credentials['token'],
                to_user_id=message.recipient_id,
                image=content,
            )
        else:
            if artifact_index:
                content = inline_artifact_bytes(
                    message.metadata,
                    artifact_index,
                )
                if content is None:
                    raise WeChatError(
                        'LazyMind inline artifact is invalid'
                    )
            else:
                content = self._lazymind.download_static_file(
                    source=source,
                    owner_user_id=str(account['owner_user_id']),
                )
            item = self._client.upload_file(
                base_url=credentials['base_url'],
                token=credentials['token'],
                to_user_id=message.recipient_id,
                content=content,
                filename=str(
                    part.get('filename') or 'lazymind-output'
                ),
            )
        return {
            'source': state_key,
            'ciphertext': self._credentials.encrypt_delivery_state(
                message.account_id,
                {'item': item},
            ),
        }

    def send_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        idempotency_key: str,
        saved_state: dict[str, Any],
    ) -> None:
        account = self._credentials.load_runtime_account(message.account_id)
        credentials = dict(account['credentials'])
        context_token = str(
            message.provider_context.get('context_token') or ''
        )
        if not context_token:
            raise WeChatError('WeChat reply context is missing')
        run_id = str(
            uuid.uuid5(
                uuid.NAMESPACE_URL,
                f'lazymind:{message.outbox_id}:run',
            )
        )
        if part.get('kind') == 'text':
            self._client.send_text(
                base_url=credentials['base_url'],
                token=credentials['token'],
                to_user_id=message.recipient_id,
                context_token=context_token,
                text=str(part.get('text') or ''),
                client_id=idempotency_key,
                run_id=run_id,
            )
            return
        if part.get('kind') not in ('image', 'file'):
            raise WeChatError('Unsupported WeChat outbound part')
        ciphertext = str(saved_state.get('ciphertext') or '')
        if not ciphertext:
            raise WeChatError('Prepared WeChat media state is missing')
        state = self._credentials.decrypt_delivery_state(
            message.account_id,
            ciphertext,
        )
        item = state.get('item')
        if not isinstance(item, dict):
            raise WeChatError('Prepared WeChat media is invalid')
        self._client.send_media(
            base_url=credentials['base_url'],
            token=credentials['token'],
            to_user_id=message.recipient_id,
            context_token=context_token,
            item=item,
            client_id=idempotency_key,
            run_id=run_id,
        )
