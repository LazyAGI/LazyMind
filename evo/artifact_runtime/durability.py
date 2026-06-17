from __future__ import annotations

import hashlib
import json
import math
import pickle
import sqlite3
import uuid
from collections.abc import Mapping
from dataclasses import dataclass, replace
from pathlib import Path
from threading import RLock
from types import MappingProxyType
from typing import Any, Protocol

from .artifact import ArtifactKey, ArtifactPayload, ArtifactRef
from .control_codec import (
    decode_control_value as decode_basic_control_value,
    encode_control_value as encode_basic_control_value,
    is_basic_control_envelope,
)
from .controller import (
    Attempt,
    AttemptClaim,
    AttemptResult,
    CommitResult,
    ControllerEvent,
    EventLog,
    PlanInstance,
    RunState,
)
from .external import (
    RETRYABLE_STATUSES,
    TERMINAL_REPLAY_STATUSES,
    ExternalCallAcquireResult,
    ExternalCallRecord,
    ExternalCallResult,
    ExternalCallWriteResult,
)
from .intervention import InterventionResult
from .intervention import InterventionKind, InterventionLog, InterventionRecord
from .intent import (
    IntentAdvanceResult,
    IntentCommandAcquireResult,
    IntentCommandKind,
    IntentCommandLog,
    IntentCommandRecord,
    IntentCommandResult,
    IntentCommandWriteResult,
    IntentControllerResult,
    IntentPlanResult,
)
from .mutation import ArtifactMutationResult
from .mutation import MutationLog
from .plan import ExecutionPlan, PlanInput, PlanOp
from .reconciliation import ReconcileResult
from .runtime_driver import RuntimeDriverCheckpoint, RuntimeDriverCheckpointSaveResult
from .store import (
    ArtifactCommitOutcome,
    ArtifactCommitRequest,
    ArtifactRecord,
    ArtifactStore,
    commit_request_fingerprint,
    source_write_fingerprint,
)
from .utils import (
    canonical_json,
    is_json_scalar,
    json_mapping_fingerprint,
    normalize_json_value,
    sorted_string_items,
    validate_nonempty,
)

_SCHEMA_VERSION = 4
_SUPPORTED_SCHEMA_VERSIONS = frozenset({1, _SCHEMA_VERSION})


@dataclass(frozen=True)
class AttemptFailureEnvelope:
    error_type: str = ''
    error_message: str = ''


class ControlPlaneCodec(Protocol):
    def encode_payload(self, value: Any) -> str:
        ...

    def decode_payload(self, payload: str) -> Any:
        ...


class ArtifactValueCodec(Protocol):
    @property
    def codec_id(self) -> str:
        ...

    def encode(self, value: Any) -> bytes:
        ...

    def decode(self, payload: bytes) -> Any:
        ...


class PickleArtifactValueCodec:
    """Trusted local-process codec for artifact values only."""

    codec_id = 'pickle:v5'

    def encode(self, value: Any) -> bytes:
        return pickle.dumps(value, protocol=5)

    def decode(self, payload: bytes) -> Any:
        return pickle.loads(payload)


class JSONControlPlaneCodec:
    def encode_payload(self, value: Any) -> str:
        return _canonical_json(_encode_control(value))

    def decode_payload(self, payload: str) -> Any:
        return _decode_control(json.loads(payload))


def encode_control_value(value: Any) -> Any:
    return _encode_control(value)


def decode_control_value(value: Any) -> Any:
    return _decode_control(value)


class SQLiteEventLog(EventLog):
    def __init__(self, path: str | Path, *, codec: ControlPlaneCodec | None = None) -> None:
        self.path = str(path)
        self.codec = codec or JSONControlPlaneCodec()
        self._connection = _connect(self.path)
        self._lock = RLock()
        _init_schema(self._connection)

    def append(self, event: ControllerEvent) -> int:
        payload_json = self.codec.encode_payload(event.payload)
        with self._lock:
            cursor = self._connection.execute(
                'INSERT INTO controller_events(run_id, event_type, payload_json) VALUES (?, ?, ?)',
                (event.run_id, event.event_type, payload_json),
            )
            self._connection.commit()
            return int(cursor.lastrowid)

    def scan(self, run_id: str) -> list[ControllerEvent]:
        rows = self._connection.execute(
            'SELECT seq, event_type, payload_json FROM controller_events WHERE run_id = ? ORDER BY seq',
            (run_id,),
        ).fetchall()
        return [
            ControllerEvent(
                event_type=str(row['event_type']),
                run_id=run_id,
                payload=self.codec.decode_payload(str(row['payload_json'])),
                seq=int(row['seq']),
            )
            for row in rows
        ]

    def scan_since(self, seq: int = 0, *, limit: int = 1000) -> tuple[ControllerEvent, ...]:
        _validate_scan_window(seq, limit)
        rows = self._connection.execute(
            """
            SELECT seq, run_id, event_type, payload_json
            FROM controller_events
            WHERE seq > ?
            ORDER BY seq
            LIMIT ?
            """,
            (seq, limit),
        ).fetchall()
        return tuple(
            ControllerEvent(
                event_type=str(row['event_type']),
                run_id=str(row['run_id']),
                payload=self.codec.decode_payload(str(row['payload_json'])),
                seq=int(row['seq']),
            )
            for row in rows
        )

    def max_seq(self) -> int:
        row = self._connection.execute('SELECT MAX(seq) AS seq FROM controller_events').fetchone()
        return 0 if row is None or row['seq'] is None else int(row['seq'])


class SQLiteArtifactStore(ArtifactStore):
    def __init__(
        self,
        path: str | Path,
        *,
        value_codec: ArtifactValueCodec | None = None,
        control_codec: ControlPlaneCodec | None = None,
    ) -> None:
        self.path = str(path)
        self.value_codec = value_codec or PickleArtifactValueCodec()
        self.control_codec = control_codec or JSONControlPlaneCodec()
        self._connection = _connect(self.path)
        self._lock = RLock()
        _init_schema(self._connection)

    def latest(self, key: ArtifactKey) -> ArtifactRef | None:
        row = self._connection.execute(
            'SELECT MAX(version) AS version FROM artifact_records WHERE artifact_id = ? AND partition = ?',
            (key.artifact_id, key.partition),
        ).fetchone()
        if row is None or row['version'] is None:
            return None
        return ArtifactRef(key, int(row['version']))

    def get(self, ref: ArtifactRef) -> ArtifactRecord | None:
        row = self._connection.execute(
            """
            SELECT artifact_id, partition, version, value_blob, value_codec, producer_run_id, producer_attempt_id,
                   producer_op_id, input_refs_json, metadata_json
            FROM artifact_records
            WHERE artifact_id = ? AND partition = ? AND version = ?
            """,
            (ref.key.artifact_id, ref.key.partition, ref.version),
        ).fetchone()
        return None if row is None else self._record_from_row(row)

    def history(self, key: ArtifactKey) -> tuple[ArtifactRecord, ...]:
        rows = self._connection.execute(
            """
            SELECT artifact_id, partition, version, value_blob, value_codec, producer_run_id, producer_attempt_id,
                   producer_op_id, input_refs_json, metadata_json
            FROM artifact_records
            WHERE artifact_id = ? AND partition = ?
            ORDER BY version
            """,
            (key.artifact_id, key.partition),
        ).fetchall()
        return tuple(self._record_from_row(row) for row in rows)

    def put_source(self, key: ArtifactKey, value: Any, *, metadata: Mapping[str, Any] | None = None) -> ArtifactRef:
        with self._lock:
            _begin_immediate(self._connection)
            try:
                payload = ArtifactPayload.from_value(value, metadata=metadata, role='source')
                ref = self._next_ref(key)
                self._insert_record(
                    ArtifactRecord(
                        key,
                        ref,
                        payload,
                        ('', ''),
                        '',
                        MappingProxyType({}),
                        MappingProxyType(dict(metadata or {})),
                    )
                )
                self._connection.commit()
                return ref
            except Exception:
                self._connection.rollback()
                raise

    def put_source_once(
        self,
        command_id: str,
        key: ArtifactKey,
        value: Any,
        *,
        expected_ref: ArtifactRef | None = None,
        create_only: bool = False,
        metadata: Mapping[str, Any] | None = None,
    ) -> ArtifactCommitOutcome:
        validate_nonempty(command_id, 'command_id')
        with self._lock:
            _begin_immediate(self._connection)
            try:
                payload = ArtifactPayload.from_value(value, metadata=metadata, role='source')
                fingerprint = source_write_fingerprint(key, payload, expected_ref, create_only)
                row = self._source_write_row(command_id)
                if row is not None:
                    if str(row['request_fingerprint']) == fingerprint:
                        outcome = self.control_codec.decode_payload(str(row['outcome_json']))
                        self._connection.commit()
                        return outcome
                    self._connection.commit()
                    return ArtifactCommitOutcome('conflict', reason='source command conflict')

                latest = self.latest(key)
                if create_only and latest is not None:
                    record = self.get(latest)
                    if record is not None and record.value == payload:
                        outcome = ArtifactCommitOutcome('committed', {key: latest})
                        self._record_source_write(command_id, fingerprint, outcome)
                        self._connection.commit()
                        return outcome
                    outcome = ArtifactCommitOutcome('conflict', reason='source already exists')
                    self._record_source_write(command_id, fingerprint, outcome)
                    self._connection.commit()
                    return outcome
                if expected_ref is not None and latest != expected_ref:
                    outcome = ArtifactCommitOutcome('stale', reason='expected ref is not latest')
                    self._record_source_write(command_id, fingerprint, outcome)
                    self._connection.commit()
                    return outcome

                ref = self._next_ref(key)
                self._insert_record(
                    ArtifactRecord(
                        key,
                        ref,
                        payload,
                        ('', command_id),
                        '',
                        MappingProxyType({}),
                        MappingProxyType(dict(metadata or {})),
                    )
                )
                outcome = ArtifactCommitOutcome('committed', {key: ref})
                self._record_source_write(command_id, fingerprint, outcome)
                self._connection.commit()
                return outcome
            except Exception:
                self._connection.rollback()
                raise

    def commit(self, request: ArtifactCommitRequest) -> ArtifactCommitOutcome:
        with self._lock:
            _begin_immediate(self._connection)
            try:
                existing = self._commit_row(request.run_id, request.attempt_id)
                if existing is not None:
                    fingerprint = commit_request_fingerprint(request)
                    if str(existing['request_fingerprint']) == fingerprint:
                        outcome = self.control_codec.decode_payload(str(existing['outcome_json']))
                        self._connection.commit()
                        return outcome
                    self._connection.commit()
                    return ArtifactCommitOutcome('conflict', reason='producer_id conflict')

                fingerprint = commit_request_fingerprint(request)
                shape_failure = self._validate_commit_shape(request)
                if shape_failure is not None:
                    self._record_commit(request, fingerprint, shape_failure)
                    self._connection.commit()
                    return shape_failure

                failure = self._validate_commit_freshness(request)
                if failure is not None:
                    self._record_commit(request, fingerprint, failure)
                    self._connection.commit()
                    return failure

                output_refs = {key: self._next_ref(key) for key in request.output_keys}
                for key in request.output_keys:
                    self._insert_record(
                        ArtifactRecord(
                            key,
                            output_refs[key],
                            ArtifactPayload.from_value(request.output_values[key], role='materialized'),
                            (request.run_id, request.attempt_id),
                            request.producer_op_id,
                            request.input_refs,
                            MappingProxyType({}),
                        )
                    )
                outcome = ArtifactCommitOutcome('committed', output_refs)
                self._record_commit(request, fingerprint, outcome)
                self._connection.commit()
                return outcome
            except Exception:
                self._connection.rollback()
                raise

    def _record_from_row(self, row: sqlite3.Row) -> ArtifactRecord:
        if str(row['value_codec']) != self.value_codec.codec_id:
            raise ValueError(f"artifact value codec mismatch: {row['value_codec']}")
        key = ArtifactKey(str(row['artifact_id']), str(row['partition'] or ''))
        return ArtifactRecord(
            key,
            ArtifactRef(key, int(row['version'])),
            self.value_codec.decode(bytes(row['value_blob'])),
            (str(row['producer_run_id']), str(row['producer_attempt_id'])),
            str(row['producer_op_id']),
            self.control_codec.decode_payload(str(row['input_refs_json'])),
            self.control_codec.decode_payload(str(row['metadata_json'])),
        )

    def _insert_record(self, record: ArtifactRecord) -> None:
        self._connection.execute(
            """
            INSERT INTO artifact_records(
                artifact_id, partition, version, value_blob, value_codec, producer_run_id, producer_attempt_id,
                producer_op_id, input_refs_json, metadata_json
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                record.key.artifact_id,
                record.key.partition,
                record.ref.version,
                self.value_codec.encode(record.value),
                self.value_codec.codec_id,
                record.producer_id[0],
                record.producer_id[1],
                record.producer_op_id,
                _canonical_json(_encode_artifact_ref_map(record.input_refs)),
                _canonical_json(_encode_string_any_map(record.metadata)),
            ),
        )

    def _next_ref(self, key: ArtifactKey) -> ArtifactRef:
        row = self._connection.execute(
            'SELECT MAX(version) AS version FROM artifact_records WHERE artifact_id = ? AND partition = ?',
            (key.artifact_id, key.partition),
        ).fetchone()
        latest = None if row is None else row['version']
        return ArtifactRef(key, 1 if latest is None else int(latest) + 1)

    def _commit_row(self, run_id: str, attempt_id: str) -> sqlite3.Row | None:
        return self._connection.execute(
            'SELECT request_fingerprint, outcome_json FROM artifact_commits WHERE run_id = ? AND attempt_id = ?',
            (run_id, attempt_id),
        ).fetchone()

    def _source_write_row(self, command_id: str) -> sqlite3.Row | None:
        return self._connection.execute(
            'SELECT request_fingerprint, outcome_json FROM artifact_source_writes WHERE command_id = ?',
            (command_id,),
        ).fetchone()

    def _record_source_write(self, command_id: str, fingerprint: str, outcome: ArtifactCommitOutcome) -> None:
        self._connection.execute(
            """
            INSERT INTO artifact_source_writes(command_id, request_fingerprint, outcome_json)
            VALUES (?, ?, ?)
            """,
            (command_id, fingerprint, self.control_codec.encode_payload(outcome)),
        )

    def _record_commit(self, request: ArtifactCommitRequest, fingerprint: str, outcome: ArtifactCommitOutcome) -> None:
        self._connection.execute(
            """
            INSERT INTO artifact_commits(run_id, attempt_id, request_fingerprint, outcome_json)
            VALUES (?, ?, ?, ?)
            """,
            (request.run_id, request.attempt_id, fingerprint, self.control_codec.encode_payload(outcome)),
        )

    def _validate_commit_shape(self, request: ArtifactCommitRequest) -> ArtifactCommitOutcome | None:
        if len(set(request.output_keys)) != len(request.output_keys):
            return ArtifactCommitOutcome('failed', reason='duplicate output keys')
        if set(request.output_keys) != set(request.output_values):
            return ArtifactCommitOutcome('failed', reason='output keys do not match declared outputs')
        return None

    def _validate_commit_freshness(self, request: ArtifactCommitRequest) -> ArtifactCommitOutcome | None:
        for key, ref in request.input_refs.items():
            if key != ref.key:
                return ArtifactCommitOutcome('failed', reason='input key/ref mismatch')
            if self.get(ref) is None:
                return ArtifactCommitOutcome('stale', reason=f'input ref not found: {ref}')
            if self.latest(key) != ref:
                return ArtifactCommitOutcome('stale', reason=f'input ref is not latest: {ref}')
        return None


class SQLiteMutationLog(MutationLog):
    def __init__(self, path: str | Path, *, codec: ControlPlaneCodec | None = None) -> None:
        self.path = str(path)
        self.codec = codec or JSONControlPlaneCodec()
        self._connection = _connect(self.path)
        self._lock = RLock()
        _init_schema(self._connection)

    def get(self, command_id: str) -> ArtifactMutationResult | None:
        row = self._connection.execute(
            'SELECT result_json FROM mutation_results WHERE command_id = ?',
            (command_id,),
        ).fetchone()
        return None if row is None else self.codec.decode_payload(str(row['result_json']))

    def record(self, command_id: str, result: ArtifactMutationResult) -> None:
        with self._lock:
            self._connection.execute(
                'INSERT OR IGNORE INTO mutation_results(command_id, result_json) VALUES (?, ?)',
                (command_id, self.codec.encode_payload(result)),
            )
            self._connection.commit()


class SQLiteInterventionLog(InterventionLog):
    def __init__(self, path: str | Path, *, codec: ControlPlaneCodec | None = None) -> None:
        self.path = str(path)
        self.codec = codec or JSONControlPlaneCodec()
        self._connection = _connect(self.path)
        self._lock = RLock()
        _init_schema(self._connection)

    def get(self, command_id: str) -> InterventionRecord | None:
        row = self._connection.execute(
            'SELECT kind, result_json FROM intervention_records WHERE command_id = ?',
            (command_id,),
        ).fetchone()
        if row is None:
            return None
        return InterventionRecord(str(row['kind']), self.codec.decode_payload(str(row['result_json'])))

    def record(self, command_id: str, kind: InterventionKind, result: InterventionResult) -> None:
        with self._lock:
            self._connection.execute(
                'INSERT OR IGNORE INTO intervention_records(command_id, kind, result_json) VALUES (?, ?, ?)',
                (command_id, kind, self.codec.encode_payload(result)),
            )
            self._connection.commit()


class SQLiteExternalCallLedger:
    def __init__(self, path: str | Path, *, codec: ControlPlaneCodec | None = None) -> None:
        self.path = str(path)
        self.codec = codec or JSONControlPlaneCodec()
        self._connection = _connect(self.path)
        self._lock = RLock()
        _init_schema(self._connection)

    def encode_payload(self, value: Any) -> str:
        return self.codec.encode_payload(value)

    def begin(
        self,
        key: str,
        payload_fingerprint: str,
        *,
        now: float,
        claim_expires_at: float,
        attempt_metadata: Mapping[str, Any] | None = None,
    ) -> ExternalCallAcquireResult:
        with self._lock:
            _begin_immediate(self._connection)
            try:
                row = self._row_for_key(key)
                if row is None:
                    record = ExternalCallRecord(
                        key,
                        payload_fingerprint,
                        'in_progress',
                        claim_token=_new_claim_token(),
                        claim_expires_at=claim_expires_at,
                        attempt_metadata=dict(attempt_metadata or {}),
                    )
                    self._upsert_record(record)
                    self._connection.commit()
                    return ExternalCallAcquireResult('started', record.claim_token, record)

                record = self._record_from_row(row)
                if record.payload_fingerprint != payload_fingerprint:
                    self._connection.commit()
                    return ExternalCallAcquireResult('conflict', record=record)
                if record.status in TERMINAL_REPLAY_STATUSES:
                    self._connection.commit()
                    return ExternalCallAcquireResult('replay', record=record)
                if record.status == 'in_progress' and record.claim_expires_at > now:
                    self._connection.commit()
                    return ExternalCallAcquireResult('in_progress', record=record)
                if record.status in RETRYABLE_STATUSES or record.status == 'in_progress':
                    reclaimed = ExternalCallRecord(
                        key,
                        payload_fingerprint,
                        'in_progress',
                        claim_token=_new_claim_token(),
                        claim_expires_at=claim_expires_at,
                        attempt_metadata=dict(attempt_metadata or {}),
                    )
                    self._upsert_record(reclaimed)
                    self._connection.commit()
                    return ExternalCallAcquireResult('started', reclaimed.claim_token, reclaimed)
                self._connection.commit()
                return ExternalCallAcquireResult('replay', record=record)
            except Exception:
                self._connection.rollback()
                raise

    def complete(self, key: str, claim_token: str, result: ExternalCallResult) -> ExternalCallWriteResult:
        if result.status != 'completed':
            raise ValueError('complete requires a completed result')
        return self._terminal_write(key, claim_token, result)

    def fail(self, key: str, claim_token: str, result: ExternalCallResult) -> ExternalCallWriteResult:
        if result.status in {'completed', 'conflict'}:
            raise ValueError('fail requires a non-completed, non-conflict result')
        return self._terminal_write(key, claim_token, result)

    def reclaim_expired(self, now: float) -> tuple[ExternalCallRecord, ...]:
        with self._lock:
            rows = self._connection.execute(
                """
                SELECT * FROM external_call_records
                WHERE status = 'in_progress' AND claim_expires_at <= ?
                ORDER BY key
                """,
                (now,),
            ).fetchall()
            return tuple(self._record_from_row(row) for row in rows)

    def _terminal_write(self, key: str, claim_token: str, result: ExternalCallResult) -> ExternalCallWriteResult:
        with self._lock:
            _begin_immediate(self._connection)
            try:
                row = self._row_for_key(key)
                current = None if row is None else self._record_from_row(row)
                if current is None or current.status != 'in_progress' or current.claim_token != claim_token:
                    self._connection.commit()
                    return ExternalCallWriteResult('stale', current)
                record = ExternalCallRecord(
                    current.key,
                    current.payload_fingerprint,
                    result.status,
                    result=result,
                    attempt_metadata=current.attempt_metadata,
                )
                self._upsert_record(record)
                self._connection.commit()
                return ExternalCallWriteResult('recorded', record)
            except Exception:
                self._connection.rollback()
                raise

    def _row_for_key(self, key: str) -> sqlite3.Row | None:
        return self._connection.execute('SELECT * FROM external_call_records WHERE key = ?', (key,)).fetchone()

    def _upsert_record(self, record: ExternalCallRecord) -> None:
        result = record.result
        self._connection.execute(
            """
            INSERT INTO external_call_records(
                key, payload_fingerprint, status, claim_token, claim_expires_at,
                result_value_json, error_type, error_message, result_metadata_json, attempt_metadata_json
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(key) DO UPDATE SET
                payload_fingerprint = excluded.payload_fingerprint,
                status = excluded.status,
                claim_token = excluded.claim_token,
                claim_expires_at = excluded.claim_expires_at,
                result_value_json = excluded.result_value_json,
                error_type = excluded.error_type,
                error_message = excluded.error_message,
                result_metadata_json = excluded.result_metadata_json,
                attempt_metadata_json = excluded.attempt_metadata_json
            """,
            (
                record.key,
                record.payload_fingerprint,
                record.status,
                record.claim_token,
                float(record.claim_expires_at),
                None if result is None else self.codec.encode_payload(result.value),
                '' if result is None else result.error_type,
                '' if result is None else result.error_message,
                None if result is None else self.codec.encode_payload(dict(result.metadata)),
                self.codec.encode_payload(dict(record.attempt_metadata)),
            ),
        )

    def _record_from_row(self, row: sqlite3.Row) -> ExternalCallRecord:
        result = None
        if str(row['status']) != 'in_progress':
            value_json = row['result_value_json']
            metadata_json = row['result_metadata_json']
            result = ExternalCallResult(
                str(row['status']),
                None if value_json is None else self.codec.decode_payload(str(value_json)),
                str(row['error_type'] or ''),
                str(row['error_message'] or ''),
                {} if metadata_json is None else self.codec.decode_payload(str(metadata_json)),
            )
        return ExternalCallRecord(
            str(row['key']),
            str(row['payload_fingerprint']),
            str(row['status']),
            str(row['claim_token'] or ''),
            float(row['claim_expires_at'] or 0.0),
            result,
            self.codec.decode_payload(str(row['attempt_metadata_json'])),
        )


class SQLiteRuntimeDriverCheckpointStore:
    def __init__(self, path: str | Path) -> None:
        self.path = str(path)
        self._connection = _connect(self.path)
        self._lock = RLock()
        _init_schema(self._connection)

    def load(self, checkpoint_id: str = 'default') -> RuntimeDriverCheckpoint:
        _validate_checkpoint_id(checkpoint_id)
        with self._lock:
            self._ensure_checkpoint_row(checkpoint_id)
            self._connection.commit()
            row = self._row_for_checkpoint(checkpoint_id)
            if row is None:
                raise RuntimeError('runtime driver checkpoint bootstrap failed')
            return self._checkpoint_from_row(row)

    def save(
        self,
        checkpoint: RuntimeDriverCheckpoint,
        *,
        expected_revision: int,
    ) -> RuntimeDriverCheckpointSaveResult:
        _validate_runtime_checkpoint_save(checkpoint, expected_revision)
        with self._lock:
            _begin_immediate(self._connection)
            try:
                self._ensure_checkpoint_row(checkpoint.checkpoint_id)
                cursor = self._connection.execute(
                    """
                    UPDATE runtime_driver_checkpoints
                    SET revision = revision + 1,
                        cursor = ?,
                        last_tick_id = ?,
                        last_tick_started_at = ?,
                        last_tick_finished_at = ?,
                        consecutive_idle_ticks = ?
                    WHERE checkpoint_id = ?
                      AND revision = ?
                      AND cursor <= ?
                    """,
                    (
                        checkpoint.cursor,
                        checkpoint.last_tick_id,
                        checkpoint.last_tick_started_at,
                        checkpoint.last_tick_finished_at,
                        checkpoint.consecutive_idle_ticks,
                        checkpoint.checkpoint_id,
                        expected_revision,
                        checkpoint.cursor,
                    ),
                )
                if cursor.rowcount == 1:
                    row = self._row_for_checkpoint(checkpoint.checkpoint_id)
                    if row is None:
                        raise RuntimeError('saved runtime driver checkpoint is missing')
                    saved = self._checkpoint_from_row(row)
                    self._connection.commit()
                    return RuntimeDriverCheckpointSaveResult('saved', saved)

                row = self._row_for_checkpoint(checkpoint.checkpoint_id)
                if row is None:
                    raise RuntimeError('runtime driver checkpoint bootstrap failed')
                current = self._checkpoint_from_row(row)
                if current.revision != expected_revision:
                    self._connection.commit()
                    return RuntimeDriverCheckpointSaveResult('stale')
                if current.cursor > checkpoint.cursor:
                    raise ValueError('cursor cannot move backwards')
                raise RuntimeError('runtime driver checkpoint CAS failed unexpectedly')
            except Exception:
                self._connection.rollback()
                raise

    def _ensure_checkpoint_row(self, checkpoint_id: str) -> None:
        self._connection.execute(
            """
            INSERT OR IGNORE INTO runtime_driver_checkpoints(
                checkpoint_id, revision, cursor, last_tick_id,
                last_tick_started_at, last_tick_finished_at, consecutive_idle_ticks
            )
            VALUES (?, 0, 0, '', 0, 0, 0)
            """,
            (checkpoint_id,),
        )

    def _row_for_checkpoint(self, checkpoint_id: str) -> sqlite3.Row | None:
        return self._connection.execute(
            'SELECT * FROM runtime_driver_checkpoints WHERE checkpoint_id = ?',
            (checkpoint_id,),
        ).fetchone()

    @staticmethod
    def _checkpoint_from_row(row: sqlite3.Row) -> RuntimeDriverCheckpoint:
        return RuntimeDriverCheckpoint(
            str(row['checkpoint_id']),
            int(row['revision']),
            int(row['cursor']),
            str(row['last_tick_id']),
            float(row['last_tick_started_at']),
            float(row['last_tick_finished_at']),
            int(row['consecutive_idle_ticks']),
        )


class SQLiteIntentCommandLog(IntentCommandLog):
    def __init__(self, path: str | Path, *, codec: ControlPlaneCodec | None = None) -> None:
        self.path = str(path)
        self.codec = codec or JSONControlPlaneCodec()
        self._connection = _connect(self.path)
        self._lock = RLock()
        _init_schema(self._connection)

    def reserve(
        self,
        command_id: str,
        request_fingerprint: str,
        kind: IntentCommandKind,
        *,
        now: float,
        claim_expires_at: float,
        owner_id: str,
    ) -> IntentCommandAcquireResult:
        _validate_intent_record_key(command_id, request_fingerprint)
        _validate_intent_claim_inputs(now, claim_expires_at, owner_id)
        with self._lock:
            _begin_immediate(self._connection)
            try:
                claim_token = _new_claim_token()
                cursor = self._connection.execute(
                    """
                    INSERT OR IGNORE INTO intent_command_records(
                        command_id, request_fingerprint, kind, result_json,
                        claim_token, claim_expires_at, owner_id, reserved_at
                    )
                    VALUES (?, ?, ?, NULL, ?, ?, ?, ?)
                    """,
                    (command_id, request_fingerprint, kind, claim_token, claim_expires_at, owner_id, now),
                )
                inserted = cursor.rowcount == 1
                row = self._row_for_command(command_id)
                if row is None:
                    raise RuntimeError('intent command reserve failed')
                record = self._record_from_row(row)
                if record.request_fingerprint != request_fingerprint or record.kind != kind:
                    self._connection.commit()
                    return IntentCommandAcquireResult('conflict', record)
                if record.result is None:
                    if not inserted and record.claim_expires_at <= now:
                        claim_token = _new_claim_token()
                        updated = self._connection.execute(
                            """
                            UPDATE intent_command_records
                            SET claim_token = ?,
                                claim_expires_at = ?,
                                owner_id = ?,
                                reserved_at = ?
                            WHERE command_id = ?
                              AND kind = ?
                              AND request_fingerprint = ?
                              AND result_json IS NULL
                              AND claim_expires_at <= ?
                            """,
                            (
                                claim_token,
                                claim_expires_at,
                                owner_id,
                                now,
                                command_id,
                                kind,
                                request_fingerprint,
                                now,
                            ),
                        )
                        if updated.rowcount == 1:
                            row = self._row_for_command(command_id)
                            if row is None:
                                raise RuntimeError('intent command reclaim failed')
                            record = self._record_from_row(row)
                            self._connection.commit()
                            return IntentCommandAcquireResult('reserved', record)
                        row = self._row_for_command(command_id)
                        if row is None:
                            raise RuntimeError('intent command reserve row disappeared')
                        record = self._record_from_row(row)
                        if record.result is not None:
                            self._connection.commit()
                            return IntentCommandAcquireResult('replay', record)
                        self._connection.commit()
                        return IntentCommandAcquireResult('in_progress', record)
                    self._connection.commit()
                    return IntentCommandAcquireResult('reserved' if inserted else 'in_progress', record)
                self._connection.commit()
                return IntentCommandAcquireResult('replay', record)
            except Exception:
                self._connection.rollback()
                raise

    def complete(
        self,
        command_id: str,
        request_fingerprint: str,
        kind: IntentCommandKind,
        *,
        claim_token: str,
        result: IntentCommandResult,
    ) -> IntentCommandWriteResult:
        _validate_intent_record_key(command_id, request_fingerprint)
        if not claim_token or not claim_token.strip():
            raise ValueError('claim_token must be non-empty')
        result_json = self.codec.encode_payload(replace(result, replayed=False))
        with self._lock:
            _begin_immediate(self._connection)
            try:
                cursor = self._connection.execute(
                    """
                    UPDATE intent_command_records
                    SET result_json = ?
                    WHERE command_id = ?
                      AND kind = ?
                      AND request_fingerprint = ?
                      AND claim_token = ?
                      AND result_json IS NULL
                    """,
                    (result_json, command_id, kind, request_fingerprint, claim_token),
                )
                row = self._row_for_command(command_id)
                if row is None:
                    self._connection.commit()
                    return IntentCommandWriteResult('stale')
                record = self._record_from_row(row)
                if cursor.rowcount == 1:
                    self._connection.commit()
                    return IntentCommandWriteResult('recorded', record)
                self._connection.commit()
                return IntentCommandWriteResult('stale', record)
            except Exception:
                self._connection.rollback()
                raise

    def _row_for_command(self, command_id: str) -> sqlite3.Row | None:
        return self._connection.execute(
            'SELECT * FROM intent_command_records WHERE command_id = ?',
            (command_id,),
        ).fetchone()

    def _record_from_row(self, row: sqlite3.Row) -> IntentCommandRecord:
        result_json = row['result_json']
        return IntentCommandRecord(
            str(row['command_id']),
            str(row['request_fingerprint']),
            str(row['kind']),
            None if result_json is None else self.codec.decode_payload(str(result_json)),
            str(row['claim_token'] or ''),
            float(row['claim_expires_at'] or 0.0),
            str(row['owner_id'] or ''),
            float(row['reserved_at'] or 0.0),
        )


@dataclass(frozen=True)
class SQLiteRuntimeStores:
    event_log: SQLiteEventLog
    artifact_store: SQLiteArtifactStore
    mutation_log: SQLiteMutationLog
    intervention_log: SQLiteInterventionLog
    external_call_ledger: SQLiteExternalCallLedger
    runtime_driver_checkpoints: SQLiteRuntimeDriverCheckpointStore
    intent_command_log: SQLiteIntentCommandLog

    def close(self) -> None:
        for store in (
            self.event_log,
            self.artifact_store,
            self.mutation_log,
            self.intervention_log,
            self.external_call_ledger,
            self.runtime_driver_checkpoints,
            self.intent_command_log,
        ):
            _close_sqlite_owner(store)


def open_sqlite_runtime(
    path: str | Path,
    *,
    value_codec: ArtifactValueCodec | None = None,
    control_codec: ControlPlaneCodec | None = None,
) -> SQLiteRuntimeStores:
    codec = control_codec or JSONControlPlaneCodec()
    return SQLiteRuntimeStores(
        SQLiteEventLog(path, codec=codec),
        SQLiteArtifactStore(path, value_codec=value_codec, control_codec=codec),
        SQLiteMutationLog(path, codec=codec),
        SQLiteInterventionLog(path, codec=codec),
        SQLiteExternalCallLedger(path, codec=codec),
        SQLiteRuntimeDriverCheckpointStore(path),
        SQLiteIntentCommandLog(path, codec=codec),
    )


def request_fingerprint(request_payload: Mapping[str, Any]) -> str:
    return json_mapping_fingerprint(request_payload, allow_tuple=True, reject_reserved_envelope=False)


def artifact_value_hash(value: Any, codec: ArtifactValueCodec) -> str:
    return hashlib.sha256(codec.codec_id.encode() + b'\0' + codec.encode(value)).hexdigest()


def _validate_runtime_checkpoint_save(checkpoint: RuntimeDriverCheckpoint, expected_revision: int) -> None:
    _validate_checkpoint_id(checkpoint.checkpoint_id)
    if expected_revision < 0:
        raise ValueError('expected_revision must be >= 0')
    if checkpoint.revision < 0:
        raise ValueError('checkpoint.revision must be >= 0')
    if checkpoint.cursor < 0:
        raise ValueError('checkpoint.cursor must be >= 0')


def _validate_checkpoint_id(checkpoint_id: str) -> None:
    if not checkpoint_id or not checkpoint_id.strip():
        raise ValueError('checkpoint_id must be non-empty')


def _validate_intent_record_key(command_id: str, request_fingerprint: str) -> None:
    if not command_id or not command_id.strip():
        raise ValueError('command_id must be non-empty')
    if not request_fingerprint or not request_fingerprint.strip():
        raise ValueError('request_fingerprint must be non-empty')


def _validate_intent_claim_inputs(now: float, claim_expires_at: float, owner_id: str) -> None:
    if not math.isfinite(now):
        raise ValueError('now must be finite')
    if not math.isfinite(claim_expires_at):
        raise ValueError('claim_expires_at must be finite')
    if claim_expires_at <= now:
        raise ValueError('claim_expires_at must be greater than now')
    if not owner_id or not owner_id.strip():
        raise ValueError('owner_id must be non-empty')


def _encode_control(value: Any) -> Any:
    if isinstance(value, ArtifactKey | ArtifactRef | ArtifactPayload):
        return encode_basic_control_value(value)
    if isinstance(value, PlanInput):
        return _envelope(
            'PlanInput',
            name=value.name,
            key=_encode_control(value.key),
            version=_encode_control(value.version) if value.version is not None else None,
            planned=value.planned,
            required=value.required,
            input_kind=value.input_kind,
            collection_name=value.collection_name,
        )
    if isinstance(value, PlanOp):
        return _envelope(
            'PlanOp',
            op_id=value.op_id,
            input_bindings=[_encode_control(item) for item in value.input_bindings],
            output_key_by_name=_encode_string_artifact_key_map(value.output_key_by_name),
            depends_on=_encode_string_list(value.depends_on),
            graph_revision=value.graph_revision,
            flow=value.flow,
            stage=value.stage,
            tags=_encode_string_any_map(value.tags),
        )
    if isinstance(value, ExecutionPlan):
        return _envelope(
            'ExecutionPlan',
            plan_id=value.plan_id,
            graph_revision=value.graph_revision,
            layers=_encode_execution_plan_layers(value.layers),
        )
    if isinstance(value, PlanInstance):
        return _envelope(
            'PlanInstance',
            run_id=value.run_id,
            plan_id=value.plan_id,
            plan_version=value.plan_version,
            epoch=value.epoch,
            graph_revision=value.graph_revision,
            target_artifacts=_encode_artifact_key_list(value.target_artifacts),
            reason=value.reason,
            plan=_encode_control(value.plan),
        )
    if isinstance(value, Attempt):
        return _envelope(
            'Attempt',
            attempt_id=value.attempt_id,
            run_id=value.run_id,
            plan_version=value.plan_version,
            epoch=value.epoch,
            op_id=value.op_id,
            resolved_input_refs=_encode_artifact_ref_map(value.resolved_input_refs),
            output_artifact_keys=_encode_artifact_key_list(value.output_artifact_keys),
            depends_on=_encode_string_list(value.depends_on),
            status=value.status,
            attempt_number=value.attempt_number,
            claim_id=value.claim_id,
            output_refs=_encode_artifact_ref_map(value.output_refs),
            reason=value.reason,
            worker_id=value.worker_id,
            lease_expires_at=value.lease_expires_at,
            claim_generation=value.claim_generation,
            lease_recovery_count=value.lease_recovery_count,
        )
    if isinstance(value, AttemptClaim):
        return _envelope(
            'AttemptClaim',
            claim_id=value.claim_id,
            attempt_id=value.attempt_id,
            run_id=value.run_id,
            plan_version=value.plan_version,
            epoch=value.epoch,
            op_id=value.op_id,
            plan_op=_encode_control(value.plan_op),
            resolved_input_refs=_encode_artifact_ref_map(value.resolved_input_refs),
            output_artifact_keys=_encode_artifact_key_list(value.output_artifact_keys),
            cancel_requested=value.cancel_requested,
            worker_id=value.worker_id,
            lease_expires_at=value.lease_expires_at,
            claim_generation=value.claim_generation,
        )
    if isinstance(value, AttemptResult):
        return _envelope(
            'AttemptResult',
            attempt_id=value.attempt_id,
            status=value.status,
            commit_status=value.commit_status,
            reason=value.reason,
        )
    if isinstance(value, AttemptFailureEnvelope):
        return _envelope('AttemptFailureEnvelope', error_type=value.error_type, error_message=value.error_message)
    if isinstance(value, CommitResult):
        return _envelope(
            'CommitResult',
            status=value.status,
            output_refs=_encode_artifact_ref_map(value.output_refs),
            reason=value.reason,
        )
    if isinstance(value, RunState):
        return _envelope(
            'RunState',
            run_id=value.run_id,
            status=value.status,
            active_plan_version=value.active_plan_version,
            epoch=value.epoch,
        )
    if isinstance(value, ArtifactCommitOutcome):
        return _envelope(
            'ArtifactCommitOutcome',
            status=value.status,
            output_refs=_encode_artifact_ref_map(value.output_refs),
            reason=value.reason,
        )
    if isinstance(value, ArtifactMutationResult):
        return _envelope(
            'ArtifactMutationResult',
            status=value.status,
            artifact=_encode_control(value.artifact),
            ref=_encode_control(value.ref) if value.ref is not None else None,
            reason=value.reason,
        )
    if isinstance(value, ReconcileResult):
        return _envelope(
            'ReconcileResult',
            status=value.status,
            changed_artifacts=_encode_artifact_key_list(value.changed_artifacts),
            materialize_artifacts=_encode_artifact_key_list(value.materialize_artifacts),
            target_artifacts=_encode_artifact_key_list(value.target_artifacts),
            plan_instance=_encode_control(value.plan_instance) if value.plan_instance is not None else None,
            reason=value.reason,
        )
    if isinstance(value, InterventionResult):
        return _envelope(
            'InterventionResult',
            status=value.status,
            mutation_result=_encode_control(value.mutation_result) if value.mutation_result is not None else None,
            reconcile_result=_encode_control(value.reconcile_result) if value.reconcile_result is not None else None,
            run=_encode_control(value.run) if value.run is not None else None,
            reason=value.reason,
        )
    if isinstance(value, IntentControllerResult):
        return _envelope(
            'IntentControllerResult',
            action=value.action,
            run_id=value.run_id,
            status=value.status,
            retry_attempt_ids=_encode_string_list(value.retry_attempt_ids),
            reason=value.reason,
        )
    if isinstance(value, IntentAdvanceResult):
        return _envelope(
            'IntentAdvanceResult',
            status=value.status,
            ticks=value.ticks,
            cursor=value.cursor,
            partial_sync=value.partial_sync,
            recovered_run_ids=_encode_string_list(value.recovered_run_ids),
            dispatched_run_ids=_encode_string_list(value.dispatched_run_ids),
        )
    if isinstance(value, IntentPlanResult):
        return _envelope(
            'IntentPlanResult',
            run_id=value.run_id,
            plan_id=value.plan_id,
            plan_version=value.plan_version,
            target_artifacts=_encode_artifact_key_list(value.target_artifacts),
            target_artifact_count=value.target_artifact_count,
            reason=value.reason,
        )
    if isinstance(value, IntentCommandResult):
        return _envelope(
            'IntentCommandResult',
            status=value.status,
            kind=value.kind,
            replayed=value.replayed,
            intervention_result=_encode_control(
                value.intervention_result) if value.intervention_result is not None else None,
            controller_result=_encode_control(value.controller_result) if value.controller_result is not None else None,
            advance_result=_encode_control(value.advance_result) if value.advance_result is not None else None,
            reason=value.reason,
            plan_result=_encode_control(value.plan_result) if value.plan_result is not None else None,
        )
    if isinstance(value, Mapping) and _is_artifact_ref_map(value):
        return _encode_artifact_ref_map(value)
    if isinstance(value, dict):
        return _encode_event_payload(value)
    if _is_json_scalar(value):
        return value
    raise TypeError(f'unsupported control-plane value: {type(value).__name__}')


def _decode_control(value: Any) -> Any:
    if _is_json_scalar(value):
        return value
    if isinstance(value, list):
        return [_decode_control(item) for item in value]
    if not isinstance(value, dict):
        raise TypeError(f'unsupported encoded value: {type(value).__name__}')
    if 'schema_version' not in value or 'type' not in value:
        return _decode_plain_json_object(value)
    if value['schema_version'] not in _SUPPORTED_SCHEMA_VERSIONS:
        raise ValueError(f"unsupported schema_version: {value['schema_version']}")

    item_type = value['type']
    if is_basic_control_envelope(value):
        return decode_basic_control_value(value)
    if item_type == 'ArtifactKey':
        return ArtifactKey(str(value['artifact_id']), str(value.get('partition') or ''))
    if item_type == 'ArtifactRef':
        return ArtifactRef(_decode_control(value['key']), int(value['version']))
    if item_type == 'ArtifactPayload':
        return ArtifactPayload(
            str(value['schema']),
            _decode_json_compatible(value['payload']),
            _decode_string_any_map(value['metadata']),
            tuple(_decode_json_compatible(item) for item in value.get('fragments') or ()),
            str(value.get('role') or ''),
        )
    if item_type == 'PlanInput':
        version = _decode_control(value['version']) if value.get('version') is not None else None
        return PlanInput(
            str(value['name']),
            _decode_control(value['key']),
            version,
            bool(value['planned']),
            bool(value['required']),
            input_kind=str(value.get('input_kind') or 'single'),
            collection_name=str(value.get('collection_name') or ''),
        )
    if item_type == 'PlanOp':
        return PlanOp(
            str(value['op_id']),
            tuple(_decode_control(item) for item in value['input_bindings']),
            _decode_string_artifact_key_map(value['output_key_by_name']),
            _decode_string_list(value['depends_on']),
            int(value['graph_revision']),
            str(value.get('flow') or ''),
            str(value.get('stage') or ''),
            MappingProxyType(_decode_string_any_map(value['tags'])),
        )
    if item_type == 'ExecutionPlan':
        return ExecutionPlan(
            str(value['plan_id']),
            int(value['graph_revision']),
            _decode_execution_plan_layers(value['layers']),
        )
    if item_type == 'PlanInstance':
        return PlanInstance(
            str(value['run_id']),
            str(value['plan_id']),
            int(value['plan_version']),
            int(value['epoch']),
            int(value['graph_revision']),
            _decode_artifact_key_list(value['target_artifacts']),
            str(value.get('reason') or ''),
            _decode_control(value['plan']),
        )
    if item_type == 'Attempt':
        return Attempt(
            str(value['attempt_id']),
            str(value['run_id']),
            int(value['plan_version']),
            int(value['epoch']),
            str(value['op_id']),
            dict(_decode_artifact_ref_map(value['resolved_input_refs'])),
            _decode_artifact_key_list(value['output_artifact_keys']),
            _decode_string_list(value['depends_on']),
            str(value['status']),
            int(value['attempt_number']),
            str(value.get('claim_id') or ''),
            dict(_decode_artifact_ref_map(value['output_refs'])),
            str(value.get('reason') or ''),
            str(value.get('worker_id') or ''),
            float(value.get('lease_expires_at') or 0),
            int(value.get('claim_generation') or 0),
            int(value.get('lease_recovery_count') or 0),
        )
    if item_type == 'AttemptClaim':
        return AttemptClaim(
            str(value['claim_id']),
            str(value['attempt_id']),
            str(value['run_id']),
            int(value['plan_version']),
            int(value['epoch']),
            str(value['op_id']),
            _decode_control(value['plan_op']),
            dict(_decode_artifact_ref_map(value['resolved_input_refs'])),
            _decode_artifact_key_list(value['output_artifact_keys']),
            bool(value.get('cancel_requested', False)),
            str(value.get('worker_id') or ''),
            float(value.get('lease_expires_at') or 0),
            int(value.get('claim_generation') or 0),
        )
    if item_type == 'AttemptResult':
        return AttemptResult(str(value['attempt_id']), str(value['status']),
                             value.get('commit_status'), str(value.get('reason') or ''))
    if item_type == 'AttemptFailureEnvelope':
        return AttemptFailureEnvelope(str(value.get('error_type') or ''), str(value.get('error_message') or ''))
    if item_type == 'CommitResult':
        return CommitResult(str(value['status']),
                            dict(_decode_artifact_ref_map(value['output_refs'])),
                            str(value.get('reason') or ''))
    if item_type == 'RunState':
        return RunState(str(value['run_id']), str(value['status']), value.get(
            'active_plan_version'), int(value.get('epoch') or 0))
    if item_type == 'ArtifactCommitOutcome':
        return ArtifactCommitOutcome(str(value['status']), _decode_artifact_ref_map(
            value['output_refs']), str(value.get('reason') or ''))
    if item_type == 'ArtifactMutationResult':
        ref = _decode_control(value['ref']) if value.get('ref') is not None else None
        return ArtifactMutationResult(str(value['status']), _decode_control(
            value['artifact']), ref, str(value.get('reason') or ''))
    if item_type == 'ReconcileResult':
        plan_instance = _decode_control(value['plan_instance']) if value.get('plan_instance') is not None else None
        return ReconcileResult(
            str(value['status']),
            _decode_artifact_key_list(value['changed_artifacts']),
            _decode_artifact_key_list(value['materialize_artifacts']),
            _decode_artifact_key_list(value['target_artifacts']),
            plan_instance,
            str(value.get('reason') or ''),
        )
    if item_type == 'InterventionResult':
        return InterventionResult(
            str(value['status']),
            _decode_control(value['mutation_result']) if value.get('mutation_result') is not None else None,
            _decode_control(value['reconcile_result']) if value.get('reconcile_result') is not None else None,
            _decode_control(value['run']) if value.get('run') is not None else None,
            str(value.get('reason') or ''),
        )
    if item_type == 'IntentControllerResult':
        return IntentControllerResult(
            str(value['action']),
            str(value['run_id']),
            str(value['status']),
            _decode_string_list(value['retry_attempt_ids']),
            str(value.get('reason') or ''),
        )
    if item_type == 'IntentAdvanceResult':
        return IntentAdvanceResult(
            str(value['status']),
            int(value['ticks']),
            int(value['cursor']),
            bool(value.get('partial_sync', False)),
            _decode_string_list(value['recovered_run_ids']),
            _decode_string_list(value['dispatched_run_ids']),
        )
    if item_type == 'IntentPlanResult':
        return IntentPlanResult(
            str(value['run_id']),
            str(value['plan_id']),
            int(value['plan_version']),
            _decode_artifact_key_list(value['target_artifacts']),
            int(value['target_artifact_count']),
            str(value.get('reason') or ''),
        )
    if item_type == 'IntentCommandResult':
        return IntentCommandResult(
            str(value['status']),
            str(value['kind']),
            bool(value.get('replayed', False)),
            _decode_control(value['intervention_result']) if value.get('intervention_result') is not None else None,
            _decode_control(value['controller_result']) if value.get('controller_result') is not None else None,
            _decode_control(value['advance_result']) if value.get('advance_result') is not None else None,
            str(value.get('reason') or ''),
            _decode_control(value['plan_result']) if value['plan_result'] is not None else None,
        )
    if item_type == 'EventPayload':
        return {_require_string(key): _decode_control(item) for key, item in value['items']}
    if item_type == 'ArtifactRefMap':
        return _decode_artifact_ref_map(value)
    if item_type == 'StringAnyMap':
        return _decode_string_any_map(value)
    if item_type == 'ArtifactKeyList':
        return _decode_artifact_key_list(value)
    if item_type == 'StringList':
        return _decode_string_list(value)
    if item_type == 'PlanOpList':
        return tuple(_decode_control(item) for item in value['items'])
    if item_type == 'ExecutionPlanLayers':
        return tuple(_decode_control(layer) for layer in value['items'])
    raise ValueError(f'unsupported control-plane type: {item_type}')


def _envelope(item_type: str, **fields: Any) -> dict[str, Any]:
    return {'schema_version': _SCHEMA_VERSION, 'type': item_type, **fields}


def _encode_event_payload(values: dict[Any, Any]) -> dict[str, Any]:
    _reject_reserved_envelope_shape(values)
    items: list[list[Any]] = []
    for key in sorted(values):
        if not isinstance(key, str):
            raise TypeError('EventPayload keys must be strings')
        items.append([key, _encode_event_value(values[key])])
    return _envelope('EventPayload', items=items)


def _encode_event_value(value: Any) -> Any:
    if _is_json_scalar(value):
        return value
    if isinstance(value, list):
        return [_encode_event_value(item) for item in value]
    if isinstance(value, tuple):
        return [_encode_event_value(item) for item in value]
    if isinstance(value, Mapping) and _is_artifact_ref_map(value):
        return _encode_artifact_ref_map(value)
    if type(value) is dict:
        _reject_reserved_envelope_shape(value)
        return {key: _encode_event_value(item) for key, item in _sorted_string_items(value)}
    if isinstance(value, Mapping):
        raise TypeError('plain event payload mappings must be dicts with string keys')
    return _encode_control(value)


def _decode_plain_json_object(values: dict[str, Any]) -> dict[str, Any]:
    return {key: _decode_control(item) for key, item in _sorted_string_items(values)}


def _encode_artifact_ref_map(values: Mapping[ArtifactKey, ArtifactRef]) -> dict[str, Any]:
    return _envelope(
        'ArtifactRefMap',
        items=[[_encode_control(key), _encode_control(ref)] for key, ref in sorted(values.items())],
    )


def _decode_artifact_ref_map(value: Mapping[str, Any]) -> Mapping[ArtifactKey, ArtifactRef]:
    if value.get('type') != 'ArtifactRefMap':
        raise ValueError('expected ArtifactRefMap')
    return MappingProxyType({_decode_control(key): _decode_control(ref) for key, ref in value['items']})


def _encode_string_any_map(values: Mapping[str, Any]) -> dict[str, Any]:
    return _envelope('StringAnyMap', items=[[key, _encode_json_compatible(item)]
                     for key, item in _sorted_string_items(values)])


def _decode_string_any_map(value: Mapping[str, Any]) -> Mapping[str, Any]:
    if value.get('type') != 'StringAnyMap':
        raise ValueError('expected StringAnyMap')
    return MappingProxyType({_require_string(key): _decode_json_compatible(item) for key, item in value['items']})


def _encode_artifact_key_list(values: tuple[ArtifactKey, ...]) -> dict[str, Any]:
    return _envelope('ArtifactKeyList', items=[_encode_control(key) for key in values])


def _decode_artifact_key_list(value: Mapping[str, Any]) -> tuple[ArtifactKey, ...]:
    if value.get('type') != 'ArtifactKeyList':
        raise ValueError('expected ArtifactKeyList')
    return tuple(_decode_control(item) for item in value['items'])


def _encode_string_artifact_key_map(values: Mapping[str, ArtifactKey]) -> dict[str, Any]:
    return _envelope(
        'StringArtifactKeyMap',
        items=[[name, _encode_control(key)] for name, key in _sorted_string_items(values)],
    )


def _decode_string_artifact_key_map(value: Mapping[str, Any]) -> Mapping[str, ArtifactKey]:
    if value.get('type') != 'StringArtifactKeyMap':
        raise ValueError('expected StringArtifactKeyMap')
    return MappingProxyType({_require_string(name): _decode_control(key) for name, key in value['items']})


def _encode_string_list(values: tuple[str, ...]) -> dict[str, Any]:
    return _envelope('StringList', items=list(values))


def _decode_string_list(value: Mapping[str, Any]) -> tuple[str, ...]:
    if value.get('type') != 'StringList':
        raise ValueError('expected StringList')
    return tuple(_require_string(item) for item in value['items'])


def _encode_plan_op_list(values: tuple[PlanOp, ...]) -> dict[str, Any]:
    return _envelope('PlanOpList', items=[_encode_control(plan_op) for plan_op in values])


def _encode_execution_plan_layers(values: tuple[tuple[PlanOp, ...], ...]) -> dict[str, Any]:
    return _envelope('ExecutionPlanLayers', items=[_encode_plan_op_list(layer) for layer in values])


def _decode_execution_plan_layers(value: Mapping[str, Any]) -> tuple[tuple[PlanOp, ...], ...]:
    if value.get('type') != 'ExecutionPlanLayers':
        raise ValueError('expected ExecutionPlanLayers')
    return tuple(_decode_control(layer) for layer in value['items'])


def _encode_json_compatible(value: Any) -> Any:
    return normalize_json_value(value, allow_tuple=False, reject_reserved_envelope=True)


def _decode_json_compatible(value: Any) -> Any:
    if _is_json_scalar(value):
        return value
    if isinstance(value, list):
        return [_decode_json_compatible(item) for item in value]
    if isinstance(value, dict):
        return {key: _decode_json_compatible(item) for key, item in _sorted_string_items(value)}
    raise TypeError(f'value is not JSON-compatible: {type(value).__name__}')


def _sorted_string_items(values: Mapping[Any, Any]) -> list[tuple[str, Any]]:
    return sorted_string_items(values)


def _reject_reserved_envelope_shape(values: Mapping[Any, Any]) -> None:
    if 'schema_version' in values and 'type' in values:
        raise TypeError('plain JSON objects cannot use reserved schema_version/type envelope keys')


def _is_json_scalar(value: Any) -> bool:
    return is_json_scalar(value)


def _require_string(value: Any) -> str:
    if not isinstance(value, str):
        raise TypeError('mapping keys must be strings')
    return value


def _canonical_json(value: Any) -> str:
    return canonical_json(value)


def _begin_immediate(connection: sqlite3.Connection) -> None:
    connection.execute('BEGIN IMMEDIATE')


def _new_claim_token() -> str:
    return uuid.uuid4().hex


def _validate_scan_window(seq: int, limit: int) -> None:
    if seq < 0:
        raise ValueError('seq must be >= 0')
    if limit < 1:
        raise ValueError('limit must be >= 1')


def _is_artifact_ref_map(value: Mapping[Any, Any]) -> bool:
    return bool(value) and all(
        isinstance(
            key,
            ArtifactKey) and isinstance(
            item,
            ArtifactRef) for key,
        item in value.items())


def _connect(path: str) -> sqlite3.Connection:
    connection = sqlite3.connect(path, check_same_thread=False)
    connection.row_factory = sqlite3.Row
    return connection


def _close_sqlite_owner(owner: Any) -> None:
    connection = getattr(owner, '_connection', None)
    if connection is None:
        return
    try:
        connection.close()
    except sqlite3.Error:
        pass
    finally:
        try:
            owner._connection = None
        except Exception:
            pass


def _init_schema(connection: sqlite3.Connection) -> None:
    connection.executescript(
        """
        CREATE TABLE IF NOT EXISTS controller_events (
            seq INTEGER PRIMARY KEY AUTOINCREMENT,
            run_id TEXT NOT NULL,
            event_type TEXT NOT NULL,
            payload_json TEXT NOT NULL
        );
        CREATE INDEX IF NOT EXISTS idx_controller_events_run_seq
            ON controller_events(run_id, seq);
        CREATE TABLE IF NOT EXISTS artifact_records (
            artifact_id TEXT NOT NULL,
            partition TEXT NOT NULL DEFAULT '',
            version INTEGER NOT NULL,
            value_blob BLOB NOT NULL,
            value_codec TEXT NOT NULL,
            producer_run_id TEXT NOT NULL,
            producer_attempt_id TEXT NOT NULL,
            producer_op_id TEXT NOT NULL,
            input_refs_json TEXT NOT NULL,
            metadata_json TEXT NOT NULL,
            PRIMARY KEY (artifact_id, partition, version)
        );
        CREATE TABLE IF NOT EXISTS artifact_commits (
            run_id TEXT NOT NULL,
            attempt_id TEXT NOT NULL,
            request_fingerprint TEXT NOT NULL,
            outcome_json TEXT NOT NULL,
            PRIMARY KEY (run_id, attempt_id)
        );
        CREATE TABLE IF NOT EXISTS artifact_source_writes (
            command_id TEXT PRIMARY KEY,
            request_fingerprint TEXT NOT NULL,
            outcome_json TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS mutation_results (
            command_id TEXT PRIMARY KEY,
            result_json TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS intervention_records (
            command_id TEXT PRIMARY KEY,
            kind TEXT NOT NULL,
            result_json TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS external_call_records (
            key TEXT PRIMARY KEY,
            payload_fingerprint TEXT NOT NULL,
            status TEXT NOT NULL,
            claim_token TEXT NOT NULL,
            claim_expires_at REAL NOT NULL,
            result_value_json TEXT,
            error_type TEXT NOT NULL,
            error_message TEXT NOT NULL,
            result_metadata_json TEXT,
            attempt_metadata_json TEXT NOT NULL
        );
        CREATE INDEX IF NOT EXISTS idx_external_call_records_expired_claims
            ON external_call_records(status, claim_expires_at);
        CREATE TABLE IF NOT EXISTS runtime_driver_checkpoints (
            checkpoint_id TEXT PRIMARY KEY,
            revision INTEGER NOT NULL,
            cursor INTEGER NOT NULL,
            last_tick_id TEXT NOT NULL,
            last_tick_started_at REAL NOT NULL,
            last_tick_finished_at REAL NOT NULL,
            consecutive_idle_ticks INTEGER NOT NULL
        );
        CREATE TABLE IF NOT EXISTS intent_command_records (
            command_id TEXT PRIMARY KEY,
            request_fingerprint TEXT NOT NULL,
            kind TEXT NOT NULL,
            result_json TEXT,
            claim_token TEXT NOT NULL DEFAULT '',
            claim_expires_at REAL NOT NULL DEFAULT 0,
            owner_id TEXT NOT NULL DEFAULT '',
            reserved_at REAL NOT NULL DEFAULT 0
        );
        """
    )
    _migrate_intent_command_records(connection)
    connection.commit()


def _migrate_intent_command_records(connection: sqlite3.Connection) -> None:
    rows = connection.execute('PRAGMA table_info(intent_command_records)').fetchall()
    columns = {str(row['name']) for row in rows}
    migrations = {
        'claim_token': "ALTER TABLE intent_command_records ADD COLUMN claim_token TEXT NOT NULL DEFAULT ''",
        'claim_expires_at': 'ALTER TABLE intent_command_records ADD COLUMN claim_expires_at REAL NOT NULL DEFAULT 0',
        'owner_id': "ALTER TABLE intent_command_records ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''",
        'reserved_at': 'ALTER TABLE intent_command_records ADD COLUMN reserved_at REAL NOT NULL DEFAULT 0',
    }
    for column, sql in migrations.items():
        if column not in columns:
            connection.execute(sql)
