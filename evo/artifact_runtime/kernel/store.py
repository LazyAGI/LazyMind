from __future__ import annotations

import hashlib
import json
import os
import pickle
import sqlite3
import tempfile
import time
from collections.abc import Iterator, Mapping, Sequence
from contextlib import contextmanager, suppress
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Literal, TypeVar

from .artifact import ArtifactKey, ArtifactRef
from .errors import ArtifactStoreCorruptionError, IdempotencyConflictError


SQLITE_TIMEOUT_SECONDS = 30.0
SQLITE_BUSY_TIMEOUT_MS = 30000
SQLITE_BOOTSTRAP_ATTEMPTS = 5
MATERIALIZATION_CLAIM_TTL_SECONDS = 300.0
T = TypeVar('T')


@dataclass(frozen=True)
class ArtifactRecord:
    ref: ArtifactRef
    run_id: str
    value: object
    kind: Literal['external', 'op_output']
    input_refs: Mapping[ArtifactKey, ArtifactRef] = field(default_factory=dict)
    producer_op_id: str = ''
    metadata: Mapping[str, str] = field(default_factory=dict)


@dataclass(frozen=True)
class ArtifactEvent:
    seq: int
    run_id: str
    kind: str
    refs: tuple[ArtifactRef, ...]


@dataclass(frozen=True)
class StoreResult:
    status: Literal['ok', 'stale']
    refs: tuple[ArtifactRef, ...] = ()


ClaimStatus = Literal['ok', 'stale', 'already_done', 'busy']


@dataclass(frozen=True)
class ClaimResult:
    status: ClaimStatus


class SQLiteArtifactStore:
    def __init__(self, root: str | Path) -> None:
        self._root = Path(root)
        self._payload_root = self._root / 'payloads'
        self._root.mkdir(parents=True, exist_ok=True)
        self._payload_root.mkdir(parents=True, exist_ok=True)
        self._conn = sqlite3.connect(
            self._root / 'artifact_store.sqlite3',
            timeout=SQLITE_TIMEOUT_SECONDS,
            check_same_thread=False,
        )
        self._conn.row_factory = sqlite3.Row
        self._conn.execute('PRAGMA foreign_keys = ON')
        self._conn.execute(f'PRAGMA busy_timeout = {SQLITE_BUSY_TIMEOUT_MS}')
        self._conn.execute('PRAGMA synchronous = FULL')
        self._enable_wal()
        self._init_schema()

    def close(self) -> None:
        self._conn.close()

    @property
    def root(self) -> Path:
        return self._root

    def commit_external(self, run_id: str, key: ArtifactKey, value: object, *,
                        idempotency_key: str, expected_ref: ArtifactRef | None = None,
                        metadata: Mapping[str, str] | None = None) -> StoreResult:
        _require_text(run_id, 'run_id')
        _require_text(idempotency_key, 'idempotency_key')
        _require_key(key)
        payload = pickle.dumps(value, protocol=pickle.HIGHEST_PROTOCOL)
        metadata = dict(metadata or {})
        request_hash = _request_hash(
            'commit_external',
            run_id,
            _key_json(key),
            _ref_json(expected_ref),
            metadata,
            _sha256(payload),
        )

        with self._transaction():
            replay = self._replay(run_id, idempotency_key, request_hash)
            if replay is not None:
                return replay
            if expected_ref is not None and self.effective_artifacts(run_id).get(key) != expected_ref:
                result = StoreResult('stale')
                self._remember(run_id, idempotency_key, request_hash, result)
                return result

            ref = self._next_ref(run_id, key)
            payload_path = self._payload_path(run_id, ref)
            _write_payload(payload_path, payload)
            self._insert_record(run_id, ref, 'external', '', {}, payload_path, metadata)
            self._upsert_head(run_id, ref)
            result = StoreResult('ok', (ref,))
            self._remember(run_id, idempotency_key, request_hash, result)
            self._event(run_id, 'committed', result.refs)
            return result

    def commit_outputs(self, run_id: str, producer_op_id: str, output_values: Mapping[ArtifactKey, object],
                       input_refs: Mapping[ArtifactKey, ArtifactRef], *, idempotency_key: str,
                       metadata: Mapping[str, str] | None = None,
                       ) -> StoreResult:
        _require_text(run_id, 'run_id')
        _require_text(producer_op_id, 'producer_op_id')
        _require_text(idempotency_key, 'idempotency_key')
        if not output_values:
            raise ValueError('output_values must not be empty')
        outputs = dict(output_values)
        inputs = dict(input_refs)
        output_keys = tuple(sorted(outputs))
        input_keys = tuple(sorted(inputs))
        for output_key in output_keys:
            _require_key(output_key)
        for input_key in input_keys:
            input_ref = inputs[input_key]
            _require_key(input_key)
            _require_ref(input_ref)
            if input_ref.key != input_key:
                raise ValueError('input_refs keys must match their refs')
        metadata = dict(metadata or {})
        payloads = {key: pickle.dumps(value, protocol=pickle.HIGHEST_PROTOCOL) for key, value in outputs.items()}
        request_hash = _request_hash(
            'commit_outputs',
            run_id,
            producer_op_id,
            [_key_json(key) for key in output_keys],
            [_ref_json(inputs[key]) for key in input_keys],
            metadata,
            [_sha256(payloads[key]) for key in output_keys],
        )

        with self._transaction():
            replay = self._replay(run_id, idempotency_key, request_hash)
            if replay is not None:
                return replay
            effective = self.effective_artifacts(run_id)
            if any(effective.get(key) != ref for key, ref in inputs.items()):
                result = StoreResult('stale')
                self._remember(run_id, idempotency_key, request_hash, result)
                return result
            if set(output_keys) <= set(effective):
                result = StoreResult('stale')
                self._remember(run_id, idempotency_key, request_hash, result)
                return result

            refs = tuple(self._next_ref(run_id, key) for key in output_keys)
            for ref in refs:
                payload_path = self._payload_path(run_id, ref)
                _write_payload(payload_path, payloads[ref.key])
                self._insert_record(run_id, ref, 'op_output', producer_op_id, inputs, payload_path, metadata)
                self._upsert_head(run_id, ref)

            result = StoreResult('ok', refs)
            self._remember(run_id, idempotency_key, request_hash, result)
            self._event(run_id, 'committed', result.refs)
            return result

    def claim_materialization(self, run_id: str, materialization_key: str, output_keys: Sequence[ArtifactKey],
                              input_refs: Mapping[ArtifactKey, ArtifactRef], *, claim_token: str,
                              claim_ttl_seconds: float = MATERIALIZATION_CLAIM_TTL_SECONDS) -> ClaimResult:
        _require_text(run_id, 'run_id')
        _require_text(materialization_key, 'materialization_key')
        _require_text(claim_token, 'claim_token')
        if claim_ttl_seconds <= 0:
            raise ValueError('claim_ttl_seconds must be positive')
        outputs = tuple(sorted(output_keys))
        inputs = dict(input_refs)
        if not outputs:
            raise ValueError('output_keys must not be empty')
        for output_key in outputs:
            _require_key(output_key)
        for input_key, input_ref in inputs.items():
            _require_key(input_key)
            _require_ref(input_ref)
            if input_ref.key != input_key:
                raise ValueError('input_refs keys must match their refs')

        with self._transaction():
            effective = self.effective_artifacts(run_id)
            if any(effective.get(key) != ref for key, ref in inputs.items()):
                return ClaimResult('stale')
            if set(outputs) <= set(effective):
                return ClaimResult('already_done')
            now = time.time()
            expires_at = now + claim_ttl_seconds
            changed = self._conn.execute(
                """
                INSERT INTO materialization_claims(run_id, materialization_key, claim_token, expires_at)
                VALUES (?, ?, ?, ?)
                ON CONFLICT(run_id, materialization_key)
                DO UPDATE SET claim_token = excluded.claim_token, expires_at = excluded.expires_at
                WHERE materialization_claims.expires_at <= ?
                """,
                (run_id, materialization_key, claim_token, expires_at, now),
            ).rowcount
            if changed != 1:
                return ClaimResult('busy')
            return ClaimResult('ok')

    def release_materialization(self, run_id: str, materialization_key: str, *, claim_token: str) -> None:
        _require_text(run_id, 'run_id')
        _require_text(materialization_key, 'materialization_key')
        _require_text(claim_token, 'claim_token')
        with self._transaction():
            self._conn.execute(
                """
                DELETE FROM materialization_claims
                WHERE run_id = ? AND materialization_key = ? AND claim_token = ?
                """,
                (run_id, materialization_key, claim_token),
            )

    def effective_artifacts(self, run_id: str) -> dict[ArtifactKey, ArtifactRef]:
        _require_text(run_id, 'run_id')
        rows = self._conn.execute(
            """
            SELECT h.artifact_id, h.partition, h.version, r.kind, r.input_refs_json
            FROM artifact_heads h
            JOIN artifact_records r
              ON r.run_id = h.run_id
             AND r.artifact_id = h.artifact_id
             AND r.partition = h.partition
             AND r.version = h.version
            WHERE h.run_id = ?
            """,
            (run_id,),
        ).fetchall()
        candidate = {
            ArtifactKey(row['artifact_id'], row['partition']): ArtifactRef(
                ArtifactKey(row['artifact_id'], row['partition']), row['version']
            )
            for row in rows
        }
        provenance = {
            candidate[ArtifactKey(row['artifact_id'], row['partition'])]: _refs_from_json(row['input_refs_json'])
            for row in rows
            if row['kind'] == 'op_output'
        }
        changed = True
        while changed:
            changed = False
            for ref, input_refs in tuple(provenance.items()):
                if ref.key in candidate and any(candidate.get(input_ref.key) != input_ref for input_ref in input_refs):
                    del candidate[ref.key]
                    changed = True
        return candidate

    def get(self, run_id: str, ref: ArtifactRef) -> ArtifactRecord | None:
        _require_text(run_id, 'run_id')
        _require_ref(ref)
        row = self._record_row(run_id, ref)
        if row is None:
            return None
        path = self._root / row['payload_path']
        try:
            with path.open('rb') as file:
                value = pickle.load(file)
        except FileNotFoundError as exc:
            if self._record_row(run_id, ref) is None:
                return None
            raise ArtifactStoreCorruptionError(f'payload file is missing for {ref}: {path}') from exc
        except Exception as exc:
            raise ArtifactStoreCorruptionError(f'payload file is unreadable for {ref}: {path}') from exc
        if self._record_row(run_id, ref) is None:
            return None
        return ArtifactRecord(
            ref=ref,
            run_id=row['run_id'],
            value=value,
            kind=row['kind'],
            input_refs={item.key: item for item in _refs_from_json(row['input_refs_json'])},
            producer_op_id=row['producer_op_id'],
            metadata=json.loads(row['metadata_json']),
        )

    def history(self, run_id: str, key: ArtifactKey) -> tuple[ArtifactRecord, ...]:
        _require_text(run_id, 'run_id')
        _require_key(key)
        rows = self._conn.execute(
            """
            SELECT version
            FROM artifact_records
            WHERE run_id = ? AND artifact_id = ? AND partition = ?
            ORDER BY version
            """,
            (run_id, key.artifact_id, key.partition),
        ).fetchall()
        records = (self.get(run_id, ArtifactRef(key, row['version'])) for row in rows)
        return tuple(record for record in records if record is not None)

    def run_ids(self, key: ArtifactKey | None = None) -> tuple[str, ...]:
        if key is None:
            rows = self._conn.execute(
                'SELECT DISTINCT run_id FROM artifact_heads ORDER BY run_id'
            ).fetchall()
        else:
            _require_key(key)
            rows = self._conn.execute(
                """
                SELECT DISTINCT run_id
                FROM artifact_heads
                WHERE artifact_id = ? AND partition = ?
                ORDER BY run_id
                """,
                (key.artifact_id, key.partition),
            ).fetchall()
        return tuple(str(row['run_id']) for row in rows)

    def events_since(self, seq: int, run_id: str | None = None) -> tuple[ArtifactEvent, ...]:
        if run_id is None:
            rows = self._conn.execute(
                'SELECT seq, run_id, kind, refs_json FROM artifact_events WHERE seq > ? ORDER BY seq',
                (seq,),
            ).fetchall()
        else:
            rows = self._conn.execute(
                """
                SELECT seq, run_id, kind, refs_json
                FROM artifact_events
                WHERE seq > ? AND run_id = ?
                ORDER BY seq
                """,
                (seq, run_id),
            ).fetchall()
        return tuple(
            ArtifactEvent(row['seq'], row['run_id'], row['kind'], _refs_from_json(row['refs_json']))
            for row in rows
        )

    def invalidate(self, run_id: str, keys: Sequence[ArtifactKey] = (), refs: Sequence[ArtifactRef] = (),
                   *, idempotency_key: str) -> StoreResult:
        _require_text(run_id, 'run_id')
        _require_text(idempotency_key, 'idempotency_key')
        keys, refs = _validated_targets(keys, refs)
        request_hash = _request_hash('invalidate', run_id, *_targets_json(keys, refs))

        with self._transaction():
            replay = self._replay(run_id, idempotency_key, request_hash)
            if replay is not None:
                return replay
            affected = self._head_refs(run_id, keys, refs)
            for ref in affected:
                self._conn.execute(
                    """
                    DELETE FROM artifact_heads
                    WHERE run_id = ? AND artifact_id = ? AND partition = ? AND version = ?
                    """,
                    (run_id, ref.key.artifact_id, ref.key.partition, ref.version),
                )
            result = StoreResult('ok', affected)
            self._remember(run_id, idempotency_key, request_hash, result)
            if affected:
                self._event(run_id, 'invalidated', affected)
            return result

    def delete_artifacts(self, run_id: str, keys: Sequence[ArtifactKey] = (),
                         refs: Sequence[ArtifactRef] = (), *, idempotency_key: str
                         ) -> tuple[ArtifactRef, ...]:
        _require_text(run_id, 'run_id')
        _require_text(idempotency_key, 'idempotency_key')
        keys, refs = _validated_targets(keys, refs)
        request_hash = _request_hash('delete_artifacts', run_id, *_targets_json(keys, refs))

        with self._transaction():
            replay = self._replay(run_id, idempotency_key, request_hash)
            if replay is not None:
                return replay.refs
            rows = self._record_rows(run_id, keys, refs)
            deleted_refs = tuple(_row_ref(row) for row in rows)
            payloads = tuple(self._root / row['payload_path'] for row in rows)
            for ref in deleted_refs:
                self._conn.execute(
                    """
                    DELETE FROM artifact_records
                    WHERE run_id = ? AND artifact_id = ? AND partition = ? AND version = ?
                    """,
                    (run_id, ref.key.artifact_id, ref.key.partition, ref.version),
                )
            self._remember(run_id, idempotency_key, request_hash, StoreResult('ok', deleted_refs))
            if deleted_refs:
                self._event(run_id, 'deleted', deleted_refs)
        _unlink_files(payloads)
        return deleted_refs

    def delete_run(self, run_id: str) -> tuple[ArtifactRef, ...]:
        _require_text(run_id, 'run_id')
        with self._transaction():
            rows = self._conn.execute(
                """
                SELECT artifact_id, partition, version, payload_path
                FROM artifact_records
                WHERE run_id = ?
                ORDER BY artifact_id, partition, version
                """,
                (run_id,),
            ).fetchall()
            refs = tuple(_row_ref(row) for row in rows)
            payloads = tuple(self._root / row['payload_path'] for row in rows)
            self._conn.execute('DELETE FROM artifact_records WHERE run_id = ?', (run_id,))
            self._conn.execute('DELETE FROM artifact_versions WHERE run_id = ?', (run_id,))
            self._conn.execute('DELETE FROM artifact_events WHERE run_id = ?', (run_id,))
            self._conn.execute('DELETE FROM artifact_idempotency WHERE run_id = ?', (run_id,))
            self._conn.execute('DELETE FROM materialization_claims WHERE run_id = ?', (run_id,))
        _unlink_files(payloads)
        with self._transaction():
            _remove_empty_dirs(self._payload_run_dir(run_id))
        return refs

    def gc(self) -> None:
        with self._transaction():
            self._conn.execute('DELETE FROM materialization_claims WHERE expires_at <= ?', (time.time(),))
            rows = self._conn.execute('SELECT payload_path FROM artifact_records').fetchall()
            live = {self._root / row['payload_path'] for row in rows}
            if self._payload_root.exists():
                for path in self._payload_root.rglob('*'):
                    if path.is_file() and path not in live:
                        path.unlink(missing_ok=True)
            _remove_empty_dirs(self._payload_root)
        self._conn.execute('PRAGMA wal_checkpoint(TRUNCATE)').fetchone()

    def _init_schema(self) -> None:
        _retry_sqlite(self._create_schema)

    def _enable_wal(self) -> None:
        def enable() -> None:
            row = self._conn.execute('PRAGMA journal_mode = WAL').fetchone()
            if str(row[0]).lower() != 'wal':
                raise sqlite3.OperationalError('failed to enable SQLite WAL mode')

        _retry_sqlite(enable)

    def _create_schema(self) -> None:
        self._conn.executescript(
            """
            CREATE TABLE IF NOT EXISTS artifact_versions(
              run_id TEXT NOT NULL,
              artifact_id TEXT NOT NULL,
              partition TEXT NOT NULL,
              next_version INTEGER NOT NULL CHECK(next_version >= 1),
              PRIMARY KEY (run_id, artifact_id, partition)
            );

            CREATE TABLE IF NOT EXISTS artifact_records(
              artifact_id TEXT NOT NULL,
              partition TEXT NOT NULL,
              version INTEGER NOT NULL CHECK(version >= 1),
              run_id TEXT NOT NULL,
              kind TEXT NOT NULL CHECK(kind IN ('external', 'op_output')),
              producer_op_id TEXT NOT NULL,
              input_refs_json TEXT NOT NULL,
              payload_path TEXT NOT NULL,
              metadata_json TEXT NOT NULL,
              PRIMARY KEY (run_id, artifact_id, partition, version)
            );

            CREATE TABLE IF NOT EXISTS artifact_heads(
              run_id TEXT NOT NULL,
              artifact_id TEXT NOT NULL,
              partition TEXT NOT NULL,
              version INTEGER NOT NULL CHECK(version >= 1),
              PRIMARY KEY (run_id, artifact_id, partition),
              FOREIGN KEY (run_id, artifact_id, partition, version)
                REFERENCES artifact_records(run_id, artifact_id, partition, version)
                ON DELETE CASCADE
            );

            CREATE TABLE IF NOT EXISTS artifact_idempotency(
              run_id TEXT NOT NULL,
              idempotency_key TEXT NOT NULL,
              request_hash TEXT NOT NULL,
              result_json TEXT NOT NULL,
              PRIMARY KEY (run_id, idempotency_key)
            );

            CREATE TABLE IF NOT EXISTS artifact_events(
              seq INTEGER PRIMARY KEY AUTOINCREMENT,
              run_id TEXT NOT NULL,
              kind TEXT NOT NULL,
              refs_json TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS materialization_claims(
              run_id TEXT NOT NULL,
              materialization_key TEXT NOT NULL,
              claim_token TEXT NOT NULL,
              expires_at REAL NOT NULL,
              PRIMARY KEY (run_id, materialization_key)
            );

            CREATE INDEX IF NOT EXISTS idx_events_run_seq
              ON artifact_events(run_id, seq);
            """
        )

    @contextmanager
    def _transaction(self) -> Iterator[None]:
        self._conn.execute('BEGIN IMMEDIATE')
        try:
            yield
        except Exception:
            self._conn.rollback()
            raise
        else:
            try:
                self._conn.commit()
            except Exception:
                with suppress(Exception):
                    self._conn.rollback()
                raise

    def _next_ref(self, run_id: str, key: ArtifactKey) -> ArtifactRef:
        row = self._conn.execute(
            """
            INSERT INTO artifact_versions(run_id, artifact_id, partition, next_version)
            VALUES (?, ?, ?, 2)
            ON CONFLICT(run_id, artifact_id, partition)
            DO UPDATE SET next_version = next_version + 1
            RETURNING next_version - 1 AS version
            """,
            (run_id, key.artifact_id, key.partition),
        ).fetchone()
        return ArtifactRef(key, row['version'])

    def _insert_record(self, run_id: str, ref: ArtifactRef, kind: str, producer_op_id: str,
                       input_refs: Mapping[ArtifactKey, ArtifactRef], payload_path: Path,
                       metadata: Mapping[str, str]) -> None:
        self._conn.execute(
            """
            INSERT INTO artifact_records(
              artifact_id, partition, version, run_id, kind, producer_op_id,
              input_refs_json, payload_path, metadata_json
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                ref.key.artifact_id,
                ref.key.partition,
                ref.version,
                run_id,
                kind,
                producer_op_id,
                _refs_json(input_refs.values()),
                str(payload_path.relative_to(self._root)),
                json.dumps(dict(metadata), sort_keys=True, separators=(',', ':')),
            ),
        )

    def _upsert_head(self, run_id: str, ref: ArtifactRef) -> None:
        self._conn.execute(
            """
            INSERT INTO artifact_heads(run_id, artifact_id, partition, version)
            VALUES (?, ?, ?, ?)
            ON CONFLICT(run_id, artifact_id, partition)
            DO UPDATE SET version = excluded.version
            """,
            (run_id, ref.key.artifact_id, ref.key.partition, ref.version),
        )

    def _record_row(self, run_id: str, ref: ArtifactRef) -> sqlite3.Row | None:
        return self._conn.execute(
            """
            SELECT *
            FROM artifact_records
            WHERE run_id = ? AND artifact_id = ? AND partition = ? AND version = ?
            """,
            (run_id, ref.key.artifact_id, ref.key.partition, ref.version),
        ).fetchone()

    def _head_refs(self, run_id: str, keys: Sequence[ArtifactKey],
                   refs: Sequence[ArtifactRef]) -> tuple[ArtifactRef, ...]:
        found: dict[ArtifactKey, ArtifactRef] = {}
        for key in keys:
            row = self._conn.execute(
                """
                SELECT artifact_id, partition, version
                FROM artifact_heads
                WHERE run_id = ? AND artifact_id = ? AND partition = ?
                """,
                (run_id, key.artifact_id, key.partition),
            ).fetchone()
            if row is not None:
                found[key] = _row_ref(row)
        for ref in refs:
            row = self._conn.execute(
                """
                SELECT artifact_id, partition, version
                FROM artifact_heads
                WHERE run_id = ? AND artifact_id = ? AND partition = ? AND version = ?
                """,
                (run_id, ref.key.artifact_id, ref.key.partition, ref.version),
            ).fetchone()
            if row is not None:
                found[ref.key] = ref
        return tuple(found[key] for key in sorted(found))

    def _record_rows(self, run_id: str, keys: Sequence[ArtifactKey],
                     refs: Sequence[ArtifactRef]) -> tuple[sqlite3.Row, ...]:
        rows: dict[ArtifactRef, sqlite3.Row] = {}
        for key in keys:
            for row in self._conn.execute(
                """
                SELECT artifact_id, partition, version, payload_path
                FROM artifact_records
                WHERE run_id = ? AND artifact_id = ? AND partition = ?
                ORDER BY version
                """,
                (run_id, key.artifact_id, key.partition),
            ).fetchall():
                rows[_row_ref(row)] = row
        for ref in refs:
            row = self._conn.execute(
                """
                SELECT artifact_id, partition, version, payload_path
                FROM artifact_records
                WHERE run_id = ? AND artifact_id = ? AND partition = ? AND version = ?
                """,
                (run_id, ref.key.artifact_id, ref.key.partition, ref.version),
            ).fetchone()
            if row is not None:
                rows[ref] = row
        return tuple(rows[ref] for ref in sorted(rows))

    def _payload_path(self, run_id: str, ref: ArtifactRef) -> Path:
        return self._payload_run_dir(run_id) / self._payload_key_path(ref)

    def _payload_run_dir(self, run_id: str) -> Path:
        run_hash = _sha256(run_id.encode())
        return self._payload_root / run_hash[:2] / run_hash

    def _payload_key_path(self, ref: ArtifactRef) -> Path:
        key_hash = _sha256(f'{ref.key.artifact_id}\0{ref.key.partition}'.encode())
        return (
            Path(key_hash[:2])
            / key_hash
            / str(ref.version // 1000)
            / f'{ref.version}.pkl'
        )

    def _replay(self, run_id: str, idempotency_key: str, request_hash: str) -> StoreResult | None:
        row = self._conn.execute(
            """
            SELECT request_hash, result_json
            FROM artifact_idempotency
            WHERE run_id = ? AND idempotency_key = ?
            """,
            (run_id, idempotency_key),
        ).fetchone()
        if row is None:
            return None
        if row['request_hash'] != request_hash:
            raise IdempotencyConflictError(f'idempotency_key {idempotency_key!r} was reused with a different request')
        return _result_from_json(row['result_json'])

    def _remember(self, run_id: str, idempotency_key: str, request_hash: str, result: StoreResult) -> None:
        self._conn.execute(
            """
            INSERT INTO artifact_idempotency(run_id, idempotency_key, request_hash, result_json)
            VALUES (?, ?, ?, ?)
            """,
            (run_id, idempotency_key, request_hash, _result_json(result)),
        )

    def _event(self, run_id: str, kind: str, refs: Sequence[ArtifactRef]) -> None:
        self._conn.execute(
            'INSERT INTO artifact_events(run_id, kind, refs_json) VALUES (?, ?, ?)',
            (run_id, kind, _refs_json(refs)),
        )


def _write_payload(path: Path, payload: bytes) -> None:
    _ensure_dir(path.parent)
    tmp_name = ''
    try:
        with tempfile.NamedTemporaryFile('wb', dir=path.parent, prefix=f'.{path.name}.', delete=False) as file:
            tmp_name = file.name
            file.write(payload)
            file.flush()
            os.fsync(file.fileno())
        os.replace(tmp_name, path)
        _fsync_dir(path.parent)
    except Exception:
        if tmp_name:
            Path(tmp_name).unlink(missing_ok=True)
        raise


def _retry_sqlite(operation: Callable[[], T]) -> T:
    for attempt in range(SQLITE_BOOTSTRAP_ATTEMPTS):
        try:
            return operation()
        except sqlite3.OperationalError as exc:
            if 'locked' not in str(exc).lower() or attempt == SQLITE_BOOTSTRAP_ATTEMPTS - 1:
                raise
            time.sleep(0.05 * (2 ** attempt))
    raise AssertionError('unreachable')


def _fsync_dir(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _ensure_dir(path: Path) -> None:
    # Keep newly-created payload directories durable before the DB record commits.
    missing: list[Path] = []
    current = path
    while not current.exists():
        missing.append(current)
        current = current.parent
    for directory in reversed(missing):
        directory.mkdir(exist_ok=True)
        _fsync_dir(directory.parent)
        _fsync_dir(directory)


def _unlink_files(paths: Sequence[Path]) -> None:
    for path in paths:
        with suppress(OSError):
            path.unlink(missing_ok=True)


def _remove_empty_dirs(root: Path) -> None:
    if not root.exists():
        return
    dirs = (item for item in root.rglob('*') if item.is_dir())
    for path in sorted(dirs, key=lambda item: len(item.parts), reverse=True):
        try:
            path.rmdir()
        except OSError:
            continue
    with suppress(OSError):
        root.rmdir()


def _request_hash(*items: object) -> str:
    data = json.dumps(items, sort_keys=True, separators=(',', ':')).encode()
    return _sha256(data)


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _targets_json(
    keys: Sequence[ArtifactKey],
    refs: Sequence[ArtifactRef],
) -> tuple[list[list[str]], list[list[object] | None]]:
    return [_key_json(key) for key in keys], [_ref_json(ref) for ref in refs]


def _validated_targets(
    keys: Sequence[ArtifactKey],
    refs: Sequence[ArtifactRef],
) -> tuple[tuple[ArtifactKey, ...], tuple[ArtifactRef, ...]]:
    keys = tuple(sorted(keys))
    refs = tuple(sorted(refs))
    for key in keys:
        _require_key(key)
    for ref in refs:
        _require_ref(ref)
    return keys, refs


def _refs_json(refs: Sequence[ArtifactRef]) -> str:
    return json.dumps([_ref_json(ref) for ref in refs], sort_keys=True, separators=(',', ':'))


def _refs_from_json(value: str) -> tuple[ArtifactRef, ...]:
    return tuple(_ref_from_json(item) for item in json.loads(value))


def _key_json(key: ArtifactKey) -> list[str]:
    return [key.artifact_id, key.partition]


def _ref_json(ref: ArtifactRef | None) -> list[object] | None:
    if ref is None:
        return None
    return [ref.key.artifact_id, ref.key.partition, ref.version]


def _ref_from_json(value: Sequence[object]) -> ArtifactRef:
    return ArtifactRef(ArtifactKey(str(value[0]), str(value[1])), int(value[2]))


def _result_json(result: StoreResult) -> str:
    return json.dumps(
        {'status': result.status, 'refs': [_ref_json(ref) for ref in result.refs]},
        sort_keys=True,
        separators=(',', ':'),
    )


def _result_from_json(value: str) -> StoreResult:
    data = json.loads(value)
    return StoreResult(data['status'], tuple(_ref_from_json(item) for item in data['refs']))


def _row_ref(row: sqlite3.Row) -> ArtifactRef:
    return ArtifactRef(ArtifactKey(row['artifact_id'], row['partition']), row['version'])


def _require_text(value: str, name: str) -> None:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f'{name} must be non-empty')


def _require_key(key: ArtifactKey) -> None:
    if not isinstance(key, ArtifactKey):
        raise TypeError('key must be an ArtifactKey')


def _require_ref(ref: ArtifactRef) -> None:
    if not isinstance(ref, ArtifactRef):
        raise TypeError('ref must be an ArtifactRef')
