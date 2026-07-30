from typing import Any

import psycopg
from psycopg.rows import dict_row

from channel_gateway.common.ports.providers import (
    AccountCredentialRepository,
    PayloadCipher,
)
from channel_gateway.feishu.domain import (
    FeishuAppCredentials,
    FeishuWorkspace,
)


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


class FeishuWorkspaceStore:
    """Provider-owned persistence for the native Feishu workspace."""

    def __init__(self, dsn: str):
        self._dsn = dsn

    def _connect(self):
        return psycopg.connect(self._dsn, row_factory=dict_row)

    def initialize(self) -> None:
        with self._connect() as connection:
            connection.execute(
                """
                CREATE TABLE IF NOT EXISTS feishu_workspaces (
                    account_id TEXT PRIMARY KEY
                        REFERENCES channel_accounts(id) ON DELETE CASCADE,
                    chat_id TEXT UNIQUE,
                    owner_open_id TEXT NOT NULL,
                    status VARCHAR(32) NOT NULL,
                    last_error TEXT NOT NULL DEFAULT '',
                    created_at TIMESTAMPTZ NOT NULL
                        DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMPTZ NOT NULL
                        DEFAULT CURRENT_TIMESTAMP
                )
                """
            )

    def get_by_account(
        self,
        account_id: str,
    ) -> FeishuWorkspace | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT account_id, chat_id, owner_open_id,
                       status, last_error
                FROM feishu_workspaces
                WHERE account_id = %s
                """,
                (account_id,),
            ).fetchone()
        return self._workspace(row)

    def save_ready(
        self,
        *,
        account_id: str,
        chat_id: str,
        owner_open_id: str,
    ) -> FeishuWorkspace:
        with self._connect() as connection:
            row = connection.execute(
                """
                INSERT INTO feishu_workspaces(
                    account_id, chat_id, owner_open_id,
                    status, last_error
                )
                VALUES(%s, %s, %s, 'ready', '')
                ON CONFLICT(account_id) DO UPDATE SET
                    chat_id = EXCLUDED.chat_id,
                    owner_open_id = EXCLUDED.owner_open_id,
                    status = 'ready',
                    last_error = '',
                    updated_at = CURRENT_TIMESTAMP
                RETURNING account_id, chat_id, owner_open_id,
                          status, last_error
                """,
                (account_id, chat_id, owner_open_id),
            ).fetchone()
        workspace = self._workspace(row)
        if workspace is None:
            raise RuntimeError('Feishu workspace was not saved')
        return workspace

    def mark_failed(
        self,
        *,
        account_id: str,
        owner_open_id: str,
        error: str,
    ) -> None:
        with self._connect() as connection:
            connection.execute(
                """
                INSERT INTO feishu_workspaces(
                    account_id, owner_open_id, status, last_error
                )
                VALUES(%s, %s, 'failed', %s)
                ON CONFLICT(account_id) DO UPDATE SET
                    owner_open_id = EXCLUDED.owner_open_id,
                    status = 'failed',
                    last_error = EXCLUDED.last_error,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (account_id, owner_open_id, error[:1000]),
            )

    def delete(self, account_id: str) -> None:
        with self._connect() as connection:
            connection.execute(
                'DELETE FROM feishu_workspaces WHERE account_id = %s',
                (account_id,),
            )

    @staticmethod
    def _workspace(row: dict | None) -> FeishuWorkspace | None:
        if not row:
            return None
        return FeishuWorkspace(
            account_id=str(row['account_id']),
            chat_id=str(row.get('chat_id') or ''),
            owner_open_id=str(row['owner_open_id']),
            status=str(row['status']),
            last_error=str(row.get('last_error') or ''),
        )
