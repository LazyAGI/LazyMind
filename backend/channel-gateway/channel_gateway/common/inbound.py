from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Protocol

from channel_gateway.common.channel_message import ChannelMessageService
from channel_gateway.common.database import GatewayStore


_logger = logging.getLogger(__name__)


class ReplySender(Protocol):
    def __call__(
        self,
        to_user_id: str,
        context_token: str,
        text: str,
        message_key: str,
    ) -> None:
        ...


@dataclass(frozen=True, slots=True)
class InboundMessage:
    provider: str
    account_id: str
    external_address_hash: str
    owner_user_id: str
    sender_id: str
    context_token: str
    text: str
    message_key: str


class InboundMessageProcessor:
    """Owns channel-independent claim, execution, and reply-outbox semantics."""

    def __init__(self, *, store: GatewayStore, messages: ChannelMessageService):
        self._store = store
        self._messages = messages

    def process(
        self,
        message: InboundMessage,
        *,
        claim_owner: str,
        send: ReplySender,
    ) -> None:
        pending = self._store.get_pending_reply(
            message.account_id,
            message.message_key,
        )
        if pending is not None:
            pending['message_key'] = message.message_key
            if not self._send_pending(message.account_id, pending, send):
                return
            final_status = (
                'failed' if pending.get('intent_kind') == 'failed' else 'replied'
            )
            self._store.mark_message_processed(
                message.account_id,
                message.message_key,
                final_status,
            )
            return

        if not self._store.claim_message(
            message.account_id,
            message.message_key,
            claim_owner,
        ):
            return

        try:
            result = self._messages.process(
                provider=message.provider,
                account_id=message.account_id,
                external_address_hash=message.external_address_hash,
                owner_user_id=message.owner_user_id,
                text=message.text,
                request_id=f'channel_{message.message_key[:24]}',
            )
            response_text = result.text
            intent_kind = result.intent_kind.value
            final_status = 'replied'
        except Exception:
            _logger.exception(
                'channel_message_failed account_id=%s message_key=%s',
                message.account_id,
                message.message_key[:12],
            )
            response_text = 'LazyMind 暂时无法处理这条消息，请稍后重试。'
            intent_kind = 'failed'
            final_status = 'failed'

        saved = self._store.save_pending_reply(
            message.account_id,
            message.message_key,
            claim_owner,
            response_text,
            intent_kind,
            message.sender_id,
            message.context_token,
        )
        if not saved:
            _logger.warning(
                'channel_message_fenced account_id=%s message_key=%s',
                message.account_id,
                message.message_key[:12],
            )
            return
        pending = {
            'message_key': message.message_key,
            'response_text': response_text,
            'intent_kind': intent_kind,
            'response_to_user_id': message.sender_id,
            'response_context_token': message.context_token,
        }
        if not self._send_pending(message.account_id, pending, send):
            return
        self._store.mark_message_processed(
            message.account_id,
            message.message_key,
            final_status,
            claim_owner=claim_owner,
        )
        _logger.info(
            'channel_message_replied account_id=%s intent=%s message_key=%s',
            message.account_id,
            intent_kind,
            message.message_key[:12],
        )

    def retry_pending(self, account_id: str, *, send: ReplySender) -> None:
        for pending in self._store.pending_replies(account_id):
            if not self._send_pending(account_id, pending, send):
                continue
            final_status = (
                'failed' if pending.get('intent_kind') == 'failed' else 'replied'
            )
            self._store.mark_message_processed(
                account_id,
                str(pending.get('message_key') or ''),
                final_status,
            )

    def _send_pending(
        self,
        account_id: str,
        pending: dict[str, object],
        send: ReplySender,
    ) -> bool:
        message_key = str(pending.get('message_key') or '')
        try:
            send(
                str(pending.get('response_to_user_id') or ''),
                str(pending.get('response_context_token') or ''),
                str(pending.get('response_text') or ''),
                message_key,
            )
        except Exception as exc:
            _logger.warning(
                'channel_outbox_send_failed account_id=%s message_key=%s error_type=%s',
                account_id,
                message_key[:12],
                exc.__class__.__name__,
            )
            self._store.record_reply_failure(
                account_id,
                message_key,
                exc.__class__.__name__,
            )
            return False
        return True
