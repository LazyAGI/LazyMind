from __future__ import annotations

import datetime as dt
import hashlib
import re
from dataclasses import dataclass, field
from typing import Any, Literal


ACTIVE_CONNECTION_SESSION_STATUSES = (
    'preparing',
    'waiting_scan',
    'scanned',
    'verification_required',
    'confirming',
)

_HIDDEN_PROTOCOL_TAGS = re.compile(
    r'(?s)<(?:think|tool_call|tool_result|tp|trp)\b[^>]*>'
    r'.*?</(?:think|tool_call|tool_result|tp|trp)>'
)


def account_view(row: dict[str, Any]) -> dict[str, Any]:
    return {
        'id': row['id'],
        'provider': row['provider'],
        'label': row['label'],
        'status': row['status'],
        'runtime_status': row.get('runtime_status') or 'stopped',
        'connected_at': _iso(row.get('connected_at')),
        'last_poll_at': _iso(row.get('last_poll_at')),
        'last_message_at': _iso(row.get('last_message_at')),
        'last_error': row.get('last_error'),
        'updated_at': _iso(row['updated_at']),
    }


def _iso(value: dt.datetime | None) -> str | None:
    return value.isoformat() if value else None


def sanitize_channel_text(value: str) -> str:
    """Remove internal model protocol blocks before channel presentation."""
    cleaned = _HIDDEN_PROTOCOL_TAGS.sub('', str(value or ''))
    cleaned = re.sub(r'\n{3,}', '\n\n', cleaned)
    return cleaned.strip()


@dataclass(frozen=True, slots=True)
class ChannelAddress:
    """Stable provider address used for ordering and LazyMind routing."""

    canonical_key: str
    actor_key: str

    @property
    def route_hash(self) -> str:
        return hashlib.sha256(
            self.canonical_key.encode('utf-8')
        ).hexdigest()


@dataclass(frozen=True, slots=True)
class InboundEnvelope:
    provider: str
    account_id: str
    message_key: str
    order_key: str
    external_address_hash: str
    owner_user_id: str
    recipient_id: str
    text: str
    provider_context: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, slots=True)
class ReceiverCheckpoint:
    cursor: str
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, slots=True)
class RuntimeFence:
    key: str
    owner_id: str
    generation: int


@dataclass(frozen=True, slots=True)
class ClaimedInbound:
    inbox_id: str
    provider: str
    account_id: str
    message_key: str
    order_key: str
    external_address_hash: str
    owner_user_id: str
    recipient_id: str
    text: str
    provider_context: dict[str, Any]
    attempt_count: int


@dataclass(frozen=True, slots=True)
class OutboundMessage:
    provider: str
    account_id: str
    order_key: str
    recipient_id: str
    provider_context: dict[str, Any]
    text: str
    intent_kind: str
    purpose: Literal['reply', 'welcome'] = 'reply'
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, slots=True)
class ClaimedOutbound:
    outbox_id: str
    provider: str
    account_id: str
    order_key: str
    recipient_id: str
    provider_context: dict[str, Any]
    text: str
    intent_kind: str
    purpose: str
    metadata: dict[str, Any]
    rendered_parts: list[dict[str, Any]]
    next_part_index: int
    provider_state: dict[str, Any]
    attempt_count: int


@dataclass(frozen=True, slots=True)
class NativeConversationTarget:
    external_address_hash: str
    recipient_id: str
    provider_context: dict[str, Any]
    operation_id: str = ''
    operation_kind: str = ''
    reused: bool = False
    cached_result: dict[str, Any] | None = None


class NativeTargetError(RuntimeError):
    """A message failed after the provider created its visible target."""

    def __init__(self, target: NativeConversationTarget):
        super().__init__('Native conversation target operation failed')
        self.target = target
