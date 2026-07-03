from __future__ import annotations

import hashlib
import json
import sqlite3
import time
from collections.abc import Mapping
from contextlib import closing, contextmanager
from pathlib import Path
from typing import Any, Iterator


SQLITE_TIMEOUT_SECONDS = 30.0
SQLITE_BUSY_TIMEOUT_MS = 30000
EXPECTED_STATES = {'active', 'stopped', 'orphaned'}
CLEANUP_POLICIES = {'thread_delete', 'manual'}


class RouterLedgerError(RuntimeError):
    pass


class RouterAlgorithmLedger:
    def __init__(self, store_root: str | Path) -> None:
        self._root = Path(store_root)
        self._root.mkdir(parents=True, exist_ok=True)
        self._db_path = self._root / 'artifact_store.sqlite3'
        self._create_schema()

    def upsert_algorithm(
        self,
        *,
        algorithm_id: str,
        thread_id: str,
        run_id: str,
        candidate_ref: str,
        router_admin_url: str,
        service_url: str,
        code_path: str,
        config_hash: str,
        register_request_hash: str,
        cleanup_policy: str = 'thread_delete',
    ) -> dict[str, Any]:
        _require_state('active')
        _require_cleanup_policy(cleanup_policy)
        now = time.time()
        with self._transaction() as conn:
            conn.execute(
                """
                INSERT INTO evo_router_algorithms(
                  algorithm_id, thread_id, run_id, candidate_ref,
                  router_admin_url, service_url, code_path, config_hash,
                  register_request_hash, expected_state, cleanup_policy,
                  last_router_status, last_seen_at, created_at, updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, '', NULL, ?, ?)
                ON CONFLICT(algorithm_id) DO UPDATE SET
                  thread_id = excluded.thread_id,
                  run_id = excluded.run_id,
                  candidate_ref = excluded.candidate_ref,
                  router_admin_url = excluded.router_admin_url,
                  service_url = excluded.service_url,
                  code_path = excluded.code_path,
                  config_hash = excluded.config_hash,
                  register_request_hash = excluded.register_request_hash,
                  expected_state = 'active',
                  cleanup_policy = excluded.cleanup_policy,
                  updated_at = excluded.updated_at
                """,
                (
                    algorithm_id,
                    thread_id,
                    run_id,
                    candidate_ref,
                    router_admin_url,
                    service_url,
                    code_path,
                    config_hash,
                    register_request_hash,
                    cleanup_policy,
                    now,
                    now,
                ),
            )
            return _row_dict(self._row(conn, algorithm_id))

    def get_algorithm(self, algorithm_id: str) -> dict[str, Any] | None:
        with closing(self._connect()) as conn:
            row = self._row(conn, algorithm_id)
        return None if row is None else _row_dict(row)

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
        with closing(self._connect()) as conn:
            rows = conn.execute(query, params).fetchall()
        return [_row_dict(row) for row in rows]

    def mark_state(self, algorithm_id: str, expected_state: str) -> None:
        _require_state(expected_state)
        with self._transaction() as conn:
            changed = conn.execute(
                """
                UPDATE evo_router_algorithms
                SET expected_state = ?, updated_at = ?
                WHERE algorithm_id = ?
                """,
                (expected_state, time.time(), algorithm_id),
            ).rowcount
            if changed != 1:
                raise RouterLedgerError(f'algorithm is not evo-owned: {algorithm_id}')

    def record_router_status(self, algorithm_id: str, status: Mapping[str, Any] | None) -> None:
        with self._transaction() as conn:
            conn.execute(
                """
                UPDATE evo_router_algorithms
                SET last_router_status = ?, last_seen_at = ?, updated_at = ?
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
                """
            )

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
