import datetime as dt
import json
import os
import re
import sqlite3
import threading
import uuid
from pathlib import Path
from typing import Any
from urllib.parse import unquote, urlparse

import psycopg
from psycopg.rows import dict_row


ACTIVE_SESSION_STATUSES = (
    'preparing',
    'waiting_scan',
    'scanned',
    'verification_required',
    'confirming',
)

_SQLITE_BOOLEAN_COLUMNS = {'error_retryable', 'welcome_pending'}
_SQLITE_PLACEHOLDER_RE = re.compile(r'%s')
_SQLITE_FOR_UPDATE_RE = re.compile(r'\s+FOR\s+UPDATE\b', re.IGNORECASE)


def _sqlite_timestamp(raw: bytes) -> dt.datetime:
    value = dt.datetime.fromisoformat(raw.decode('utf-8').replace('Z', '+00:00'))
    return value if value.tzinfo else value.replace(tzinfo=dt.timezone.utc)


sqlite3.register_converter('TIMESTAMPTZ', _sqlite_timestamp)


def _sqlite_row(cursor, row):
    result = {description[0]: row[index] for index, description in enumerate(cursor.description)}
    for key in _SQLITE_BOOLEAN_COLUMNS:
        if result.get(key) is not None:
            result[key] = bool(result[key])
    return result


def _sqlite_path(dsn: str) -> str:
    parsed = urlparse(dsn)
    path = unquote(parsed.path)
    if parsed.netloc:
        path = f'//{parsed.netloc}{path}'
    if os.name == 'nt' and re.match(r'^/[A-Za-z]:/', path):
        path = path[1:]
    return os.path.normpath(path)


def _snapshot_dict(value: Any) -> dict[str, Any]:
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except json.JSONDecodeError:
            return {}
    return dict(value) if isinstance(value, dict) else {}


class _SQLiteConnection:
    def __init__(self, path: str):
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        self._connection = sqlite3.connect(
            path,
            timeout=30,
            detect_types=sqlite3.PARSE_DECLTYPES,
        )
        self._connection.row_factory = _sqlite_row
        self._connection.execute('PRAGMA foreign_keys = ON')
        self._connection.execute('PRAGMA busy_timeout = 30000')
        self._connection.execute('PRAGMA journal_mode = WAL')

    def __enter__(self):
        self._connection.__enter__()
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        try:
            return self._connection.__exit__(exc_type, exc_value, traceback)
        finally:
            self._connection.close()

    def execute(self, statement: str, parameters=()):
        sql = _SQLITE_PLACEHOLDER_RE.sub('?', statement)
        sql = _SQLITE_FOR_UPDATE_RE.sub('', sql)
        return self._connection.execute(sql, parameters)

    def close(self) -> None:
        self._connection.close()


class _SQLiteLease:
    def __init__(self, store: 'GatewayStore', account_id: str):
        self._store = store
        self._account_id = account_id

    def execute(self, statement: str, parameters=()):
        store = self._store
        if store is None:
            raise RuntimeError('runtime lease is closed')
        with store._connect() as connection:
            return connection.execute(statement, parameters).fetchone()

    def close(self) -> None:
        store = self._store
        if store is None:
            return
        self._store = None
        with store._runtime_lease_lock:
            store._runtime_leases.discard(self._account_id)


class GatewayStore:
    def __init__(self, dsn: str):
        self._dsn = dsn
        self._sqlite = dsn.startswith('sqlite:')
        self._sqlite_path = _sqlite_path(dsn) if self._sqlite else ''
        self._runtime_lease_lock = threading.Lock()
        self._runtime_leases: set[str] = set()

    def _connect(self):
        if self._sqlite:
            return _SQLiteConnection(self._sqlite_path)
        return psycopg.connect(self._dsn, row_factory=dict_row)

    def initialize(self) -> None:
        if self._sqlite:
            self._initialize_sqlite()
            return
        statements = (
            """
            CREATE TABLE IF NOT EXISTS channel_accounts (
                id TEXT PRIMARY KEY,
                owner_user_id TEXT NOT NULL,
                provider VARCHAR(32) NOT NULL,
                external_id_hash VARCHAR(64) NOT NULL,
                label TEXT NOT NULL,
                status VARCHAR(32) NOT NULL,
                runtime_status VARCHAR(32) NOT NULL DEFAULT 'stopped',
                last_poll_at TIMESTAMPTZ,
                last_message_at TIMESTAMPTZ,
                last_error TEXT,
                credentials_ciphertext TEXT NOT NULL,
                welcome_pending BOOLEAN NOT NULL DEFAULT FALSE,
                connected_at TIMESTAMPTZ,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                UNIQUE (owner_user_id, provider, external_id_hash)
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_connection_sessions (
                id TEXT PRIMARY KEY,
                owner_user_id TEXT NOT NULL,
                provider VARCHAR(32) NOT NULL,
                account_id TEXT REFERENCES channel_accounts(id),
                idempotency_key TEXT,
                status VARCHAR(32) NOT NULL,
                revision INTEGER NOT NULL DEFAULT 1,
                qr_version INTEGER NOT NULL DEFAULT 1,
                message TEXT NOT NULL,
                provider_state_ciphertext TEXT,
                expires_at TIMESTAMPTZ NOT NULL,
                error_code TEXT,
                error_message TEXT,
                error_retryable BOOLEAN,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE UNIQUE INDEX IF NOT EXISTS channel_connection_sessions_idempotency_idx
            ON channel_connection_sessions(owner_user_id, provider, idempotency_key)
            WHERE idempotency_key IS NOT NULL
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_connection_sessions_owner_idx
            ON channel_connection_sessions(owner_user_id, provider, updated_at DESC)
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_accounts_owner_idx
            ON channel_accounts(owner_user_id, provider, updated_at DESC)
            """,
            """
            ALTER TABLE channel_accounts
            ADD COLUMN IF NOT EXISTS runtime_status VARCHAR(32) NOT NULL DEFAULT 'stopped'
            """,
            """
            ALTER TABLE channel_accounts
            ADD COLUMN IF NOT EXISTS last_poll_at TIMESTAMPTZ
            """,
            """
            ALTER TABLE channel_accounts
            ADD COLUMN IF NOT EXISTS last_message_at TIMESTAMPTZ
            """,
            """
            ALTER TABLE channel_accounts
            ADD COLUMN IF NOT EXISTS last_error TEXT
            """,
            """
            ALTER TABLE channel_accounts
            ADD COLUMN IF NOT EXISTS welcome_pending BOOLEAN NOT NULL DEFAULT FALSE
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_checkpoints (
                account_id TEXT PRIMARY KEY REFERENCES channel_accounts(id) ON DELETE CASCADE,
                cursor TEXT NOT NULL DEFAULT '',
                longpoll_timeout_ms INTEGER NOT NULL DEFAULT 35000,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_routes (
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                external_address_hash VARCHAR(64) NOT NULL,
                conversation_id TEXT NOT NULL,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, external_address_hash)
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_navigation_states (
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                external_address_hash VARCHAR(64) NOT NULL,
                mode VARCHAR(32) NOT NULL DEFAULT 'active',
                snapshot_json JSONB NOT NULL DEFAULT '{}'::jsonb,
                snapshot_expires_at TIMESTAMPTZ,
                history_conversation_id TEXT,
                history_next_page_token TEXT,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, external_address_hash),
                CHECK (mode IN ('active', 'new_pending'))
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_processed_messages (
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                message_key VARCHAR(64) NOT NULL,
                status VARCHAR(32) NOT NULL,
                response_text TEXT,
                response_media_ciphertext TEXT,
                intent_kind VARCHAR(32),
                processed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, message_key)
            )
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS response_text TEXT
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS response_media_ciphertext TEXT
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS intent_kind VARCHAR(32)
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS response_to_user_id TEXT
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS response_context_token TEXT
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS claim_owner TEXT
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS reply_attempt_count INTEGER NOT NULL DEFAULT 0
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS reply_last_error TEXT
            """,
            """
            ALTER TABLE channel_processed_messages
            ADD COLUMN IF NOT EXISTS reply_next_attempt_at TIMESTAMPTZ
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_processed_messages_time_idx
            ON channel_processed_messages(processed_at)
            """,
        )
        with self._connect() as connection:
            for statement in statements:
                connection.execute(statement)

    def _initialize_sqlite(self) -> None:
        statements = (
            """
            CREATE TABLE IF NOT EXISTS channel_accounts (
                id TEXT PRIMARY KEY,
                owner_user_id TEXT NOT NULL,
                provider VARCHAR(32) NOT NULL,
                external_id_hash VARCHAR(64) NOT NULL,
                label TEXT NOT NULL,
                status VARCHAR(32) NOT NULL,
                runtime_status VARCHAR(32) NOT NULL DEFAULT 'stopped',
                last_poll_at TIMESTAMPTZ,
                last_message_at TIMESTAMPTZ,
                last_error TEXT,
                credentials_ciphertext TEXT NOT NULL,
                welcome_pending BOOLEAN NOT NULL DEFAULT FALSE,
                connected_at TIMESTAMPTZ,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                UNIQUE (owner_user_id, provider, external_id_hash)
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_connection_sessions (
                id TEXT PRIMARY KEY,
                owner_user_id TEXT NOT NULL,
                provider VARCHAR(32) NOT NULL,
                account_id TEXT REFERENCES channel_accounts(id),
                idempotency_key TEXT,
                status VARCHAR(32) NOT NULL,
                revision INTEGER NOT NULL DEFAULT 1,
                qr_version INTEGER NOT NULL DEFAULT 1,
                message TEXT NOT NULL,
                provider_state_ciphertext TEXT,
                expires_at TIMESTAMPTZ NOT NULL,
                error_code TEXT,
                error_message TEXT,
                error_retryable BOOLEAN,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE UNIQUE INDEX IF NOT EXISTS channel_connection_sessions_idempotency_idx
            ON channel_connection_sessions(owner_user_id, provider, idempotency_key)
            WHERE idempotency_key IS NOT NULL
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_connection_sessions_owner_idx
            ON channel_connection_sessions(owner_user_id, provider, updated_at DESC)
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_accounts_owner_idx
            ON channel_accounts(owner_user_id, provider, updated_at DESC)
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_checkpoints (
                account_id TEXT PRIMARY KEY REFERENCES channel_accounts(id) ON DELETE CASCADE,
                cursor TEXT NOT NULL DEFAULT '',
                longpoll_timeout_ms INTEGER NOT NULL DEFAULT 35000,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_routes (
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                external_address_hash VARCHAR(64) NOT NULL,
                conversation_id TEXT NOT NULL,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, external_address_hash)
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_navigation_states (
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                external_address_hash VARCHAR(64) NOT NULL,
                mode VARCHAR(32) NOT NULL DEFAULT 'active',
                snapshot_json TEXT NOT NULL DEFAULT '{}',
                snapshot_expires_at TIMESTAMPTZ,
                history_conversation_id TEXT,
                history_next_page_token TEXT,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, external_address_hash),
                CHECK (mode IN ('active', 'new_pending'))
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_processed_messages (
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                message_key VARCHAR(64) NOT NULL,
                status VARCHAR(32) NOT NULL,
                response_text TEXT,
                response_media_ciphertext TEXT,
                intent_kind VARCHAR(32),
                response_to_user_id TEXT,
                response_context_token TEXT,
                claim_owner TEXT,
                reply_attempt_count INTEGER NOT NULL DEFAULT 0,
                reply_last_error TEXT,
                reply_next_attempt_at TIMESTAMPTZ,
                processed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, message_key)
            )
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_processed_messages_time_idx
            ON channel_processed_messages(processed_at)
            """,
        )
        with self._connect() as connection:
            for statement in statements:
                connection.execute(statement)

    def ping(self) -> None:
        with self._connect() as connection:
            connection.execute('SELECT 1').fetchone()

    def reserve_session(
        self,
        *,
        session_id: str,
        owner_user_id: str,
        provider: str,
        idempotency_key: str | None,
        expires_at: dt.datetime,
    ) -> tuple[dict[str, Any], bool]:
        with self._connect() as connection:
            if self._sqlite:
                connection.execute('BEGIN IMMEDIATE')
            else:
                connection.execute(
                    'SELECT pg_advisory_xact_lock(hashtext(%s))',
                    (f'{owner_user_id}:{provider}',),
                )
            if idempotency_key:
                existing = connection.execute(
                    """
                    SELECT * FROM channel_connection_sessions
                    WHERE owner_user_id = %s AND provider = %s AND idempotency_key = %s
                    """,
                    (owner_user_id, provider, idempotency_key),
                ).fetchone()
                if existing:
                    return existing, False
            active = connection.execute(
                """
                SELECT * FROM channel_connection_sessions
                WHERE owner_user_id = %s AND provider = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                ORDER BY created_at DESC
                LIMIT 1
                """,
                (owner_user_id, provider),
            ).fetchone()
            if active:
                return active, False
            row = connection.execute(
                """
                INSERT INTO channel_connection_sessions(
                    id, owner_user_id, provider, idempotency_key,
                    status, revision, qr_version, message, expires_at
                )
                VALUES(%s, %s, %s, %s, 'preparing', 1, 1, %s, %s)
                RETURNING *
                """,
                (
                    session_id,
                    owner_user_id,
                    provider,
                    idempotency_key,
                    '正在生成二维码',
                    expires_at,
                ),
            ).fetchone()
            return row, True

    def set_qr_ready(
        self,
        session_id: str,
        state_ciphertext: str,
        expires_at: dt.datetime,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = 'waiting_scan',
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = %s,
                    expires_at = %s,
                    error_code = NULL,
                    error_message = NULL,
                    error_retryable = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND status = 'preparing'
                RETURNING *
                """,
                ('请使用微信扫码并在手机上确认', state_ciphertext, expires_at, session_id),
            ).fetchone()

    def get_session(self, owner_user_id: str, session_id: str) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                SELECT * FROM channel_connection_sessions
                WHERE id = %s AND owner_user_id = %s
                """,
                (session_id, owner_user_id),
            ).fetchone()

    def get_session_internal(self, session_id: str) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                'SELECT * FROM channel_connection_sessions WHERE id = %s',
                (session_id,),
            ).fetchone()

    def update_active_session(
        self,
        *,
        session_id: str,
        qr_version: int,
        status: str,
        message: str,
        state_ciphertext: str,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = %s,
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND qr_version = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                RETURNING *
                """,
                (status, message, state_ciphertext, session_id, qr_version),
            ).fetchone()

    def mark_expired(self, session_id: str, qr_version: int) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = 'expired',
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND qr_version = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                RETURNING *
                """,
                ('二维码已过期，请刷新后重试', session_id, qr_version),
            ).fetchone()

    def mark_failed(
        self,
        session_id: str,
        qr_version: int,
        *,
        code: str,
        message: str,
        retryable: bool,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = 'failed',
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = NULL,
                    error_code = %s,
                    error_message = %s,
                    error_retryable = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND qr_version = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                RETURNING *
                """,
                (message, code, message, retryable, session_id, qr_version),
            ).fetchone()

    def refresh_session(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        state_ciphertext: str,
        expires_at: dt.datetime,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = 'waiting_scan',
                    revision = revision + 1,
                    qr_version = qr_version + 1,
                    message = %s,
                    provider_state_ciphertext = %s,
                    expires_at = %s,
                    error_code = NULL,
                    error_message = NULL,
                    error_retryable = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND owner_user_id = %s
                  AND (
                      status = 'expired'
                      OR (status = 'failed' AND error_retryable = TRUE)
                  )
                RETURNING *
                """,
                (
                    '请使用微信扫码并在手机上确认',
                    state_ciphertext,
                    expires_at,
                    session_id,
                    owner_user_id,
                ),
            ).fetchone()

    def cancel_session(self, owner_user_id: str, session_id: str) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = 'canceled',
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND owner_user_id = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                RETURNING *
                """,
                ('连接已取消', session_id, owner_user_id),
            ).fetchone()

    def save_connected_account(
        self,
        *,
        session_id: str,
        qr_version: int,
        owner_user_id: str,
        provider: str,
        external_id_hash: str,
        label: str,
        credentials_ciphertext: str,
    ) -> dict[str, Any] | None:
        account_id = f'ca_{uuid.uuid4().hex}'
        with self._connect() as connection:
            active_session = connection.execute(
                """
                SELECT id FROM channel_connection_sessions
                WHERE id = %s
                  AND qr_version = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                FOR UPDATE
                """,
                (session_id, qr_version),
            ).fetchone()
            if not active_session:
                return None
            account = connection.execute(
                """
                INSERT INTO channel_accounts(
                    id, owner_user_id, provider, external_id_hash, label,
                    status, credentials_ciphertext, welcome_pending, connected_at
                )
                VALUES(%s, %s, %s, %s, %s, 'connected', %s, TRUE, CURRENT_TIMESTAMP)
                ON CONFLICT(owner_user_id, provider, external_id_hash)
                DO UPDATE SET
                    label = EXCLUDED.label,
                    status = 'connected',
                    credentials_ciphertext = EXCLUDED.credentials_ciphertext,
                    connected_at = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                RETURNING *
                """,
                (
                    account_id,
                    owner_user_id,
                    provider,
                    external_id_hash,
                    label,
                    credentials_ciphertext,
                ),
            ).fetchone()
            updated = connection.execute(
                """
                UPDATE channel_connection_sessions
                SET account_id = %s,
                    status = 'connected',
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = NULL,
                    error_code = NULL,
                    error_message = NULL,
                    error_retryable = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND qr_version = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                RETURNING id
                """,
                (account['id'], '微信连接成功', session_id, qr_version),
            ).fetchone()
            return account if updated else None

    def mark_welcome_sent(self, account_id: str) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_accounts
                SET welcome_pending = FALSE,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND welcome_pending = TRUE
                RETURNING id
                """,
                (account_id,),
            ).fetchone()
            return row is not None

    def get_account(self, owner_user_id: str, account_id: str) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                SELECT * FROM channel_accounts
                WHERE id = %s AND owner_user_id = %s
                """,
                (account_id, owner_user_id),
            ).fetchone()

    def list_accounts(self, owner_user_id: str, provider: str) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT * FROM channel_accounts
                    WHERE owner_user_id = %s AND provider = %s
                    ORDER BY updated_at DESC
                    """,
                    (owner_user_id, provider),
                ).fetchall()
            )

    def delete_account(self, owner_user_id: str, account_id: str) -> bool:
        with self._connect() as connection:
            account = connection.execute(
                """
                SELECT id FROM channel_accounts
                WHERE id = %s AND owner_user_id = %s
                FOR UPDATE
                """,
                (account_id, owner_user_id),
            ).fetchone()
            if not account:
                return False
            connection.execute(
                """
                UPDATE channel_connection_sessions
                SET account_id = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE account_id = %s
                """,
                (account_id,),
            )
            deleted = connection.execute(
                """
                DELETE FROM channel_accounts
                WHERE id = %s AND owner_user_id = %s
                RETURNING id
                """,
                (account_id, owner_user_id),
            ).fetchone()
            return deleted is not None

    def recoverable_sessions(self) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT * FROM channel_connection_sessions
                    WHERE status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                    ORDER BY created_at
                    """
                ).fetchall()
            )

    def connected_accounts(self) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT * FROM channel_accounts
                    WHERE status = 'connected'
                    ORDER BY created_at
                    """
                ).fetchall()
            )

    def get_account_internal(self, account_id: str) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                'SELECT * FROM channel_accounts WHERE id = %s',
                (account_id,),
            ).fetchone()

    def update_account_credentials(
        self,
        account_id: str,
        credentials_ciphertext: str,
    ) -> None:
        with self._connect() as connection:
            connection.execute(
                """
                UPDATE channel_accounts
                SET credentials_ciphertext = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (credentials_ciphertext, account_id),
            )

    def acquire_runtime_lease(self, account_id: str):
        if self._sqlite:
            with self._runtime_lease_lock:
                if account_id in self._runtime_leases:
                    return None
                self._runtime_leases.add(account_id)
            return _SQLiteLease(self, account_id)
        connection = psycopg.connect(self._dsn, row_factory=dict_row, autocommit=True)
        row = connection.execute(
            'SELECT pg_try_advisory_lock(hashtext(%s), hashtext(%s)) AS acquired',
            ('channel-gateway-runtime', account_id),
        ).fetchone()
        if not row or not row['acquired']:
            connection.close()
            return None
        return connection

    @staticmethod
    def release_runtime_lease(connection) -> None:
        connection.close()

    def get_checkpoint(self, account_id: str) -> dict[str, Any]:
        with self._connect() as connection:
            row = connection.execute(
                'SELECT * FROM channel_checkpoints WHERE account_id = %s',
                (account_id,),
            ).fetchone()
            if row:
                return row
            return connection.execute(
                """
                INSERT INTO channel_checkpoints(account_id)
                VALUES(%s)
                ON CONFLICT(account_id) DO UPDATE SET account_id = EXCLUDED.account_id
                RETURNING *
                """,
                (account_id,),
            ).fetchone()

    def save_checkpoint(self, account_id: str, cursor: str, timeout_ms: int) -> None:
        with self._connect() as connection:
            connection.execute(
                """
                INSERT INTO channel_checkpoints(account_id, cursor, longpoll_timeout_ms)
                VALUES(%s, %s, %s)
                ON CONFLICT(account_id) DO UPDATE SET
                    cursor = EXCLUDED.cursor,
                    longpoll_timeout_ms = EXCLUDED.longpoll_timeout_ms,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (account_id, cursor, timeout_ms),
            )
            connection.execute(
                """
                UPDATE channel_accounts
                SET runtime_status = 'running',
                    last_poll_at = CURRENT_TIMESTAMP,
                    last_error = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (account_id,),
            )

    def claim_message(
        self,
        account_id: str,
        message_key: str,
        claim_owner: str,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                INSERT INTO channel_processed_messages(
                    account_id, message_key, status, claim_owner
                )
                VALUES(%s, %s, 'processing', %s)
                ON CONFLICT(account_id, message_key) DO UPDATE SET
                    claim_owner = EXCLUDED.claim_owner,
                    processed_at = CURRENT_TIMESTAMP
                WHERE channel_processed_messages.status = 'processing'
                  AND channel_processed_messages.claim_owner
                      IS DISTINCT FROM EXCLUDED.claim_owner
                RETURNING message_key
                """,
                (account_id, message_key, claim_owner),
            ).fetchone()
            return row is not None

    def get_pending_reply(
        self,
        account_id: str,
        message_key: str,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT response_text, intent_kind,
                       response_to_user_id, response_context_token
                FROM channel_processed_messages
                WHERE account_id = %s
                  AND message_key = %s
                  AND status = 'reply_pending'
                  AND (
                      reply_next_attempt_at IS NULL
                      OR reply_next_attempt_at <= CURRENT_TIMESTAMP
                  )
                  AND response_text IS NOT NULL
                """,
                (account_id, message_key),
            ).fetchone()
            return dict(row) if row else None

    def save_pending_reply(
        self,
        account_id: str,
        message_key: str,
        claim_owner: str,
        response_text: str,
        intent_kind: str,
        to_user_id: str,
        context_token: str,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_processed_messages
                SET status = 'reply_pending',
                    response_text = %s,
                    response_media_ciphertext = NULL,
                    intent_kind = %s,
                    response_to_user_id = %s,
                    response_context_token = %s,
                    reply_attempt_count = 0,
                    reply_last_error = NULL,
                    reply_next_attempt_at = NULL,
                    processed_at = CURRENT_TIMESTAMP
                WHERE account_id = %s
                  AND message_key = %s
                  AND status = 'processing'
                  AND claim_owner = %s
                RETURNING message_key
                """,
                (
                    response_text,
                    intent_kind,
                    to_user_id,
                    context_token,
                    account_id,
                    message_key,
                    claim_owner,
                ),
            ).fetchone()
            return row is not None

    def get_reply_media(
        self,
        account_id: str,
        message_key: str,
    ) -> str:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT response_media_ciphertext
                FROM channel_processed_messages
                WHERE account_id = %s
                  AND message_key = %s
                  AND status = 'reply_pending'
                """,
                (account_id, message_key),
            ).fetchone()
            return str(row['response_media_ciphertext'] or '') if row else ''

    def save_reply_media(
        self,
        account_id: str,
        message_key: str,
        ciphertext: str,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_processed_messages
                SET response_media_ciphertext = %s,
                    processed_at = CURRENT_TIMESTAMP
                WHERE account_id = %s
                  AND message_key = %s
                  AND status = 'reply_pending'
                RETURNING message_key
                """,
                (ciphertext, account_id, message_key),
            ).fetchone()
            return row is not None

    def pending_replies(
        self,
        account_id: str,
        limit: int = 20,
    ) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT message_key, response_text, intent_kind,
                           response_to_user_id, response_context_token,
                           reply_attempt_count
                    FROM channel_processed_messages
                    WHERE account_id = %s
                      AND status = 'reply_pending'
                      AND (
                          reply_next_attempt_at IS NULL
                          OR reply_next_attempt_at <= CURRENT_TIMESTAMP
                      )
                      AND response_text IS NOT NULL
                      AND response_to_user_id IS NOT NULL
                      AND response_context_token IS NOT NULL
                    ORDER BY processed_at
                    LIMIT %s
                    """,
                    (account_id, limit),
                ).fetchall()
            )

    def record_reply_failure(
        self,
        account_id: str,
        message_key: str,
        error: str,
        *,
        max_attempts: int = 5,
    ) -> None:
        if self._sqlite:
            with self._connect() as connection:
                row = connection.execute(
                    """
                    SELECT reply_attempt_count
                    FROM channel_processed_messages
                    WHERE account_id = %s
                      AND message_key = %s
                      AND status = 'reply_pending'
                    """,
                    (account_id, message_key),
                ).fetchone()
                if not row:
                    return
                attempts = int(row['reply_attempt_count'] or 0) + 1
                next_attempt = None
                status = 'reply_dead_letter'
                if attempts < max_attempts:
                    status = 'reply_pending'
                    next_attempt = dt.datetime.now(dt.timezone.utc) + dt.timedelta(
                        seconds=min(300, 2 ** attempts),
                    )
                connection.execute(
                    """
                    UPDATE channel_processed_messages
                    SET reply_attempt_count = %s,
                        reply_last_error = %s,
                        reply_next_attempt_at = %s,
                        status = %s,
                        processed_at = CURRENT_TIMESTAMP
                    WHERE account_id = %s
                      AND message_key = %s
                      AND status = 'reply_pending'
                    """,
                    (
                        attempts,
                        error[:500],
                        next_attempt,
                        status,
                        account_id,
                        message_key,
                    ),
                )
            return
        with self._connect() as connection:
            connection.execute(
                """
                UPDATE channel_processed_messages
                SET reply_attempt_count = reply_attempt_count + 1,
                    reply_last_error = %s,
                    reply_next_attempt_at = CASE
                        WHEN reply_attempt_count + 1 >= %s THEN NULL
                        ELSE CURRENT_TIMESTAMP
                            + make_interval(
                                secs => LEAST(
                                    300,
                                    CAST(power(2, reply_attempt_count + 1) AS INTEGER)
                                )
                            )
                    END,
                    status = CASE
                        WHEN reply_attempt_count + 1 >= %s
                            THEN 'reply_dead_letter'
                        ELSE 'reply_pending'
                    END,
                    processed_at = CURRENT_TIMESTAMP
                WHERE account_id = %s
                  AND message_key = %s
                  AND status = 'reply_pending'
                """,
                (
                    error[:500],
                    max_attempts,
                    max_attempts,
                    account_id,
                    message_key,
                ),
            )

    def mark_message_processed(
        self,
        account_id: str,
        message_key: str,
        status: str,
        *,
        claim_owner: str | None = None,
    ) -> bool:
        with self._connect() as connection:
            if claim_owner is None:
                row = connection.execute(
                    """
                    INSERT INTO channel_processed_messages(account_id, message_key, status)
                    VALUES(%s, %s, %s)
                    ON CONFLICT(account_id, message_key) DO UPDATE SET
                        status = EXCLUDED.status,
                        response_text = NULL,
                        response_media_ciphertext = NULL,
                        intent_kind = NULL,
                        response_to_user_id = NULL,
                        response_context_token = NULL,
                        claim_owner = NULL,
                        reply_attempt_count = 0,
                        reply_last_error = NULL,
                        reply_next_attempt_at = NULL,
                        processed_at = CURRENT_TIMESTAMP
                    RETURNING message_key
                    """,
                    (account_id, message_key, status),
                ).fetchone()
            else:
                row = connection.execute(
                    """
                    UPDATE channel_processed_messages
                    SET status = %s,
                        response_text = NULL,
                        response_media_ciphertext = NULL,
                        intent_kind = NULL,
                        response_to_user_id = NULL,
                        response_context_token = NULL,
                        claim_owner = NULL,
                        reply_attempt_count = 0,
                        reply_last_error = NULL,
                        reply_next_attempt_at = NULL,
                        processed_at = CURRENT_TIMESTAMP
                    WHERE account_id = %s
                      AND message_key = %s
                      AND status = 'reply_pending'
                      AND claim_owner = %s
                    RETURNING message_key
                    """,
                    (status, account_id, message_key, claim_owner),
                ).fetchone()
            connection.execute(
                """
                UPDATE channel_accounts
                SET last_message_at = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (account_id,),
            )
            return row is not None

    def get_route(self, account_id: str, external_address_hash: str) -> str:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT conversation_id FROM channel_routes
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            return str(row['conversation_id']) if row else ''

    def get_navigation_state(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT * FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            return dict(row) if row else None

    def begin_new_conversation(
        self,
        account_id: str,
        external_address_hash: str,
        draft: dict[str, Any] | None = None,
    ) -> None:
        if self._sqlite:
            with self._connect() as connection:
                row = connection.execute(
                    """
                    SELECT snapshot_json FROM channel_navigation_states
                    WHERE account_id = %s AND external_address_hash = %s
                    """,
                    (account_id, external_address_hash),
                ).fetchone()
                existing = _snapshot_dict(row.get('snapshot_json')) if row else {}
                snapshot = {'new_conversation': draft or {}}
                if isinstance(existing.get('pending_turn'), dict):
                    snapshot['pending_turn'] = existing['pending_turn']
                connection.execute(
                    """
                    DELETE FROM channel_routes
                    WHERE account_id = %s AND external_address_hash = %s
                    """,
                    (account_id, external_address_hash),
                )
                connection.execute(
                    """
                    INSERT INTO channel_navigation_states(
                        account_id, external_address_hash, mode,
                        snapshot_json, snapshot_expires_at,
                        history_conversation_id, history_next_page_token
                    )
                    VALUES(%s, %s, 'new_pending', %s, NULL, NULL, NULL)
                    ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                        mode = 'new_pending',
                        snapshot_json = EXCLUDED.snapshot_json,
                        snapshot_expires_at = NULL,
                        history_conversation_id = NULL,
                        history_next_page_token = NULL,
                        updated_at = CURRENT_TIMESTAMP
                    """,
                    (
                        account_id,
                        external_address_hash,
                        json.dumps(snapshot, ensure_ascii=False, separators=(',', ':')),
                    ),
                )
            return
        draft_json = json.dumps(
            draft or {},
            ensure_ascii=False,
            separators=(',', ':'),
        )
        with self._connect() as connection:
            connection.execute(
                """
                DELETE FROM channel_routes
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            )
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode,
                    snapshot_json, snapshot_expires_at,
                    history_conversation_id, history_next_page_token
                )
                VALUES(%s, %s, 'new_pending', %s::jsonb, NULL, NULL, NULL)
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    mode = 'new_pending',
                    snapshot_json = jsonb_build_object(
                        'new_conversation',
                        %s::jsonb
                    ) || CASE
                        WHEN jsonb_typeof(channel_navigation_states.snapshot_json) = 'object'
                          AND channel_navigation_states.snapshot_json ? 'pending_turn'
                            THEN jsonb_build_object(
                                'pending_turn',
                                channel_navigation_states.snapshot_json->'pending_turn'
                            )
                        ELSE '{}'::jsonb
                    END,
                    snapshot_expires_at = NULL,
                    history_conversation_id = NULL,
                    history_next_page_token = NULL,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (
                    account_id,
                    external_address_hash,
                    json.dumps(
                        {'new_conversation': draft or {}},
                        ensure_ascii=False,
                        separators=(',', ':'),
                    ),
                    draft_json,
                ),
            )

    def activate_conversation(
        self,
        account_id: str,
        external_address_hash: str,
        conversation_id: str,
        history_next_page_token: str | None = None,
        *,
        consume_pending_turn: bool = False,
    ) -> None:
        history_conversation_id = (
            conversation_id
            if history_next_page_token is not None
            else None
        )
        history_token = history_next_page_token or None
        if self._sqlite:
            with self._connect() as connection:
                row = connection.execute(
                    """
                    SELECT snapshot_json FROM channel_navigation_states
                    WHERE account_id = %s AND external_address_hash = %s
                    """,
                    (account_id, external_address_hash),
                ).fetchone()
                snapshot = _snapshot_dict(row.get('snapshot_json')) if row else {}
                snapshot.pop('selection', None)
                snapshot.pop('new_conversation', None)
                if consume_pending_turn:
                    snapshot.pop('pending_turn', None)
                connection.execute(
                    """
                    INSERT INTO channel_routes(account_id, external_address_hash, conversation_id)
                    VALUES(%s, %s, %s)
                    ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                        conversation_id = EXCLUDED.conversation_id,
                        updated_at = CURRENT_TIMESTAMP
                    """,
                    (account_id, external_address_hash, conversation_id),
                )
                connection.execute(
                    """
                    INSERT INTO channel_navigation_states(
                        account_id, external_address_hash, mode, snapshot_json,
                        history_conversation_id, history_next_page_token
                    )
                    VALUES(%s, %s, 'active', %s, %s, %s)
                    ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                        mode = 'active',
                        snapshot_json = EXCLUDED.snapshot_json,
                        snapshot_expires_at = NULL,
                        history_conversation_id = EXCLUDED.history_conversation_id,
                        history_next_page_token = EXCLUDED.history_next_page_token,
                        updated_at = CURRENT_TIMESTAMP
                    """,
                    (
                        account_id,
                        external_address_hash,
                        json.dumps(snapshot, ensure_ascii=False, separators=(',', ':')),
                        history_conversation_id,
                        history_token,
                    ),
                )
            return
        with self._connect() as connection:
            connection.execute(
                """
                INSERT INTO channel_routes(account_id, external_address_hash, conversation_id)
                VALUES(%s, %s, %s)
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    conversation_id = EXCLUDED.conversation_id,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (account_id, external_address_hash, conversation_id),
            )
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode,
                    history_conversation_id, history_next_page_token
                )
                VALUES(%s, %s, 'active', %s, %s)
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    mode = 'active',
                    snapshot_json = CASE
                        WHEN jsonb_typeof(channel_navigation_states.snapshot_json) = 'object'
                            THEN CASE WHEN %s
                                THEN channel_navigation_states.snapshot_json
                                    - 'selection'
                                    - 'new_conversation'
                                    - 'pending_turn'
                                ELSE channel_navigation_states.snapshot_json
                                    - 'selection'
                                    - 'new_conversation'
                            END
                        ELSE '{}'::jsonb
                    END,
                    snapshot_expires_at = NULL,
                    history_conversation_id = EXCLUDED.history_conversation_id,
                    history_next_page_token = EXCLUDED.history_next_page_token,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (
                    account_id,
                    external_address_hash,
                    history_conversation_id,
                    history_token,
                    consume_pending_turn,
                ),
            )

    def save_selection_snapshot(
        self,
        account_id: str,
        external_address_hash: str,
        kind: str,
        items: list[dict[str, Any]],
        expires_at: dt.datetime,
        continuation: dict[str, Any] | None = None,
    ) -> None:
        selection = {'kind': kind, 'items': items}
        if continuation:
            selection['continuation'] = continuation
        selection_json = json.dumps(
            selection,
            ensure_ascii=False,
            separators=(',', ':'),
        )
        if self._sqlite:
            with self._connect() as connection:
                row = connection.execute(
                    """
                    SELECT snapshot_json FROM channel_navigation_states
                    WHERE account_id = %s AND external_address_hash = %s
                    """,
                    (account_id, external_address_hash),
                ).fetchone()
                raw_snapshot = row.get('snapshot_json') if row else None
                if isinstance(raw_snapshot, str):
                    try:
                        raw_snapshot = json.loads(raw_snapshot)
                    except json.JSONDecodeError:
                        raw_snapshot = {}
                if isinstance(raw_snapshot, list):
                    snapshot = {
                        'selection': {
                            'kind': 'conversation',
                            'items': raw_snapshot,
                        },
                    }
                else:
                    snapshot = dict(raw_snapshot) if isinstance(raw_snapshot, dict) else {}
                snapshot['selection'] = selection
                connection.execute(
                    """
                    INSERT INTO channel_navigation_states(
                        account_id, external_address_hash, mode,
                        snapshot_json, snapshot_expires_at
                    )
                    VALUES(%s, %s, 'active', %s, %s)
                    ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                        snapshot_json = EXCLUDED.snapshot_json,
                        snapshot_expires_at = EXCLUDED.snapshot_expires_at,
                        updated_at = CURRENT_TIMESTAMP
                    """,
                    (
                        account_id,
                        external_address_hash,
                        json.dumps(snapshot, ensure_ascii=False, separators=(',', ':')),
                        expires_at,
                    ),
                )
            return
        with self._connect() as connection:
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode,
                    snapshot_json, snapshot_expires_at
                )
                VALUES(
                    %s, %s, 'active',
                    jsonb_build_object('selection', %s::jsonb),
                    %s
                )
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    snapshot_json = jsonb_set(
                        CASE
                            WHEN jsonb_typeof(channel_navigation_states.snapshot_json) = 'object'
                                THEN channel_navigation_states.snapshot_json
                            WHEN jsonb_typeof(channel_navigation_states.snapshot_json) = 'array'
                                THEN jsonb_build_object(
                                    'selection',
                                    jsonb_build_object(
                                        'kind', 'conversation',
                                        'items', channel_navigation_states.snapshot_json
                                    )
                                )
                            ELSE '{}'::jsonb
                        END,
                        '{selection}',
                        %s::jsonb,
                        true
                    ),
                    snapshot_expires_at = EXCLUDED.snapshot_expires_at,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (
                    account_id,
                    external_address_hash,
                    selection_json,
                    expires_at,
                    selection_json,
                ),
            )

    def get_selection_snapshot(
        self,
        account_id: str,
        external_address_hash: str,
        expected_kind: str | None = None,
    ) -> list[dict[str, Any]] | None:
        selection = self.get_selection_context(account_id, external_address_hash)
        if selection is None:
            return None
        kind = str(selection.get('kind') or '')
        if expected_kind and kind != expected_kind:
            return None
        items = selection.get('items')
        if not isinstance(items, list):
            return None
        return [dict(item) for item in items if isinstance(item, dict)]

    def get_selection_context(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s
                  AND external_address_hash = %s
                  AND snapshot_expires_at > CURRENT_TIMESTAMP
                """,
                (account_id, external_address_hash),
            ).fetchone()
        if not row:
            return None
        snapshot = row.get('snapshot_json')
        if isinstance(snapshot, str):
            snapshot = json.loads(snapshot)
        if isinstance(snapshot, list):
            return {'kind': 'conversation', 'items': snapshot}
        if not isinstance(snapshot, dict):
            return None
        selection = snapshot.get('selection')
        return dict(selection) if isinstance(selection, dict) else None

    def clear_selection_snapshot(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> None:
        if self._sqlite:
            with self._connect() as connection:
                row = connection.execute(
                    """
                    SELECT snapshot_json FROM channel_navigation_states
                    WHERE account_id = %s AND external_address_hash = %s
                    """,
                    (account_id, external_address_hash),
                ).fetchone()
                snapshot = _snapshot_dict(row.get('snapshot_json')) if row else {}
                snapshot.pop('selection', None)
                connection.execute(
                    """
                    UPDATE channel_navigation_states
                    SET snapshot_json = %s,
                        snapshot_expires_at = NULL,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE account_id = %s AND external_address_hash = %s
                    """,
                    (
                        json.dumps(snapshot, ensure_ascii=False, separators=(',', ':')),
                        account_id,
                        external_address_hash,
                    ),
                )
            return
        with self._connect() as connection:
            connection.execute(
                """
                UPDATE channel_navigation_states
                SET snapshot_json = CASE
                        WHEN jsonb_typeof(snapshot_json) = 'object'
                            THEN snapshot_json - 'selection'
                        ELSE '{}'::jsonb
                    END,
                    snapshot_expires_at = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            )

    def save_pending_turn(
        self,
        account_id: str,
        external_address_hash: str,
        options: dict[str, Any],
    ) -> None:
        options_json = json.dumps(
            options,
            ensure_ascii=False,
            separators=(',', ':'),
        )
        if self._sqlite:
            with self._connect() as connection:
                row = connection.execute(
                    """
                    SELECT snapshot_json FROM channel_navigation_states
                    WHERE account_id = %s AND external_address_hash = %s
                    """,
                    (account_id, external_address_hash),
                ).fetchone()
                raw_snapshot = row.get('snapshot_json') if row else None
                if isinstance(raw_snapshot, str):
                    try:
                        raw_snapshot = json.loads(raw_snapshot)
                    except json.JSONDecodeError:
                        raw_snapshot = {}
                if isinstance(raw_snapshot, list):
                    snapshot = {
                        'selection': {
                            'kind': 'conversation',
                            'items': raw_snapshot,
                        },
                    }
                else:
                    snapshot = dict(raw_snapshot) if isinstance(raw_snapshot, dict) else {}
                snapshot['pending_turn'] = options
                connection.execute(
                    """
                    INSERT INTO channel_navigation_states(
                        account_id, external_address_hash, mode, snapshot_json
                    )
                    VALUES(%s, %s, 'active', %s)
                    ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                        snapshot_json = EXCLUDED.snapshot_json,
                        updated_at = CURRENT_TIMESTAMP
                    """,
                    (
                        account_id,
                        external_address_hash,
                        json.dumps(snapshot, ensure_ascii=False, separators=(',', ':')),
                    ),
                )
            return
        with self._connect() as connection:
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode, snapshot_json
                )
                VALUES(
                    %s, %s, 'active',
                    jsonb_build_object('pending_turn', %s::jsonb)
                )
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    snapshot_json = jsonb_set(
                        CASE
                            WHEN jsonb_typeof(channel_navigation_states.snapshot_json) = 'object'
                                THEN channel_navigation_states.snapshot_json
                            WHEN jsonb_typeof(channel_navigation_states.snapshot_json) = 'array'
                                THEN jsonb_build_object(
                                    'selection',
                                    jsonb_build_object(
                                        'kind', 'conversation',
                                        'items', channel_navigation_states.snapshot_json
                                    )
                                )
                            ELSE '{}'::jsonb
                        END,
                        '{pending_turn}',
                        %s::jsonb,
                        true
                    ),
                    updated_at = CURRENT_TIMESTAMP
                """,
                (
                    account_id,
                    external_address_hash,
                    options_json,
                    options_json,
                ),
            )

    def get_pending_turn(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any]:
        state = self.get_navigation_state(account_id, external_address_hash)
        if not state:
            return {}
        snapshot = state.get('snapshot_json')
        if isinstance(snapshot, str):
            snapshot = json.loads(snapshot)
        if not isinstance(snapshot, dict):
            return {}
        pending = snapshot.get('pending_turn')
        return dict(pending) if isinstance(pending, dict) else {}

    def get_new_conversation_draft(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any]:
        state = self.get_navigation_state(account_id, external_address_hash)
        if not state or state.get('mode') != 'new_pending':
            return {}
        snapshot = state.get('snapshot_json')
        if isinstance(snapshot, str):
            snapshot = json.loads(snapshot)
        if not isinstance(snapshot, dict):
            return {}
        draft = snapshot.get('new_conversation')
        return dict(draft) if isinstance(draft, dict) else {}

    def set_history_cursor(
        self,
        account_id: str,
        external_address_hash: str,
        conversation_id: str,
        next_page_token: str,
    ) -> None:
        with self._connect() as connection:
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode,
                    history_conversation_id, history_next_page_token
                )
                VALUES(%s, %s, 'active', %s, %s)
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    mode = 'active',
                    history_conversation_id = EXCLUDED.history_conversation_id,
                    history_next_page_token = EXCLUDED.history_next_page_token,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (
                    account_id,
                    external_address_hash,
                    conversation_id,
                    next_page_token or None,
                ),
            )

    def set_runtime_status(
        self,
        account_id: str,
        status: str,
        error: str | None = None,
    ) -> None:
        with self._connect() as connection:
            connection.execute(
                """
                UPDATE channel_accounts
                SET runtime_status = %s,
                    last_error = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (status, error, account_id),
            )
