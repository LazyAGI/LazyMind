import datetime as dt
import hashlib
import json
import os
import re
import sqlite3
import uuid
from pathlib import Path
from typing import Any
from urllib.parse import unquote, urlparse

from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
    RuntimeFence,
)
from channel_gateway.common.infrastructure.postgres import (
    GatewayStore,
    PostgresRuntimeLease,
)


_BOOLEAN_COLUMNS = {
    'cleanup_pending',
    'error_retryable',
    'welcome_pending',
}
_CAST_RE = re.compile(r'::(?:jsonb|text)\b', re.IGNORECASE)
_FOR_LOCK_RE = re.compile(
    r'\s+FOR\s+(?:UPDATE|SHARE)(?:\s+SKIP\s+LOCKED)?\b',
    re.IGNORECASE,
)
_PLACEHOLDER_RE = re.compile(r'%s')
_RETURNING_ALIAS_RE = re.compile(
    r'RETURNING\s+[A-Za-z_][A-Za-z0-9_]*\.\*',
    re.IGNORECASE,
)


def _database_path(dsn: str) -> str:
    parsed = urlparse(dsn)
    path = unquote(parsed.path)
    if parsed.netloc:
        path = f'//{parsed.netloc}{path}'
    if os.name == 'nt' and re.match(r'^/[A-Za-z]:/', path):
        path = path[1:]
    return os.path.normpath(path)


def _datetime_adapter(value: dt.datetime) -> str:
    if value.tzinfo is not None:
        value = value.astimezone(dt.timezone.utc).replace(tzinfo=None)
    return value.isoformat(sep=' ', timespec='microseconds')


def _timestamp_converter(raw: bytes) -> dt.datetime:
    value = dt.datetime.fromisoformat(raw.decode('utf-8').replace('Z', '+00:00'))
    return value if value.tzinfo else value.replace(tzinfo=dt.timezone.utc)


def _row_factory(cursor: sqlite3.Cursor, row: tuple[Any, ...]) -> dict[str, Any]:
    result = {
        description[0]: row[index]
        for index, description in enumerate(cursor.description)
    }
    for key in _BOOLEAN_COLUMNS:
        if result.get(key) is not None:
            result[key] = bool(result[key])
    return result


def _json_contains(actual_raw: str, expected_raw: str) -> int:
    try:
        actual = json.loads(actual_raw)
        expected = json.loads(expected_raw)
    except (TypeError, json.JSONDecodeError):
        return 0

    def contains(actual_value: Any, expected_value: Any) -> bool:
        if isinstance(expected_value, dict):
            return isinstance(actual_value, dict) and all(
                key in actual_value
                and contains(actual_value[key], expected_item)
                for key, expected_item in expected_value.items()
            )
        if isinstance(expected_value, list):
            return isinstance(actual_value, list) and all(
                any(contains(item, expected_item) for item in actual_value)
                for expected_item in expected_value
            )
        return actual_value == expected_value

    return int(contains(actual, expected))


def _json_set(
    document_raw: str,
    path: str,
    value_raw: str,
    create_missing: int,
) -> str:
    del create_missing
    try:
        document = json.loads(document_raw)
    except (TypeError, json.JSONDecodeError):
        document = {}
    try:
        value = json.loads(value_raw)
    except (TypeError, json.JSONDecodeError):
        value = value_raw
    if not isinstance(document, dict):
        document = {}
    key = str(path).strip('{}')
    document[key] = value
    return json.dumps(document, ensure_ascii=False, separators=(',', ':'))


def _translate(statement: str) -> str:
    if 'pg_advisory_xact_lock' in statement:
        return 'SELECT 1'
    sql = _PLACEHOLDER_RE.sub('?', statement)
    sql = _CAST_RE.sub('', sql)
    sql = _FOR_LOCK_RE.sub('', sql)
    sql = _RETURNING_ALIAS_RE.sub('RETURNING *', sql)
    sql = re.sub(
        r"CURRENT_TIMESTAMP\s*-\s*INTERVAL\s*'60 seconds'",
        "datetime(CURRENT_TIMESTAMP, '-60 seconds')",
        sql,
        flags=re.IGNORECASE,
    )
    sql = re.sub(
        r"CURRENT_TIMESTAMP\s*-\s*INTERVAL\s*'1 day'",
        "datetime(CURRENT_TIMESTAMP, '-1 day')",
        sql,
        flags=re.IGNORECASE,
    )
    sql = re.sub(r'ARRAY\[\?\]', '?', sql, flags=re.IGNORECASE)
    sql = re.sub(
        r'metadata\s+@>\s+\?',
        'json_contains(metadata, ?) = 1',
        sql,
        flags=re.IGNORECASE,
    )
    sql = re.sub(
        r'provider_context\s*=\s*provider_context\s*\|\|\s*\?',
        'provider_context = json_patch(provider_context, ?)',
        sql,
        flags=re.IGNORECASE,
    )
    return sql


sqlite3.register_adapter(dt.datetime, _datetime_adapter)
sqlite3.register_converter('TIMESTAMPTZ', _timestamp_converter)


class _SQLiteConnection:
    def __init__(self, path: str):
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        self._connection = sqlite3.connect(
            path,
            timeout=30,
            detect_types=sqlite3.PARSE_DECLTYPES,
            isolation_level=None,
        )
        self._connection.row_factory = _row_factory
        self._connection.create_function(
            'md5',
            1,
            lambda value: hashlib.md5(
                str(value).encode('utf-8'),
                usedforsecurity=False,
            ).hexdigest(),
        )
        self._connection.create_function(
            'json_contains',
            2,
            _json_contains,
        )
        self._connection.create_function(
            'jsonb_set',
            4,
            _json_set,
        )
        self._connection.execute('PRAGMA foreign_keys = ON')
        self._connection.execute('PRAGMA busy_timeout = 30000')
        self._connection.execute('PRAGMA journal_mode = WAL')

    def __enter__(self) -> '_SQLiteConnection':
        self._connection.execute('BEGIN IMMEDIATE')
        return self

    def __exit__(self, exc_type, exc_value, traceback) -> None:
        try:
            if exc_type is None:
                self._connection.commit()
            else:
                self._connection.rollback()
        finally:
            self._connection.close()

    def execute(
        self,
        statement: str,
        parameters: tuple[Any, ...] | list[Any] = (),
    ) -> sqlite3.Cursor:
        if 'pg_advisory_xact_lock' in statement:
            parameters = ()
        return self._connection.execute(_translate(statement), parameters)


def _snapshot(value: Any) -> dict[str, Any]:
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except json.JSONDecodeError:
            return {}
    return dict(value) if isinstance(value, dict) else {}


class SQLiteGatewayStore(GatewayStore):
    """SQLite dialect for the container store contract used by local/Desktop."""

    def __init__(self, dsn: str):
        super().__init__(dsn)
        self._path = _database_path(dsn)

    def _connect(self) -> _SQLiteConnection:
        return _SQLiteConnection(self._path)

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
                credential_revision INTEGER NOT NULL DEFAULT 1,
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
                cleanup_pending BOOLEAN NOT NULL DEFAULT FALSE,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_runtime_leases (
                lease_key TEXT PRIMARY KEY,
                owner_id TEXT NOT NULL,
                generation INTEGER NOT NULL,
                lease_until TIMESTAMPTZ NOT NULL,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_checkpoints (
                account_id TEXT PRIMARY KEY
                    REFERENCES channel_accounts(id) ON DELETE CASCADE,
                cursor TEXT NOT NULL DEFAULT '',
                longpoll_timeout_ms INTEGER NOT NULL DEFAULT 35000,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_routes (
                account_id TEXT NOT NULL
                    REFERENCES channel_accounts(id) ON DELETE CASCADE,
                external_address_hash VARCHAR(64) NOT NULL,
                conversation_id TEXT NOT NULL,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, external_address_hash)
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_navigation_states (
                account_id TEXT NOT NULL
                    REFERENCES channel_accounts(id) ON DELETE CASCADE,
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
                account_id TEXT NOT NULL
                    REFERENCES channel_accounts(id) ON DELETE CASCADE,
                message_key VARCHAR(64) NOT NULL,
                status VARCHAR(32) NOT NULL,
                response_text TEXT,
                response_media_ciphertext TEXT,
                intent_kind VARCHAR(32),
                response_to_user_id TEXT,
                response_context_token TEXT,
                response_provider_context TEXT,
                claim_owner TEXT,
                reply_attempt_count INTEGER NOT NULL DEFAULT 0,
                reply_last_error TEXT,
                reply_next_attempt_at TIMESTAMPTZ,
                processed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (account_id, message_key)
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS channel_inbox (
                id TEXT PRIMARY KEY,
                ingest_sequence INTEGER UNIQUE,
                account_id TEXT NOT NULL
                    REFERENCES channel_accounts(id) ON DELETE CASCADE,
                provider VARCHAR(32) NOT NULL,
                message_key VARCHAR(128) NOT NULL,
                order_key VARCHAR(128) NOT NULL,
                external_address_hash VARCHAR(64) NOT NULL,
                owner_user_id TEXT NOT NULL,
                recipient_id TEXT NOT NULL,
                text TEXT NOT NULL,
                provider_context TEXT NOT NULL DEFAULT '{}',
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
            CREATE TABLE IF NOT EXISTS channel_outbox (
                id TEXT PRIMARY KEY,
                created_sequence INTEGER UNIQUE,
                inbox_id TEXT
                    REFERENCES channel_inbox(id) ON DELETE SET NULL,
                account_id TEXT NOT NULL
                    REFERENCES channel_accounts(id) ON DELETE CASCADE,
                dedupe_key VARCHAR(160) NOT NULL,
                provider VARCHAR(32) NOT NULL,
                order_key VARCHAR(128) NOT NULL,
                sequence INTEGER NOT NULL DEFAULT 0,
                recipient_id TEXT NOT NULL,
                provider_context TEXT NOT NULL DEFAULT '{}',
                text TEXT NOT NULL,
                intent_kind VARCHAR(64) NOT NULL,
                purpose VARCHAR(32) NOT NULL DEFAULT 'reply',
                metadata TEXT NOT NULL DEFAULT '{}',
                rendered_parts TEXT NOT NULL DEFAULT '[]',
                next_part_index INTEGER NOT NULL DEFAULT 0,
                provider_state TEXT NOT NULL DEFAULT '{}',
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
            CREATE TRIGGER IF NOT EXISTS channel_inbox_sequence
            AFTER INSERT ON channel_inbox
            WHEN NEW.ingest_sequence IS NULL
            BEGIN
                UPDATE channel_inbox
                SET ingest_sequence = (
                    SELECT COALESCE(MAX(ingest_sequence), 0) + 1
                    FROM channel_inbox
                    WHERE id <> NEW.id
                )
                WHERE id = NEW.id;
            END
            """,
            """
            CREATE TRIGGER IF NOT EXISTS channel_outbox_sequence
            AFTER INSERT ON channel_outbox
            WHEN NEW.created_sequence IS NULL
            BEGIN
                UPDATE channel_outbox
                SET created_sequence = (
                    SELECT COALESCE(MAX(created_sequence), 0) + 1
                    FROM channel_outbox
                    WHERE id <> NEW.id
                )
                WHERE id = NEW.id;
            END
            """,
        )
        indexes = (
            """
            CREATE UNIQUE INDEX IF NOT EXISTS
                channel_connection_sessions_idempotency_idx
            ON channel_connection_sessions(
                owner_user_id, provider, idempotency_key
            )
            WHERE idempotency_key IS NOT NULL
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_connection_sessions_owner_idx
            ON channel_connection_sessions(
                owner_user_id, provider, updated_at DESC
            )
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_accounts_owner_idx
            ON channel_accounts(owner_user_id, provider, updated_at DESC)
            """,
            """
            CREATE INDEX IF NOT EXISTS channel_processed_messages_time_idx
            ON channel_processed_messages(processed_at)
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
        )
        with self._connect() as connection:
            for statement in statements:
                connection.execute(statement)
            self._migrate_columns(connection)
            for statement in indexes:
                connection.execute(statement)
            self._migrate_legacy_outbox(connection)

    @staticmethod
    def _migrate_columns(connection: _SQLiteConnection) -> None:
        additions = {
            'channel_accounts': {
                'runtime_status': "VARCHAR(32) NOT NULL DEFAULT 'stopped'",
                'last_poll_at': 'TIMESTAMPTZ',
                'last_message_at': 'TIMESTAMPTZ',
                'last_error': 'TEXT',
                'credential_revision': 'INTEGER NOT NULL DEFAULT 1',
                'welcome_pending': 'BOOLEAN NOT NULL DEFAULT FALSE',
            },
            'channel_connection_sessions': {
                'cleanup_pending': 'BOOLEAN NOT NULL DEFAULT FALSE',
            },
            'channel_processed_messages': {
                'response_text': 'TEXT',
                'response_media_ciphertext': 'TEXT',
                'intent_kind': 'VARCHAR(32)',
                'response_to_user_id': 'TEXT',
                'response_context_token': 'TEXT',
                'response_provider_context': 'TEXT',
                'claim_owner': 'TEXT',
                'reply_attempt_count': 'INTEGER NOT NULL DEFAULT 0',
                'reply_last_error': 'TEXT',
                'reply_next_attempt_at': 'TIMESTAMPTZ',
            },
        }
        for table, columns in additions.items():
            rows = connection.execute(
                f'PRAGMA table_info({table})'
            ).fetchall()
            existing = {str(row['name']) for row in rows}
            for name, declaration in columns.items():
                if name not in existing:
                    connection.execute(
                        f'ALTER TABLE {table} '
                        f'ADD COLUMN {name} {declaration}'
                    )

    @staticmethod
    def _migrate_legacy_outbox(connection: _SQLiteConnection) -> None:
        connection.execute(
            """
            INSERT INTO channel_outbox(
                id, inbox_id, account_id, dedupe_key, provider, order_key,
                sequence, recipient_id, provider_context, text, intent_kind,
                purpose, metadata, status, attempt_count,
                next_attempt_at, last_error
            )
            SELECT
                'co_legacy_' || md5(old.account_id || ':' || old.message_key),
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
                        THEN json_object(
                            'context_token', old.response_context_token
                        )
                        ELSE '{}'
                    END
                ),
                old.response_text,
                COALESCE(old.intent_kind, 'chat'),
                'reply',
                '{}',
                CASE
                    WHEN old.status = 'reply_dead_letter' THEN 'dead'
                    ELSE 'pending'
                END,
                old.reply_attempt_count,
                old.reply_next_attempt_at,
                old.reply_last_error
            FROM channel_processed_messages AS old
            JOIN channel_accounts AS account ON account.id = old.account_id
            WHERE old.status IN ('reply_pending', 'reply_dead_letter')
              AND old.response_text IS NOT NULL
              AND old.response_to_user_id IS NOT NULL
            ON CONFLICT(account_id, dedupe_key) DO NOTHING
            """
        )

    def acquire_runtime_lease(
        self,
        account_id: str,
    ) -> PostgresRuntimeLease | None:
        owner_id = f'rl_{uuid.uuid4().hex}'
        lease_seconds = 120
        now = dt.datetime.now(dt.timezone.utc)
        lease_until = now + dt.timedelta(seconds=lease_seconds)
        with self._connect() as connection:
            row = connection.execute(
                """
                INSERT INTO channel_runtime_leases(
                    lease_key, owner_id, generation, lease_until
                )
                VALUES(%s, %s, 1, %s)
                ON CONFLICT(lease_key) DO UPDATE SET
                    owner_id = EXCLUDED.owner_id,
                    generation = channel_runtime_leases.generation + 1,
                    lease_until = EXCLUDED.lease_until,
                    updated_at = CURRENT_TIMESTAMP
                WHERE channel_runtime_leases.lease_until <= %s
                RETURNING lease_key, owner_id, generation
                """,
                (account_id, owner_id, lease_until, now),
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
        now = dt.datetime.now(dt.timezone.utc)
        lease_until = now + dt.timedelta(seconds=lease_seconds)
        with self._connect() as connection:
            row = connection.execute(
                """
                UPDATE channel_runtime_leases
                SET lease_until = %s,
                    updated_at = CURRENT_TIMESTAMP
                WHERE lease_key = %s
                  AND owner_id = %s
                  AND generation = %s
                  AND lease_until > %s
                RETURNING lease_key
                """,
                (
                    lease_until,
                    fence.key,
                    fence.owner_id,
                    fence.generation,
                    now,
                ),
            ).fetchone()
            return row is not None

    def orphaned_provisioning_accounts(
        self,
        provider: str,
    ) -> list[dict[str, Any]]:
        with self._connect() as connection:
            return list(
                connection.execute(
                    """
                    SELECT account.*,
                           (
                               SELECT session.id
                               FROM channel_connection_sessions AS session
                               WHERE session.account_id = account.id
                               ORDER BY session.updated_at DESC
                               LIMIT 1
                           ) AS registration_session_id
                    FROM channel_accounts AS account
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
                                'feishu-registration:' || session.id
                            )
                           AND lease.lease_until > CURRENT_TIMESTAMP
                          WHERE session.account_id = account.id
                      )
                    ORDER BY account.created_at
                    """,
                    (provider,),
                ).fetchall()
            )

    def claim_next_inbound(
        self,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> ClaimedInbound | None:
        now = dt.datetime.now(dt.timezone.utc)
        lease_until = now + dt.timedelta(seconds=lease_seconds)
        with self._connect() as connection:
            candidate = connection.execute(
                """
                SELECT inbox.id
                FROM channel_inbox AS inbox
                JOIN channel_accounts AS account
                  ON account.id = inbox.account_id
                WHERE (
                    inbox.status = 'pending'
                    OR (
                        inbox.status = 'retry_wait'
                        AND inbox.next_attempt_at <= %s
                    )
                    OR (
                        inbox.status = 'processing'
                        AND inbox.lease_until < %s
                    )
                )
                AND account.status = 'connected'
                AND NOT EXISTS (
                    SELECT 1
                    FROM channel_inbox AS earlier
                    WHERE earlier.account_id = inbox.account_id
                      AND earlier.order_key = inbox.order_key
                      AND earlier.status NOT IN (
                          'completed', 'ignored', 'dead'
                      )
                      AND earlier.ingest_sequence < inbox.ingest_sequence
                )
                ORDER BY inbox.ingest_sequence
                LIMIT 1
                """,
                (now, now),
            ).fetchone()
            if not candidate:
                return None
            row = connection.execute(
                """
                UPDATE channel_inbox
                SET status = 'processing',
                    lease_owner = %s,
                    lease_until = %s,
                    attempt_count = attempt_count + 1,
                    next_attempt_at = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                RETURNING *
                """,
                (claim_owner, lease_until, candidate['id']),
            ).fetchone()
        return self._claimed_inbound(row)

    def claim_next_outbound(
        self,
        claim_owner: str,
        *,
        lease_seconds: int,
    ) -> ClaimedOutbound | None:
        now = dt.datetime.now(dt.timezone.utc)
        lease_until = now + dt.timedelta(seconds=lease_seconds)
        with self._connect() as connection:
            candidate = connection.execute(
                """
                SELECT outbox.id
                FROM channel_outbox AS outbox
                WHERE (
                    outbox.status = 'pending'
                    OR (
                        outbox.status = 'retry_wait'
                        AND outbox.next_attempt_at <= %s
                    )
                    OR (
                        outbox.status = 'sending'
                        AND outbox.lease_until < %s
                    )
                )
                AND NOT EXISTS (
                    SELECT 1
                    FROM channel_outbox AS earlier
                    WHERE earlier.account_id = outbox.account_id
                      AND earlier.order_key = outbox.order_key
                      AND earlier.status NOT IN ('sent', 'dead')
                      AND earlier.created_sequence
                          < outbox.created_sequence
                )
                ORDER BY outbox.created_sequence
                LIMIT 1
                """,
                (now, now),
            ).fetchone()
            if not candidate:
                return None
            row = connection.execute(
                """
                UPDATE channel_outbox
                SET status = 'sending',
                    lease_owner = %s,
                    lease_until = %s,
                    attempt_count = attempt_count + 1,
                    next_attempt_at = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                RETURNING *
                """,
                (claim_owner, lease_until, candidate['id']),
            ).fetchone()
        return self._claimed_outbound(row)

    def save_feishu_workspace_state(
        self,
        account_id: str,
        external_address_hash: str,
        state: dict[str, Any],
    ) -> None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            value = _snapshot(row.get('snapshot_json')) if row else {}
            value['feishu_workspace'] = state
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
                    self._json(value),
                ),
            )

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
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            value = _snapshot(row.get('snapshot_json')) if row else {}
            workspace = value.get('feishu_workspace')
            if not isinstance(workspace, dict):
                workspace = {}
            if (
                operation_id
                and str(workspace.get('active_operation_id') or '')
                != operation_id
            ):
                return dict(workspace)
            workspace.update(patch)
            value['feishu_workspace'] = workspace
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
                    self._json(value),
                ),
            )
            return dict(workspace)

    def save_feishu_workspace_message(
        self,
        account_id: str,
        external_address_hash: str,
        message_id: str,
    ) -> None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            value = _snapshot(row.get('snapshot_json')) if row else {}
            workspace = value.get('feishu_workspace')
            if not isinstance(workspace, dict):
                workspace = {}
            workspace['message_id'] = message_id
            value['feishu_workspace'] = workspace
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
                    self._json(value),
                ),
            )

    def begin_new_conversation(
        self,
        account_id: str,
        external_address_hash: str,
        draft: dict[str, Any] | None = None,
    ) -> None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            current = _snapshot(row.get('snapshot_json')) if row else {}
            value = dict(current)
            value.pop('selection', None)
            value['new_conversation'] = draft or {}
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
                (account_id, external_address_hash, self._json(value)),
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
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            value = _snapshot(row.get('snapshot_json')) if row else {}
            value.pop('selection', None)
            value.pop('new_conversation', None)
            if consume_pending_turn:
                value.pop('pending_turn', None)
            connection.execute(
                """
                INSERT INTO channel_routes(
                    account_id, external_address_hash, conversation_id
                )
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
                    history_conversation_id =
                        EXCLUDED.history_conversation_id,
                    history_next_page_token =
                        EXCLUDED.history_next_page_token,
                    updated_at = CURRENT_TIMESTAMP
                """,
                (
                    account_id,
                    external_address_hash,
                    self._json(value),
                    history_conversation_id,
                    history_next_page_token or None,
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
        selection: dict[str, Any] = {
            'id': uuid.uuid4().hex,
            'kind': kind,
            'items': items,
        }
        if continuation:
            selection['continuation'] = continuation
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            raw = row.get('snapshot_json') if row else None
            if isinstance(raw, str):
                try:
                    raw = json.loads(raw)
                except json.JSONDecodeError:
                    raw = {}
            if isinstance(raw, list):
                value: dict[str, Any] = {
                    'selection': {
                        'kind': 'conversation',
                        'items': raw,
                    }
                }
            else:
                value = dict(raw) if isinstance(raw, dict) else {}
            value['selection'] = selection
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
                    self._json(value),
                    expires_at,
                ),
            )

    def clear_selection_snapshot(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            value = _snapshot(row.get('snapshot_json')) if row else {}
            value.pop('selection', None)
            connection.execute(
                """
                UPDATE channel_navigation_states
                SET snapshot_json = %s,
                    snapshot_expires_at = NULL,
                    updated_at = CURRENT_TIMESTAMP
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (
                    self._json(value),
                    account_id,
                    external_address_hash,
                ),
            )

    def save_pending_turn(
        self,
        account_id: str,
        external_address_hash: str,
        options: dict[str, Any],
    ) -> None:
        with self._connect() as connection:
            row = connection.execute(
                """
                SELECT snapshot_json FROM channel_navigation_states
                WHERE account_id = %s AND external_address_hash = %s
                """,
                (account_id, external_address_hash),
            ).fetchone()
            raw = row.get('snapshot_json') if row else None
            if isinstance(raw, str):
                try:
                    raw = json.loads(raw)
                except json.JSONDecodeError:
                    raw = {}
            if isinstance(raw, list):
                value: dict[str, Any] = {
                    'selection': {
                        'kind': 'conversation',
                        'items': raw,
                    }
                }
            else:
                value = dict(raw) if isinstance(raw, dict) else {}
            value['pending_turn'] = options
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
                (account_id, external_address_hash, self._json(value)),
            )
