import hashlib
from collections.abc import Callable
from dataclasses import asdict
from typing import Any

from channel_gateway.common.domain.channel import RuntimeFence, account_view
from channel_gateway.common.errors import GatewayError
from channel_gateway.common.ports.providers import (
    AccountCredentialRepository,
    PayloadCipher,
)
from channel_gateway.feishu.domain import FeishuAppCredentials
from channel_gateway.feishu.ports import FeishuAccountRepository


class FeishuCredentialStore:
    """Decrypts Feishu app credentials only at the provider boundary."""

    def __init__(
        self,
        *,
        store: AccountCredentialRepository,
        cipher: PayloadCipher,
    ):
        self._store = store
        self._cipher = cipher

    def load_runtime_account(
        self,
        account_id: str,
    ) -> dict[str, Any]:
        account = self._store.get_account_internal(account_id)
        if not account:
            raise RuntimeError('Channel account does not exist')
        if account['provider'] != 'feishu':
            raise RuntimeError('Channel account is not a Feishu account')
        owner_user_id = str(account['owner_user_id'])
        ciphertext = str(account['credentials_ciphertext'] or '')
        try:
            payload = self._cipher.decrypt(
                owner_user_id,
                ciphertext,
            )
            credentials = FeishuAppCredentials(
                app_id=str(payload['app_id']).strip(),
                app_secret=str(payload['app_secret']).strip(),
                provider_account_id=str(
                    payload['provider_account_id']
                ).strip(),
                provider_tenant_key=str(
                    payload.get('provider_tenant_key') or ''
                ).strip(),
                display_name=str(
                    payload.get('display_name') or ''
                ).strip(),
            )
        except Exception as exc:
            raise RuntimeError(
                'Cannot decrypt Feishu app credentials'
            ) from exc
        if (
            not credentials.app_id
            or not credentials.app_secret
            or not credentials.provider_account_id
        ):
            raise RuntimeError('Feishu app credentials are incomplete')
        if self._cipher.needs_migration(ciphertext):
            self._store.update_account_credentials(
                account_id,
                self._cipher.encrypt(owner_user_id, payload),
                int(account['credential_revision']),
            )
        return {
            **dict(account),
            'credentials': credentials,
        }


class FeishuAccountService:
    def __init__(
        self,
        *,
        store: FeishuAccountRepository,
        cipher: PayloadCipher,
        on_account_connected: Callable[[str], None] | None = None,
        on_account_disconnected: Callable[[str], None] | None = None,
    ):
        self._store = store
        self._cipher = cipher
        self._on_account_connected = on_account_connected
        self._on_account_disconnected = on_account_disconnected

    def connect_registered_account(
        self,
        *,
        owner_user_id: str,
        credentials: FeishuAppCredentials,
        runtime_fence: RuntimeFence,
        notify_runtime: bool = True,
    ) -> dict[str, Any]:
        external_id_hash = hashlib.sha256(
            (
                f'{credentials.app_id}:'
                f'{credentials.provider_account_id}'
            ).encode('utf-8')
        ).hexdigest()
        label_name = (
            credentials.display_name
            or credentials.provider_account_id
        )
        account = self._store.connect_referenced_account(
            owner_user_id=owner_user_id,
            provider='feishu',
            external_id_hash=external_id_hash,
            label=f'飞书 · {label_name}',
            credentials_ciphertext=self._cipher.encrypt(
                owner_user_id,
                asdict(credentials),
            ),
            status='provisioning',
            runtime_fence=runtime_fence,
        )
        if account is None:
            raise GatewayError(
                409,
                'FEISHU_ACCOUNT_ALREADY_BOUND',
                '该飞书身份已绑定到另一个 LazyMind 用户',
            )
        if notify_runtime and self._on_account_connected:
            self._on_account_connected(str(account['id']))
        return account_view(account)

    def start_account_runtime(self, account_id: str) -> None:
        if self._on_account_connected:
            self._on_account_connected(account_id)

    def list_accounts(self, owner_user_id: str) -> dict[str, Any]:
        rows = self._store.list_accounts(owner_user_id, 'feishu')
        return {'items': [account_view(row) for row in rows]}

    def disconnect_account(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> None:
        if not self._store.get_account(owner_user_id, account_id):
            raise GatewayError(
                404,
                'ACCOUNT_NOT_FOUND',
                '飞书账号不存在或已解除连接',
            )
        if not self._store.delete_account(
            owner_user_id,
            account_id,
        ):
            raise GatewayError(
                409,
                'ACCOUNT_STATE_CHANGED',
                '飞书账号状态已经变化，请刷新后重试',
            )
        if self._on_account_disconnected:
            self._on_account_disconnected(account_id)

    def discard_provisioned_account(
        self,
        *,
        owner_user_id: str,
        account_id: str,
        runtime_fence: RuntimeFence | None = None,
    ) -> bool:
        if self._on_account_disconnected:
            self._on_account_disconnected(account_id)
        deleted = (
            self._store.delete_orphaned_provisioning_account(
                owner_user_id=owner_user_id,
                account_id=account_id,
                runtime_fence=runtime_fence,
            )
            if runtime_fence is not None
            else self._store.delete_account(owner_user_id, account_id)
        )
        if deleted:
            return True
        return self._store.get_account(owner_user_id, account_id) is None

    def cleanup_orphaned_provisioning(self) -> None:
        for account in self._store.orphaned_provisioning_accounts(
            'feishu'
        ):
            account_id = str(account['id'])
            session_id = str(
                account.get('registration_session_id') or ''
            )
            lease_key = (
                f'feishu-registration:{session_id}'
                if session_id
                else f'feishu-provisioning-cleanup:{account_id}'
            )
            lease = self._store.acquire_runtime_lease(lease_key)
            if lease is None:
                continue
            try:
                claimed = (
                    self._store
                    .claim_orphaned_provisioning_account(
                        provider='feishu',
                        account_id=account_id,
                        runtime_fence=lease.fence,
                    )
                )
                if claimed is None:
                    continue
                owner_user_id = str(claimed['owner_user_id'])
                lease.keepalive()
                if self._on_account_disconnected:
                    self._on_account_disconnected(account_id)
                self._store.delete_orphaned_provisioning_account(
                    owner_user_id=owner_user_id,
                    account_id=account_id,
                    runtime_fence=lease.fence,
                )
            finally:
                lease.close()
