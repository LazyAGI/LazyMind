from __future__ import annotations

from typing import Any, Protocol

from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
    InboundEnvelope,
    NativeConversationTarget,
    OutboundMessage,
    ReceiverCheckpoint,
    RuntimeFence,
)


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
        native_operation: dict | None = None,
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
        native_operation: dict | None = None,
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
    ) -> None:
        ...


class DeliveryProviderRegistry(Protocol):
    def delivery(self, provider: str) -> DeliveryProvider | None:
        ...


class NativeConversationSurface(Protocol):
    def open_conversation(
        self,
        *,
        account_id: str,
        owner_user_id: str,
        current_external_address_hash: str,
        current_provider_context: dict,
        has_current_conversation: bool,
        operation_kind: str,
        request_id: str,
        request_text: str,
        prepared_command: dict,
        grounding_messages: list[str],
        prepared_catalog: dict,
    ) -> NativeConversationTarget | None:
        ...


class NativeConversationSurfaceRegistry(Protocol):
    def surface(
        self,
        provider: str,
    ) -> NativeConversationSurface | None:
        ...


class NativeThreadRepository(Protocol):
    def reserve_native_operation(
        self,
        *,
        account_id: str,
        provider: str,
        operation_id: str,
        operation_kind: str,
        container_id: str,
        source_external_address_hash: str,
        prepared_command: dict,
        grounding_messages: list[str],
        prepared_catalog: dict,
    ) -> dict:
        ...

    def attach_native_operation_target(
        self,
        *,
        account_id: str,
        provider: str,
        operation_id: str,
        root_message_id: str,
        external_address_hash: str,
    ) -> dict:
        ...

    def fail_native_operation(
        self,
        *,
        account_id: str,
        provider: str,
        operation_id: str,
        error: str,
    ) -> None:
        ...

    def defer_native_operation(
        self,
        *,
        account_id: str,
        provider: str,
        operation_id: str,
        error: str,
    ) -> None:
        ...

    def record_native_thread(
        self,
        *,
        account_id: str,
        provider: str,
        container_id: str,
        root_message_id: str,
        thread_id: str,
        external_address_hash: str,
        operation_id: str = '',
    ) -> None:
        ...
