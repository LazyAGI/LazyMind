from __future__ import annotations

import logging
import threading
from typing import Any

from channel_gateway.common.errors import GatewayError
from channel_gateway.common.ports.providers import (
    AccountAdapter,
    AccountAdapterResolver,
    AccountLookupRepository,
    AccountRuntime,
    ConnectionAdapterResolver,
    ConnectionLookupRepository,
    InteractiveConnectionAdapter,
    ReceiverRepository,
)


_logger = logging.getLogger(__name__)


class AccountApplicationService:
    """Routes account binding and lifecycle to a provider adapter."""

    def __init__(
        self,
        *,
        store: AccountLookupRepository,
        providers: AccountAdapterResolver,
    ):
        self._store = store
        self._providers = providers

    def list_accounts(
        self,
        owner_user_id: str,
        provider: str,
    ) -> dict[str, Any]:
        return self._adapter(provider).list_accounts(owner_user_id)

    def disconnect_account(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> None:
        account = self._store.get_account(owner_user_id, account_id)
        if not account:
            raise GatewayError(
                404,
                'ACCOUNT_NOT_FOUND',
                '频道账号不存在或已解除连接',
            )
        provider = str(account.get('provider') or '')
        self._adapter(provider).disconnect_account(
            owner_user_id,
            account_id,
        )

    def _adapter(self, provider: str) -> AccountAdapter:
        normalized = provider.strip().lower()
        adapter = self._providers.accounts(normalized)
        if adapter is None:
            raise self._unsupported(normalized or provider)
        return adapter

    @staticmethod
    def _unsupported(provider: str) -> GatewayError:
        return GatewayError(
            422,
            'PROVIDER_NOT_SUPPORTED',
            f'暂不支持频道类型：{provider}',
        )


class ConnectionApplicationService:
    """Routes provider-neutral API operations to the registered adapter."""

    def __init__(
        self,
        *,
        store: ConnectionLookupRepository,
        providers: ConnectionAdapterResolver,
    ):
        self._store = store
        self._providers = providers

    def create_session(
        self,
        *,
        owner_user_id: str,
        provider: str,
        idempotency_key: str | None,
    ) -> dict[str, Any]:
        adapter = self._resolve(provider)
        return adapter.create_session(
            owner_user_id=owner_user_id,
            idempotency_key=idempotency_key,
        )

    def get_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any]:
        adapter = self._for_session(owner_user_id, session_id)
        return adapter.get_session(owner_user_id, session_id)

    def submit_challenge(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        challenge_type: str,
        value: str,
    ) -> dict[str, Any]:
        adapter = self._for_session(owner_user_id, session_id)
        return adapter.submit_challenge(
            owner_user_id=owner_user_id,
            session_id=session_id,
            challenge_type=challenge_type,
            value=value,
        )

    def refresh_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any]:
        adapter = self._for_session(owner_user_id, session_id)
        return adapter.refresh_session(owner_user_id, session_id)

    def cancel_session(self, owner_user_id: str, session_id: str) -> None:
        adapter = self._for_session(owner_user_id, session_id)
        adapter.cancel_session(owner_user_id, session_id)

    def _for_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> InteractiveConnectionAdapter:
        session = self._store.get_session(owner_user_id, session_id)
        if not session:
            raise GatewayError(404, 'LOGIN_NOT_FOUND', '连接会话不存在')
        return self._resolve(str(session.get('provider') or ''))

    def _resolve(self, provider: str) -> InteractiveConnectionAdapter:
        normalized = provider.strip().lower()
        adapter = self._providers.connection(normalized)
        if adapter is None:
            raise GatewayError(
                422,
                'PROVIDER_NOT_SUPPORTED',
                f'暂不支持频道类型：{normalized or provider}',
            )
        return adapter


class AccountRuntimeSupervisor:
    """Reconciles one provider's database desired state into local workers."""

    def __init__(
        self,
        *,
        provider: str,
        store: ReceiverRepository,
        runtime: AccountRuntime,
        interval_seconds: float = 2,
    ):
        self._provider = provider
        self._store = store
        self._runtime = runtime
        self._interval_seconds = interval_seconds
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        if self._thread and self._thread.is_alive():
            return
        self._thread = threading.Thread(
            target=self._run,
            name=f'channel-{self._provider}-reconciler',
            daemon=True,
        )
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        if self._thread:
            self._thread.join(timeout=self._interval_seconds + 1)
            self._thread = None
        self._runtime.stop()

    def _run(self) -> None:
        while not self._stop.is_set():
            try:
                self._runtime.reconcile_accounts(
                    self._store.runtime_accounts(self._provider)
                )
            except Exception:
                _logger.exception(
                    'channel_runtime_reconcile_failed provider=%s',
                    self._provider,
                )
            self._stop.wait(self._interval_seconds)
