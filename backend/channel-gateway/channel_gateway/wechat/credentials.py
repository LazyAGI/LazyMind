from __future__ import annotations

from typing import Any

from channel_gateway.common.ports.providers import (
    AccountCredentialRepository,
    PayloadCipher,
)


class WeChatCredentialStore:
    """Owns all plaintext/ciphertext transitions for WeChat credentials."""

    def __init__(
        self,
        store: AccountCredentialRepository,
        cipher: PayloadCipher,
    ):
        self._store = store
        self._cipher = cipher

    def load_runtime_account(self, account_id: str) -> dict[str, Any]:
        account = self._store.get_account_internal(account_id)
        if not account:
            raise RuntimeError('Channel account does not exist')
        if account['provider'] != 'wechat':
            raise RuntimeError('Channel account is not a WeChat account')
        owner_user_id = str(account['owner_user_id'])
        ciphertext = str(account['credentials_ciphertext'] or '')
        try:
            raw = self._cipher.decrypt(owner_user_id, ciphertext)
        except Exception as exc:
            raise RuntimeError('Cannot decrypt channel credentials') from exc
        credentials = {
            'token': str(raw.get('token') or ''),
            'account_id': str(raw.get('account_id') or ''),
            'authorized_user_id': str(raw.get('authorized_user_id') or ''),
            'base_url': str(raw.get('base_url') or '').rstrip('/'),
        }
        if not all(credentials.values()):
            raise RuntimeError('Channel credentials are incomplete')
        if self._cipher.needs_migration(ciphertext):
            self._store.update_account_credentials(
                account_id,
                self._cipher.encrypt(owner_user_id, raw),
                int(account['credential_revision']),
            )
        return {
            **dict(account),
            'credentials': credentials,
        }

    def encrypt_delivery_state(
        self,
        account_id: str,
        value: dict[str, Any],
    ) -> str:
        account = self._store.get_account_internal(account_id)
        if not account:
            raise RuntimeError('Channel account does not exist')
        return self._cipher.encrypt(str(account['owner_user_id']), value)

    def decrypt_delivery_state(
        self,
        account_id: str,
        value: str,
    ) -> dict[str, Any]:
        account = self._store.get_account_internal(account_id)
        if not account:
            raise RuntimeError('Channel account does not exist')
        return self._cipher.decrypt(str(account['owner_user_id']), value)
