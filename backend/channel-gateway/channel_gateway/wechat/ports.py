import datetime as dt
from collections.abc import Callable
from typing import Any, Protocol

from channel_gateway.common.ports.providers import RuntimeLease


class WeChatLoginClient(Protocol):
    def start_login(
        self,
        local_tokens: tuple[str, ...] = (),
    ) -> tuple[str, str, str]:
        ...

    def poll_login_status(
        self,
        qrcode: str,
        base_url: str,
        verify_code: str = '',
    ) -> dict[str, Any]:
        ...


class WeChatReceiverClient(Protocol):
    def get_updates(
        self,
        *,
        base_url: str,
        token: str,
        cursor: str,
        timeout_ms: int,
    ) -> dict[str, Any]:
        ...

    def notify_start(self, *, base_url: str, token: str) -> None:
        ...

    def download_media(
        self,
        media: dict[str, Any],
        *,
        image_aeskey: str = '',
        max_bytes: int,
        max_download_bytes: int,
        fallback_aes_keys: tuple[str, ...] = (),
        validate_plaintext: Callable[[bytes], bool] | None = None,
        on_download_bytes: Callable[[int], None] | None = None,
    ) -> tuple[bytes, str]:
        ...


class WeChatDeliveryClient(Protocol):
    def get_config(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        context_token: str,
    ) -> dict[str, Any]:
        ...

    def send_typing(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        typing_ticket: str,
        typing: bool,
    ) -> None:
        ...

    def send_text(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        context_token: str,
        text: str,
        client_id: str,
        run_id: str,
    ) -> None:
        ...

    def upload_image(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        image: bytes,
    ) -> dict[str, Any]:
        ...

    def upload_file(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        content: bytes,
        filename: str,
    ) -> dict[str, Any]:
        ...

    def send_media(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        context_token: str,
        item: dict[str, Any],
        client_id: str,
        run_id: str,
    ) -> None:
        ...

    def send_tool_progress(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        context_token: str,
        tool_name: str,
        tool_call_id: str,
        status: str,
        started: bool,
        client_id: str,
        run_id: str,
    ) -> None:
        ...


class WeChatConnectionRepository(Protocol):
    def acquire_runtime_lease(
        self,
        account_id: str,
    ) -> RuntimeLease | None:
        ...

    def recoverable_sessions(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
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

    def refresh_session(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        state_ciphertext: str,
        expires_at: dt.datetime,
        message: str,
    ) -> dict[str, Any] | None:
        ...

    def cancel_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any] | None:
        ...

    def save_connected_account(
        self,
        *,
        session_id: str,
        qr_version: int,
        expected_revision: int,
        owner_user_id: str,
        provider: str,
        external_id_hash: str,
        label: str,
        credentials_ciphertext: str,
        conflict_message: str,
        connected_message: str,
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

    def delete_account(self, owner_user_id: str, account_id: str) -> bool:
        ...
