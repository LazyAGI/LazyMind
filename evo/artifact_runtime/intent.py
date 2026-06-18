from __future__ import annotations

import math
import time
import uuid
from collections.abc import Mapping
from dataclasses import dataclass, field, replace
from threading import RLock
from types import MappingProxyType
from typing import Any, Callable, Literal, Protocol, cast

from .artifact import ArtifactKey, ArtifactRef, ArtifactVersionResolver
from .control_codec import decode_control_value, encode_control_value
from .controller import RunController
from .errors import DAGGraphError, MissingArtifactVersionError, UnknownTargetError
from .graph import DAGGraph
from .intervention import (
    FlowInterventionCoordinator,
    InterventionResult,
    MaterializeInterventionRequest,
    PatchAndReconcileRequest,
)
from .runtime_driver import DurableRuntimeDriver, RuntimeDriverResult
from .utils import json_mapping_fingerprint, normalize_json_value, validate_nonempty

IntentCommandKind = Literal['submit_plan', 'patch_and_reconcile',
                            'materialize', 'retry_failed', 'run_control', 'run_until_idle']
IntentCommandStatus = Literal['applied', 'failed']
IntentCommandAcquireStatus = Literal['reserved', 'replay', 'conflict', 'in_progress']
IntentCommandWriteStatus = Literal['recorded', 'stale']
PlanSubmitStatus = Literal['submitted', 'failed']
RunControlAction = Literal['pause', 'resume', 'cancel']

_INTENT_STATUSES = frozenset({'applied', 'failed'})
_ACQUIRE_STATUSES = frozenset({'reserved', 'replay', 'conflict', 'in_progress'})
_WRITE_STATUSES = frozenset({'recorded', 'stale'})
_PLAN_SUBMIT_STATUSES = frozenset({'submitted', 'failed'})
_RUN_CONTROL_ACTIONS = frozenset({'pause', 'resume', 'cancel'})


@dataclass(frozen=True)
class _IntentSpec:
    intent_type: type
    kind: IntentCommandKind
    payload_builder: Callable[[Any], dict[str, Any]]
    handler_name: str
    uses_prepared: bool = False


@dataclass(frozen=True)
class IntentCommandPolicy:
    claim_lease_seconds: float = 300.0
    owner_id: str = 'evo-runtime-intent'

    def __post_init__(self) -> None:
        if not math.isfinite(self.claim_lease_seconds) or self.claim_lease_seconds <= 0:
            raise ValueError('claim_lease_seconds must be finite and > 0')
        validate_nonempty(self.owner_id, 'owner_id')


@dataclass(frozen=True)
class SubmitPlanIntent:
    targets: tuple[ArtifactKey, ...]
    reason: str = 'submit_plan'

    def __post_init__(self) -> None:
        object.__setattr__(self, 'targets', _stable_targets(self.targets))


@dataclass(frozen=True)
class PatchAndReconcileIntent:
    artifact: ArtifactKey
    value: Any
    expected_ref: ArtifactRef | None
    patch_source: str = 'intent'
    include_downstream: bool = True
    pause_first: bool = False
    resume_after: bool = False
    reason: str = 'patch_and_reconcile'


@dataclass(frozen=True)
class MaterializeIntent:
    artifacts: tuple[ArtifactKey, ...]
    include_downstream: bool = True
    resume_after: bool = False
    reason: str = 'manual_materialize'

    def __post_init__(self) -> None:
        object.__setattr__(self, 'artifacts', tuple(sorted(set(self.artifacts))))


@dataclass(frozen=True)
class RetryFailedIntent:
    reason: str = 'retry_failed'


@dataclass(frozen=True)
class RunControlIntent:
    action: RunControlAction
    reason: str = 'run_control'

    def __post_init__(self) -> None:
        if self.action not in _RUN_CONTROL_ACTIONS:
            raise ValueError(f'invalid run control action: {self.action}')


@dataclass(frozen=True)
class RunUntilIdleIntent:
    reason: str = 'run_until_idle'


TypedIntent = (
    SubmitPlanIntent
    | PatchAndReconcileIntent
    | MaterializeIntent
    | RetryFailedIntent
    | RunControlIntent
    | RunUntilIdleIntent
)


@dataclass(frozen=True)
class IntentCommandRequest:
    command_id: str
    run_id: str
    intent: TypedIntent
    advance_until_idle: bool = False
    metadata: Mapping[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        validate_nonempty(self.command_id, 'command_id')
        validate_nonempty(self.run_id, 'run_id')
        object.__setattr__(self, 'metadata', MappingProxyType(dict(self.metadata)))

    @property
    def kind(self) -> IntentCommandKind:
        return intent_kind(self.intent)


@dataclass(frozen=True)
class PreparedIntentPayload:
    request_fingerprint: str
    payload: Mapping[str, Any]


@dataclass(frozen=True)
class IntentControllerResult:
    action: str
    run_id: str
    status: IntentCommandStatus
    retry_attempt_ids: tuple[str, ...] = ()
    reason: str = ''

    def __post_init__(self) -> None:
        validate_nonempty(self.action, 'action')
        validate_nonempty(self.run_id, 'run_id')
        if self.status not in _INTENT_STATUSES:
            raise ValueError(f'invalid intent controller status: {self.status}')
        object.__setattr__(self, 'retry_attempt_ids', tuple(sorted(set(self.retry_attempt_ids))))


@dataclass(frozen=True)
class IntentAdvanceResult:
    status: str
    ticks: int
    cursor: int
    partial_sync: bool = False
    recovered_run_ids: tuple[str, ...] = ()
    dispatched_run_ids: tuple[str, ...] = ()

    def __post_init__(self) -> None:
        validate_nonempty(self.status, 'status')
        if self.ticks < 0:
            raise ValueError('ticks must be >= 0')
        if self.cursor < 0:
            raise ValueError('cursor must be >= 0')
        object.__setattr__(self, 'recovered_run_ids', tuple(sorted(set(self.recovered_run_ids))))
        object.__setattr__(self, 'dispatched_run_ids', tuple(sorted(set(self.dispatched_run_ids))))


@dataclass(frozen=True)
class IntentPlanResult:
    run_id: str
    plan_id: str
    plan_version: int
    target_artifacts: tuple[ArtifactKey, ...]
    target_artifact_count: int = 0
    reason: str = ''

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.plan_id, 'plan_id')
        if self.plan_version < 1:
            raise ValueError('plan_version must be >= 1')
        target_artifacts = _stable_targets(self.target_artifacts)
        object.__setattr__(self, 'target_artifacts', target_artifacts)
        object.__setattr__(self, 'target_artifact_count', len(target_artifacts))


@dataclass(frozen=True)
class PlanSubmitResult:
    status: PlanSubmitStatus
    plan_result: IntentPlanResult | None = None
    reason: str = ''

    def __post_init__(self) -> None:
        if self.status not in _PLAN_SUBMIT_STATUSES:
            raise ValueError(f'invalid plan submit status: {self.status}')
        if self.status == 'submitted' and self.plan_result is None:
            raise ValueError('submitted plan result requires plan_result')
        if self.status == 'failed' and self.plan_result is not None:
            raise ValueError('failed plan result must not include plan_result')


class PlanSubmitter(Protocol):
    def submit_plan_intent(
        self,
        run_id: str,
        *,
        command_id: str,
        targets: tuple[ArtifactKey, ...],
        reason: str,
    ) -> PlanSubmitResult:
        ...


class GraphPlanSubmitter:
    def __init__(self, graph: DAGGraph, resolver: ArtifactVersionResolver, controller: RunController) -> None:
        self.graph = graph
        self.resolver = resolver
        self.controller = controller

    def submit_plan_intent(
        self,
        run_id: str,
        *,
        command_id: str,
        targets: tuple[ArtifactKey, ...],
        reason: str,
    ) -> PlanSubmitResult:
        try:
            plan = self.graph.build_plan_for_keys(self.resolver, set(targets))
            instance = self.controller.submit_plan(run_id, plan, targets=set(
                targets), reason=reason, command_id=command_id)
        except UnknownTargetError:
            return PlanSubmitResult('failed', reason='unknown_target')
        except MissingArtifactVersionError:
            return PlanSubmitResult('failed', reason='missing_input_version')
        except DAGGraphError:
            return PlanSubmitResult('failed', reason='plan_build_failed')
        except ValueError:
            return PlanSubmitResult('failed', reason='submit_failed')
        return PlanSubmitResult(
            'submitted',
            IntentPlanResult(
                instance.run_id,
                instance.plan_id,
                instance.plan_version,
                instance.target_artifacts,
                reason=instance.reason,
            ),
        )


@dataclass(frozen=True)
class IntentCommandResult:
    status: IntentCommandStatus
    kind: IntentCommandKind
    replayed: bool = False
    intervention_result: InterventionResult | None = None
    controller_result: IntentControllerResult | None = None
    advance_result: IntentAdvanceResult | None = None
    reason: str = ''
    plan_result: IntentPlanResult | None = None

    def __post_init__(self) -> None:
        if self.status not in _INTENT_STATUSES:
            raise ValueError(f'invalid intent command status: {self.status}')
        if self.plan_result is not None and self.kind != 'submit_plan':
            raise ValueError('plan_result is only valid for submit_plan intent results')


@dataclass(frozen=True)
class IntentCommandRecord:
    command_id: str
    request_fingerprint: str
    kind: IntentCommandKind
    result: IntentCommandResult | None = None
    claim_token: str = ''
    claim_expires_at: float = 0.0
    owner_id: str = ''
    reserved_at: float = 0.0

    def __post_init__(self) -> None:
        validate_nonempty(self.command_id, 'command_id')
        validate_nonempty(self.request_fingerprint, 'request_fingerprint')
        if self.claim_token:
            validate_nonempty(self.owner_id, 'owner_id')
            if not math.isfinite(self.reserved_at):
                raise ValueError('reserved_at must be finite')
            if not math.isfinite(self.claim_expires_at):
                raise ValueError('claim_expires_at must be finite')
            if self.claim_expires_at <= self.reserved_at:
                raise ValueError('claim_expires_at must be greater than reserved_at')


@dataclass(frozen=True)
class IntentCommandAcquireResult:
    status: IntentCommandAcquireStatus
    record: IntentCommandRecord

    def __post_init__(self) -> None:
        if self.status not in _ACQUIRE_STATUSES:
            raise ValueError(f'invalid intent command acquire status: {self.status}')
        if self.status == 'reserved' and not self.record.claim_token:
            raise ValueError('reserved intent acquire result requires claim_token')


@dataclass(frozen=True)
class IntentCommandWriteResult:
    status: IntentCommandWriteStatus
    record: IntentCommandRecord | None = None

    def __post_init__(self) -> None:
        if self.status not in _WRITE_STATUSES:
            raise ValueError(f'invalid intent command write status: {self.status}')


@dataclass(frozen=True)
class _PreparedIntentCommand:
    fingerprint: str
    payload: Mapping[str, Any]
    intent_payload: Mapping[str, Any]
    metadata: Mapping[str, Any]


class IntentCommandLog(Protocol):
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
        ...

    def complete(
        self,
        command_id: str,
        request_fingerprint: str,
        kind: IntentCommandKind,
        *,
        claim_token: str,
        result: IntentCommandResult,
    ) -> IntentCommandWriteResult:
        ...


class InMemoryIntentCommandLog:
    def __init__(self) -> None:
        self._lock = RLock()
        self._records: dict[str, IntentCommandRecord] = {}

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
        _validate_record_key(command_id, request_fingerprint)
        _validate_claim_inputs(now, claim_expires_at, owner_id)
        with self._lock:
            record = self._records.get(command_id)
            if record is None:
                record = _claimed_intent_record(command_id, request_fingerprint, kind, now, claim_expires_at, owner_id)
                self._records[command_id] = record
                return IntentCommandAcquireResult('reserved', record)
            if record.request_fingerprint != request_fingerprint or record.kind != kind:
                return IntentCommandAcquireResult('conflict', record)
            if record.result is None:
                if record.claim_expires_at <= now:
                    reclaimed = _claimed_intent_record(command_id, request_fingerprint,
                                                       kind, now, claim_expires_at, owner_id)
                    self._records[command_id] = reclaimed
                    return IntentCommandAcquireResult('reserved', reclaimed)
                return IntentCommandAcquireResult('in_progress', record)
            return IntentCommandAcquireResult('replay', record)

    def complete(
        self,
        command_id: str,
        request_fingerprint: str,
        kind: IntentCommandKind,
        *,
        claim_token: str,
        result: IntentCommandResult,
    ) -> IntentCommandWriteResult:
        _validate_record_key(command_id, request_fingerprint)
        validate_nonempty(claim_token, 'claim_token')
        with self._lock:
            record = self._records.get(command_id)
            if record is None:
                return IntentCommandWriteResult('stale')
            if record.request_fingerprint != request_fingerprint or record.kind != kind:
                return IntentCommandWriteResult('stale', record)
            if record.result is not None:
                return IntentCommandWriteResult('stale', record)
            if record.claim_token != claim_token:
                return IntentCommandWriteResult('stale', record)
            completed = replace(record, result=replace(result, replayed=False))
            self._records[command_id] = completed
            return IntentCommandWriteResult('recorded', completed)


class IntentCommandGateway:
    def __init__(
        self,
        *,
        controller: RunController,
        intervention: FlowInterventionCoordinator | None = None,
        driver: DurableRuntimeDriver | None = None,
        log: IntentCommandLog | None = None,
        policy: IntentCommandPolicy | None = None,
        plan_submitter: PlanSubmitter | None = None,
        clock: Callable[[], float] = time.time,
    ) -> None:
        self.controller = controller
        self.intervention = intervention
        self.driver = driver
        self.log = log or InMemoryIntentCommandLog()
        self.policy = policy or IntentCommandPolicy()
        self.plan_submitter = plan_submitter
        self.clock = clock

    def execute(self, request: IntentCommandRequest) -> IntentCommandResult:
        try:
            prepared = _prepare_request(request)
        except (TypeError, ValueError):
            return IntentCommandResult('failed', _safe_kind(request), reason='invalid_payload')

        now = self.clock()
        claim_expires_at = now + self.policy.claim_lease_seconds
        acquire = self.log.reserve(
            request.command_id,
            prepared.fingerprint,
            request.kind,
            now=now,
            claim_expires_at=claim_expires_at,
            owner_id=self.policy.owner_id,
        )
        if acquire.status == 'conflict':
            return self._failed(request, 'command_conflict')
        if acquire.status == 'in_progress':
            return self._failed(request, 'command_in_progress')
        if acquire.status == 'replay':
            assert acquire.record.result is not None
            return replace(acquire.record.result, replayed=True)

        claim_token = acquire.record.claim_token
        if not claim_token:
            return self._failed(request, 'stale_intent_claim')
        result = self._execute_reserved(request, prepared)
        completed = self.log.complete(
            request.command_id,
            prepared.fingerprint,
            request.kind,
            claim_token=claim_token,
            result=result,
        )
        if completed.status == 'recorded':
            assert completed.record is not None and completed.record.result is not None
            return completed.record.result
        if completed.record is not None and completed.record.result is not None:
            return replace(completed.record.result, replayed=True)
        return self._failed(request, 'stale_intent_claim')

    def _failed(self, request: IntentCommandRequest, reason: str, **fields: Any) -> IntentCommandResult:
        return IntentCommandResult('failed', request.kind, reason=reason, **fields)

    def _command_failed(self, request: IntentCommandRequest, error: ValueError) -> IntentCommandResult:
        return self._failed(request, str(error) or 'command_failed')

    def _execute_reserved(self, request: IntentCommandRequest, prepared: _PreparedIntentCommand) -> IntentCommandResult:
        spec = _intent_spec(request.intent)
        handler = getattr(self, spec.handler_name)
        if spec.uses_prepared:
            return handler(request, prepared)
        return handler(request)

    def _submit_plan(
        self,
        request: IntentCommandRequest,
    ) -> IntentCommandResult:
        if self.plan_submitter is None:
            return self._failed(request, 'plan_submitter_not_configured')
        intent = cast(SubmitPlanIntent, request.intent)
        result = self.plan_submitter.submit_plan_intent(
            request.run_id,
            command_id=f'{request.command_id}:submit_plan',
            targets=intent.targets,
            reason=intent.reason,
        )
        if result.status == 'failed':
            return self._failed(request, result.reason or 'submit_failed')
        assert result.plan_result is not None
        return self._maybe_advance(
            request,
            IntentCommandResult(
                'applied',
                request.kind,
                plan_result=result.plan_result))

    def _patch_and_reconcile(
        self,
        request: IntentCommandRequest,
        prepared: _PreparedIntentCommand,
    ) -> IntentCommandResult:
        if self.intervention is None:
            return self._failed(request, 'intervention_not_configured')
        intent = cast(PatchAndReconcileIntent, request.intent)
        try:
            result = self.intervention.patch_and_reconcile(
                PatchAndReconcileRequest(
                    run_id=request.run_id,
                    command_id=f'{request.command_id}:patch_and_reconcile',
                    artifact=intent.artifact,
                    value=intent.value,
                    expected_ref=intent.expected_ref,
                    patch_source=intent.patch_source,
                    include_downstream=intent.include_downstream,
                    pause_first=intent.pause_first,
                    resume_after=intent.resume_after,
                    reason=intent.reason,
                    metadata=MappingProxyType(dict(prepared.metadata)),
                )
            )
        except ValueError as error:
            return self._command_failed(request, error)
        status = 'applied' if result.status == 'applied' else 'failed'
        out = IntentCommandResult(status, request.kind, intervention_result=result, reason=result.reason)
        return self._maybe_advance(request, out)

    def _materialize(
        self,
        request: IntentCommandRequest,
    ) -> IntentCommandResult:
        if self.intervention is None:
            return self._failed(request, 'intervention_not_configured')
        intent = cast(MaterializeIntent, request.intent)
        try:
            result = self.intervention.materialize(
                MaterializeInterventionRequest(
                    request.run_id,
                    f'{request.command_id}:materialize',
                    intent.artifacts,
                    intent.include_downstream,
                    intent.resume_after,
                    intent.reason,
                )
            )
        except ValueError as error:
            return self._command_failed(request, error)
        status = 'applied' if result.status == 'applied' else 'failed'
        out = IntentCommandResult(status, request.kind, intervention_result=result, reason=result.reason)
        return self._maybe_advance(request, out)

    def _retry_failed(
        self,
        request: IntentCommandRequest,
    ) -> IntentCommandResult:
        try:
            attempts = self.controller.retry_failed(request.run_id, command_id=f'{request.command_id}:retry_failed')
        except ValueError as error:
            return self._command_failed(request, error)
        result = IntentControllerResult('retry_failed', request.run_id, 'applied',
                                        tuple(attempt.attempt_id for attempt in attempts))
        return self._maybe_advance(request, IntentCommandResult('applied', request.kind, controller_result=result))

    def _run_control(
        self,
        request: IntentCommandRequest,
    ) -> IntentCommandResult:
        intent = cast(RunControlIntent, request.intent)
        action = intent.action
        try:
            if action == 'pause':
                run = self.controller.pause(request.run_id, command_id=f'{request.command_id}:pause')
            elif action == 'resume':
                run = self.controller.resume(request.run_id, command_id=f'{request.command_id}:resume')
            else:
                run = self.controller.cancel(request.run_id, command_id=f'{request.command_id}:cancel')
        except ValueError as error:
            return self._command_failed(request, error)
        result = IntentControllerResult(action, run.run_id, 'applied', reason=run.status)
        return self._maybe_advance(
            request,
            IntentCommandResult(
                'applied',
                request.kind,
                controller_result=result,
                reason=run.status))

    def _run_until_idle(
        self,
        request: IntentCommandRequest,
    ) -> IntentCommandResult:
        if self.driver is None:
            return self._failed(request, 'driver_not_configured')
        try:
            driver_result = self.driver.run_until_idle(run_ids=(request.run_id,))
        except ValueError as error:
            return self._command_failed(request, error)
        advance = intent_advance_result(driver_result)
        if driver_result.status == 'idle':
            return IntentCommandResult('applied', request.kind, advance_result=advance)
        return self._failed(request, driver_result.status, advance_result=advance)

    def _maybe_advance(self, request: IntentCommandRequest, result: IntentCommandResult) -> IntentCommandResult:
        if not request.advance_until_idle or result.status != 'applied':
            return result
        if self.driver is None:
            return replace(result, status='failed', reason='driver_not_configured')
        try:
            driver_result = self.driver.run_until_idle(run_ids=(request.run_id,))
        except ValueError as error:
            return replace(result, status='failed', reason=str(error) or 'command_failed')
        advance = intent_advance_result(driver_result)
        if driver_result.status != 'idle':
            return replace(result, status='failed', advance_result=advance, reason=driver_result.status)
        return replace(result, advance_result=advance)


def intent_kind(intent: TypedIntent) -> IntentCommandKind:
    return _intent_spec(intent).kind


def intent_request_fingerprint(request: IntentCommandRequest) -> str:
    return _prepare_request(request).fingerprint


def prepare_intent_payload(request: IntentCommandRequest) -> PreparedIntentPayload:
    """Return the runtime-owned canonical payload used for request fingerprinting."""
    prepared = _prepare_request(request)
    return PreparedIntentPayload(prepared.fingerprint, prepared.payload)


def prepared_intent_payload_fingerprint(payload: Mapping[str, Any]) -> str:
    return json_mapping_fingerprint(dict(payload), allow_tuple=True, reject_reserved_envelope=False)


def intent_request_from_payload(
    command_id: str,
    payload: Mapping[str, Any],
    *,
    expected_fingerprint: str | None = None,
) -> IntentCommandRequest:
    fingerprint = prepared_intent_payload_fingerprint(payload)
    if expected_fingerprint is not None and fingerprint != expected_fingerprint:
        raise ValueError('request_fingerprint mismatch')
    intent_payload = payload.get('intent')
    if not isinstance(intent_payload, Mapping):
        raise ValueError('prepared intent payload must include intent object')
    request = IntentCommandRequest(
        command_id,
        str(payload.get('run_id') or ''),
        _intent_from_payload(str(payload.get('kind') or ''), intent_payload),
        advance_until_idle=bool(payload.get('advance_until_idle')),
        metadata=_metadata_from_payload(payload.get('metadata')),
    )
    if expected_fingerprint is not None and intent_request_fingerprint(request) != expected_fingerprint:
        raise ValueError('request_fingerprint mismatch')
    return request


def _prepare_request(request: IntentCommandRequest) -> _PreparedIntentCommand:
    _intent_spec(request.intent)
    if isinstance(request.intent, RunUntilIdleIntent) and request.advance_until_idle:
        raise ValueError('RunUntilIdleIntent cannot also advance_until_idle')
    intent_payload = _intent_payload(request.intent)
    metadata = _json_value(dict(request.metadata))
    payload = {
        'kind': request.kind,
        'run_id': request.run_id,
        'intent': intent_payload,
        'advance_until_idle': request.advance_until_idle,
        'metadata': metadata,
    }
    return _PreparedIntentCommand(
        json_mapping_fingerprint(payload, allow_tuple=True, reject_reserved_envelope=False),
        payload,
        intent_payload,
        metadata,
    )


def intent_advance_result(result: RuntimeDriverResult) -> IntentAdvanceResult:
    return IntentAdvanceResult(
        result.status,
        result.ticks,
        result.cursor,
        result.partial_sync,
        result.recovered_run_ids,
        result.dispatched_run_ids,
    )


def _safe_kind(request: IntentCommandRequest) -> IntentCommandKind:
    try:
        return request.kind
    except (TypeError, ValueError):
        return 'submit_plan'


def _intent_payload(intent: TypedIntent) -> dict[str, Any]:
    return _intent_spec(intent).payload_builder(intent)


def _intent_from_payload(kind: str, payload: Mapping[str, Any]) -> TypedIntent:
    if kind == 'submit_plan':
        targets = tuple(_artifact_key_from_payload(item) for item in _list_payload(payload.get('targets')))
        return SubmitPlanIntent(targets, reason=str(payload.get('reason') or 'submit_plan'))
    if kind == 'patch_and_reconcile':
        return PatchAndReconcileIntent(
            _artifact_key_from_payload(payload.get('artifact')),
            decode_control_value(payload.get('value')),
            _artifact_ref_from_payload(payload.get('expected_ref')),
            patch_source=str(payload.get('patch_source') or 'intent'),
            include_downstream=bool(payload.get('include_downstream')),
            pause_first=bool(payload.get('pause_first')),
            resume_after=bool(payload.get('resume_after')),
            reason=str(payload.get('reason') or 'patch_and_reconcile'),
        )
    if kind == 'materialize':
        artifacts = tuple(_artifact_key_from_payload(item) for item in _list_payload(payload.get('artifacts')))
        return MaterializeIntent(
            artifacts,
            include_downstream=bool(payload.get('include_downstream')),
            resume_after=bool(payload.get('resume_after')),
            reason=str(payload.get('reason') or 'manual_materialize'),
        )
    if kind == 'retry_failed':
        return RetryFailedIntent(reason=str(payload.get('reason') or 'retry_failed'))
    if kind == 'run_control':
        return RunControlIntent(
            cast(
                RunControlAction, str(
                    payload.get('action') or '')), reason=str(
                payload.get('reason') or 'run_control'))
    if kind == 'run_until_idle':
        return RunUntilIdleIntent(reason=str(payload.get('reason') or 'run_until_idle'))
    raise ValueError(f'unsupported prepared intent kind: {kind}')


def _submit_plan_payload(intent: SubmitPlanIntent) -> dict[str, Any]:
    if not intent.targets:
        raise ValueError('submit plan targets must not be empty')
    return {
        'targets': [_artifact_key(target) for target in intent.targets],
        'reason': intent.reason,
    }


def _patch_and_reconcile_payload(intent: PatchAndReconcileIntent) -> dict[str, Any]:
    return {
        'artifact': _artifact_key(intent.artifact),
        'value': _json_value(intent.value),
        'expected_ref': _artifact_ref(intent.expected_ref) if intent.expected_ref is not None else None,
        'patch_source': intent.patch_source,
        'include_downstream': intent.include_downstream,
        'pause_first': intent.pause_first,
        'resume_after': intent.resume_after,
        'reason': intent.reason,
    }


def _materialize_payload(intent: MaterializeIntent) -> dict[str, Any]:
    return {
        'artifacts': [_artifact_key(artifact) for artifact in intent.artifacts],
        'include_downstream': intent.include_downstream,
        'resume_after': intent.resume_after,
        'reason': intent.reason,
    }


def _retry_failed_payload(intent: RetryFailedIntent) -> dict[str, Any]:
    return {'reason': intent.reason}


def _run_control_payload(intent: RunControlIntent) -> dict[str, Any]:
    return {'action': intent.action, 'reason': intent.reason}


def _run_until_idle_payload(intent: RunUntilIdleIntent) -> dict[str, Any]:
    return {'reason': intent.reason}


_INTENT_SPECS: tuple[_IntentSpec, ...] = (
    _IntentSpec(SubmitPlanIntent, 'submit_plan', _submit_plan_payload, '_submit_plan'),
    _IntentSpec(PatchAndReconcileIntent, 'patch_and_reconcile',
                _patch_and_reconcile_payload, '_patch_and_reconcile', uses_prepared=True),
    _IntentSpec(MaterializeIntent, 'materialize', _materialize_payload, '_materialize'),
    _IntentSpec(RetryFailedIntent, 'retry_failed', _retry_failed_payload, '_retry_failed'),
    _IntentSpec(RunControlIntent, 'run_control', _run_control_payload, '_run_control'),
    _IntentSpec(RunUntilIdleIntent, 'run_until_idle', _run_until_idle_payload, '_run_until_idle'),
)


def _intent_spec(intent: Any) -> _IntentSpec:
    for spec in _INTENT_SPECS:
        if isinstance(intent, spec.intent_type):
            return spec
    raise TypeError('invalid intent payload')


def _stable_targets(targets: tuple[ArtifactKey, ...]) -> tuple[ArtifactKey, ...]:
    return tuple(
        sorted(
            set(targets), key=lambda target: (
                getattr(
                    target, 'artifact_id', ''), getattr(
                    target, 'partition', ''))))


def _artifact_key(key: ArtifactKey) -> dict[str, str]:
    _validate_artifact_key(key)
    return {'artifact_id': key.artifact_id, 'partition': key.partition}


def _artifact_ref(ref: ArtifactRef) -> dict[str, Any]:
    if not isinstance(ref, ArtifactRef):
        raise TypeError('expected_ref must be an ArtifactRef')
    return {'key': _artifact_key(ref.key), 'version': ref.version}


def _artifact_key_from_payload(value: Any) -> ArtifactKey:
    if not isinstance(value, Mapping):
        raise ValueError('artifact key payload must be an object')
    return ArtifactKey(str(value.get('artifact_id') or ''), str(value.get('partition') or ''))


def _artifact_ref_from_payload(value: Any) -> ArtifactRef | None:
    if value is None:
        return None
    if not isinstance(value, Mapping):
        raise ValueError('artifact ref payload must be an object')
    return ArtifactRef(_artifact_key_from_payload(value.get('key')), int(value.get('version') or 0))


def _list_payload(value: Any) -> list[Any]:
    if not isinstance(value, list):
        raise ValueError('prepared payload field must be a list')
    return value


def _metadata_from_payload(value: Any) -> Mapping[str, Any]:
    decoded = decode_control_value(value if value is not None else {})
    if not isinstance(decoded, Mapping):
        raise ValueError('prepared metadata payload must be an object')
    return decoded


def _validate_artifact_key(key: ArtifactKey) -> None:
    if not isinstance(key, ArtifactKey):
        raise TypeError('artifact must be an ArtifactKey')


def _validate_record_key(command_id: str, request_fingerprint: str) -> None:
    validate_nonempty(command_id, 'command_id')
    validate_nonempty(request_fingerprint, 'request_fingerprint')


def _validate_claim_inputs(now: float, claim_expires_at: float, owner_id: str) -> None:
    if not math.isfinite(now):
        raise ValueError('now must be finite')
    if not math.isfinite(claim_expires_at):
        raise ValueError('claim_expires_at must be finite')
    if claim_expires_at <= now:
        raise ValueError('claim_expires_at must be greater than now')
    validate_nonempty(owner_id, 'owner_id')


def _claimed_intent_record(
    command_id: str,
    request_fingerprint: str,
    kind: IntentCommandKind,
    now: float,
    claim_expires_at: float,
    owner_id: str,
) -> IntentCommandRecord:
    return IntentCommandRecord(
        command_id,
        request_fingerprint,
        kind,
        claim_token=uuid.uuid4().hex,
        claim_expires_at=claim_expires_at,
        owner_id=owner_id,
        reserved_at=now,
    )


def _json_value(value: Any) -> Any:
    return normalize_json_value(encode_control_value(value), allow_tuple=True, reject_reserved_envelope=False)
