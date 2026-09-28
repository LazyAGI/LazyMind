import datetime as dt
import hashlib
import json
import re
import uuid
from typing import Any

import psycopg
from psycopg.rows import dict_row

from channel_gateway.common.errors import GatewayError, RuntimeLeaseLostError
from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
    InboundEnvelope,
    OutboundMessage,
    ReceiverCheckpoint,
    RuntimeFence,
)
from channel_gateway.common.ports.providers import PayloadCipher


_JSON_NUL_ESCAPE = re.compile(r'(?<!\\)((?:\\\\)*)\\u0000')


def decode_snapshot(value: Any) -> dict[str, Any]:
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
    def __init__(
        self,
        dsn: str,
        payload_cipher: PayloadCipher | None = None,
    ):
        self._dsn = dsn
        self._payload_cipher = payload_cipher

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
                sensitive_payload_ciphertext TEXT,
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
            ALTER TABLE channel_inbox
            ADD COLUMN IF NOT EXISTS sensitive_payload_ciphertext TEXT
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
            CREATE INDEX IF NOT EXISTS channel_outbox_monitor_idx
            ON channel_outbox(provider, status, created_sequence)
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
            connection.execute(
                'ALTER TABLE channel_connection_sessions ADD COLUMN IF NOT EXISTS requested_account_id TEXT'
            )
            connection.execute('ALTER TABLE channel_accounts ADD COLUMN IF NOT EXISTS identity_metadata TEXT '
                               "NOT NULL DEFAULT '{}'")
            connection.execute('ALTER TABLE channel_accounts ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ')
            self._initialize_notifications(connection)
            connection.execute('ALTER TABLE channel_accounts ADD COLUMN IF NOT EXISTS default_recipient_id TEXT '
                               "NOT NULL DEFAULT ''")
            connection.execute('ALTER TABLE channel_notification_targets ADD COLUMN IF NOT EXISTS label TEXT '
                               "NOT NULL DEFAULT ''")
            connection.execute('ALTER TABLE channel_notification_targets ADD COLUMN IF NOT EXISTS kind TEXT '
                               "NOT NULL DEFAULT 'conversation'")

    @staticmethod
    def _initialize_notifications(connection) -> None:
        connection.execute('''
            CREATE TABLE IF NOT EXISTS channel_notification_targets (
                account_id TEXT NOT NULL REFERENCES channel_accounts(id) ON DELETE CASCADE,
                recipient_id TEXT NOT NULL,
                context_ciphertext TEXT,
                label TEXT NOT NULL DEFAULT '',
                kind TEXT NOT NULL DEFAULT 'conversation',
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, recipient_id)
            )
        ''')
        connection.execute("""
            INSERT INTO channel_notification_targets(account_id, recipient_id)
            SELECT DISTINCT inbox.account_id, inbox.recipient_id
            FROM channel_inbox inbox JOIN channel_accounts account ON account.id = inbox.account_id
            WHERE account.provider = 'feishu' AND inbox.provider = account.provider
                AND inbox.owner_user_id = account.owner_user_id
                AND account.status = 'connected' AND account.credentials_ciphertext <> ''
                AND account.archived_at IS NULL AND inbox.recipient_id <> ''
            ON CONFLICT(account_id, recipient_id) DO NOTHING
        """)

    def disconnect_account(self, owner_user_id: str, account_id: str, *, retain_credentials: bool = False,
                           expected_revision: int | None = None,
                           runtime_fence: RuntimeFence | None = None) -> bool:
        """Revoke delivery while retaining the identity and all historical rows."""
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(connection, runtime_fence)
            account = connection.execute('''
                SELECT id, provider, credential_revision FROM channel_accounts
                WHERE id = %s AND owner_user_id = %s FOR UPDATE
            ''', (account_id, owner_user_id)).fetchone()
            if not account:
                return False
            if expected_revision is not None and account['credential_revision'] != expected_revision:
                return False
            connection.execute('''
                UPDATE channel_accounts SET status = 'disconnected', runtime_status = 'stopped',
                    credentials_ciphertext = CASE WHEN %s THEN credentials_ciphertext ELSE '' END,
                    credential_revision = credential_revision + 1,
                    updated_at = CURRENT_TIMESTAMP WHERE id = %s
            ''', (retain_credentials, account_id))
            connection.execute('''
                UPDATE channel_connection_sessions SET status = 'canceled', provider_state_ciphertext = NULL,
                    revision = revision + 1, message = '账号已断开', updated_at = CURRENT_TIMESTAMP
                WHERE owner_user_id = %s AND requested_account_id = %s AND provider = %s
                    AND status IN ('preparing','waiting_scan','scanned','verification_required','confirming')
            ''', (owner_user_id, account_id, account['provider']))
            connection.execute('''
                UPDATE channel_outbox SET status = CASE
                    WHEN purpose = 'notification' AND status = 'sending' THEN 'unknown'
                    WHEN purpose = 'notification' THEN 'skipped' ELSE 'dead' END,
                    last_error = CASE WHEN purpose = 'notification' AND status = 'sending'
                    THEN 'NOTIFICATION_DELIVERY_UNKNOWN' ELSE 'NOTIFICATION_TARGET_UNAVAILABLE' END,
                    lease_owner = NULL, lease_until = NULL,
                    next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
                WHERE account_id = %s AND status IN ('pending','retry_wait','sending')
            ''', (account_id,))
            connection.execute('''
                UPDATE channel_notification_targets SET context_ciphertext = NULL WHERE account_id = %s
            ''', (account_id,))
            connection.execute('''
                UPDATE channel_runtime_leases SET generation = generation + 1, lease_until = CURRENT_TIMESTAMP
                WHERE lease_key = %s
            ''', (account_id,))
            return True

    def resume_account(
        self,
        owner_user_id: str,
        account_id: str,
        credential_revision: int,
        provider: str = 'feishu',
    ):
        """Only the caller that changes desired state starts a runtime; never restore erased keys."""
        with self._connect() as connection:
            return connection.execute('''
                UPDATE channel_accounts SET status = 'connected', runtime_status = 'starting',
                    last_error = NULL, updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND owner_user_id = %s AND provider = %s
                    AND status = 'disconnected' AND credentials_ciphertext <> '' AND credential_revision = %s
                    AND archived_at IS NULL
                RETURNING *
            ''', (account_id, owner_user_id, provider, credential_revision)).fetchone()

    def complete_reused_connection(self, session_id: str, owner_user_id: str, account_id: str):
        with self._connect() as connection:
            account = connection.execute('''
                SELECT id FROM channel_accounts WHERE id = %s AND owner_user_id = %s
                    AND provider = 'feishu' AND status = 'connected' AND credentials_ciphertext <> '' FOR UPDATE
            ''', (account_id, owner_user_id)).fetchone()
            if not account:
                raise GatewayError(409, 'ACCOUNT_STATE_CHANGED', '飞书账号状态已经变化，请刷新后重试')
            return connection.execute('''
                UPDATE channel_connection_sessions SET status = 'connected', account_id = %s,
                    message = '已复用原飞书连接', revision = revision + 1, updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND owner_user_id = %s AND requested_account_id = %s AND status = 'preparing'
                RETURNING *
            ''', (account_id, session_id, owner_user_id, account_id)).fetchone()

    def complete_reauthorized_connection(
        self, *, session_id: str, qr_version: int, owner_user_id: str, account_id: str,
        external_id_hash: str, credentials_ciphertext: str, runtime_fence: RuntimeFence,
    ):
        """Replace credentials on the original identity atomically, retaining references and history."""
        with self._connect() as connection:
            self._lock_runtime_fence(connection, runtime_fence)
            account = connection.execute('''
                SELECT id FROM channel_accounts WHERE id = %s AND owner_user_id = %s
                    AND provider = 'feishu' AND external_id_hash = %s AND archived_at IS NULL FOR UPDATE
            ''', (account_id, owner_user_id, external_id_hash)).fetchone()
            if not account:
                raise GatewayError(409, 'ACCOUNT_IDENTITY_MISMATCH', '请重新授权原飞书机器人')
            session = connection.execute('''
                UPDATE channel_connection_sessions SET status = 'connected', account_id = %s,
                    revision = revision + 1, message = '原飞书机器人已重新授权', provider_state_ciphertext = NULL,
                    error_code = NULL, error_message = NULL, error_retryable = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND owner_user_id = %s AND requested_account_id = %s AND qr_version = %s
                    AND status IN ('preparing','waiting_scan','scanned','verification_required','confirming')
                    AND expires_at > CURRENT_TIMESTAMP RETURNING id
            ''', (account_id, session_id, owner_user_id, account_id, qr_version)).fetchone()
            if not session:
                raise GatewayError(409, 'ACCOUNT_STATE_CHANGED', '连接会话已经变化，请刷新后重试')
            return connection.execute('''
                UPDATE channel_accounts SET credentials_ciphertext = %s,
                    credential_revision = credential_revision + 1, status = 'connected', runtime_status = 'starting',
                    last_error = NULL, connected_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
                WHERE id = %s RETURNING *
            ''', (credentials_ciphertext, account_id)).fetchone()

    def update_account_identity(self, account_id: str, metadata: dict, credential_revision: int):
        with self._connect() as connection:
            return connection.execute('''
                UPDATE channel_accounts SET identity_metadata = %s
                WHERE id = %s AND provider = 'feishu' AND credential_revision = %s AND archived_at IS NULL
                RETURNING *
            ''', (self._json(metadata), account_id, credential_revision)).fetchone()

    def rename_account(self, owner: str, account_id: str, label: str):
        with self._connect() as connection:
            return connection.execute('''
                UPDATE channel_accounts SET label = %s, updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND owner_user_id = %s AND provider = 'feishu' AND archived_at IS NULL
                RETURNING *
            ''', (label, account_id, owner)).fetchone()

    def archive_account(self, owner: str, account_id: str) -> None:
        """Remove only an unbound record from active use; never delete its message history."""
        with self._connect() as connection:
            row = connection.execute('''
                SELECT * FROM channel_accounts WHERE id = %s AND owner_user_id = %s FOR UPDATE
            ''', (account_id, owner)).fetchone()
            if not row:
                raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
            if row['provider'] != 'feishu':
                raise GatewayError(422, 'PROVIDER_NOT_SUPPORTED', '此操作仅支持飞书')
            if row.get('archived_at'):
                return
            if row['status'] != 'disconnected' or row['credentials_ciphertext']:
                raise GatewayError(409, 'ACCOUNT_UNBIND_REQUIRED', '请先解除绑定，再删除记录')
            connection.execute('''
                UPDATE channel_accounts SET archived_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
            ''', (account_id,))
            connection.execute('''
                UPDATE channel_connection_sessions SET status = 'canceled', provider_state_ciphertext = NULL,
                    revision = revision + 1, message = '账号记录已删除', updated_at = CURRENT_TIMESTAMP
                WHERE owner_user_id = %s AND requested_account_id = %s
                    AND status IN ('preparing','waiting_scan','scanned','verification_required','confirming')
            ''', (owner, account_id))

    def ping(self) -> None:
        with self._connect() as connection:
            connection.execute('SELECT 1').fetchone()

    def notification_targets(self, owner, account_id, *, cursor='', limit=20, recipient_id=''):
        account = self.get_account(owner, account_id)
        if not account:
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        with self._connect() as connection:
            rows = connection.execute('''
                SELECT recipient_id, context_ciphertext, label, kind FROM channel_notification_targets
                WHERE account_id = %s AND recipient_id > %s AND (%s = '' OR recipient_id = %s)
                ORDER BY recipient_id LIMIT %s
            ''', (account_id, cursor, recipient_id, recipient_id, limit + 1)).fetchall()
        items = [{'recipient_id': row['recipient_id'], 'label': row['label'] or row['recipient_id'],
                  **({'kind': row['kind']} if row['kind'] in ('group', 'conversation') else {}),
                  'available': account['status'] == 'connected' and bool(account['credentials_ciphertext'])
                  and (account['provider'] != 'wechat' or bool(row['context_ciphertext']))} for row in rows[:limit]]
        return {'provider': account['provider'], 'items': items,
                'next_cursor': items[-1]['recipient_id'] if len(rows) > limit else ''}

    def cache_notification_groups(self, owner, account_id, credential_revision, groups):
        with self._connect() as connection:
            account = connection.execute("""
                SELECT id FROM channel_accounts WHERE id = %s AND owner_user_id = %s AND provider = 'feishu'
                    AND status = 'connected' AND archived_at IS NULL AND credential_revision = %s FOR UPDATE
            """, (account_id, owner, credential_revision)).fetchone()
            if not account:
                raise GatewayError(409, 'ACCOUNT_STATE_CHANGED', '账号状态已经变化，请刷新后重试')
            for group in groups:
                connection.execute("""
                    INSERT INTO channel_notification_targets(account_id, recipient_id, label, kind)
                    VALUES(%s, %s, %s, 'group') ON CONFLICT(account_id, recipient_id) DO UPDATE SET
                        label = EXCLUDED.label, kind = 'group', updated_at = CURRENT_TIMESTAMP
                """, (account_id, group['recipient_id'], group['label']))

    def cache_wecom_notification_sessions(self, owner, account_id, credential_revision, sessions):
        with self._connect() as connection:
            account = connection.execute("""
                SELECT id FROM channel_accounts WHERE id = %s AND owner_user_id = %s AND provider = 'wecom'
                    AND status = 'connected' AND archived_at IS NULL AND credential_revision = %s FOR UPDATE
            """, (account_id, owner, credential_revision)).fetchone()
            if not account:
                raise GatewayError(409, 'ACCOUNT_STATE_CHANGED', '账号状态已经变化，请刷新后重试')
            context = self._payload_cipher.encrypt(owner, {'transport': 'cli'})
            # The API returns the complete currently-sendable set (at most 20).
            # Replace stale targets so a removed bot/group cannot remain selectable.
            connection.execute(
                'DELETE FROM channel_notification_targets WHERE account_id = %s',
                (account_id,),
            )
            for session in sessions:
                connection.execute("""
                    INSERT INTO channel_notification_targets(
                        account_id, recipient_id, context_ciphertext, label, kind
                    ) VALUES(%s, %s, %s, %s, %s)
                    ON CONFLICT(account_id, recipient_id) DO UPDATE SET
                        context_ciphertext = EXCLUDED.context_ciphertext,
                        label = EXCLUDED.label, kind = EXCLUDED.kind,
                        updated_at = CURRENT_TIMESTAMP
                """, (account_id, session['recipient_id'], context,
                      session['label'], session['kind']))

    def set_default_recipient(self, owner, account_id, recipient_id, credential_revision):
        with self._connect() as connection:
            account = connection.execute("""
                SELECT id FROM channel_accounts WHERE id = %s AND owner_user_id = %s
                    AND archived_at IS NULL AND credential_revision = %s FOR UPDATE
            """, (account_id, owner, credential_revision)).fetchone()
            if not account:
                raise GatewayError(409, 'ACCOUNT_STATE_CHANGED', '账号状态已经变化，请刷新后重试')
            if recipient_id and not connection.execute("""
                SELECT 1 FROM channel_notification_targets target JOIN channel_accounts account
                    ON account.id = target.account_id WHERE target.account_id = %s AND target.recipient_id = %s
                    AND account.status = 'connected' AND account.credentials_ciphertext <> ''
                    AND (account.provider <> 'wechat' OR target.context_ciphertext IS NOT NULL)
            """, (account_id, recipient_id)).fetchone():
                raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '接收对象不可用，请重新选择')
            return connection.execute("""
                UPDATE channel_accounts SET default_recipient_id = %s, updated_at = CURRENT_TIMESTAMP WHERE id = %s
                RETURNING *
            """, (recipient_id, account_id)).fetchone()

    def notification_context(self, owner, account_id, recipient_id, provider):
        with self._connect() as connection:
            row = connection.execute('''
                SELECT target.context_ciphertext FROM channel_notification_targets target
                JOIN channel_accounts account ON account.id = target.account_id
                WHERE account.id = %s AND account.owner_user_id = %s AND account.provider = %s
                    AND account.status = 'connected' AND account.credentials_ciphertext <> ''
                    AND target.recipient_id = %s
            ''', (account_id, owner, provider, recipient_id)).fetchone()
        context = {}
        if row and row['context_ciphertext']:
            context = self._payload_cipher.decrypt(owner, row['context_ciphertext'])
        if not row or (provider == 'wechat' and not context.get('context_token')):
            raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '通知接收对象不可用，请重新连接并发送消息')
        return context

    @staticmethod
    def _notification_id(owner, payload, retry_of='', idempotency_key=''):
        identity = '\x00'.join([owner, payload['event_id'], payload['channel'], payload['account_id'],
                                payload['recipient_id'], retry_of, idempotency_key])
        return hashlib.sha256(identity.encode()).hexdigest()

    def notification_retry_receipt(self, owner, payload, retry_of, key):
        try:
            return self.get_notification(owner, self._notification_id(owner, payload, retry_of, key))
        except GatewayError as error:
            if error.code != 'NOTIFICATION_NOT_FOUND':
                raise
            return None

    def enqueue_notification(self, owner, payload, *, retry_of='', idempotency_key='', occurred_at='',
                             confirm_duplicate_risk=False, source_notification_id=''):
        notice_id = self._notification_id(owner, payload, retry_of, idempotency_key)
        with self._connect() as connection:
            account = connection.execute('''
                SELECT id, status FROM channel_accounts WHERE id = %s AND owner_user_id = %s
                    AND provider = %s FOR UPDATE
            ''', (payload['account_id'], owner, payload['channel'])).fetchone()
            if not account:
                raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '通知账号不可用')
            # Recheck after the account lock: another request may have created
            # this receipt while Core verification was in flight.
            existing = connection.execute('''
                SELECT * FROM channel_outbox WHERE id = %s AND account_id = %s AND purpose = 'notification'
            ''', (notice_id, account['id'])).fetchone()
            if existing:
                return self._notification_view(existing)
            if account['status'] != 'connected':
                raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '通知账号不可用')
            metadata = {'notification': payload, 'owner_user_id': owner,
                        'retry_of': retry_of, 'occurred_at': occurred_at,
                        'source_notification_id': source_notification_id}
            checkpoint = None
            if retry_of:
                chain = connection.execute('''
                    SELECT * FROM channel_outbox WHERE account_id = %s AND provider = %s
                        AND recipient_id = %s AND purpose = 'notification'
                        AND metadata -> 'notification' ->> 'event_id' = %s
                    ORDER BY created_sequence FOR UPDATE
                ''', (account['id'], payload['channel'], payload['recipient_id'], payload['event_id'])).fetchall()
                if not any(row['id'] == retry_of for row in chain):
                    raise GatewayError(404, 'NOTIFICATION_NOT_FOUND', '通知记录不存在')
                if any(row['status'] in ('pending', 'retry_wait', 'sending', 'sent') for row in chain):
                    raise GatewayError(409, 'NOTIFICATION_STATE_CHANGED', '当前通知不能重发')
                checkpoint = chain[-1]
                if checkpoint['status'] not in ('dead', 'unknown'):
                    raise GatewayError(409, 'NOTIFICATION_STATE_CHANGED', '当前通知不能重发')
                # A confirmed retry supersedes its ancestor's unknown outcome.
                # Unknown records on a separate historical branch still need
                # confirmation; clicking a failed ancestor cannot hide them.
                by_id = {row['id']: row for row in chain}
                ancestors = set()
                parent = checkpoint
                while parent:
                    links = self._dict(parent['metadata'])
                    parent_id = links.get('retry_checkpoint_of') or links.get('retry_of')
                    if not parent_id or parent_id in ancestors:
                        break
                    ancestors.add(parent_id)
                    parent = by_id.get(parent_id)
                unknown = checkpoint['status'] == 'unknown' or any(
                    row['status'] == 'unknown' and row['id'] not in ancestors for row in chain)
                if unknown and not confirm_duplicate_risk:
                    raise GatewayError(409, 'NOTIFICATION_CONFIRMATION_REQUIRED', '发送结果未知，重发可能产生重复通知')
                metadata['retry_checkpoint_of'] = checkpoint['id']
            event_label = {'succeeded': '任务完成', 'failed': '任务失败', 'waiting': '等待处理'}[payload['event']]
            text = f"{payload['title']}\n{event_label} {occurred_at}\n{payload['body']}\n任务：{payload['task_id']}"
            connection.execute('''
                INSERT INTO channel_outbox(id, account_id, dedupe_key, provider, order_key, sequence,
                    recipient_id, provider_context, text, intent_kind, purpose, metadata,
                    rendered_parts, next_part_index, provider_state)
                VALUES(%s,%s,%s,%s,%s,0,%s,'{}',%s,'message','notification',%s::jsonb,%s::jsonb,%s,%s::jsonb)
                ON CONFLICT (account_id, dedupe_key) DO NOTHING
            ''', (notice_id, payload['account_id'], 'notification:' + notice_id, payload['channel'],
                  'notification:' + payload['recipient_id'], payload['recipient_id'],
                  text, self._json(metadata), self._json(self._list(checkpoint['rendered_parts']) if checkpoint else []),
                  checkpoint['next_part_index'] if checkpoint else 0,
                  self._json(self._dict(checkpoint['provider_state']) if checkpoint else {})))
        return self.get_notification(owner, notice_id)

    def get_notification(self, owner, notice_id):
        with self._connect() as connection:
            row = connection.execute('''
                SELECT outbox.* FROM channel_outbox outbox JOIN channel_accounts account
                    ON account.id = outbox.account_id
                WHERE outbox.id = %s AND account.owner_user_id = %s AND outbox.purpose = 'notification'
            ''', (notice_id, owner)).fetchone()
        if not row:
            raise GatewayError(404, 'NOTIFICATION_NOT_FOUND', '通知记录不存在')
        return self._notification_view(row)

    def notification_history(self, owner, task_id, cursor=0, limit=20):
        with self._connect() as connection:
            rows = connection.execute('''
                SELECT outbox.* FROM channel_outbox outbox JOIN channel_accounts account
                    ON account.id = outbox.account_id
                WHERE account.owner_user_id = %s AND outbox.purpose = 'notification'
                    AND outbox.metadata -> 'notification' ->> 'task_id' = %s
                    AND outbox.created_sequence > %s ORDER BY outbox.created_sequence LIMIT %s
            ''', (owner, task_id, cursor, limit + 1)).fetchall()
        return {'items': [self._notification_view(row) for row in rows[:limit]],
                'next_cursor': str(rows[limit - 1]['created_sequence']) if len(rows) > limit else ''}

    def _notification_view(self, row):
        metadata = self._dict(row['metadata'])
        status = {'pending': 'queued', 'retry_wait': 'queued', 'dead': 'failed'}.get(row['status'], row['status'])
        return {'notification_id': row['id'], 'outbox_id': row['id'], 'status': status,
                'source_notification_id': metadata.get('source_notification_id', ''),
                'reason': row['last_error'] or '', 'attempt_count': row['attempt_count'],
                'retryable': status in ('failed', 'unknown'), 'retry_of': metadata.get('retry_of', ''),
                'created_at': row['created_at'], 'updated_at': row['updated_at'],
                'occurred_at': metadata.get('occurred_at', ''),
                'payload': metadata['notification']}

    def finish_notification(self, notice_id, claim_owner, status, reason):
        with self._connect() as connection:
            return bool(connection.execute('''
                UPDATE channel_outbox SET status = %s, last_error = %s, lease_owner = NULL,
                    lease_until = NULL, next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND purpose = 'notification' AND status = 'sending'
                    AND lease_owner = %s AND lease_until >= CURRENT_TIMESTAMP RETURNING id
            ''', (status, reason, notice_id, claim_owner)).fetchone())

    @staticmethod
    def _expire_notifications(connection):
        # A process may die after the platform accepted a part and before the
        # checkpoint committed. A request identifier is not proof of idempotency.
        connection.execute('''
            UPDATE channel_outbox SET status = 'unknown', last_error = 'NOTIFICATION_DELIVERY_UNKNOWN',
                lease_owner = NULL, lease_until = NULL, updated_at = CURRENT_TIMESTAMP
            WHERE purpose = 'notification' AND status = 'sending' AND lease_until < CURRENT_TIMESTAMP
        ''')

    def reserve_session(
        self,
        *,
        session_id: str,
        owner_user_id: str,
        provider: str,
        idempotency_key: str | None,
        expires_at: dt.datetime,
        requested_account_id: str | None = None,
        reuse_existing: bool = False,
        state_ciphertext: str | None = None,
    ) -> tuple[dict[str, Any], bool]:
        with self._connect() as connection:
            connection.execute(
                'SELECT pg_advisory_xact_lock(hashtext(%s))',
                (f'{owner_user_id}:{provider}',),
            )
            if requested_account_id:
                account = connection.execute('''
                    SELECT id FROM channel_accounts WHERE id = %s AND owner_user_id = %s
                        AND provider = %s AND archived_at IS NULL
                ''', (requested_account_id, owner_user_id, provider)).fetchone()
                if not account:
                    raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '重连账号不存在')
            if idempotency_key:
                existing = connection.execute(
                    """
                    SELECT * FROM channel_connection_sessions
                    WHERE owner_user_id = %s AND provider = %s AND idempotency_key = %s
                    """,
                    (owner_user_id, provider, idempotency_key),
                ).fetchone()
                if existing:
                    if (not reuse_existing or requested_account_id is not None) and (
                        existing.get('requested_account_id') != requested_account_id
                    ):
                        raise GatewayError(409, 'ACCOUNT_STATE_CHANGED', '该连接请求已用于其他账号')
                    return existing, False
            if reuse_existing and not requested_account_id:
                accounts = connection.execute('''
                    SELECT id, credentials_ciphertext FROM channel_accounts
                    WHERE owner_user_id = %s AND provider = %s AND archived_at IS NULL ORDER BY id
                ''', (owner_user_id, provider)).fetchall()
                if len(accounts) > 1:
                    raise GatewayError(409, 'ACCOUNT_SELECTION_REQUIRED', '请选择要复用的飞书机器人')
                if accounts:
                    if not accounts[0]['credentials_ciphertext']:
                        raise GatewayError(409, 'FEISHU_REAUTHORIZATION_REQUIRED', '请选择原机器人重新授权')
                    requested_account_id = accounts[0]['id']
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
                if active.get('requested_account_id') != requested_account_id:
                    raise GatewayError(409, 'ACCOUNT_STATE_CHANGED', '请先完成或取消当前连接请求')
                return active, False
            row = connection.execute(
                """
                INSERT INTO channel_connection_sessions(
                    id, owner_user_id, provider, idempotency_key,
                    status, revision, qr_version, message, expires_at, requested_account_id, provider_state_ciphertext
                )
                VALUES(%s, %s, %s, %s, 'preparing', 1, 1, %s, %s, %s, %s)
                RETURNING *
                """,
                (
                    session_id,
                    owner_user_id,
                    provider,
                    idempotency_key,
                    '正在生成二维码',
                    expires_at,
                    requested_account_id,
                    state_ciphertext,
                ),
            ).fetchone()
            return row, True

    def validate_reconnect(self, session_id, owner, provider, identity):
        session = self.get_session(owner, session_id)
        expected = session.get('requested_account_id') if session else None
        if expected:
            account = self.get_account(owner, expected)
            if not account or account['provider'] != provider or account['external_id_hash'] != identity:
                self.mark_failed(session_id, session['qr_version'], code='ACCOUNT_IDENTITY_MISMATCH',
                                 message='重连身份与原账号不一致，请重新连接', retryable=False)
                raise GatewayError(409, 'ACCOUNT_IDENTITY_MISMATCH', '重连身份与原账号不一致')

    def assert_identity_available(self, owner, provider, identity):
        with self._connect() as connection:
            row = connection.execute('''
                SELECT owner_user_id FROM channel_accounts WHERE provider = %s AND external_id_hash = %s
            ''', (provider, identity)).fetchone()
        if row and row['owner_user_id'] != owner:
            raise GatewayError(409, 'ACCOUNT_ALREADY_BOUND', '该账号身份已被绑定')

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
        runtime_fence: RuntimeFence | None = None,
    ) -> dict[str, Any] | None:
        account_id = f'ca_{uuid.uuid4().hex}'
        with self._connect() as connection:
            if runtime_fence is not None:
                self._lock_runtime_fence(connection, runtime_fence)
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
                WHERE %s AND channel_accounts.archived_at IS NULL
                  AND NOT (channel_accounts.provider = 'feishu' AND EXCLUDED.status = 'provisioning')
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
                SELECT account.*,
                    CASE WHEN account.provider <> 'wechat' THEN TRUE ELSE EXISTS (
                        SELECT 1 FROM channel_notification_targets target
                        WHERE target.account_id = account.id
                          AND target.context_ciphertext IS NOT NULL
                    ) END AS notification_ready
                FROM channel_accounts account
                WHERE account.id = %s AND account.owner_user_id = %s
                  AND account.archived_at IS NULL
                """,
                (account_id, owner_user_id),
            ).fetchone()

    def list_accounts(self, owner_user_id: str, provider: str) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT account.*,
                        CASE WHEN account.provider <> 'wechat' THEN TRUE ELSE EXISTS (
                            SELECT 1 FROM channel_notification_targets target
                            WHERE target.account_id = account.id
                              AND target.context_ciphertext IS NOT NULL
                        ) END AS notification_ready
                    FROM channel_accounts account
                    WHERE account.owner_user_id = %s AND account.provider = %s
                      AND account.archived_at IS NULL
                    ORDER BY account.updated_at DESC
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

    def _remember_notification_target(self, connection, envelope: InboundEnvelope) -> None:
        # Register the known recipient atomically with its authenticated inbox entry.
        context = dict(envelope.sensitive_context)
        if envelope.provider_context.get('context_token'):
            context['context_token'] = envelope.provider_context['context_token']
        ciphertext = self._payload_cipher.encrypt(envelope.owner_user_id, context) if context else None
        connection.execute('''
            INSERT INTO channel_notification_targets(account_id, recipient_id, context_ciphertext)
            VALUES(%s, %s, %s)
            ON CONFLICT(account_id, recipient_id) DO UPDATE SET
                context_ciphertext = COALESCE(EXCLUDED.context_ciphertext,
                    channel_notification_targets.context_ciphertext),
                updated_at = CURRENT_TIMESTAMP
        ''', (envelope.account_id, envelope.recipient_id, ciphertext))

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
                SELECT status, provider, owner_user_id
                FROM channel_accounts
                WHERE id = %s
                FOR SHARE
                """,
                (account_id,),
            ).fetchone()
            if not account or account['status'] != 'connected':
                raise RuntimeError('channel account is not connected')
            for envelope in envelopes:
                if (envelope.account_id != account_id or envelope.provider != account['provider']
                        or envelope.owner_user_id != account['owner_user_id']):
                    raise RuntimeError('Channel inbound account binding is invalid')
                row = connection.execute(
                    """
                    INSERT INTO channel_inbox(
                        id, account_id, provider, message_key, order_key,
                        external_address_hash, owner_user_id, recipient_id,
                        text, provider_context, sensitive_payload_ciphertext
                    )
                    VALUES(
                        %s, %s, %s, %s, %s,
                        %s, %s, %s, %s, %s::jsonb, %s
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
                        self._sensitive_ciphertext(envelope),
                    ),
                ).fetchone()
                inserted += int(row is not None)
                if row is not None:
                    self._remember_notification_target(connection, envelope)
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
                SET last_poll_at = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (account_id,),
            )
        return inserted

    def find_inbound_by_provider_context(
        self,
        *,
        provider: str,
        account_id: str,
        recipient_id: str,
        expected_context: dict[str, Any],
    ) -> dict[str, Any] | None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT text, provider_context
                FROM channel_inbox
                WHERE provider = %s
                  AND account_id = %s
                  AND recipient_id = %s
                  AND provider_context @> %s::jsonb
                ORDER BY ingest_sequence DESC
                LIMIT 1
                """,
                (
                    provider,
                    account_id,
                    recipient_id,
                    self._json(expected_context),
                ),
            ).fetchone()
        if not row:
            return None
        return {
            'text': str(row['text']),
            'provider_context': self._dict(row['provider_context']),
        }

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
        retained_provider_context: dict[str, Any],
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
                    provider_context = %s::jsonb,
                    sensitive_payload_ciphertext = NULL,
                    lease_owner = NULL,
                    lease_until = NULL,
                    last_error = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                """,
                (self._json(retained_provider_context), inbox_id),
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
        retained_provider_context: dict[str, Any],
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
                connection.execute(
                    """
                    UPDATE channel_inbox
                    SET provider_context = %s::jsonb,
                        sensitive_payload_ciphertext = NULL
                    WHERE id = %s
                    """,
                    (self._json(retained_provider_context), inbox_id),
                )
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
            self._expire_notifications(connection)
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
                          AND earlier.status NOT IN ('sent', 'dead', 'skipped', 'unknown')
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
        after_sequence: int = 0,
    ) -> list[ClaimedOutbound]:
        with self._connect() as connection:
            rows = connection.execute(
                """
                SELECT *
                FROM channel_outbox
                WHERE provider = %s
                  AND status = 'sent'
                  AND created_sequence > %s
                  AND metadata @> %s::jsonb
                ORDER BY created_sequence
                LIMIT %s
                """,
                (
                    provider,
                    max(0, after_sequence),
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

    def list_sent_task_artifact_outbounds(
        self,
        *,
        provider: str,
        limit: int,
        after_sequence: int = 0,
        monitor_version: int,
    ) -> list[ClaimedOutbound]:
        with self._connect() as connection:
            rows = connection.execute(
                """
                SELECT *
                FROM channel_outbox
                WHERE provider = %s
                  AND status = 'sent'
                  AND created_sequence > %s
                  AND metadata @> %s::jsonb
                ORDER BY created_sequence
                LIMIT %s
                """,
                (
                    provider,
                    max(0, after_sequence),
                    self._json({
                        'task_monitor': True,
                        'task_artifact_monitor_version': monitor_version,
                    }),
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
        provider_context: dict[str, Any] | None = None,
    ) -> dict[str, int]:
        prefix = f'task-artifact:{parent.outbox_id}:{part_index}:'
        order_key = f'task-artifact:{parent.outbox_id}:{part_index}'
        chat_id = str(
            parent.provider_context.get('chat_id')
            or parent.recipient_id
        )
        child_provider_context = (
            dict(provider_context)
            if provider_context is not None
            else {'chat_id': chat_id}
        )
        child_provider_context.setdefault('chat_id', chat_id)
        with self._connect() as connection:
            for sequence, artifact in enumerate(artifacts):
                artifact_key = str(artifact.get('artifact_key') or '')
                source = str(artifact.get('source') or '')
                delivery_id = str(artifact.get('delivery_id') or '')
                kind = str(artifact.get('kind') or 'image')
                if (
                    len(artifact_key) != 64
                    or not source
                    or len(source) > 2048
                    or not delivery_id
                    or len(delivery_id) > 512
                    or kind not in {'image', 'file'}
                ):
                    continue
                rendered_part = {
                    'kind': kind,
                    'source': source,
                    'delivery_id': delivery_id,
                }
                if kind == 'image':
                    rendered_part['alt'] = str(
                        artifact.get('caption') or ''
                    )[:300]
                else:
                    rendered_part['filename'] = str(
                        artifact.get('filename') or 'lazymind-output'
                    )[:255]
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
                    ON CONFLICT(account_id, dedupe_key) DO UPDATE
                    SET rendered_parts = EXCLUDED.rendered_parts,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE channel_outbox.status = 'pending'
                    """,
                    (
                        f'co_{uuid.uuid4().hex}',
                        parent.account_id,
                        f'{prefix}{artifact_key}',
                        parent.provider,
                        order_key,
                        sequence,
                        parent.recipient_id,
                        self._json(child_provider_context),
                        self._json({
                            'task_artifact': True,
                            'parent_outbox_id': parent.outbox_id,
                            'parent_part_index': part_index,
                        }),
                        self._json([rendered_part]),
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

    def sync_task_status_outbound(
        self,
        *,
        parent: ClaimedOutbound,
        part_index: int,
        text: str,
    ) -> str:
        dedupe_key = f'task-status:{parent.outbox_id}:{part_index}:terminal'
        with self._connect() as connection:
            row = connection.execute(
                """
                INSERT INTO channel_outbox(
                    id, inbox_id, account_id, dedupe_key,
                    provider, order_key, sequence,
                    recipient_id, provider_context, text, intent_kind,
                    purpose, metadata, rendered_parts, status
                )
                VALUES(
                    %s, NULL, %s, %s,
                    %s, %s, 0,
                    %s, %s::jsonb, %s, 'task_status',
                    'task_status', %s::jsonb, %s::jsonb, 'pending'
                )
                ON CONFLICT(account_id, dedupe_key) DO UPDATE
                SET text = EXCLUDED.text,
                    rendered_parts = EXCLUDED.rendered_parts,
                    updated_at = CURRENT_TIMESTAMP
                WHERE channel_outbox.status = 'pending'
                RETURNING status
                """,
                (
                    f'co_{uuid.uuid4().hex}',
                    parent.account_id,
                    dedupe_key,
                    parent.provider,
                    f'task-status:{parent.outbox_id}:{part_index}',
                    parent.recipient_id,
                    self._json(parent.provider_context),
                    text,
                    self._json({
                        'task_status': True,
                        'parent_outbox_id': parent.outbox_id,
                    }),
                    self._json([{'kind': 'text', 'text': text}]),
                ),
            ).fetchone()
            if row:
                return str(row['status'])
            existing = connection.execute(
                """
                SELECT status FROM channel_outbox
                WHERE account_id = %s AND dedupe_key = %s
                """,
                (parent.account_id, dedupe_key),
            ).fetchone()
        return str(existing['status']) if existing else ''

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

    def _claimed_inbound(
        self,
        row: dict[str, Any] | None,
    ) -> ClaimedInbound | None:
        if not row:
            return None
        provider_context = self._dict(row['provider_context'])
        ciphertext = str(
            row.get('sensitive_payload_ciphertext') or ''
        )
        if ciphertext:
            if self._payload_cipher is None:
                raise RuntimeError(
                    'Channel inbox contains an encrypted payload but no '
                    'payload cipher is configured'
                )
            provider_context.update(
                self._payload_cipher.decrypt(
                    str(row['owner_user_id']),
                    ciphertext,
                )
            )
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
            provider_context=provider_context,
            attempt_count=int(row['attempt_count']),
        )

    def _sensitive_ciphertext(self, envelope: InboundEnvelope) -> str | None:
        if not envelope.sensitive_context:
            return None
        if self._payload_cipher is None:
            raise RuntimeError(
                'Sensitive channel input requires a payload cipher'
            )
        return self._payload_cipher.encrypt(
            envelope.owner_user_id,
            envelope.sensitive_context,
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
            created_sequence=int(row['created_sequence']),
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
                SELECT status, provider, owner_user_id
                FROM channel_accounts
                WHERE id = %s
                FOR SHARE
                """,
                (account_id,),
            ).fetchone()
            if not account or account['status'] != 'connected':
                raise RuntimeError('channel account is not connected')
            if (envelope.account_id != account_id or envelope.provider != 'feishu'
                    or account['provider'] != 'feishu' or envelope.owner_user_id != account['owner_user_id']):
                raise RuntimeError('Channel inbound account binding is invalid')
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
            snapshot = decode_snapshot(row.get('snapshot_json')) if row else {}
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
                    text, provider_context, sensitive_payload_ciphertext
                )
                VALUES(
                    %s, %s, %s, %s, %s,
                    %s, %s, %s, %s, %s::jsonb, %s
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
                    self._sensitive_ciphertext(envelope),
                ),
            ).fetchone()
            if not inserted:
                raise RuntimeError('Feishu inbox claim was lost')
            self._remember_notification_target(connection, envelope)
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
            snapshot = decode_snapshot(row.get('snapshot_json')) if row else {}
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
            snapshot = decode_snapshot(row.get('snapshot_json')) if row else {}
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
