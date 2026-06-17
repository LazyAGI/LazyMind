from __future__ import annotations

import time
import uuid
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field, replace
from threading import RLock
from types import MappingProxyType
from typing import Any, Literal, Protocol

from .utils import (
    canonical_json,
    is_json_scalar,
    json_mapping_fingerprint,
    normalize_json_mapping,
    normalize_json_value,
    sorted_string_items,
    validate_nonempty,
)

ExternalCallStatus = Literal[
    'completed',
    'failed_permanent',
    'failed_transient',
    'timeout',
    'rate_limited',
    'ambiguous',
    'conflict',
]
ExternalCallAcquireStatus = Literal['started', 'replay', 'conflict', 'in_progress']
ExternalCallWriteStatus = Literal['recorded', 'stale']

TERMINAL_REPLAY_STATUSES = frozenset({'completed', 'failed_permanent', 'ambiguous'})
RETRYABLE_STATUSES = frozenset({'failed_transient', 'timeout', 'rate_limited'})
ALL_EXTERNAL_STATUSES = TERMINAL_REPLAY_STATUSES | RETRYABLE_STATUSES | {'conflict'}
ALL_ACQUIRE_STATUSES = frozenset({'started', 'replay', 'conflict', 'in_progress'})
ALL_WRITE_STATUSES = frozenset({'recorded', 'stale'})


@dataclass(frozen=True)
class ExternalCallRequest:
    run_id: str
    attempt_id: str
    plan_version: int
    op_id: str
    call_id: str
    payload: Mapping[str, Any]
    idempotency_key: str
    payload_fingerprint: str
    metadata: Mapping[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.attempt_id, 'attempt_id')
        validate_nonempty(self.op_id, 'op_id')
        validate_nonempty(self.call_id, 'call_id')
        validate_nonempty(self.idempotency_key, 'idempotency_key')
        validate_nonempty(self.payload_fingerprint, 'payload_fingerprint')
        if self.plan_version < 1:
            raise ValueError('plan_version must be >= 1')
        object.__setattr__(self, 'payload', MappingProxyType(dict(self.payload)))
        object.__setattr__(self, 'metadata', MappingProxyType(dict(self.metadata)))


@dataclass(frozen=True)
class ExternalCallResult:
    status: ExternalCallStatus
    value: Any = None
    error_type: str = ''
    error_message: str = ''
    metadata: Mapping[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        if self.status not in ALL_EXTERNAL_STATUSES:
            raise ValueError(f'invalid external call status: {self.status}')
        if not isinstance(self.error_type, str):
            raise TypeError('error_type must be a string')
        if not isinstance(self.error_message, str):
            raise TypeError('error_message must be a string')
        object.__setattr__(self, 'metadata', MappingProxyType(dict(self.metadata)))


@dataclass(frozen=True)
class ExternalCallRecord:
    key: str
    payload_fingerprint: str
    status: ExternalCallStatus | Literal['in_progress']
    claim_token: str = ''
    claim_expires_at: float = 0.0
    result: ExternalCallResult | None = None
    attempt_metadata: Mapping[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        validate_nonempty(self.key, 'key')
        validate_nonempty(self.payload_fingerprint, 'payload_fingerprint')
        if self.status != 'in_progress' and self.status not in ALL_EXTERNAL_STATUSES:
            raise ValueError(f'invalid external call record status: {self.status}')
        if self.status != 'in_progress' and self.result is None:
            raise ValueError('terminal external call records require result')
        object.__setattr__(self, 'attempt_metadata', MappingProxyType(dict(self.attempt_metadata)))


@dataclass(frozen=True)
class ExternalCallAcquireResult:
    status: ExternalCallAcquireStatus
    claim_token: str = ''
    record: ExternalCallRecord | None = None

    def __post_init__(self) -> None:
        if self.status not in ALL_ACQUIRE_STATUSES:
            raise ValueError(f'invalid external call acquire status: {self.status}')


@dataclass(frozen=True)
class ExternalCallWriteResult:
    status: ExternalCallWriteStatus
    record: ExternalCallRecord | None = None

    def __post_init__(self) -> None:
        if self.status not in ALL_WRITE_STATUSES:
            raise ValueError(f'invalid external call write status: {self.status}')


@dataclass(frozen=True)
class ExternalCallPolicy:
    claim_lease_seconds: float = 300.0

    def __post_init__(self) -> None:
        if self.claim_lease_seconds <= 0:
            raise ValueError('claim_lease_seconds must be > 0')


class CancellationToken:
    def __init__(self, is_cancel_requested: Callable[[], bool] | None = None) -> None:
        self._is_cancel_requested = is_cancel_requested or (lambda: False)

    def is_cancel_requested(self) -> bool:
        return bool(self._is_cancel_requested())

    def raise_if_cancelled(self) -> None:
        if self.is_cancel_requested():
            raise ExternalCallCancelledError('cancel_requested')


class ExternalCallCancelledError(RuntimeError):
    pass


class ExternalCallRunner(Protocol):
    def invoke(self, request: ExternalCallRequest, token: CancellationToken) -> ExternalCallResult:
        ...


class ControlPlaneValueValidator(Protocol):
    def encode_payload(self, value: Any) -> str:
        ...


class ExternalCallLedger(Protocol):
    def begin(
        self,
        key: str,
        payload_fingerprint: str,
        *,
        now: float,
        claim_expires_at: float,
        attempt_metadata: Mapping[str, Any] | None = None,
    ) -> ExternalCallAcquireResult:
        ...

    def complete(self, key: str, claim_token: str, result: ExternalCallResult) -> ExternalCallWriteResult:
        ...

    def fail(self, key: str, claim_token: str, result: ExternalCallResult) -> ExternalCallWriteResult:
        ...

    def reclaim_expired(self, now: float) -> tuple[ExternalCallRecord, ...]:
        ...


class InMemoryExternalCallLedger:
    def __init__(self) -> None:
        self._records: dict[str, ExternalCallRecord] = {}
        self._lock = RLock()

    def begin(
        self,
        key: str,
        payload_fingerprint: str,
        *,
        now: float,
        claim_expires_at: float,
        attempt_metadata: Mapping[str, Any] | None = None,
    ) -> ExternalCallAcquireResult:
        validate_nonempty(key, 'key')
        validate_nonempty(payload_fingerprint, 'payload_fingerprint')
        with self._lock:
            record = self._records.get(key)
            if record is None:
                return self._claim(key, payload_fingerprint, claim_expires_at, attempt_metadata)
            if record.payload_fingerprint != payload_fingerprint:
                return ExternalCallAcquireResult('conflict', record=record)
            if record.status in TERMINAL_REPLAY_STATUSES:
                return ExternalCallAcquireResult('replay', record=record)
            if record.status in RETRYABLE_STATUSES:
                return self._claim(key, payload_fingerprint, claim_expires_at, attempt_metadata)
            if record.status == 'in_progress' and record.claim_expires_at <= now:
                return self._claim(key, payload_fingerprint, claim_expires_at, attempt_metadata)
            if record.status == 'in_progress':
                return ExternalCallAcquireResult('in_progress', record=record)
            return ExternalCallAcquireResult('replay', record=record)

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
            return tuple(
                record
                for record in self._records.values()
                if record.status == 'in_progress' and record.claim_expires_at <= now
            )

    def _claim(
        self,
        key: str,
        payload_fingerprint: str,
        claim_expires_at: float,
        attempt_metadata: Mapping[str, Any] | None,
    ) -> ExternalCallAcquireResult:
        claim_token = uuid.uuid4().hex
        record = ExternalCallRecord(
            key,
            payload_fingerprint,
            'in_progress',
            claim_token=claim_token,
            claim_expires_at=claim_expires_at,
            attempt_metadata=dict(attempt_metadata or {}),
        )
        self._records[key] = record
        return ExternalCallAcquireResult('started', claim_token, record)

    def _terminal_write(self, key: str, claim_token: str, result: ExternalCallResult) -> ExternalCallWriteResult:
        validate_nonempty(key, 'key')
        validate_nonempty(claim_token, 'claim_token')
        with self._lock:
            current = self._records.get(key)
            if current is None or current.status != 'in_progress' or current.claim_token != claim_token:
                return ExternalCallWriteResult('stale', current)
            record = replace(current, status=result.status, claim_token='', claim_expires_at=0.0, result=result)
            self._records[key] = record
            return ExternalCallWriteResult('recorded', record)


class ExternalCallGateway:
    def __init__(
        self,
        ledger: ExternalCallLedger,
        *,
        policy: ExternalCallPolicy | None = None,
        validator: ControlPlaneValueValidator | None = None,
        clock: Callable[[], float] | None = None,
    ) -> None:
        self.ledger = ledger
        self.policy = policy or ExternalCallPolicy()
        self.validator = validator or _validator_from_ledger(ledger) or _PlainJSONControlValidator()
        self.clock = clock or now_seconds

    def call(
        self,
        *,
        run_id: str,
        attempt_id: str,
        plan_version: int,
        op_id: str,
        call_id: str,
        payload: Mapping[str, Any],
        runner: ExternalCallRunner,
        token: CancellationToken | None = None,
        idempotency_key: str | None = None,
        payload_fingerprint: str | None = None,
        metadata: Mapping[str, Any] | None = None,
    ) -> ExternalCallResult:
        token = token or CancellationToken()
        if token.is_cancel_requested():
            return cancellation_result()

        try:
            fingerprint = payload_fingerprint or canonical_payload_fingerprint(payload)
            self.validator.encode_payload(dict(metadata or {}))
        except (TypeError, ValueError) as error:
            return ExternalCallResult('failed_permanent', error_type=type(error).__name__, error_message=str(error))

        if token.is_cancel_requested():
            return cancellation_result()

        key = idempotency_key or default_idempotency_key(run_id, plan_version, op_id, call_id)
        request = ExternalCallRequest(
            run_id,
            attempt_id,
            plan_version,
            op_id,
            call_id,
            payload,
            key,
            fingerprint,
            metadata or {},
        )
        now = self.clock()
        acquire = self.ledger.begin(
            key,
            fingerprint,
            now=now,
            claim_expires_at=now + self.policy.claim_lease_seconds,
            attempt_metadata={**dict(metadata or {}), 'attempt_id': attempt_id, 'run_id': run_id, 'op_id': op_id},
        )
        if acquire.status == 'replay':
            return _result_or_transient(acquire.record)
        if acquire.status == 'conflict':
            return conflict_result(acquire.record)
        if acquire.status == 'in_progress':
            return in_progress_result()

        claim_token = acquire.claim_token
        try:
            token.raise_if_cancelled()
            result = runner.invoke(request, token)
        except ExternalCallCancelledError:
            result = cancellation_result()
        except Exception as error:  # noqa: BLE001 - gateway boundary classifies unexpected client failures.
            result = ExternalCallResult('failed_transient', error_type=type(error).__name__, error_message=str(error))

        result = self._preflight_result(result)
        write = (
            self.ledger.complete(key, claim_token, result)
            if result.status == 'completed'
            else self.ledger.fail(key, claim_token, result)
        )
        if write.status == 'recorded':
            return _result_or_transient(write.record)
        return _stale_write_result(write.record)

    def _preflight_result(self, result: ExternalCallResult) -> ExternalCallResult:
        if result.status == 'conflict':
            return ExternalCallResult('failed_permanent', error_type='external_runner_conflict_status')
        if not isinstance(result.error_type, str) or not isinstance(result.error_message, str):
            return ExternalCallResult(
                'failed_permanent',
                error_type='external_result_not_serializable',
                error_message='external call result error fields are not control-plane serializable',
            )
        try:
            self.validator.encode_payload(result.value)
            self.validator.encode_payload(dict(result.metadata))
        except (TypeError, ValueError):
            return ExternalCallResult(
                'failed_permanent',
                error_type='external_result_not_serializable',
                error_message='external call result value or metadata is not control-plane serializable',
            )
        return result


def default_idempotency_key(run_id: str, plan_version: int, op_id: str, call_id: str) -> str:
    return f'{run_id}:{plan_version}:{op_id}:{call_id}'


def canonical_payload_fingerprint(payload: Mapping[str, Any]) -> str:
    return json_mapping_fingerprint(payload, allow_tuple=False, reject_reserved_envelope=True)


def cancellation_result() -> ExternalCallResult:
    return ExternalCallResult('failed_transient', error_type='cancel_requested', error_message='cancel_requested')


def conflict_result(record: ExternalCallRecord | None) -> ExternalCallResult:
    return ExternalCallResult(
        'conflict',
        error_type='external_call_conflict',
        error_message='idempotency key was reused with a different payload fingerprint',
        metadata={'existing_status': record.status if record is not None else ''},
    )


def in_progress_result() -> ExternalCallResult:
    return ExternalCallResult('failed_transient', error_type='external_call_in_progress')


def stale_claim_result() -> ExternalCallResult:
    return ExternalCallResult('failed_transient', error_type='external_call_claim_stale')


def now_seconds() -> float:
    return time.time()


def _result_or_transient(record: ExternalCallRecord | None) -> ExternalCallResult:
    if record is None or record.result is None:
        return stale_claim_result()
    return record.result


def _stale_write_result(record: ExternalCallRecord | None) -> ExternalCallResult:
    if record is not None and record.status != 'in_progress' and record.result is not None:
        return record.result
    return stale_claim_result()


class _PlainJSONControlValidator:
    def encode_payload(self, value: Any) -> str:
        if _is_json_scalar(value):
            return _canonical_json(value)
        if type(value) is dict:
            return _canonical_json(_validate_json_object(value))
        raise TypeError(f'unsupported control-plane value: {type(value).__name__}')


def _validate_json_object(payload: Mapping[str, Any]) -> dict[str, Any]:
    if not isinstance(payload, Mapping):
        raise TypeError('payload must be a mapping')
    return normalize_json_mapping(payload, allow_tuple=False, reject_reserved_envelope=True)


def _validate_json_value(value: Any) -> Any:
    return normalize_json_value(value, allow_tuple=False, reject_reserved_envelope=True)


def _sorted_string_items(values: Mapping[Any, Any]) -> list[tuple[str, Any]]:
    return sorted_string_items(values)


def _is_json_scalar(value: Any) -> bool:
    return is_json_scalar(value)


def _canonical_json(value: Any) -> str:
    return canonical_json(value)


def _validator_from_ledger(ledger: ExternalCallLedger) -> ControlPlaneValueValidator | None:
    encode_payload = getattr(ledger, 'encode_payload', None)
    if callable(encode_payload):
        return ledger  # type: ignore[return-value]
    return None
