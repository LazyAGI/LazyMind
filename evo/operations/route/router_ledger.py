from __future__ import annotations

import hashlib
import json
import re
import sqlite3
import time
from collections.abc import Mapping
from contextlib import closing, contextmanager
from pathlib import Path
from typing import Any, Iterator
from uuid import uuid4

SQLITE_TIMEOUT_SECONDS = 30.0
SQLITE_BUSY_TIMEOUT_MS = 30000
CLAIM_LEASE_SECONDS = 1800.0
EXPECTED_STATES = {'active', 'stopped', 'orphaned', 'claiming', 'deleting', 'managing'}
CLEANUP_POLICIES = {'thread_delete', 'manual'}
THREAD_ID = re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,127}')


class RouterLedgerError(RuntimeError):
    pass


class RouterAlgorithmLedger:
    def __init__(self, store_root: str | Path) -> None:
        self._root = Path(store_root)
        self._root.mkdir(parents=True, exist_ok=True)
        self._db_path = self._root / 'artifact_store.sqlite3'
        self._create_schema()

    def claim_algorithm(
        self,
        *,
        algorithm_id: str,
        thread_id: str,
        run_id: str,
        candidate_ref: str,
        router_admin_url: str,
        service_url: str,
        code_path: str,
        instance_count: int,
        config_hash: str,
        register_request_hash: str,
        cleanup_policy: str = 'thread_delete',
    ) -> tuple[dict[str, Any], str | None]:
        _require_cleanup_policy(cleanup_policy)
        _require_owner(thread_id, run_id)
        now = time.time()
        with self._transaction() as conn:
            self._recover_stale(conn, now)
            foreign_router = conn.execute(
                """
                SELECT algorithm_id
                FROM evo_router_algorithms
                WHERE router_admin_url != ? OR service_url != ?
                LIMIT 1
                """,
                (router_admin_url, service_url),
            ).fetchone()
            if foreign_router is not None:
                raise RouterLedgerError('all evo algorithms must use the same router')
            deleting_workspace = conn.execute(
                """
                SELECT algorithm_id
                FROM evo_router_algorithms
                WHERE code_path = ? AND expected_state = 'deleting'
                LIMIT 1
                """,
                (code_path,),
            ).fetchone()
            if deleting_workspace is not None:
                raise RouterLedgerError(f'algorithm workspace is being deleted: {code_path}')
            previous = self._row(conn, algorithm_id)
            if previous is not None and previous['expected_state'] in {'claiming', 'deleting', 'managing'}:
                raise RouterLedgerError(f'algorithm operation is already in progress: {algorithm_id}')
            changed = conn.execute(
                """
                INSERT INTO evo_router_algorithms(
                  algorithm_id, thread_id, run_id, candidate_ref,
                  router_admin_url, service_url, code_path, instance_count, config_hash,
                  register_request_hash, expected_state, cleanup_policy,
                  last_router_status, last_seen_at, created_at, updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'claiming', ?, '', NULL, ?, ?)
                ON CONFLICT(algorithm_id) DO UPDATE SET
                  instance_count = excluded.instance_count,
                  expected_state = 'claiming',
                  updated_at = excluded.updated_at
                WHERE evo_router_algorithms.thread_id = excluded.thread_id
                  AND evo_router_algorithms.router_admin_url = excluded.router_admin_url
                  AND evo_router_algorithms.service_url = excluded.service_url
                  AND evo_router_algorithms.register_request_hash = excluded.register_request_hash
                """,
                (
                    algorithm_id,
                    thread_id,
                    run_id,
                    candidate_ref,
                    router_admin_url,
                    service_url,
                    code_path,
                    instance_count,
                    config_hash,
                    register_request_hash,
                    cleanup_policy,
                    now,
                    now,
                ),
            ).rowcount
            if changed != 1:
                raise RouterLedgerError(f'algorithm ownership or registration conflict: {algorithm_id}')
            return _row_dict(self._row(conn, algorithm_id)), (
                None if previous is None else str(previous['expected_state'])
            )

    def resolve_claim(
        self,
        algorithm_id: str,
        thread_id: str,
        register_request_hash: str,
        claimed_at: float,
        expected_state: str | None,
    ) -> None:
        with self._transaction() as conn:
            where = """
                algorithm_id = ? AND thread_id = ?
                AND register_request_hash = ? AND expected_state = 'claiming'
                AND updated_at = ?
            """
            params = (algorithm_id, thread_id, register_request_hash, claimed_at)
            if expected_state is None:
                changed = conn.execute(f'DELETE FROM evo_router_algorithms WHERE {where}', params).rowcount
            else:
                _require_state(expected_state)
                changed = conn.execute(
                    f"""
                    UPDATE evo_router_algorithms
                    SET expected_state = ?, updated_at = ?
                    WHERE {where}
                    """,
                    (expected_state, time.time(), *params),
                ).rowcount
            if changed != 1:
                raise RouterLedgerError(f'algorithm claim is no longer current: {algorithm_id}')

    def get_algorithm(self, algorithm_id: str) -> dict[str, Any] | None:
        with self._transaction() as conn:
            self._recover_stale(conn, time.time())
            row = self._row(conn, algorithm_id)
        return None if row is None else _row_dict(row)

    def begin_delete(self, algorithm_id: str) -> tuple[dict[str, Any], str]:
        now = time.time()
        with self._transaction() as conn:
            self._recover_stale(conn, now)
            previous = self._row(conn, algorithm_id)
            if previous is None:
                raise RouterLedgerError(f'algorithm is not evo-owned: {algorithm_id}')
            _require_owner(str(previous['thread_id']), str(previous['run_id']))
            previous_state = str(previous['expected_state'])
            if previous_state in {'claiming', 'deleting', 'managing'}:
                raise RouterLedgerError(f'algorithm operation is already in progress: {algorithm_id}')
            workspace_busy = conn.execute(
                """
                SELECT algorithm_id
                FROM evo_router_algorithms
                WHERE code_path = ? AND algorithm_id != ?
                  AND expected_state IN ('claiming', 'deleting')
                LIMIT 1
                """,
                (previous['code_path'], algorithm_id),
            ).fetchone()
            if workspace_busy is not None:
                raise RouterLedgerError(f'algorithm workspace operation is in progress: {previous["code_path"]}')
            conn.execute(
                """
                UPDATE evo_router_algorithms
                SET expected_state = 'deleting', updated_at = ?
                WHERE algorithm_id = ?
                """,
                (now, algorithm_id),
            )
            return _row_dict(self._row(conn, algorithm_id)), previous_state

    def resolve_delete(
        self,
        algorithm_id: str,
        claimed_at: float,
        expected_state: str | None,
    ) -> None:
        with self._transaction() as conn:
            params = (algorithm_id, claimed_at)
            if expected_state is None:
                changed = conn.execute(
                    """
                    DELETE FROM evo_router_algorithms
                    WHERE algorithm_id = ? AND expected_state = 'deleting' AND updated_at = ?
                    """,
                    params,
                ).rowcount
            else:
                _require_state(expected_state)
                changed = conn.execute(
                    """
                    UPDATE evo_router_algorithms
                    SET expected_state = ?, updated_at = ?
                    WHERE algorithm_id = ? AND expected_state = 'deleting' AND updated_at = ?
                    """,
                    (expected_state, time.time(), *params),
                ).rowcount
            if changed != 1:
                raise RouterLedgerError(f'algorithm delete is no longer current: {algorithm_id}')

    def begin_manage(self, algorithm_id: str) -> tuple[dict[str, Any], str]:
        now = time.time()
        with self._transaction() as conn:
            self._recover_stale(conn, now)
            previous = self._row(conn, algorithm_id)
            if previous is None:
                raise RouterLedgerError(f'algorithm is not evo-owned: {algorithm_id}')
            previous_state = str(previous['expected_state'])
            if previous_state in {'claiming', 'deleting', 'managing'}:
                raise RouterLedgerError(f'algorithm operation is already in progress: {algorithm_id}')
            conn.execute(
                """
                UPDATE evo_router_algorithms
                SET expected_state = 'managing', updated_at = ?
                WHERE algorithm_id = ?
                """,
                (now, algorithm_id),
            )
            return _row_dict(self._row(conn, algorithm_id)), previous_state

    def resolve_manage(self, algorithm_id: str, claimed_at: float, expected_state: str) -> None:
        _require_state(expected_state)
        with self._transaction() as conn:
            changed = conn.execute(
                """
                UPDATE evo_router_algorithms
                SET expected_state = ?, updated_at = ?
                WHERE algorithm_id = ? AND expected_state = 'managing' AND updated_at = ?
                """,
                (expected_state, time.time(), algorithm_id, claimed_at),
            ).rowcount
            if changed != 1:
                raise RouterLedgerError(f'algorithm management is no longer current: {algorithm_id}')

    def list_algorithms(
        self,
        *,
        thread_id: str = '',
        algorithm_id: str = '',
        expected_state: str = '',
    ) -> list[dict[str, Any]]:
        query = 'SELECT * FROM evo_router_algorithms'
        clauses: list[str] = []
        params: list[str] = []
        if thread_id:
            clauses.append('thread_id = ?')
            params.append(thread_id)
        if algorithm_id:
            clauses.append('algorithm_id = ?')
            params.append(algorithm_id)
        if expected_state:
            _require_state(expected_state)
            clauses.append('expected_state = ?')
            params.append(expected_state)
        if clauses:
            query = f'{query} WHERE {" AND ".join(clauses)}'
        query = f'{query} ORDER BY updated_at DESC, algorithm_id'
        with self._transaction() as conn:
            self._recover_stale(conn, time.time())
            rows = conn.execute(query, params).fetchall()
        return [_row_dict(row) for row in rows]

    @contextmanager
    def router_mutation(self) -> Iterator[None]:
        token = uuid4().hex
        now = time.time()
        with self._transaction() as conn:
            changed = conn.execute(
                """
                UPDATE evo_router_mutation_lock
                SET claim_token = ?, expires_at = ?
                WHERE id = 1 AND (claim_token = '' OR expires_at <= ?)
                """,
                (token, now + CLAIM_LEASE_SECONDS, now),
            ).rowcount
            if changed != 1:
                raise RouterLedgerError('another Router mutation is in progress')
        try:
            yield
        finally:
            with self._transaction() as conn:
                conn.execute(
                    """
                    UPDATE evo_router_mutation_lock
                    SET claim_token = '', expires_at = 0
                    WHERE id = 1 AND claim_token = ?
                    """,
                    (token,),
                )

    def record_router_status(self, algorithm_id: str, status: Mapping[str, Any] | None) -> None:
        with self._transaction() as conn:
            conn.execute(
                """
                UPDATE evo_router_algorithms
                SET last_router_status = ?, last_seen_at = ?,
                    updated_at = CASE
                      WHEN expected_state IN ('claiming', 'deleting', 'managing') THEN updated_at
                      ELSE ?
                    END
                WHERE algorithm_id = ?
                """,
                (_json(status or {}), time.time(), time.time(), algorithm_id),
            )

    def record_ab_strategy(
        self,
        *,
        thread_id: str,
        candidate_ref: str,
        previous_strategy: Mapping[str, Any] | None,
        next_strategy: Mapping[str, Any] | None,
        reason: str,
    ) -> None:
        with self._transaction() as conn:
            conn.execute(
                """
                INSERT INTO evo_router_ab_strategy_audit(
                  thread_id, candidate_ref, previous_strategy_json,
                  next_strategy_json, reason, created_at
                )
                VALUES (?, ?, ?, ?, ?, ?)
                """,
                (
                    thread_id,
                    candidate_ref,
                    _json(previous_strategy or {}),
                    _json(next_strategy or {}),
                    reason,
                    time.time(),
                ),
            )

    def latest_ab_audit(self) -> dict[str, Any] | None:
        with closing(self._connect()) as conn:
            row = conn.execute(
                """
                SELECT *
                FROM evo_router_ab_strategy_audit
                ORDER BY id DESC
                LIMIT 1
                """
            ).fetchone()
        return None if row is None else _row_dict(row)

    def _create_schema(self) -> None:
        with closing(self._connect()) as conn:
            conn.executescript(
                """
                CREATE TABLE IF NOT EXISTS evo_router_algorithms(
                  algorithm_id TEXT PRIMARY KEY NOT NULL,
                  thread_id TEXT NOT NULL,
                  run_id TEXT NOT NULL,
                  candidate_ref TEXT NOT NULL,
                  router_admin_url TEXT NOT NULL,
                  service_url TEXT NOT NULL,
                  code_path TEXT NOT NULL,
                  instance_count INTEGER NOT NULL DEFAULT 1,
                  config_hash TEXT NOT NULL,
                  register_request_hash TEXT NOT NULL,
                  expected_state TEXT NOT NULL,
                  cleanup_policy TEXT NOT NULL,
                  last_router_status TEXT NOT NULL,
                  last_seen_at REAL,
                  created_at REAL NOT NULL,
                  updated_at REAL NOT NULL
                );

                CREATE INDEX IF NOT EXISTS idx_evo_router_algorithms_thread
                  ON evo_router_algorithms(thread_id);

                CREATE TABLE IF NOT EXISTS evo_router_ab_strategy_audit(
                  id INTEGER PRIMARY KEY AUTOINCREMENT,
                  thread_id TEXT NOT NULL,
                  candidate_ref TEXT NOT NULL,
                  previous_strategy_json TEXT NOT NULL,
                  next_strategy_json TEXT NOT NULL,
                  reason TEXT NOT NULL,
                  created_at REAL NOT NULL
                );

                CREATE TABLE IF NOT EXISTS evo_router_mutation_lock(
                  id INTEGER PRIMARY KEY CHECK(id = 1),
                  claim_token TEXT NOT NULL,
                  expires_at REAL NOT NULL
                );

                INSERT OR IGNORE INTO evo_router_mutation_lock(id, claim_token, expires_at)
                VALUES (1, '', 0);
                """
            )
            conn.execute('BEGIN IMMEDIATE')
            columns = {row['name'] for row in conn.execute('PRAGMA table_info(evo_router_algorithms)')}
            if 'instance_count' not in columns:
                conn.execute('ALTER TABLE evo_router_algorithms ADD COLUMN instance_count INTEGER')
            self._recover_stale(conn, time.time())
            conn.commit()

    def _connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self._db_path, timeout=SQLITE_TIMEOUT_SECONDS)
        conn.row_factory = sqlite3.Row
        conn.execute(f'PRAGMA busy_timeout = {SQLITE_BUSY_TIMEOUT_MS}')
        conn.execute('PRAGMA synchronous = FULL')
        row = conn.execute('PRAGMA journal_mode = WAL').fetchone()
        if str(row[0]).lower() != 'wal':
            conn.close()
            raise sqlite3.OperationalError('failed to enable SQLite WAL mode')
        return conn

    @contextmanager
    def _transaction(self) -> Iterator[sqlite3.Connection]:
        with closing(self._connect()) as conn:
            conn.execute('BEGIN IMMEDIATE')
            try:
                yield conn
            except Exception:
                conn.rollback()
                raise
            else:
                conn.commit()

    def _row(self, conn: sqlite3.Connection, algorithm_id: str) -> sqlite3.Row | None:
        return conn.execute(
            'SELECT * FROM evo_router_algorithms WHERE algorithm_id = ?',
            (algorithm_id,),
        ).fetchone()

    def _recover_stale(self, conn: sqlite3.Connection, now: float) -> None:
        conn.execute(
            """
            UPDATE evo_router_algorithms
            SET expected_state = 'orphaned', updated_at = ?
            WHERE expected_state IN ('claiming', 'deleting', 'managing') AND updated_at < ?
            """,
            (now, now - CLAIM_LEASE_SECONDS),
        )


def json_hash(value: object) -> str:
    return hashlib.sha256(_json(value).encode()).hexdigest()


def _json(value: object) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))


def _row_dict(row: sqlite3.Row | None) -> dict[str, Any]:
    if row is None:
        raise RouterLedgerError('ledger row is missing')
    return {key: row[key] for key in row.keys()}


def _require_state(value: str) -> None:
    if value not in EXPECTED_STATES:
        raise ValueError(f'expected_state must be one of: {sorted(EXPECTED_STATES)}')


def _require_cleanup_policy(value: str) -> None:
    if value not in CLEANUP_POLICIES:
        raise ValueError(f'cleanup_policy must be one of: {sorted(CLEANUP_POLICIES)}')


def _require_owner(thread_id: str, run_id: str) -> None:
    if THREAD_ID.fullmatch(thread_id) is None:
        raise RouterLedgerError(f'invalid owner thread_id: {thread_id}')
    if run_id != thread_id:
        raise RouterLedgerError('owner run_id must match thread_id')
