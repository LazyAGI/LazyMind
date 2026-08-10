import datetime as dt
import threading
from collections.abc import Callable
from typing import Any, Protocol

from channel_gateway.common.domain.channel import (
    ClaimedOutbound,
    InboundEnvelope,
    RuntimeFence,
)
from channel_gateway.common.domain.chat import CoreStreamUpdate
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.common.ports.providers import ReceiverRepository
from channel_gateway.common.ports.repository import NavigationRepository
from channel_gateway.common.ports.providers import RuntimeLease
from channel_gateway.feishu.domain import (
    FeishuAppCredentials,
    FeishuAppRegistration,
    FeishuInboundAction,
    FeishuInboundMenu,
    FeishuInboundMessage,
)


class FeishuWorkspaceRepository(Protocol):
    def get_route(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> str:
        ...

    def get_feishu_workspace_state(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any]:
        ...

    def get_new_conversation_draft(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any]:
        ...

    def activate_conversation(
        self,
        account_id: str,
        external_address_hash: str,
        conversation_id: str,
        history_next_page_token: str | None = None,
        *,
        consume_pending_turn: bool = False,
        preserve_selection: bool = False,
    ) -> None:
        ...

    def save_feishu_workspace_state_if_revision(
        self,
        account_id: str,
        external_address_hash: str,
        state: dict[str, Any],
        expected_revision: int,
    ) -> bool:
        ...

    def claim_feishu_workspace_and_ingest(
        self,
        account_id: str,
        external_address_hash: str,
        state: dict[str, Any],
        expected_revision: int,
        expected_message_id: str,
        expected_operation_id: str,
        envelope: InboundEnvelope,
        runtime_fence: RuntimeFence,
        new_conversation_action: dict[str, Any] | None = None,
    ) -> bool:
        ...

    def has_active_inbound(
        self,
        account_id: str,
        order_key: str,
    ) -> bool:
        ...

    def patch_feishu_workspace_state(
        self,
        account_id: str,
        external_address_hash: str,
        patch: dict[str, Any],
        operation_id: str = '',
    ) -> dict[str, Any]:
        ...

    def save_feishu_workspace_message(
        self,
        account_id: str,
        external_address_hash: str,
        message_id: str,
        operation_id: str,
        expected_message_id: str,
        expected_revision: int | None = None,
        *,
        advance_revision: bool = True,
    ) -> dict[str, Any]:
        ...


class FeishuRuntimeRepository(
    ReceiverRepository,
    NavigationRepository,
    FeishuWorkspaceRepository,
    Protocol,
):
    pass


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

    def send_card_to_user(
        self,
        *,
        open_id: str,
        card: dict[str, Any],
        idempotency_key: str,
    ) -> str:
        ...

    def send_card_to_user_with_chat(
        self,
        *,
        open_id: str,
        card: dict[str, Any],
        idempotency_key: str,
    ) -> tuple[str, str]:
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

    def upload_image(self, *, content: bytes) -> str:
        ...

    def download_image(
        self,
        *,
        image_key: str,
        message_id: str,
    ) -> bytes:
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
        message_id: str = '',
        should_render: Callable[[], bool] | None = None,
        collapse_process: bool = True,
        render_card: Callable[
            [CoreStreamUpdate, bool, bool],
            dict[str, Any],
        ] | None = None,
    ) -> ReplyStream:
        ...


class FeishuReceiverFactory(Protocol):
    def create_receiver(
        self,
        credentials: FeishuAppCredentials,
        on_message: Callable[[FeishuInboundMessage], None],
        on_action: Callable[
            [FeishuInboundAction],
            dict[str, Any] | None,
        ],
        on_menu: Callable[[FeishuInboundMenu], None],
    ) -> FeishuReceiverClient:
        ...

    def create_sender(
        self,
        credentials: FeishuAppCredentials,
    ) -> FeishuOutboundClient:
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

    def sync_task_artifact_outbounds(
        self,
        *,
        parent: ClaimedOutbound,
        part_index: int,
        artifacts: list[dict[str, str]],
    ) -> dict[str, int]:
        ...

    def compare_and_save_sent_task_monitor_state(
        self,
        *,
        outbox_id: str,
        part_index: int,
        expected_revision: int,
        state: dict[str, Any],
        complete: bool,
    ) -> dict[str, Any] | None:
        ...
