import datetime as dt
import threading
from collections.abc import Callable
from typing import Any, Protocol

from channel_gateway.common.domain.channel import (
    ClaimedOutbound,
    RuntimeFence,
)
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.common.ports.providers import RuntimeLease
from channel_gateway.feishu.domain import (
    FeishuAppCredentials,
    FeishuAppRegistration,
    FeishuInboundAction,
    FeishuInboundMessage,
)


class FeishuAccountRepository(Protocol):
    def acquire_runtime_lease(
        self,
        lease_key: str,
    ) -> RuntimeLease | None:
        ...

    def orphaned_provisioning_accounts(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
        ...

    def claim_orphaned_provisioning_account(
        self,
        *,
        provider: str,
        account_id: str,
        runtime_fence: RuntimeFence,
    ) -> dict[str, Any] | None:
        ...

    def delete_orphaned_provisioning_account(
        self,
        *,
        owner_user_id: str,
        account_id: str,
        runtime_fence: RuntimeFence,
    ) -> bool:
        ...

    def connect_referenced_account(
        self,
        *,
        owner_user_id: str,
        provider: str,
        external_id_hash: str,
        label: str,
        credentials_ciphertext: str,
        status: str,
        runtime_fence: RuntimeFence | None = None,
    ) -> dict[str, Any] | None:
        ...

    def get_account(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> dict[str, Any] | None:
        ...

    def list_accounts(
        self,
        owner_user_id: str,
        provider: str,
    ) -> list[dict[str, Any]]:
        ...

    def delete_account(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> bool:
        ...


class FeishuAppRegistrar(Protocol):
    def register(
        self,
        *,
        on_qr_code: Callable[[str, int], None],
        on_status_change: Callable[[str], None],
        cancel_event: threading.Event,
    ) -> FeishuAppRegistration:
        ...


class FeishuConnectionRepository(Protocol):
    def acquire_runtime_lease(self, lease_key: str):
        ...

    def reserve_session(
        self,
        *,
        session_id: str,
        owner_user_id: str,
        provider: str,
        idempotency_key: str | None,
        expires_at: dt.datetime,
    ) -> tuple[dict[str, Any], bool]:
        ...

    def recoverable_sessions(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
        ...

    def set_qr_ready(
        self,
        session_id: str,
        state_ciphertext: str,
        expires_at: dt.datetime,
        message: str,
    ) -> dict[str, Any] | None:
        ...

    def get_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any] | None:
        ...

    def get_session_internal(
        self,
        session_id: str,
    ) -> dict[str, Any] | None:
        ...

    def update_active_session(
        self,
        *,
        session_id: str,
        qr_version: int,
        expected_revision: int,
        status: str,
        message: str,
        state_ciphertext: str,
        expires_at: dt.datetime | None = None,
    ) -> dict[str, Any] | None:
        ...

    def begin_provisioning_cleanup(
        self,
        session_id: str,
        qr_version: int,
        runtime_fence: RuntimeFence | None = None,
    ) -> dict[str, Any] | None:
        ...

    def complete_provisioning_cleanup(
        self,
        session_id: str,
        qr_version: int,
        runtime_fence: RuntimeFence | None = None,
    ) -> None:
        ...

    def restart_connection_session(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        expires_at: dt.datetime,
    ) -> dict[str, Any] | None:
        ...

    def cancel_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any] | None:
        ...

    def mark_expired(
        self,
        session_id: str,
        qr_version: int,
    ) -> dict[str, Any] | None:
        ...

    def mark_failed(
        self,
        session_id: str,
        qr_version: int,
        *,
        code: str,
        message: str,
        retryable: bool,
    ) -> dict[str, Any] | None:
        ...

    def complete_provisioned_connection(
        self,
        *,
        session_id: str,
        qr_version: int,
        owner_user_id: str,
        account_id: str,
        message: str,
        runtime_fence: RuntimeFence | None = None,
    ) -> dict[str, Any] | None:
        ...

    def attach_provisioning_account(
        self,
        *,
        session_id: str,
        qr_version: int,
        owner_user_id: str,
        account_id: str,
        runtime_fence: RuntimeFence | None = None,
    ) -> dict[str, Any] | None:
        ...

    def get_account(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> dict[str, Any] | None:
        ...

    def claim_welcome(self, account_id: str) -> bool:
        ...


class FeishuReceiverClient(Protocol):
    def start(self) -> None:
        ...

    def stop(self) -> None:
        ...

    def is_ready(self) -> bool:
        ...

    def connection_state(self) -> str:
        ...


class FeishuOutboundClient(Protocol):
    def close(self) -> None:
        ...

    def send_markdown(
        self,
        *,
        chat_id: str,
        text: str,
        idempotency_key: str,
    ) -> str:
        ...

    def send_markdown_to_user(
        self,
        *,
        open_id: str,
        text: str,
        idempotency_key: str,
    ) -> str:
        ...

    def send_image(
        self,
        *,
        chat_id: str,
        content: bytes,
        caption: str,
        idempotency_key: str,
    ) -> None:
        ...

    def send_card(
        self,
        *,
        chat_id: str,
        card: dict[str, Any],
        idempotency_key: str,
    ) -> str:
        ...

    def update_card(
        self,
        *,
        message_id: str,
        card: dict[str, Any],
    ) -> None:
        ...

    def send_file(
        self,
        *,
        chat_id: str,
        content: bytes,
        filename: str,
        idempotency_key: str,
    ) -> None:
        ...

    def start_card_stream(
        self,
        *,
        chat_id: str,
        initial_card: dict[str, Any],
    ) -> ReplyStream:
        ...


class FeishuReceiverFactory(Protocol):
    def create_receiver(
        self,
        credentials: FeishuAppCredentials,
        on_message: Callable[[FeishuInboundMessage], None],
        on_action: Callable[[FeishuInboundAction], None],
    ) -> FeishuReceiverClient:
        ...


class FeishuOutboundFactory(Protocol):
    def create_sender(
        self,
        credentials: FeishuAppCredentials,
    ) -> FeishuOutboundClient:
        ...


class FeishuTaskOutboxRepository(Protocol):
    def list_sent_task_outbounds(
        self,
        *,
        provider: str,
        limit: int,
    ) -> list[ClaimedOutbound]:
        ...

    def save_sent_outbound_part_state(
        self,
        *,
        outbox_id: str,
        part_index: int,
        state: dict[str, Any],
    ) -> bool:
        ...
