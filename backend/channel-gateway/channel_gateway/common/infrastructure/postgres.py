import datetime as dt
import json
import re
import uuid
from typing import Any

import psycopg
from psycopg.rows import dict_row

from channel_gateway.common.errors import RuntimeLeaseLostError
from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
    InboundEnvelope,
    OutboundMessage,
    ReceiverCheckpoint,
    RuntimeFence,
)


_JSON_NUL_ESCAPE = re.compile(r'(?<!\\)((?:\\\\)*)\\u0000')


def _snapshot(value: Any) -> dict[str, Any]:
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except json.JSONDecodeError:
            return {}
    if isinstance(value, list):
        return {
            'selection': {
                'kind': 'conversation',
                'items': list(value),
            }
        }
    return dict(value) if isinstance(value, dict) else {}


class PostgresRuntimeLease:
    """Renewable database lease with a generation used to fence stale owners."""

    def __init__(
        self,
        store: 'GatewayStore',
        fence: RuntimeFence,
        lease_seconds: int,
    ):
        self._store = store
        self._fence = fence
        self._lease_seconds = lease_seconds
        self._closed = False

    @property
    def fence(self) -> RuntimeFence:
        return self._fence

    def keepalive(self) -> None:
        if not self._store.renew_runtime_lease(
            self._fence,
            lease_seconds=self._lease_seconds,
        ):
            raise RuntimeLeaseLostError(
                'Channel runtime lease was lost'
            )

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        try:
            self._store.release_runtime_lease(self._fence)
        except Exception:
            # The lease expires on its own; cleanup must not terminate a
            # provider's reconciliation loop during a database outage.
            pass


class GatewayStore:
    def __init__(self, dsn: str):
        self._dsn = dsn

    def _connect(self):
        return psycopg.connect(self._dsn, row_factory=dict_row)

    def initialize(self) -> None:
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
                credential_revision BIGINT NOT NULL DEFAULT 1,
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
            ALTER TABLE channel_connection_sessions
            ADD COLUMN IF NOT EXISTS cleanup_pending BOOLEAN
                NOT NULL DEFAULT FALSE
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
            ALTER TABLE channel_accounts
            ADD COLUMN IF NOT EXISTS credential_revision BIGINT NOT NULL DEFAULT 1
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_runtime_leases (
                lease_key TEXT PRIMARY KEY,
                owner_id TEXT NOT NULL,
                generation BIGINT NOT NULL,
                lease_until TIMESTAMPTZ NOT NULL,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
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
            ADD COLUMN IF NOT EXISTS response_provider_context JSONB
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
            """
            CREATE TABLE IF NOT EXISTS channel_inbox (
                id TEXT PRIMARY KEY,
                ingest_sequence BIGSERIAL UNIQUE,
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                provider VARCHAR(32) NOT NULL,
                message_key VARCHAR(128) NOT NULL,
                order_key VARCHAR(128) NOT NULL,
                external_address_hash VARCHAR(64) NOT NULL,
                owner_user_id TEXT NOT NULL,
                recipient_id TEXT NOT NULL,
                text TEXT NOT NULL,
                provider_context JSONB NOT NULL DEFAULT '{}'::jsonb,
                status VARCHAR(32) NOT NULL DEFAULT 'pending',
                attempt_count INTEGER NOT NULL DEFAULT 0,
                lease_owner TEXT,
                lease_until TIMESTAMPTZ,
                next_attempt_at TIMESTAMPTZ,
                last_error TEXT,
                received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                UNIQUE(account_id, message_key)
            )
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_inbox_claim_idx
            ON channel_inbox(status, next_attempt_at, received_at)
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_inbox_order_idx
            ON channel_inbox(account_id, order_key, ingest_sequence)
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_outbox (
                id TEXT PRIMARY KEY,
                created_sequence BIGSERIAL UNIQUE,
                inbox_id TEXT REFERENCES channel_inbox(id) ON DELETE SET NULL,
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                dedupe_key VARCHAR(160) NOT NULL,
                provider VARCHAR(32) NOT NULL,
                order_key VARCHAR(128) NOT NULL,
                sequence INTEGER NOT NULL DEFAULT 0,
                recipient_id TEXT NOT NULL,
                provider_context JSONB NOT NULL DEFAULT '{}'::jsonb,
                text TEXT NOT NULL,
                intent_kind VARCHAR(64) NOT NULL,
                purpose VARCHAR(32) NOT NULL DEFAULT 'reply',
                metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
                rendered_parts JSONB NOT NULL DEFAULT '[]'::jsonb,
                next_part_index INTEGER NOT NULL DEFAULT 0,
                provider_state JSONB NOT NULL DEFAULT '{}'::jsonb,
                status VARCHAR(32) NOT NULL DEFAULT 'pending',
                attempt_count INTEGER NOT NULL DEFAULT 0,
                lease_owner TEXT,
                lease_until TIMESTAMPTZ,
                next_attempt_at TIMESTAMPTZ,
                last_error TEXT,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE UNIQUE INDEX IF NOT EXISTS channel_outbox_dedupe_idx
            ON channel_outbox(account_id, dedupe_key)
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_outbox_claim_idx
            ON channel_outbox(status, next_attempt_at, created_at)
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_outbox_order_idx
            ON channel_outbox(account_id, order_key, created_sequence)
            """,
            """
            INSERT INTO channel_outbox(
                id, inbox_id, account_id, dedupe_key, provider, order_key,
                sequence, recipient_id, provider_context, text, intent_kind,
                purpose, metadata, status, attempt_count,
                next_attempt_at, last_error
            )
            SELECT
                'co_legacy_' || md5(
                    old.account_id || ':' || old.message_key
                ),
                NULL,
                old.account_id,
                'legacy:' || old.message_key,
                account.provider,
                'legacy:' || old.message_key,
                0,
                old.response_to_user_id,
                COALESCE(
                    old.response_provider_context,
                    CASE
                        WHEN old.response_context_token IS NOT NULL
                        THEN jsonb_build_object(
                            'context_token',
                            old.response_context_token
                        )
                        ELSE '{}'::jsonb
                    END
                ),
                old.response_text,
                COALESCE(old.intent_kind, 'chat'),
                'reply',
                '{}'::jsonb,
                CASE
                    WHEN old.status = 'reply_dead_letter'
                    THEN 'dead'
                    ELSE 'pending'
                END,
                old.reply_attempt_count,
                old.reply_next_attempt_at,
                old.reply_last_error
            FROM channel_processed_messages AS old
            JOIN channel_accounts AS account
              ON account.id = old.account_id
            WHERE old.status IN ('reply_pending', 'reply_dead_letter')
              AND old.response_text IS NOT NULL
              AND old.response_to_user_id IS NOT NULL
            ON CONFLICT(account_id, dedupe_key) DO NOTHING
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
        message: str,
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
                (message, state_ciphertext, expires_at, session_id),
            ).fetchone()

    def restart_connection_session(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        expires_at: dt.datetime,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = 'preparing',
                    revision = revision + 1,
                    qr_version = qr_version + 1,
                    message = '正在生成二维码',
                    provider_state_ciphertext = NULL,
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
                (expires_at, session_id, owner_user_id),
            ).fetchone()

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
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(
                    connection,
                    runtime_fence,
                )
            session = connection.execute(
                """
                UPDATE channel_connection_sessions
                SET account_id = %s,
                    status = 'connected',
                    cleanup_pending = FALSE,
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = NULL,
                    error_code = NULL,
                    error_message = NULL,
                    error_retryable = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND owner_user_id = %s
                  AND qr_version = %s
                  AND status IN (
                      'preparing',
                      'waiting_scan',
                      'scanned',
                      'verification_required',
                      'confirming'
                  )
                RETURNING *
                """,
                (
                    account_id,
                    message,
                    session_id,
                    owner_user_id,
                    qr_version,
                ),
            ).fetchone()
            if session is None:
                return None
            account = connection.execute(
                """
                UPDATE channel_accounts
                SET status = 'connected',
                    connected_at = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND owner_user_id = %s
                  AND status = 'provisioning'
                RETURNING id
                """,
                (account_id, owner_user_id),
            ).fetchone()
            if account is None:
                raise RuntimeError(
                    'Provisioned Feishu account is unavailable'
                )
            return session

    def attach_provisioning_account(
        self,
        *,
        session_id: str,
        qr_version: int,
        owner_user_id: str,
        account_id: str,
        runtime_fence: RuntimeFence | None = None,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(
                    connection,
                    runtime_fence,
                )
            return connection.execute(
                """
                UPDATE channel_connection_sessions AS session
                SET account_id = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE session.id = %s
                  AND session.owner_user_id = %s
                  AND session.qr_version = %s
                  AND session.status = 'confirming'
                  AND EXISTS (
                      SELECT 1
                      FROM channel_accounts AS account
                      WHERE account.id = %s
                        AND account.owner_user_id = %s
                        AND account.provider = 'feishu'
                        AND account.status = 'provisioning'
                  )
                RETURNING session.*
                """,
                (
                    account_id,
                    session_id,
                    owner_user_id,
                    qr_version,
                    account_id,
                    owner_user_id,
                ),
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

    def begin_provisioning_cleanup(
        self,
        session_id: str,
        qr_version: int,
        runtime_fence: RuntimeFence | None = None,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(
                    connection,
                    runtime_fence,
                )
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET cleanup_pending = TRUE,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND qr_version = %s
                  AND status IN (
                      'preparing',
                      'waiting_scan',
                      'scanned',
                      'verification_required',
                      'confirming'
                  )
                RETURNING *
                """,
                (session_id, qr_version),
            ).fetchone()

    def complete_provisioning_cleanup(
        self,
        session_id: str,
        qr_version: int,
        runtime_fence: RuntimeFence | None = None,
    ) -> None:
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(
                    connection,
                    runtime_fence,
                )
            connection.execute(
                """
                UPDATE channel_connection_sessions
                SET cleanup_pending = FALSE,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND qr_version = %s
                """,
                (session_id, qr_version),
            )

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
        with self._connect() as connection:
            return connection.execute(
                """
                UPDATE channel_connection_sessions
                SET status = %s,
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = %s,
                    expires_at = COALESCE(%s, expires_at),
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND qr_version = %s
                  AND revision = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                RETURNING *
                """,
                (
                    status,
                    message,
                    state_ciphertext,
                    expires_at,
                    session_id,
                    qr_version,
                    expected_revision,
                ),
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
        message: str,
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
                    message,
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
        expected_revision: int,
        owner_user_id: str,
        provider: str,
        external_id_hash: str,
        label: str,
        credentials_ciphertext: str,
        conflict_message: str,
        connected_message: str,
    ) -> dict[str, Any] | None:
        account_id = f'ca_{uuid.uuid4().hex}'
        with self._connect() as connection:
            connection.execute(
                'SELECT pg_advisory_xact_lock(hashtext(%s))',
                (f'{provider}:{external_id_hash}',),
            )
            active_session = connection.execute(
                """
                SELECT id FROM channel_connection_sessions
                WHERE id = %s
                  AND qr_version = %s
                  AND revision = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                FOR UPDATE
                """,
                (session_id, qr_version, expected_revision),
            ).fetchone()
            if not active_session:
                return None
            existing_owner = connection.execute(
                """
                SELECT owner_user_id
                FROM channel_accounts
                WHERE provider = %s AND external_id_hash = %s
                LIMIT 1
                """,
                (provider, external_id_hash),
            ).fetchone()
            if (
                existing_owner
                and existing_owner['owner_user_id'] != owner_user_id
            ):
                connection.execute(
                    """
                    UPDATE channel_connection_sessions
                    SET status = 'failed',
                        revision = revision + 1,
                        message = %s,
                        provider_state_ciphertext = NULL,
                        error_code = 'ACCOUNT_ALREADY_BOUND',
                        error_message = %s,
                        error_retryable = FALSE,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE id = %s
                      AND qr_version = %s
                      AND status IN (
                          'preparing',
                          'waiting_scan',
                          'scanned',
                          'verification_required',
                          'confirming'
                      )
                    """,
                    (
                        conflict_message,
                        conflict_message,
                        session_id,
                        qr_version,
                    ),
                )
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
                    credential_revision = (
                        channel_accounts.credential_revision + 1
                    ),
                    connected_at = CASE
                        WHEN EXCLUDED.status = 'connected'
                        THEN CURRENT_TIMESTAMP
                        ELSE channel_accounts.connected_at
                    END,
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
                    cleanup_pending = FALSE,
                    revision = revision + 1,
                    message = %s,
                    provider_state_ciphertext = NULL,
                    error_code = NULL,
                    error_message = NULL,
                    error_retryable = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND qr_version = %s
                  AND revision = %s
                  AND status IN ('preparing', 'waiting_scan', 'scanned', 'verification_required', 'confirming')
                RETURNING id
                """,
                (
                    account['id'],
                    connected_message,
                    session_id,
                    qr_version,
                    expected_revision,
                ),
            ).fetchone()
            return account if updated else None

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
        if status not in {'connected', 'provisioning'}:
            raise ValueError('Unsupported channel account status')
        account_id = f'ca_{uuid.uuid4().hex}'
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(connection, runtime_fence)
            connection.execute(
                'SELECT pg_advisory_xact_lock(hashtext(%s))',
                (f'{provider}:{external_id_hash}',),
            )
            existing_owner = connection.execute(
                """
                SELECT owner_user_id
                FROM channel_accounts
                WHERE provider = %s AND external_id_hash = %s
                LIMIT 1
                """,
                (provider, external_id_hash),
            ).fetchone()
            if (
                existing_owner
                and existing_owner['owner_user_id'] != owner_user_id
            ):
                return None
            return connection.execute(
                """
                INSERT INTO channel_accounts(
                    id, owner_user_id, provider, external_id_hash, label,
                    status, credentials_ciphertext, welcome_pending,
                    connected_at
                )
                VALUES(%s, %s, %s, %s, %s, %s, %s, TRUE, %s)
                ON CONFLICT(owner_user_id, provider, external_id_hash)
                DO UPDATE SET
                    label = EXCLUDED.label,
                    status = EXCLUDED.status,
                    credentials_ciphertext = EXCLUDED.credentials_ciphertext,
                    credential_revision = (
                        channel_accounts.credential_revision + 1
                    ),
                    connected_at = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                WHERE %s
                  AND NOT (
                    channel_accounts.status = 'provisioning'
                    AND EXCLUDED.status = 'connected'
                )
                RETURNING *
                """,
                (
                    account_id,
                    owner_user_id,
                    provider,
                    external_id_hash,
                    label,
                    status,
                    credentials_ciphertext,
                    (
                        dt.datetime.now(dt.timezone.utc)
                        if status == 'connected'
                        else None
                    ),
                    runtime_fence is not None,
                ),
            ).fetchone()

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

    def recoverable_sessions(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT * FROM channel_connection_sessions
                    WHERE provider = %s
                      AND (
                          status IN (
                              'preparing',
                              'waiting_scan',
                              'scanned',
                              'verification_required',
                              'confirming'
                          )
                          OR cleanup_pending = TRUE
                      )
                    ORDER BY created_at
                    """,
                    (provider,),
                ).fetchall()
            )

    def runtime_accounts(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT account.*
                    FROM channel_accounts AS account
                    WHERE account.provider = %s
                      AND (
                          account.status = 'connected'
                          OR (
                              account.status = 'provisioning'
                              AND EXISTS (
                                  SELECT 1
                                  FROM channel_connection_sessions AS session
                                  WHERE session.account_id = account.id
                                    AND session.status = 'confirming'
                                    AND session.expires_at
                                        > CURRENT_TIMESTAMP
                              )
                          )
                      )
                    ORDER BY account.created_at
                    """,
                    (provider,),
                ).fetchall()
            )

    def orphaned_provisioning_accounts(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT account.*,
                           registration_session.id
                               AS registration_session_id
                    FROM channel_accounts AS account
                    LEFT JOIN LATERAL (
                        SELECT session.id
                        FROM channel_connection_sessions AS session
                        WHERE session.account_id = account.id
                        ORDER BY session.updated_at DESC
                        LIMIT 1
                    ) AS registration_session ON TRUE
                    WHERE account.provider = %s
                      AND account.status = 'provisioning'
                      AND account.updated_at
                          <= CURRENT_TIMESTAMP - INTERVAL '60 seconds'
                      AND NOT EXISTS (
                          SELECT 1
                          FROM channel_connection_sessions AS session
                          WHERE session.account_id = account.id
                            AND session.status IN (
                                'preparing',
                                'waiting_scan',
                                'scanned',
                                'verification_required',
                                'confirming'
                            )
                            AND session.expires_at > CURRENT_TIMESTAMP
                      )
                      AND NOT EXISTS (
                          SELECT 1
                          FROM channel_connection_sessions AS session
                          JOIN channel_runtime_leases AS lease
                            ON lease.lease_key = (
                                'feishu-registration:'
                                || session.id
                            )
                           AND lease.lease_until > CURRENT_TIMESTAMP
                          WHERE session.account_id = account.id
                      )
                    ORDER BY account.created_at
                    """,
                    (provider,),
                ).fetchall()
            )

    def claim_orphaned_provisioning_account(
        self,
        *,
        provider: str,
        account_id: str,
        runtime_fence: RuntimeFence,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            self._lock_runtime_fence(connection, runtime_fence)
            row = connection.execute(
                """
                SELECT account.*
                FROM channel_accounts AS account
                WHERE account.id = %s
                  AND account.provider = %s
                  AND account.status = 'provisioning'
                  AND account.updated_at
                      <= CURRENT_TIMESTAMP - INTERVAL '60 seconds'
                  AND NOT EXISTS (
                      SELECT 1
                      FROM channel_connection_sessions AS session
                      WHERE session.account_id = account.id
                        AND session.status IN (
                            'preparing',
                            'waiting_scan',
                            'scanned',
                            'verification_required',
                            'confirming'
                        )
                        AND session.expires_at > CURRENT_TIMESTAMP
                  )
                  AND (
                      (
                          %s = (
                              'feishu-provisioning-cleanup:'
                              || account.id
                          )
                          AND NOT EXISTS (
                              SELECT 1
                              FROM channel_connection_sessions AS session
                              WHERE session.account_id = account.id
                          )
                      )
                      OR EXISTS (
                          SELECT 1
                          FROM channel_connection_sessions AS session
                          WHERE session.account_id = account.id
                            AND %s = (
                                'feishu-registration:'
                                || session.id
                            )
                      )
                  )
                FOR UPDATE
                """,
                (
                    account_id,
                    provider,
                    runtime_fence.key,
                    runtime_fence.key,
                ),
            ).fetchone()
            return dict(row) if row else None

    def delete_orphaned_provisioning_account(
        self,
        *,
        owner_user_id: str,
        account_id: str,
        runtime_fence: RuntimeFence,
    ) -> bool:
        with self._connect() as connection:
            self._lock_runtime_fence(connection, runtime_fence)
            account = connection.execute(
                """
                SELECT id
                FROM channel_accounts
                WHERE id = %s
                  AND owner_user_id = %s
                  AND status = 'provisioning'
                FOR UPDATE
                """,
                (account_id, owner_user_id),
            ).fetchone()
            if account is None:
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
                WHERE id = %s
                  AND owner_user_id = %s
                  AND status = 'provisioning'
                RETURNING id
                """,
                (account_id, owner_user_id),
            ).fetchone()
            return deleted is not None

    def find_connected_account(
        self,
        provider: str,
        external_id_hash: str,
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            return connection.execute(
                """
                SELECT * FROM channel_accounts
                WHERE provider = %s
                  AND external_id_hash = %s
                  AND status = 'connected'
                LIMIT 1
                """,
                (provider, external_id_hash),
            ).fetchone()

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
        expected_revision: int,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_accounts
                SET credentials_ciphertext = %s,
                    credential_revision = credential_revision + 1,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND credential_revision = %s
                RETURNING id
                """,
                (
                    credentials_ciphertext,
                    account_id,
                    expected_revision,
                ),
            ).fetchone()
            return row is not None

    def acquire_runtime_lease(
        self,
        account_id: str,
    ) -> PostgresRuntimeLease | None:
        owner_id = f'rl_{uuid.uuid4().hex}'
        lease_seconds = 120
        with self._connect() as connection:
            row = connection.execute(
                """
                INSERT INTO channel_runtime_leases(
                    lease_key, owner_id, generation, lease_until
                )
                VALUES(
                    %s, %s, 1,
                    CURRENT_TIMESTAMP + make_interval(secs => %s)
                )
                ON CONFLICT(lease_key) DO UPDATE SET
                    owner_id = EXCLUDED.owner_id,
                    generation = channel_runtime_leases.generation + 1,
                    lease_until = EXCLUDED.lease_until,
                    updated_at = CURRENT_TIMESTAMP
                WHERE channel_runtime_leases.lease_until
                    <= CURRENT_TIMESTAMP
                RETURNING lease_key, owner_id, generation
                """,
                (account_id, owner_id, lease_seconds),
            ).fetchone()
        if not row:
            return None
        return PostgresRuntimeLease(
            self,
            RuntimeFence(
                key=str(row['lease_key']),
                owner_id=str(row['owner_id']),
                generation=int(row['generation']),
            ),
            lease_seconds,
        )

    def renew_runtime_lease(
        self,
        fence: RuntimeFence,
        *,
        lease_seconds: int,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_runtime_leases
                SET lease_until = CURRENT_TIMESTAMP
                        + make_interval(secs => %s),
                    updated_at = CURRENT_TIMESTAMP
                WHERE lease_key = %s
                  AND owner_id = %s
                  AND generation = %s
                  AND lease_until > CURRENT_TIMESTAMP
                RETURNING lease_key
                """,
                (
                    lease_seconds,
                    fence.key,
                    fence.owner_id,
                    fence.generation,
                ),
            ).fetchone()
            return row is not None

    def release_runtime_lease(self, fence: RuntimeFence) -> None:
        with self._connect() as connection:
            connection.execute(
                """
                UPDATE channel_runtime_leases
                SET lease_until = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                WHERE lease_key = %s
                  AND owner_id = %s
                  AND generation = %s
                """,
                (fence.key, fence.owner_id, fence.generation),
            )

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

    def ingest_batch(
        self,
        account_id: str,
        envelopes: list[InboundEnvelope],
        checkpoint: ReceiverCheckpoint | None,
        runtime_fence: RuntimeFence | None = None,
    ) -> int:
        inserted = 0
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(connection, runtime_fence)
            account = connection.execute(
                """
                SELECT status
                FROM channel_accounts
                WHERE id = %s
                FOR SHARE
                """,
                (account_id,),
            ).fetchone()
            if not account or account['status'] != 'connected':
                raise RuntimeError('channel account is not connected')
            for envelope in envelopes:
                row = connection.execute(
                    """
                    INSERT INTO channel_inbox(
                        id, account_id, provider, message_key, order_key,
                        external_address_hash, owner_user_id, recipient_id,
                        text, provider_context
                    )
                    VALUES(
                        %s, %s, %s, %s, %s,
                        %s, %s, %s, %s, %s::jsonb
                    )
                    ON CONFLICT(account_id, message_key) DO NOTHING
                    RETURNING id
                    """,
                    (
                        f'ci_{uuid.uuid4().hex}',
                        envelope.account_id,
                        envelope.provider,
                        envelope.message_key,
                        envelope.order_key,
                        envelope.external_address_hash,
                        envelope.owner_user_id,
                        envelope.recipient_id,
                        envelope.text,
                        self._json(envelope.provider_context),
                    ),
                ).fetchone()
                inserted += int(row is not None)
                connection.execute(
                    """
                    UPDATE channel_outbox
                    SET provider_context = provider_context || %s::jsonb,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE account_id = %s
                      AND order_key = %s
                      AND status IN ('pending', 'retry_wait')
                    """,
                    (
                        self._json(envelope.provider_context),
                        envelope.account_id,
                        envelope.order_key,
                    ),
                )
            if checkpoint is not None:
                timeout_ms = int(
                    checkpoint.metadata.get('longpoll_timeout_ms') or 35000
                )
                connection.execute(
                    """
                    INSERT INTO channel_checkpoints(
                        account_id, cursor, longpoll_timeout_ms
                    )
                    VALUES(%s, %s, %s)
                    ON CONFLICT(account_id) DO UPDATE SET
                        cursor = EXCLUDED.cursor,
                        longpoll_timeout_ms = EXCLUDED.longpoll_timeout_ms,
                        updated_at = CURRENT_TIMESTAMP
                    """,
                    (account_id, checkpoint.cursor, timeout_ms),
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
        return inserted

    def welcome_pending(self, account_id: str) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT welcome_pending
                FROM channel_accounts
                WHERE id = %s
                """,
                (account_id,),
            ).fetchone()
            return bool(row and row['welcome_pending'])

    def claim_welcome(self, account_id: str) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_accounts
                SET welcome_pending = FALSE,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND welcome_pending = TRUE
                RETURNING id
                """,
                (account_id,),
            ).fetchone()
            return row is not None

    def claim_next_inbound(
        self,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> ClaimedInbound | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                WITH candidate AS (
                    SELECT inbox.id
                    FROM channel_inbox AS inbox
                    JOIN channel_accounts AS account
                      ON account.id = inbox.account_id
                    WHERE (
                        inbox.status = 'pending'
                        OR (
                            inbox.status = 'retry_wait'
                            AND inbox.next_attempt_at <= CURRENT_TIMESTAMP
                        )
                        OR (
                            inbox.status = 'processing'
                            AND inbox.lease_until < CURRENT_TIMESTAMP
                        )
                    )
                    AND account.status = 'connected'
                    AND (
                      inbox.provider_context ->> '_parallel_inbound' = 'true'
                      OR NOT EXISTS (
                        SELECT 1
                        FROM channel_inbox AS earlier
                        WHERE earlier.account_id = inbox.account_id
                          AND earlier.order_key = inbox.order_key
                          AND earlier.status NOT IN (
                              'completed', 'ignored', 'dead'
                          )
                          AND (
                              earlier.ingest_sequence
                                  < inbox.ingest_sequence
                          )
                      )
                    )
                    ORDER BY inbox.ingest_sequence
                    FOR UPDATE SKIP LOCKED
                    LIMIT 1
                )
                UPDATE channel_inbox AS inbox
                SET status = 'processing',
                    lease_owner = %s,
                    lease_until = CURRENT_TIMESTAMP
                        + make_interval(secs => %s),
                    attempt_count = attempt_count + 1,
                    next_attempt_at = NULL,
                    updated_at = CURRENT_TIMESTAMP
                FROM candidate
                WHERE inbox.id = candidate.id
                RETURNING inbox.*
                """,
                (claim_owner, lease_seconds),
            ).fetchone()
        return self._claimed_inbound(row)

    def renew_inbound_lease(
        self,
        inbox_id: str,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_inbox
                SET lease_until = CURRENT_TIMESTAMP
                        + make_interval(secs => %s),
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND status = 'processing'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                RETURNING id
                """,
                (lease_seconds, inbox_id, claim_owner),
            ).fetchone()
            return row is not None

    def complete_inbound(
        self,
        inbox_id: str,
        claim_owner: str,
        outbound: list[OutboundMessage],
    ) -> bool:
        with self._connect() as connection:
            owned = connection.execute(
                """
                SELECT id, account_id
                FROM channel_inbox
                WHERE id = %s
                  AND status = 'processing'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                FOR UPDATE
                """,
                (inbox_id, claim_owner),
            ).fetchone()
            if not owned:
                return False
            self._insert_outbound(connection, inbox_id, outbound)
            connection.execute(
                """
                UPDATE channel_inbox
                SET status = 'completed',
                    lease_owner = NULL,
                    lease_until = NULL,
                    last_error = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (inbox_id,),
            )
            connection.execute(
                """
                UPDATE channel_accounts
                SET last_message_at = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (owned['account_id'],),
            )
            return True

    def record_inbound_failure(
        self,
        inbox_id: str,
        claim_owner: str,
        *,
        error: str,
        fallback: OutboundMessage,
        max_attempts: int,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT attempt_count
                FROM channel_inbox
                WHERE id = %s
                  AND status = 'processing'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                FOR UPDATE
                """,
                (inbox_id, claim_owner),
            ).fetchone()
            if not row:
                return False
            terminal = int(row['attempt_count']) >= max_attempts
            if terminal:
                self._insert_outbound(connection, inbox_id, [fallback])
                status = 'dead'
                next_attempt_at = None
            else:
                status = 'retry_wait'
                delay_seconds = min(
                    60,
                    2 ** max(1, int(row['attempt_count'])),
                )
                next_attempt_at = dt.datetime.now(
                    dt.timezone.utc
                ) + dt.timedelta(seconds=delay_seconds)
            connection.execute(
                """
                UPDATE channel_inbox
                SET status = %s,
                    lease_owner = NULL,
                    lease_until = NULL,
                    next_attempt_at = %s,
                    last_error = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (status, next_attempt_at, error[:500], inbox_id),
            )
            return terminal

    def claim_next_outbound(
        self,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> ClaimedOutbound | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                WITH candidate AS (
                    SELECT outbox.id
                    FROM channel_outbox AS outbox
                    WHERE (
                        outbox.status = 'pending'
                        OR (
                            outbox.status = 'retry_wait'
                            AND outbox.next_attempt_at <= CURRENT_TIMESTAMP
                        )
                        OR (
                            outbox.status = 'sending'
                            AND outbox.lease_until < CURRENT_TIMESTAMP
                        )
                    )
                    AND NOT EXISTS (
                        SELECT 1
                        FROM channel_outbox AS earlier
                        WHERE earlier.account_id = outbox.account_id
                          AND earlier.order_key = outbox.order_key
                          AND earlier.status NOT IN ('sent', 'dead')
                          AND (
                              earlier.created_sequence
                                  < outbox.created_sequence
                          )
                    )
                    ORDER BY outbox.created_sequence
                    FOR UPDATE SKIP LOCKED
                    LIMIT 1
                )
                UPDATE channel_outbox AS outbox
                SET status = 'sending',
                    lease_owner = %s,
                    lease_until = CURRENT_TIMESTAMP
                        + make_interval(secs => %s),
                    attempt_count = attempt_count + 1,
                    next_attempt_at = NULL,
                    updated_at = CURRENT_TIMESTAMP
                FROM candidate
                WHERE outbox.id = candidate.id
                RETURNING outbox.*
                """,
                (claim_owner, lease_seconds),
            ).fetchone()
        return self._claimed_outbound(row)

    def save_rendered_parts(
        self,
        outbox_id: str,
        claim_owner: str,
        parts: list[dict[str, Any]],
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_outbox
                SET rendered_parts = %s::jsonb,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND status = 'sending'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                  AND rendered_parts = '[]'::jsonb
                RETURNING id
                """,
                (self._json(parts), outbox_id, claim_owner),
            ).fetchone()
            return row is not None

    def renew_outbound_lease(
        self,
        outbox_id: str,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_outbox
                SET lease_until = CURRENT_TIMESTAMP
                        + make_interval(secs => %s),
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND status = 'sending'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                RETURNING id
                """,
                (lease_seconds, outbox_id, claim_owner),
            ).fetchone()
            return row is not None

    def save_outbound_part_state(
        self,
        outbox_id: str,
        claim_owner: str,
        part_index: int,
        state: dict[str, Any],
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_outbox
                SET provider_state = jsonb_set(
                        provider_state,
                        ARRAY[%s],
                        %s::jsonb,
                        TRUE
                    ),
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND status = 'sending'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                RETURNING id
                """,
                (
                    str(part_index),
                    self._json(state),
                    outbox_id,
                    claim_owner,
                ),
            ).fetchone()
            return row is not None

    def list_sent_task_outbounds(
        self,
        *,
        provider: str,
        limit: int,
    ) -> list[ClaimedOutbound]:
        with self._connect() as connection:
            rows = connection.execute(
                """
                SELECT *
                FROM channel_outbox
                WHERE provider = %s
                  AND status = 'sent'
                  AND created_at >= CURRENT_TIMESTAMP - INTERVAL '1 day'
                  AND metadata @> %s::jsonb
                ORDER BY created_sequence DESC
                LIMIT %s
                """,
                (
                    provider,
                    self._json(
                        {'task_monitor': True}
                    ),
                    max(1, limit),
                ),
            ).fetchall()
        return [
            outbound
            for outbound in (
                self._claimed_outbound(row)
                for row in rows
            )
            if outbound is not None
        ]

    def sync_task_artifact_outbounds(
        self,
        *,
        parent: ClaimedOutbound,
        part_index: int,
        artifacts: list[dict[str, str]],
    ) -> dict[str, int]:
        prefix = f'task-artifact:{parent.outbox_id}:{part_index}:'
        order_key = f'task-artifact:{parent.outbox_id}:{part_index}'
        chat_id = str(
            parent.provider_context.get('chat_id')
            or parent.recipient_id
        )
        with self._connect() as connection:
            for sequence, artifact in enumerate(artifacts):
                artifact_key = str(artifact.get('artifact_key') or '')
                source = str(artifact.get('source') or '')
                delivery_id = str(artifact.get('delivery_id') or '')
                if (
                    len(artifact_key) != 64
                    or not source
                    or len(source) > 2048
                    or not delivery_id
                    or len(delivery_id) > 512
                ):
                    continue
                connection.execute(
                    """
                    INSERT INTO channel_outbox(
                        id, inbox_id, account_id, dedupe_key,
                        provider, order_key, sequence,
                        recipient_id, provider_context, text, intent_kind,
                        purpose, metadata, rendered_parts, status
                    )
                    VALUES(
                        %s, NULL, %s, %s,
                        %s, %s, %s,
                        %s, %s::jsonb, '', 'task_artifact',
                        'task_artifact', %s::jsonb, %s::jsonb, 'pending'
                    )
                    ON CONFLICT(account_id, dedupe_key) DO NOTHING
                    """,
                    (
                        f'co_{uuid.uuid4().hex}',
                        parent.account_id,
                        f'{prefix}{artifact_key}',
                        parent.provider,
                        order_key,
                        sequence,
                        parent.recipient_id,
                        self._json({'chat_id': chat_id}),
                        self._json({
                            'task_artifact': True,
                            'parent_outbox_id': parent.outbox_id,
                            'parent_part_index': part_index,
                        }),
                        self._json([{
                            'kind': 'image',
                            'source': source,
                            'alt': str(
                                artifact.get('caption') or ''
                            )[:300],
                            'delivery_id': delivery_id,
                        }]),
                    ),
                )
            rows = connection.execute(
                """
                SELECT status, COUNT(*) AS item_count
                FROM channel_outbox
                WHERE account_id = %s
                  AND dedupe_key LIKE %s
                GROUP BY status
                """,
                (parent.account_id, f'{prefix}%'),
            ).fetchall()
        counts = {
            str(row['status']): int(row['item_count'])
            for row in rows
        }
        sent = counts.get('sent', 0)
        dead = counts.get('dead', 0)
        total = sum(counts.values())
        return {
            'total': total,
            'sent': sent,
            'dead': dead,
            'inflight': total - sent - dead,
        }

    def compare_and_save_sent_task_monitor_state(
        self,
        *,
        outbox_id: str,
        part_index: int,
        expected_revision: int,
        state: dict[str, Any],
        complete: bool,
    ) -> dict[str, Any] | None:
        part_key = str(part_index)
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT provider_state, metadata
                FROM channel_outbox
                WHERE id = %s AND status = 'sent'
                FOR UPDATE
                """,
                (outbox_id,),
            ).fetchone()
            if row is None:
                return None
            provider_state = self._dict(row['provider_state'])
            current = self._dict(provider_state.get(part_key))
            monitor = self._dict(current.get('task_monitor'))
            if int(monitor.get('monitor_revision') or 0) != expected_revision:
                return current
            next_state = dict(state)
            next_monitor = self._dict(next_state.get('task_monitor'))
            next_monitor['monitor_revision'] = expected_revision + 1
            next_state['task_monitor'] = next_monitor
            provider_state[part_key] = next_state
            metadata = self._dict(row['metadata'])
            metadata['task_monitor'] = not complete
            connection.execute(
                """
                UPDATE channel_outbox
                SET provider_state = %s::jsonb,
                    metadata = %s::jsonb,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND status = 'sent'
                """,
                (
                    self._json(provider_state),
                    self._json(metadata),
                    outbox_id,
                ),
            )
            return next_state

    def advance_outbound(
        self,
        outbox_id: str,
        claim_owner: str,
        next_part_index: int,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_outbox
                SET next_part_index = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND status = 'sending'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                RETURNING id
                """,
                (next_part_index, outbox_id, claim_owner),
            ).fetchone()
            return row is not None

    def complete_outbound(
        self,
        outbox_id: str,
        claim_owner: str,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_outbox
                SET status = 'sent',
                    lease_owner = NULL,
                    lease_until = NULL,
                    last_error = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND status = 'sending'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                RETURNING account_id, purpose
                """,
                (outbox_id, claim_owner),
            ).fetchone()
            if not row:
                return False
            if row['purpose'] == 'welcome':
                connection.execute(
                    """
                    UPDATE channel_accounts
                    SET welcome_pending = FALSE,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE id = %s
                    """,
                    (row['account_id'],),
                )
            return True

    def record_outbound_failure(
        self,
        outbox_id: str,
        claim_owner: str,
        *,
        error: str,
        max_attempts: int,
    ) -> None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT attempt_count
                FROM channel_outbox
                WHERE id = %s
                  AND status = 'sending'
                  AND lease_owner = %s
                  AND lease_until >= CURRENT_TIMESTAMP
                FOR UPDATE
                """,
                (outbox_id, claim_owner),
            ).fetchone()
            if not row:
                return
            terminal = int(row['attempt_count']) >= max_attempts
            delay_seconds = min(
                300,
                2 ** max(1, int(row['attempt_count'])),
            )
            next_attempt_at = (
                None
                if terminal
                else dt.datetime.now(dt.timezone.utc)
                + dt.timedelta(seconds=delay_seconds)
            )
            connection.execute(
                """
                UPDATE channel_outbox
                SET status = %s,
                    lease_owner = NULL,
                    lease_until = NULL,
                    next_attempt_at = %s,
                    last_error = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (
                    'dead' if terminal else 'retry_wait',
                    next_attempt_at,
                    error[:500],
                    outbox_id,
                ),
            )

    @staticmethod
    def _json(value: Any) -> str:
        encoded = json.dumps(
            value,
            ensure_ascii=False,
            separators=(',', ':'),
        )
        return _JSON_NUL_ESCAPE.sub(r'\1', encoded)

    def _insert_outbound(
        self,
        connection: Any,
        inbox_id: str,
        outbound: list[OutboundMessage],
    ) -> None:
        for sequence, message in enumerate(outbound):
            if (
                message.purpose == 'welcome'
                and not self._reserve_welcome(connection, message.account_id)
            ):
                continue
            connection.execute(
                """
                INSERT INTO channel_outbox(
                    id, inbox_id, account_id, dedupe_key,
                    provider, order_key, sequence,
                    recipient_id, provider_context, text, intent_kind,
                    purpose, metadata
                )
                VALUES(
                    %s, %s, %s, %s,
                    %s, %s, %s,
                    %s, %s::jsonb, %s, %s, %s, %s::jsonb
                )
                """,
                (
                    f'co_{uuid.uuid4().hex}',
                    inbox_id,
                    message.account_id,
                    f'{inbox_id}:{sequence}',
                    message.provider,
                    message.order_key,
                    sequence,
                    message.recipient_id,
                    self._json(message.provider_context),
                    message.text.replace('\x00', ''),
                    message.intent_kind,
                    message.purpose,
                    self._json(message.metadata),
                ),
            )

    @staticmethod
    def _reserve_welcome(connection: Any, account_id: str) -> bool:
        account = connection.execute(
            """
            SELECT welcome_pending
            FROM channel_accounts
            WHERE id = %s
            FOR UPDATE
            """,
            (account_id,),
        ).fetchone()
        if not account or not account['welcome_pending']:
            return False
        existing = connection.execute(
            """
            SELECT 1
            FROM channel_outbox
            WHERE account_id = %s
              AND purpose = 'welcome'
              AND status NOT IN ('sent', 'dead')
            LIMIT 1
            """,
            (account_id,),
        ).fetchone()
        return existing is None

    @staticmethod
    def _claimed_inbound(
        row: dict[str, Any] | None,
    ) -> ClaimedInbound | None:
        if not row:
            return None
        return ClaimedInbound(
            inbox_id=str(row['id']),
            provider=str(row['provider']),
            account_id=str(row['account_id']),
            message_key=str(row['message_key']),
            order_key=str(row['order_key']),
            external_address_hash=str(row['external_address_hash']),
            owner_user_id=str(row['owner_user_id']),
            recipient_id=str(row['recipient_id']),
            text=str(row['text']),
            provider_context=GatewayStore._dict(row['provider_context']),
            attempt_count=int(row['attempt_count']),
        )

    @staticmethod
    def _claimed_outbound(
        row: dict[str, Any] | None,
    ) -> ClaimedOutbound | None:
        if not row:
            return None
        rendered_parts = GatewayStore._list(row['rendered_parts'])
        return ClaimedOutbound(
            outbox_id=str(row['id']),
            provider=str(row['provider']),
            account_id=str(row['account_id']),
            order_key=str(row['order_key']),
            recipient_id=str(row['recipient_id']),
            provider_context=GatewayStore._dict(row['provider_context']),
            text=str(row['text']),
            intent_kind=str(row['intent_kind']),
            purpose=str(row['purpose']),
            metadata=GatewayStore._dict(row['metadata']),
            rendered_parts=[
                dict(part)
                for part in rendered_parts
                if isinstance(part, dict)
            ],
            next_part_index=int(row['next_part_index']),
            provider_state=GatewayStore._dict(row['provider_state']),
            attempt_count=int(row['attempt_count']),
        )

    @staticmethod
    def _dict(value: Any) -> dict[str, Any]:
        if isinstance(value, str):
            value = json.loads(value)
        return dict(value) if isinstance(value, dict) else {}

    @staticmethod
    def _list(value: Any) -> list[Any]:
        if isinstance(value, str):
            value = json.loads(value)
        return list(value) if isinstance(value, list) else []

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

    def get_feishu_workspace_state(
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
        workspace = snapshot.get('feishu_workspace')
        return dict(workspace) if isinstance(workspace, dict) else {}

    def save_feishu_workspace_state_if_revision(
        self,
        account_id: str,
        external_address_hash: str,
        state: dict[str, Any],
        expected_revision: int,
        *,
        preserve_current_message: bool = True,
    ) -> bool:
        value = json.dumps(
            state,
            ensure_ascii=False,
            separators=(',', ':'),
        )
        with self._connect() as connection:
            if expected_revision == 0:
                inserted = connection.execute(
                    """
                    INSERT INTO channel_navigation_states(
                        account_id, external_address_hash, mode, snapshot_json
                    )
                    VALUES(
                        %s, %s, 'active',
                        jsonb_build_object(
                            'feishu_workspace', %s::jsonb
                        )
                    )
                    ON CONFLICT(account_id, external_address_hash) DO NOTHING
                    RETURNING 1 AS saved
                    """,
                    (account_id, external_address_hash, value),
                ).fetchone()
                if inserted:
                    return True
            row = connection.execute(
                """
                UPDATE channel_navigation_states
                SET snapshot_json = jsonb_set(
                        CASE
                            WHEN jsonb_typeof(snapshot_json) = 'object'
                                THEN snapshot_json
                            WHEN jsonb_typeof(snapshot_json) = 'array'
                                THEN jsonb_build_object(
                                    'selection',
                                    jsonb_build_object(
                                        'kind', 'conversation',
                                        'items', snapshot_json
                                    )
                                )
                            ELSE '{}'::jsonb
                        END,
                        '{feishu_workspace}',
                        jsonb_set(
                            %s::jsonb,
                            '{message_id}',
                            CASE
                                WHEN %s THEN COALESCE(
                                    to_jsonb(NULLIF(
                                        snapshot_json
                                            -> 'feishu_workspace'
                                            ->> 'message_id',
                                        ''
                                    )),
                                    %s::jsonb -> 'message_id',
                                    '""'::jsonb
                                )
                                ELSE COALESCE(
                                    %s::jsonb -> 'message_id',
                                    '""'::jsonb
                                )
                            END,
                            true
                        ),
                        true
                    ),
                    updated_at = CURRENT_TIMESTAMP
                WHERE account_id = %s
                    AND external_address_hash = %s
                    AND COALESCE(
                        snapshot_json -> 'feishu_workspace' ->> 'revision',
                        '0'
                    ) = %s::text
                RETURNING 1 AS saved
                """,
                (
                    value,
                    preserve_current_message,
                    value,
                    value,
                    account_id,
                    external_address_hash,
                    str(expected_revision),
                ),
            ).fetchone()
        return bool(row)

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
    ) -> bool:
        with self._connect() as connection:
            self._lock_runtime_fence(connection, runtime_fence)
            connection.execute(
                'SELECT pg_advisory_xact_lock(hashtext(%s))',
                (f'channel-navigation:{account_id}:{external_address_hash}',),
            )
            account = connection.execute(
                """
                SELECT status
                FROM channel_accounts
                WHERE id = %s
                FOR SHARE
                """,
                (account_id,),
            ).fetchone()
            if not account or account['status'] != 'connected':
                raise RuntimeError('channel account is not connected')
            existing = connection.execute(
                """
                SELECT 1 AS present
                FROM channel_inbox
                WHERE account_id = %s AND message_key = %s
                """,
                (account_id, envelope.message_key),
            ).fetchone()
            if existing:
                return False
            if envelope.provider_context.get('_parallel_inbound') is not True:
                active = connection.execute(
                    """
                    SELECT 1 AS present
                    FROM channel_inbox
                    WHERE account_id = %s AND order_key = %s
                      AND status NOT IN ('completed', 'ignored', 'dead')
                    LIMIT 1
                    """,
                    (account_id, envelope.order_key),
                ).fetchone()
                if active:
                    return False
            row = connection.execute(
                """
                SELECT snapshot_json
                FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                FOR UPDATE
                """,
                (account_id, external_address_hash),
            ).fetchone()
            snapshot = _snapshot(row.get('snapshot_json')) if row else {}
            current = snapshot.get('feishu_workspace')
            if not isinstance(current, dict):
                current = {}
            if int(current.get('revision') or 0) != expected_revision:
                return False
            if (
                str(current.get('message_id') or '')
                != expected_message_id
                or str(current.get('active_operation_id') or '')
                != expected_operation_id
            ):
                return False
            snapshot['feishu_workspace'] = state
            snapshot_json = self._json(snapshot)
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode, snapshot_json
                )
                VALUES(%s, %s, 'active', %s::jsonb)
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    snapshot_json = EXCLUDED.snapshot_json,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (account_id, external_address_hash, snapshot_json),
            )
            inserted = connection.execute(
                """
                INSERT INTO channel_inbox(
                    id, account_id, provider, message_key, order_key,
                    external_address_hash, owner_user_id, recipient_id,
                    text, provider_context
                )
                VALUES(
                    %s, %s, %s, %s, %s,
                    %s, %s, %s, %s, %s::jsonb
                )
                ON CONFLICT(account_id, message_key) DO NOTHING
                RETURNING id
                """,
                (
                    f'ci_{uuid.uuid4().hex}',
                    envelope.account_id,
                    envelope.provider,
                    envelope.message_key,
                    envelope.order_key,
                    envelope.external_address_hash,
                    envelope.owner_user_id,
                    envelope.recipient_id,
                    envelope.text,
                    self._json(envelope.provider_context),
                ),
            ).fetchone()
            if not inserted:
                raise RuntimeError('Feishu inbox claim was lost')
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
        return True

    def has_active_inbound(
        self,
        account_id: str,
        order_key: str,
    ) -> bool:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT 1 AS present
                FROM channel_inbox
                WHERE account_id = %s AND order_key = %s
                  AND status NOT IN ('completed', 'ignored', 'dead')
                LIMIT 1
                """,
                (account_id, order_key),
            ).fetchone()
            return row is not None

    def patch_feishu_workspace_state(
        self,
        account_id: str,
        external_address_hash: str,
        patch: dict[str, Any],
        operation_id: str = '',
    ) -> dict[str, Any]:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json
                FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                FOR UPDATE
                """,
                (account_id, external_address_hash),
            ).fetchone()
            snapshot = _snapshot(row.get('snapshot_json')) if row else {}
            workspace = snapshot.get('feishu_workspace')
            if not isinstance(workspace, dict):
                workspace = {}
            if operation_id and str(
                workspace.get('active_operation_id') or ''
            ) != operation_id:
                return dict(workspace)
            current_revision = max(
                0,
                int(workspace.get('revision') or 0),
            )
            workspace = {**workspace, **patch}
            workspace['revision'] = current_revision + 1
            snapshot['feishu_workspace'] = workspace
            value = self._json(snapshot)
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode, snapshot_json
                )
                VALUES(%s, %s, 'active', %s::jsonb)
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    snapshot_json = EXCLUDED.snapshot_json,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (account_id, external_address_hash, value),
            )
        return dict(workspace)

    def save_feishu_workspace_message(
        self,
        account_id: str,
        external_address_hash: str,
        message_id: str,
        operation_id: str,
        expected_message_id: str,
        expected_revision: int | None = None,
    ) -> dict[str, Any]:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json
                FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                FOR UPDATE
                """,
                (account_id, external_address_hash),
            ).fetchone()
            snapshot = _snapshot(row.get('snapshot_json')) if row else {}
            workspace = snapshot.get('feishu_workspace')
            if not isinstance(workspace, dict):
                workspace = {}
            if operation_id and str(
                workspace.get('active_operation_id') or ''
            ) != operation_id:
                return dict(workspace)
            if str(workspace.get('message_id') or '') != expected_message_id:
                return dict(workspace)
            if (
                expected_revision is not None
                and int(workspace.get('revision') or 0) != expected_revision
            ):
                return dict(workspace)
            workspace = dict(workspace)
            workspace['message_id'] = message_id
            snapshot['feishu_workspace'] = workspace
            value = self._json(snapshot)
            connection.execute(
                """
                INSERT INTO channel_navigation_states(
                    account_id, external_address_hash, mode, snapshot_json
                )
                VALUES(%s, %s, 'active', %s::jsonb)
                ON CONFLICT(account_id, external_address_hash) DO UPDATE SET
                    snapshot_json = EXCLUDED.snapshot_json,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (
                    account_id,
                    external_address_hash,
                    value,
                ),
            )
        return workspace

    def begin_new_conversation(
        self,
        account_id: str,
        external_address_hash: str,
        draft: dict[str, Any] | None = None,
    ) -> None:
        draft_json = json.dumps(
            draft or {},
            ensure_ascii=False,
            separators=(',', ':'),
        )
        with self._connect() as connection:
            connection.execute(
                'SELECT pg_advisory_xact_lock(hashtext(%s))',
                (f'channel-navigation:{account_id}:{external_address_hash}',),
            )
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
                            THEN channel_navigation_states.snapshot_json
                                - 'selection'
                                - 'new_conversation'
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
        preserve_selection: bool = False,
    ) -> None:
        history_conversation_id = (
            conversation_id
            if history_next_page_token is not None
            else None
        )
        history_token = history_next_page_token or None
        with self._connect() as connection:
            connection.execute(
                'SELECT pg_advisory_xact_lock(hashtext(%s))',
                (f'channel-navigation:{account_id}:{external_address_hash}',),
            )
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
                            THEN CASE
                            WHEN %s AND %s
                                THEN channel_navigation_states.snapshot_json
                                    - 'new_conversation'
                                    - 'pending_turn'
                            WHEN %s
                                THEN channel_navigation_states.snapshot_json
                                    - 'new_conversation'
                                    - 'pending_turn'
                                    - 'selection'
                            WHEN %s
                                THEN channel_navigation_states.snapshot_json
                                    - 'new_conversation'
                            ELSE channel_navigation_states.snapshot_json
                                - 'new_conversation'
                                - 'selection'
                            END
                        ELSE '{}'::jsonb
                    END,
                    snapshot_expires_at = CASE WHEN %s
                        THEN channel_navigation_states.snapshot_expires_at
                        ELSE NULL
                    END,
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
                    preserve_selection,
                    consume_pending_turn,
                    preserve_selection,
                    preserve_selection,
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
        selection = {
            'id': uuid.uuid4().hex,
            'kind': kind,
            'items': items,
        }
        if continuation:
            selection['continuation'] = continuation
        selection_json = json.dumps(
            selection,
            ensure_ascii=False,
            separators=(',', ':'),
        )
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
        runtime_fence: RuntimeFence | None = None,
    ) -> None:
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(connection, runtime_fence)
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

    @staticmethod
    def _lock_runtime_fence(
        connection: Any,
        fence: RuntimeFence,
    ) -> None:
        row = connection.execute(
            """
            SELECT lease_key
            FROM channel_runtime_leases
            WHERE lease_key = %s
              AND owner_id = %s
              AND generation = %s
              AND lease_until > CURRENT_TIMESTAMP
            FOR UPDATE
            """,
            (fence.key, fence.owner_id, fence.generation),
        ).fetchone()
        if row is None:
            raise RuntimeLeaseLostError(
                'Channel runtime lease was lost'
            )
