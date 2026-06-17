from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
import hashlib
import pickle
from threading import RLock
from types import MappingProxyType
from typing import Any, Literal, Protocol

from .artifact import ArtifactKey, ArtifactPayload, ArtifactRef
from .controller import Attempt, AttemptExecutionResult, CommitResult
from .plan import PlanOp
from .utils import canonical_json, is_json_scalar, json_mapping_fingerprint, validate_nonempty

ArtifactCommitStatus = Literal['committed', 'stale', 'conflict', 'failed']


@dataclass(frozen=True)
class ArtifactRecord:
    key: ArtifactKey
    ref: ArtifactRef
    value: ArtifactPayload
    producer_id: tuple[str, str]
    producer_op_id: str
    input_refs: Mapping[ArtifactKey, ArtifactRef] = field(default_factory=lambda: MappingProxyType({}))
    metadata: Mapping[str, Any] = field(default_factory=lambda: MappingProxyType({}))

    def __post_init__(self) -> None:
        object.__setattr__(self, 'input_refs', _freeze_mapping(self.input_refs))
        object.__setattr__(self, 'metadata', _freeze_mapping(self.metadata))


@dataclass(frozen=True)
class ArtifactCommitRequest:
    run_id: str
    attempt_id: str
    producer_op_id: str
    output_keys: tuple[ArtifactKey, ...]
    output_values: Mapping[ArtifactKey, Any]
    input_refs: Mapping[ArtifactKey, ArtifactRef]

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.attempt_id, 'attempt_id')
        validate_nonempty(self.producer_op_id, 'producer_op_id')
        object.__setattr__(self, 'output_keys', tuple(self.output_keys))
        object.__setattr__(self, 'output_values', _freeze_mapping(self.output_values))
        object.__setattr__(self, 'input_refs', _freeze_mapping(self.input_refs))


@dataclass(frozen=True)
class ArtifactCommitOutcome:
    status: ArtifactCommitStatus
    output_refs: Mapping[ArtifactKey, ArtifactRef] = field(default_factory=lambda: MappingProxyType({}))
    reason: str = ''

    def __post_init__(self) -> None:
        object.__setattr__(self, 'output_refs', _freeze_mapping(self.output_refs))


class ArtifactStore(Protocol):
    def latest(self, key: ArtifactKey) -> ArtifactRef | None:
        ...

    def get(self, ref: ArtifactRef) -> ArtifactRecord | None:
        ...

    def history(self, key: ArtifactKey) -> tuple[ArtifactRecord, ...]:
        ...

    def put_source(self, key: ArtifactKey, value: Any, *, metadata: Mapping[str, Any] | None = None) -> ArtifactRef:
        ...

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
        ...

    def commit(self, request: ArtifactCommitRequest) -> ArtifactCommitOutcome:
        ...


class InMemoryArtifactStore:
    """Single-process artifact store for FC-2 compare-and-set semantics."""

    def __init__(self) -> None:
        self._records_by_key: dict[ArtifactKey, list[ArtifactRecord]] = {}
        self._records_by_ref: dict[tuple[str, str, int], ArtifactRecord] = {}
        self._latest_by_key: dict[ArtifactKey, ArtifactRef] = {}
        self._commits_by_producer: dict[tuple[str, str], tuple[str, ArtifactCommitOutcome]] = {}
        self._source_writes: dict[str, tuple[str, ArtifactCommitOutcome]] = {}
        self._lock = RLock()

    def latest(self, key: ArtifactKey) -> ArtifactRef | None:
        with self._lock:
            return self._latest_by_key.get(key)

    def get(self, ref: ArtifactRef) -> ArtifactRecord | None:
        with self._lock:
            record = self._records_by_ref.get(_ref_key(ref))
            return None if record is None else _record_snapshot(record)

    def history(self, key: ArtifactKey) -> tuple[ArtifactRecord, ...]:
        with self._lock:
            return tuple(_record_snapshot(record) for record in self._records_by_key.get(key, ()))

    def put_source(self, key: ArtifactKey, value: Any, *, metadata: Mapping[str, Any] | None = None) -> ArtifactRef:
        with self._lock:
            payload = _payload(value, metadata=metadata, role='source')
            ref = self._next_ref(key)
            record = _make_record(
                key=key,
                ref=ref,
                value=payload,
                producer_id=('', ''),
                producer_op_id='',
                input_refs={},
                metadata=metadata or {},
            )
            self._write_record(record)
            return ref

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
            payload = _payload(value, metadata=metadata, role='source')
            identity = source_write_fingerprint(key, payload, expected_ref, create_only)
            if previous := self._source_writes.get(command_id):
                previous_identity, outcome = previous
                return outcome if previous_identity == identity else ArtifactCommitOutcome(
                    'conflict', reason='source command conflict')
            latest = self._latest_by_key.get(key)
            if create_only and latest is not None:
                record = self._records_by_ref.get(_ref_key(latest))
                if record is not None and record.value == payload:
                    outcome = ArtifactCommitOutcome('committed', {key: latest})
                    self._source_writes[command_id] = (identity, outcome)
                    return outcome
                outcome = ArtifactCommitOutcome('conflict', reason='source already exists')
                self._source_writes[command_id] = (identity, outcome)
                return outcome
            if expected_ref is not None and latest != expected_ref:
                outcome = ArtifactCommitOutcome('stale', reason='expected ref is not latest')
                self._source_writes[command_id] = (identity, outcome)
                return outcome
            ref = self._next_ref(key)
            self._write_record(_make_record(key=key, ref=ref, value=payload, producer_id=(
                '', command_id), producer_op_id='', input_refs={}, metadata=metadata or {}))
            outcome = ArtifactCommitOutcome('committed', {key: ref})
            self._source_writes[command_id] = (identity, outcome)
            return outcome

    def commit(self, request: ArtifactCommitRequest) -> ArtifactCommitOutcome:
        with self._lock:
            producer_id = (request.run_id, request.attempt_id)
            fingerprint = commit_request_fingerprint(request)
            if previous := self._commits_by_producer.get(producer_id):
                previous_fingerprint, outcome = previous
                return outcome if previous_fingerprint == fingerprint else ArtifactCommitOutcome(
                    'conflict', reason='producer_id conflict')

            failure = self._validate_commit_request(request)
            if failure is not None:
                self._commits_by_producer[producer_id] = (fingerprint, failure)
                return failure

            output_refs = {key: self._next_ref(key) for key in request.output_keys}
            records = [
                _make_record(
                    key=key,
                    ref=output_refs[key],
                    value=_payload(request.output_values[key], role='materialized'),
                    producer_id=producer_id,
                    producer_op_id=request.producer_op_id,
                    input_refs=request.input_refs,
                    metadata={},
                )
                for key in request.output_keys
            ]
            for record in records:
                self._write_record(record)
            outcome = ArtifactCommitOutcome('committed', output_refs)
            self._commits_by_producer[producer_id] = (fingerprint, outcome)
            return outcome

    def _next_ref(self, key: ArtifactKey) -> ArtifactRef:
        latest = self._latest_by_key.get(key)
        return ArtifactRef(key, 1 if latest is None else latest.version + 1)

    def _write_record(self, record: ArtifactRecord) -> None:
        self._records_by_key.setdefault(record.key, []).append(record)
        self._records_by_ref[_ref_key(record.ref)] = record
        self._latest_by_key[record.key] = record.ref

    def _validate_commit_request(self, request: ArtifactCommitRequest) -> ArtifactCommitOutcome | None:
        if len(set(request.output_keys)) != len(request.output_keys):
            return ArtifactCommitOutcome('failed', reason='duplicate output keys')

        if set(request.output_keys) != set(request.output_values):
            return ArtifactCommitOutcome('failed', reason='output keys do not match declared outputs')

        for key, ref in request.input_refs.items():
            if key != ref.key:
                return ArtifactCommitOutcome('failed', reason='input key/ref mismatch')
            if _ref_key(ref) not in self._records_by_ref:
                return ArtifactCommitOutcome('stale', reason=f'input ref not found: {ref}')
            if self._latest_by_key.get(key) != ref:
                return ArtifactCommitOutcome('stale', reason=f'input ref is not latest: {ref}')
        return None


class ArtifactCommitCoordinator:
    def __init__(self, store: ArtifactStore) -> None:
        self.store = store

    def commit_attempt(self, attempt: Attempt, plan_op: PlanOp, result: AttemptExecutionResult) -> CommitResult:
        if not result.ok:
            return CommitResult('failed', reason=result.error_message or result.error_type or 'execution_failed')
        output_keys = plan_op.output_keys
        if set(attempt.output_artifact_keys) != set(output_keys):
            return CommitResult('failed', reason='attempt outputs do not match plan op outputs')
        if set(result.outputs) != set(plan_op.output_key_by_name):
            return CommitResult('failed', reason='attempt outputs do not match plan op outputs')

        outcome = self.store.commit(
            ArtifactCommitRequest(
                run_id=attempt.run_id,
                attempt_id=attempt.attempt_id,
                producer_op_id=plan_op.op_id,
                output_keys=output_keys,
                output_values={key: result.outputs[name] for name, key in plan_op.output_key_by_name.items()},
                input_refs=attempt.resolved_input_refs,
            )
        )
        return CommitResult(outcome.status, dict(outcome.output_refs), outcome.reason)


class ArtifactStoreVersionResolver:
    def __init__(self, store: ArtifactStore) -> None:
        self.store = store

    def latest(self, key: ArtifactKey) -> ArtifactRef:
        ref = self.store.latest(key)
        if ref is None:
            raise KeyError(key)
        return ref


def _ref_key(ref: ArtifactRef) -> tuple[str, str, int]:
    return (ref.key.artifact_id, ref.key.partition, ref.version)


def _freeze_mapping(values: Mapping[Any, Any]) -> Mapping[Any, Any]:
    return MappingProxyType(dict(values))


def _make_record(
    *,
    key: ArtifactKey,
    ref: ArtifactRef,
    value: Any,
    producer_id: tuple[str, str],
    producer_op_id: str,
    input_refs: Mapping[ArtifactKey, ArtifactRef],
    metadata: Mapping[str, Any],
) -> ArtifactRecord:
    return ArtifactRecord(
        key=key,
        ref=ref,
        value=_payload(value),
        producer_id=producer_id,
        producer_op_id=producer_op_id,
        input_refs=_freeze_mapping(input_refs),
        metadata=_freeze_mapping(metadata),
    )


def _record_snapshot(record: ArtifactRecord) -> ArtifactRecord:
    return _make_record(
        key=record.key,
        ref=record.ref,
        value=record.value,
        producer_id=record.producer_id,
        producer_op_id=record.producer_op_id,
        input_refs=record.input_refs,
        metadata=record.metadata,
    )


def _payload(value: Any, *, metadata: Mapping[str, Any] | None = None, role: str = '') -> ArtifactPayload:
    return ArtifactPayload.from_value(value, metadata=metadata, role=role)


def source_write_fingerprint(
    key: ArtifactKey,
    payload: ArtifactPayload,
    expected_ref: ArtifactRef | None,
    create_only: bool,
) -> str:
    return json_mapping_fingerprint(
        {
            'artifact': {'artifact_id': key.artifact_id, 'partition': key.partition},
            'create_only': create_only,
            'expected_ref': None if expected_ref is None else {
                'artifact_id': expected_ref.key.artifact_id,
                'partition': expected_ref.key.partition,
                'version': expected_ref.version,
            },
            'value': _stable_payload_identity(payload),
        },
        reject_reserved_envelope=False,
    )


def commit_request_fingerprint(request: ArtifactCommitRequest) -> str:
    return json_mapping_fingerprint(
        {
            'producer_op_id': request.producer_op_id,
            'output_keys': [_artifact_key_identity(key) for key in sorted(request.output_keys)],
            'input_refs': [
                [_artifact_key_identity(key), _artifact_ref_identity(ref)]
                for key, ref in sorted(request.input_refs.items())
            ],
            'output_values': [
                [_artifact_key_identity(key), _stable_payload_identity(
                    _payload(request.output_values[key], role='materialized'))]
                for key in sorted(request.output_values)
            ],
        },
        reject_reserved_envelope=False,
    )


def _stable_payload_identity(payload: ArtifactPayload) -> dict[str, Any]:
    return {
        'schema': payload.schema,
        'metadata': _stable_value_identity(dict(payload.metadata)),
        'fragments': _stable_value_identity(tuple(payload.fragments)),
        'role': payload.role,
        'payload': _stable_value_identity(payload.payload),
    }


def _artifact_key_identity(key: ArtifactKey) -> dict[str, str]:
    return {'artifact_id': key.artifact_id, 'partition': key.partition}


def _artifact_ref_identity(ref: ArtifactRef) -> dict[str, Any]:
    return {'artifact_id': ref.key.artifact_id, 'partition': ref.key.partition, 'version': ref.version}


def _stable_value_identity(value: Any) -> Any:
    if is_json_scalar(value):
        return {'kind': 'scalar', 'value': value}
    if isinstance(value, Mapping):
        items = [
            [_stable_value_identity(key), _stable_value_identity(item)]
            for key, item in value.items()
        ]
        return {'kind': 'mapping', 'items': sorted(items, key=lambda item: canonical_json(item[0]))}
    if isinstance(value, list):
        return {'kind': 'list', 'items': [_stable_value_identity(item) for item in value]}
    if isinstance(value, tuple):
        return {'kind': 'tuple', 'items': [_stable_value_identity(item) for item in value]}
    if isinstance(value, (set, frozenset)):
        items = [_stable_value_identity(item) for item in value]
        return {'kind': type(value).__name__, 'items': sorted(items, key=canonical_json)}
    digest = hashlib.sha256(pickle.dumps(value, protocol=5)).hexdigest()
    return {'kind': 'pickle:v5', 'sha256': digest}
