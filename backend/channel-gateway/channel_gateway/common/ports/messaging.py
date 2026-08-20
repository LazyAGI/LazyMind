from __future__ import annotations

from typing import Any, Protocol

from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
    InboundEnvelope,
    OutboundMessage,
    ReceiverCheckpoint,
    RuntimeFence,
)
from channel_gateway.common.domain.chat import CoreStreamUpdate


class WelcomeRepository(Protocol):
    def welcome_pending(self, account_id: str) -> bool:
        ...


class IngestionRepository(Protocol):
    def ingest_batch(
        self,
        account_id: str,
        envelopes: list[InboundEnvelope],
        checkpoint: ReceiverCheckpoint | None,
        runtime_fence: RuntimeFence | None = None,
    ) -> int:
        ...


class InboxWorkRepository(Protocol):
    def claim_next_inbound(
        self,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> ClaimedInbound | None:
        ...

    def renew_inbound_lease(
        self,
        inbox_id: str,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> bool:
        ...

    def complete_inbound(
        self,
        inbox_id: str,
        claim_owner: str,
        outbound: list[OutboundMessage],
        retained_provider_context: dict[str, Any],
    ) -> bool:
        ...

    def record_inbound_failure(
        self,
        inbox_id: str,
        claim_owner: str,
        *,
        error: str,
        fallback: OutboundMessage,
        max_attempts: int,
        retained_provider_context: dict[str, Any],
    ) -> bool:
        """Return True when the message reached its terminal fallback."""
        ...


class MessageWorkerRepository(
    InboxWorkRepository,
    WelcomeRepository,
    Protocol,
):
    pass


class OutboxWorkRepository(Protocol):
    def claim_next_outbound(
        self,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> ClaimedOutbound | None:
        ...

    def save_rendered_parts(
        self,
        outbox_id: str,
        claim_owner: str,
        parts: list[dict[str, Any]],
    ) -> bool:
        ...

    def renew_outbound_lease(
        self,
        outbox_id: str,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> bool:
        ...

    def save_outbound_part_state(
        self,
        outbox_id: str,
        claim_owner: str,
        part_index: int,
        state: dict[str, Any],
    ) -> bool:
        ...

    def advance_outbound(
        self,
        outbox_id: str,
        claim_owner: str,
        next_part_index: int,
    ) -> bool:
        ...

    def complete_outbound(
        self,
        outbox_id: str,
        claim_owner: str,
    ) -> bool:
        ...

    def record_outbound_failure(
        self,
        outbox_id: str,
        claim_owner: str,
        *,
        error: str,
        max_attempts: int,
    ) -> None:
        ...


class DeliveryProvider(Protocol):
    def render(self, message: ClaimedOutbound) -> list[dict[str, Any]]:
        ...

    def prepare_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        saved_state: dict[str, Any],
    ) -> dict[str, Any]:
        ...

    def send_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        idempotency_key: str,
        saved_state: dict[str, Any],
    ) -> dict[str, Any] | None:
        ...


class DeliveryProviderRegistry(Protocol):
    def delivery(self, provider: str) -> DeliveryProvider | None:
        ...


class ReplyStream(Protocol):
    def update(self, snapshot: CoreStreamUpdate) -> None:
        ...

    def finish(self, final_text: str) -> bool:
        ...

    def abort(self) -> None:
        ...


class ReplyStreamProvider(Protocol):
    def open_stream(
        self,
        message: ClaimedInbound,
    ) -> ReplyStream | None:
        ...


class ReplyStreamProviderRegistry(Protocol):
    def streaming(
        self,
        provider: str,
    ) -> ReplyStreamProvider | None:
        ...
