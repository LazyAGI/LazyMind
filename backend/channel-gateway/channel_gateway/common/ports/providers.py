from typing import Any, Protocol

from channel_gateway.common.domain.channel import RuntimeFence
from channel_gateway.common.ports.messaging import IngestionRepository


class AccountAdapter(Protocol):
    def list_accounts(
        self,
        owner_user_id: str,
    ) -> dict[str, Any]:
        ...

    def disconnect_account(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> None:
        ...


class AccountAdapterResolver(Protocol):
    def accounts(self, name: str) -> AccountAdapter | None:
        ...


class AccountLookupRepository(Protocol):
    def get_account(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> dict[str, Any] | None:
        ...


class InteractiveConnectionAdapter(Protocol):
    def create_session(
        self,
        *,
        owner_user_id: str,
        idempotency_key: str | None,
    ) -> dict[str, Any]:
        ...

    def get_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any]:
        ...

    def submit_challenge(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        challenge_type: str,
        value: str,
    ) -> dict[str, Any]:
        ...

    def refresh_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any]:
        ...

    def cancel_session(self, owner_user_id: str, session_id: str) -> None:
        ...


class ConnectionAdapterResolver(Protocol):
    def connection(self, name: str) -> InteractiveConnectionAdapter | None:
        ...


class RuntimeSupervisor(Protocol):
    def start(self) -> None:
        ...

    def stop(self) -> None:
        ...


class ConnectionLookupRepository(Protocol):
    def get_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any] | None:
        ...


class PayloadCipher(Protocol):
    def encrypt(self, owner_user_id: str, payload: dict[str, Any]) -> str:
        ...

    def decrypt(self, owner_user_id: str, ciphertext: str) -> dict[str, Any]:
        ...

    def needs_migration(self, ciphertext: str) -> bool:
        ...


class AccountCredentialRepository(Protocol):
    def get_account_internal(self, account_id: str) -> dict[str, Any] | None:
        ...

    def update_account_credentials(
        self,
        account_id: str,
        credentials_ciphertext: str,
        expected_revision: int,
    ) -> bool:
        ...


class RuntimeLease(Protocol):
    @property
    def fence(self) -> RuntimeFence:
        ...

    def keepalive(self) -> None:
        ...

    def close(self) -> None:
        ...


class RuntimeCredentialStore(Protocol):
    def load_runtime_account(self, account_id: str) -> dict[str, Any]:
        ...


class ReceiverRepository(IngestionRepository, Protocol):
    def runtime_accounts(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
        ...

    def find_connected_account(
        self,
        provider: str,
        external_id_hash: str,
    ) -> dict[str, Any] | None:
        ...

    def acquire_runtime_lease(
        self,
        account_id: str,
    ) -> RuntimeLease | None:
        ...

    def get_checkpoint(self, account_id: str) -> dict[str, Any]:
        ...

    def set_runtime_status(
        self,
        account_id: str,
        status: str,
        error: str | None = None,
        runtime_fence: RuntimeFence | None = None,
    ) -> None:
        ...


class AccountRuntime(Protocol):
    def reconcile_accounts(
        self,
        accounts: list[dict[str, Any]],
    ) -> None:
        ...

    def stop(self) -> None:
        ...
