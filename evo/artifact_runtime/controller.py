from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass, field, replace
from itertools import chain
from threading import RLock
from typing import Any, Literal, Protocol

from .artifact import ArtifactKey, ArtifactRef
from .plan import ExecutionPlan, PlanOp
from .utils import validate_nonempty

RunStatus = Literal['pending', 'running', 'paused', 'completed', 'failed', 'cancel_requested', 'cancelled']
AttemptStatus = Literal['pending', 'claimed', 'completed', 'failed', 'stale', 'cancel_requested', 'cancelled']
CommitStatus = Literal['committed', 'stale', 'conflict', 'failed']
ClaimHeartbeatStatus = Literal['extended', 'stale', 'terminal']
ClaimInspectionStatus = Literal['current', 'stale', 'terminal']
RUN_CLOSED = frozenset({'completed', 'failed', 'cancelled'})
RUN_PLAN_BLOCKED = {'cancel_requested', 'cancelled'}
ATTEMPT_TERMINAL = frozenset({'completed', 'failed', 'stale', 'cancelled'})
ATTEMPT_OPEN = frozenset({'pending', 'claimed', 'cancel_requested'})
RUN_EVENTS: dict[str, RunStatus] = {f'run.{k}': v for k, v in {
    'started': 'running', 'paused': 'paused', 'resumed': 'running', 'cancel_requested': 'cancel_requested',
    'cancelled': 'cancelled', 'completed': 'completed', 'failed': 'failed',
}.items()}
ATTEMPT_EVENTS: dict[str, AttemptStatus] = {f'attempt.{k}': k for k in (
    'completed', 'failed', 'stale', 'cancelled', 'cancel_requested')}
RUN_COMMAND_KINDS = {'run.paused': 'pause', 'run.resumed': 'resume', 'run.cancel_requested': 'cancel'}
_MISSING = object()


@dataclass(frozen=True)
class RunState:
    run_id: str
    status: RunStatus = 'pending'
    active_plan_version: int | None = None
    epoch: int = 0

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')


@dataclass(frozen=True)
class PlanInstance:
    run_id: str
    plan_id: str
    plan_version: int
    epoch: int
    graph_revision: int
    target_artifacts: tuple[ArtifactKey, ...]
    reason: str
    plan: ExecutionPlan


@dataclass(frozen=True)
class Attempt:
    attempt_id: str
    run_id: str
    plan_version: int
    epoch: int
    op_id: str
    resolved_input_refs: dict[ArtifactKey, ArtifactRef]
    output_artifact_keys: tuple[ArtifactKey, ...]
    depends_on: tuple[str, ...]
    status: AttemptStatus = 'pending'
    attempt_number: int = 1
    claim_id: str = ''
    output_refs: dict[ArtifactKey, ArtifactRef] = field(default_factory=dict)
    reason: str = ''
    worker_id: str = ''
    lease_expires_at: float = 0.0
    claim_generation: int = 0
    lease_recovery_count: int = 0

    def __post_init__(self) -> None:
        object.__setattr__(self, 'output_refs', _normalize_ref_key_map(self.output_refs))


@dataclass(frozen=True)
class AttemptClaim:
    claim_id: str
    attempt_id: str
    run_id: str
    plan_version: int
    epoch: int
    op_id: str
    plan_op: PlanOp
    resolved_input_refs: dict[ArtifactKey, ArtifactRef]
    output_artifact_keys: tuple[ArtifactKey, ...]
    cancel_requested: bool = False
    worker_id: str = ''
    lease_expires_at: float = 0.0
    claim_generation: int = 0


@dataclass(frozen=True)
class AttemptExecutionResult:
    ok: bool
    outputs: dict[str, Any] = field(default_factory=dict)
    error_type: str = ''
    error_message: str = ''


@dataclass(frozen=True)
class CommitResult:
    status: CommitStatus
    output_refs: dict[ArtifactKey, ArtifactRef] = field(default_factory=dict)
    reason: str = ''

    def __post_init__(self) -> None:
        object.__setattr__(self, 'output_refs', _normalize_ref_key_map(self.output_refs))


@dataclass(frozen=True)
class AttemptResult:
    attempt_id: str
    status: AttemptStatus
    commit_status: CommitStatus | None = None
    reason: str = ''


@dataclass(frozen=True)
class ClaimHeartbeatResult:
    status: ClaimHeartbeatStatus
    attempt_id: str
    claim_id: str


@dataclass(frozen=True)
class ClaimInspectionResult:
    status: ClaimInspectionStatus
    attempt_id: str
    claim_id: str
    cancel_requested: bool = False
    attempt_status: AttemptStatus | None = None
    reason: str = ''


@dataclass(frozen=True)
class LeaseRecoveryResult:
    run_id: str
    recovered_attempt_ids: tuple[str, ...] = ()
    cancelled_attempt_ids: tuple[str, ...] = ()
    failed_attempt_ids: tuple[str, ...] = ()
    skipped_attempt_ids: tuple[str, ...] = ()


@dataclass(frozen=True)
class RetryPolicy:
    max_attempts: int = 1

    def __post_init__(self) -> None:
        if self.max_attempts < 1:
            raise ValueError('max_attempts must be >= 1')


@dataclass(frozen=True)
class ControllerEvent:
    event_type: str
    run_id: str
    payload: dict[str, Any] = field(default_factory=dict)
    seq: int = 0

    def __post_init__(self) -> None:
        validate_nonempty(self.event_type, 'event_type')
        validate_nonempty(self.run_id, 'run_id')


class EventLog(Protocol):
    def append(self, event: ControllerEvent) -> int:
        ...

    def scan(self, run_id: str) -> Iterable[ControllerEvent]:
        ...

    def scan_since(self, seq: int = 0, *, limit: int = 1000) -> tuple[ControllerEvent, ...]:
        ...

    def max_seq(self) -> int:
        ...


class InMemoryEventLog:
    def __init__(self) -> None:
        self._events: list[ControllerEvent] = []

    def append(self, event: ControllerEvent) -> int:
        self._events.append(replace(event, seq=len(self._events) + 1))
        return self._events[-1].seq

    def scan(self, run_id: str) -> list[ControllerEvent]:
        return [event for event in self._events if event.run_id == run_id]

    def scan_since(self, seq: int = 0, *, limit: int = 1000) -> tuple[ControllerEvent, ...]:
        _validate_scan_window(seq, limit)
        return tuple(event for event in self._events if event.seq > seq)[:limit]

    def max_seq(self) -> int:
        return self._events[-1].seq if self._events else 0


class AttemptExecutor(Protocol):
    def execute(self, claim: AttemptClaim, plan_op: PlanOp) -> AttemptExecutionResult:
        ...


class CommitCoordinator(Protocol):
    def commit_attempt(self, attempt: Attempt, plan_op: PlanOp, result: AttemptExecutionResult) -> CommitResult:
        ...


@dataclass(frozen=True)
class ControllerState:
    run: RunState
    run_exists: bool
    plans: dict[int, PlanInstance]
    attempts: dict[str, Attempt]
    command_results: dict[str, Any]
    latest_attempt_by_op: dict[tuple[int, str], Attempt] = field(default_factory=dict)
    producer_by_artifact: dict[tuple[int, ArtifactKey], Attempt] = field(default_factory=dict)
    attempts_by_status: dict[AttemptStatus, tuple[Attempt, ...]] = field(default_factory=dict)

    @property
    def active_plan(self) -> PlanInstance | None:
        return None if self.run.active_plan_version is None else self.plans.get(self.run.active_plan_version)


class RunController:
    # FC-1 boundary: this is a single-process run/plan/attempt state machine. Execution belongs to FC-5 worker.
    # ArtifactKey is the runtime artifact identity; partition-aware planning is supplied by DAGGraph.
    def __init__(self, *, event_log: EventLog | None = None, committer: CommitCoordinator | None = None,
                 retry_policy: RetryPolicy | None = None) -> None:
        self._lock = RLock()
        self.event_log = event_log or InMemoryEventLog()
        self.committer = committer
        self.retry_policy = retry_policy or RetryPolicy()

    def create_run(self, run_id: str) -> RunState:
        with self._lock:
            state = self.state(run_id)
            if not state.run_exists:
                self._append(run_id, 'run.created')
                state = self.state(run_id)
            return state.run

    def submit_plan(self, run_id: str, plan: ExecutionPlan, *, targets: set[ArtifactKey],
                    reason: str, command_id: str) -> PlanInstance:
        with self._lock:
            validate_nonempty(run_id, 'run_id')
            seen, state = self._seen_command(run_id, command_id, kind='submit_plan', require_run=False)
            if seen is not _MISSING:
                return state.plans[int(seen['plan_version'])]
            if not state.run_exists:
                self._append(run_id, 'run.created')
                state = self.state(run_id)
            if state.run.status in RUN_PLAN_BLOCKED:
                raise ValueError(f'cannot submit plan to {state.run.status} run')
            if state.run.active_plan_version is not None:
                self._append(run_id, 'plan.superseded', {'plan_version': state.run.active_plan_version})
                for attempt in state.attempts.values():
                    if attempt.plan_version == state.run.active_plan_version and attempt.status in ATTEMPT_OPEN:
                        self._append(run_id, 'attempt.stale', {
                                     'attempt_id': attempt.attempt_id, 'reason': 'plan_superseded'})
            version = max(state.plans, default=0) + 1
            instance = PlanInstance(run_id, plan.plan_id, version, state.run.epoch + 1, plan.graph_revision,
                                    tuple(sorted(_artifact_keys(targets))), reason, plan)
            self._append(run_id, 'plan.submitted', {'plan': instance, 'command_id': command_id})
            for plan_op in _plan_ops(plan):
                self._append(run_id, 'attempt.created', {'attempt': self._new_attempt(run_id, instance, plan_op, 1)})
            if state.run.status in {'pending', 'completed', 'failed'}:
                self._append(run_id, 'run.started')
            return instance

    def claim_ready(self, run_id: str, *, limit: int = 1, worker_id: str = '',
                    lease_expires_at: float = 0) -> list[AttemptClaim]:
        if limit < 1:
            raise ValueError('limit must be >= 1')
        with self._lock:
            _validate_claim_request(worker_id, lease_expires_at)
            state = self.state(run_id)
            claims: list[AttemptClaim] = []
            for attempt in self._ready_attempts(state)[:limit]:
                plan_op = self._plan_op_for_attempt(state, attempt)
                claim_generation = attempt.claim_generation + 1
                claim_id = f'claim:{run_id}:{attempt.attempt_id}:{claim_generation}'
                claim = AttemptClaim(claim_id, attempt.attempt_id, attempt.run_id,
                                     attempt.plan_version, attempt.epoch, attempt.op_id, plan_op,
                                     self._resolve_inputs_at_claim(
                                         state, attempt, plan_op), attempt.output_artifact_keys,
                                     worker_id=worker_id, lease_expires_at=float(lease_expires_at),
                                     claim_generation=claim_generation)
                self._append(run_id,
                             'attempt.claimed',
                             {'attempt_id': attempt.attempt_id,
                              'claim_id': claim.claim_id,
                              'resolved_input_refs': claim.resolved_input_refs,
                              'worker_id': worker_id,
                              'lease_expires_at': float(lease_expires_at),
                              'claim_generation': claim_generation})
                claims.append(claim)
            return claims

    def complete_attempt(self, claim: AttemptClaim, result: AttemptExecutionResult) -> AttemptResult:
        with self._lock:
            attempt, stale = self._attempt_or_stale_result(claim)
            if stale is not None:
                return stale
            if terminal := _terminal_result(attempt):
                return terminal
            state = self.state(attempt.run_id)
            if attempt.plan_version != state.run.active_plan_version:
                return self._finish_attempt(attempt, 'stale', reason='plan_superseded')
            if attempt.status == 'cancel_requested':
                return self._finish_attempt(attempt, 'cancelled', reason='cancel_requested')
            if self.committer is None:
                raise RuntimeError('RunController requires a committer to complete attempts')
            commit = self.committer.commit_attempt(attempt, self._plan_op_for_attempt(state, attempt), result)
            if commit.status == 'committed':
                return self._finish_attempt(
                    attempt,
                    'completed',
                    commit_status='committed',
                    output_refs=commit.output_refs)
            if commit.status == 'stale':
                return self._finish_attempt(attempt, 'stale', commit_status='stale', reason=commit.reason)
            out = self._record_failure(attempt, AttemptExecutionResult(
                False, error_message=commit.reason or commit.status), commit.status)
            self._maybe_finish_run(attempt.run_id)
            return out

    def fail_attempt(self, claim: AttemptClaim, error: dict[str, Any]) -> AttemptResult:
        with self._lock:
            attempt, stale = self._attempt_or_stale_result(claim)
            if stale is not None:
                return stale
            if terminal := _terminal_result(attempt):
                return terminal
            state = self.state(attempt.run_id)
            if attempt.plan_version != state.run.active_plan_version:
                return self._finish_attempt(attempt, 'stale', reason='plan_superseded')
            if attempt.status == 'cancel_requested':
                return self._finish_attempt(attempt, 'cancelled', reason='cancel_requested')
            result = AttemptExecutionResult(False, error_type=str(error.get('error_type') or ''),
                                            error_message=str(error.get('error_message') or 'execution_failed'))
            out = self._record_failure(attempt, result)
            self._maybe_finish_run(attempt.run_id)
            return out

    def pause(self, run_id: str, *, command_id: str) -> RunState:
        with self._lock:
            return self._status_command(run_id, command_id, 'pause', 'running', 'run.paused')

    def resume(self, run_id: str, *, command_id: str) -> RunState:
        with self._lock:
            return self._status_command(run_id, command_id, 'resume', 'paused', 'run.resumed')

    def cancel(self, run_id: str, *, command_id: str) -> RunState:
        with self._lock:
            return self.request_cancel(run_id, command_id=command_id)

    def is_cancel_requested(self, claim: AttemptClaim) -> bool:
        return self.inspect_claim(claim).cancel_requested

    def inspect_claim(self, claim: AttemptClaim) -> ClaimInspectionResult:
        with self._lock:
            attempt = self.state(claim.run_id).attempts.get(claim.attempt_id)
            if attempt is None:
                raise ValueError(f'unknown attempt_id: {claim.attempt_id}')
            if attempt.claim_id != claim.claim_id:
                return ClaimInspectionResult('stale', claim.attempt_id, claim.claim_id)
            if attempt.status in ATTEMPT_TERMINAL:
                return ClaimInspectionResult(
                    'terminal',
                    claim.attempt_id,
                    claim.claim_id,
                    attempt_status=attempt.status,
                    reason=attempt.reason)
            return ClaimInspectionResult(
                'current',
                claim.attempt_id,
                claim.claim_id,
                attempt.status == 'cancel_requested',
                attempt.status)

    def heartbeat_claim(self, claim: AttemptClaim, *, lease_expires_at: float) -> ClaimHeartbeatResult:
        if lease_expires_at <= 0:
            raise ValueError('lease_expires_at must be > 0')
        with self._lock:
            inspection = self.inspect_claim(claim)
            if inspection.status == 'stale':
                return ClaimHeartbeatResult('stale', claim.attempt_id, claim.claim_id)
            if inspection.status == 'terminal':
                return ClaimHeartbeatResult('terminal', claim.attempt_id, claim.claim_id)
            self._append(claim.run_id, 'attempt.heartbeat', {
                'attempt_id': claim.attempt_id,
                'claim_id': claim.claim_id,
                'lease_expires_at': float(lease_expires_at),
            })
            return ClaimHeartbeatResult('extended', claim.attempt_id, claim.claim_id)

    def state(self, run_id: str) -> ControllerState:
        with self._lock:
            return _replay(run_id, self.event_log.scan(run_id))

    def request_cancel(self, run_id: str, *, command_id: str) -> RunState:
        with self._lock:
            seen, state = self._seen_command(run_id, command_id, kind='cancel')
            if seen is not _MISSING or state.run.status in RUN_PLAN_BLOCKED:
                if seen is _MISSING:
                    self._record_command(run_id, command_id, 'cancel', {'status': state.run.status})
                return state.run
            active = state.run.active_plan_version
            for attempt in state.attempts.values():
                if attempt.plan_version == active and attempt.status in {'pending', 'claimed'}:
                    self._append(
                        run_id, f"attempt.{'cancelled' if attempt.status == 'pending' else 'cancel_requested'}", {
                            'attempt_id': attempt.attempt_id})
            self._append(run_id, 'run.cancel_requested', {'command_id': command_id})
            self._maybe_finish_run(run_id)
            return self.state(run_id).run

    def retry_failed(self, run_id: str, *, command_id: str) -> list[Attempt]:
        with self._lock:
            seen, state = self._seen_command(run_id, command_id, kind='retry_failed')
            if seen is not _MISSING:
                return [state.attempts[item] for item in seen.get('attempt_ids', []) if item in state.attempts]
            created = [
                self._schedule_retry(
                    run_id,
                    state.active_plan,
                    attempt) for attempt in state.attempts_by_status.get(
                    'failed',
                    ()) if state.active_plan is not None and self._retryable_latest(
                    state,
                    attempt)]
            self._record_command(run_id, command_id, 'retry_failed', {
                                 'attempt_ids': [attempt.attempt_id for attempt in created]})
            if created and state.run.status == 'failed':
                self._append(run_id, 'run.resumed')
            return created

    def recover_expired_claims(
            self,
            run_id: str,
            *,
            now: float,
            max_recoveries: int,
            command_id: str) -> LeaseRecoveryResult:
        if max_recoveries < 0:
            raise ValueError('max_recoveries must be >= 0')
        with self._lock:
            seen, state = self._seen_command(run_id, command_id, kind='lease_recovery')
            if seen is not _MISSING:
                return _lease_recovery_result_from_payload(run_id, seen)

            recovered: list[str] = []
            cancelled: list[str] = []
            failed: list[str] = []
            skipped: list[str] = []
            active = state.run.active_plan_version
            if state.run.status in RUN_CLOSED:
                skipped = [
                    attempt.attempt_id
                    for attempt in state.attempts.values()
                    if attempt.plan_version == active and attempt.status in {'claimed', 'cancel_requested'}
                ]
                result_payload = {
                    'run_id': run_id,
                    'recovered_attempt_ids': recovered,
                    'cancelled_attempt_ids': cancelled,
                    'failed_attempt_ids': failed,
                    'skipped_attempt_ids': skipped,
                }
                self._record_command(run_id, command_id, 'lease_recovery', result_payload)
                return _lease_recovery_result_from_payload(run_id, {'kind': 'lease_recovery', **result_payload})
            for attempt in state.attempts.values():
                if attempt.plan_version != active or attempt.status not in {'claimed', 'cancel_requested'}:
                    continue
                if attempt.lease_expires_at <= 0 or attempt.lease_expires_at > now:
                    skipped.append(attempt.attempt_id)
                    continue
                if attempt.status == 'cancel_requested':
                    self._append(run_id, 'attempt.cancelled', {
                        'attempt_id': attempt.attempt_id,
                        'claim_id': attempt.claim_id,
                        'reason': 'cancel_requested',
                    })
                    cancelled.append(attempt.attempt_id)
                    continue
                if attempt.lease_recovery_count < max_recoveries:
                    self._append(run_id, 'attempt.requeued', {
                        'attempt_id': attempt.attempt_id,
                        'claim_id': attempt.claim_id,
                        'reason': 'lease_expired',
                    })
                    recovered.append(attempt.attempt_id)
                    continue
                self._record_lease_exhausted(attempt)
                failed.append(attempt.attempt_id)

            self._maybe_finish_run(run_id)
            result_payload = {
                'run_id': run_id,
                'recovered_attempt_ids': recovered,
                'cancelled_attempt_ids': cancelled,
                'failed_attempt_ids': failed,
                'skipped_attempt_ids': skipped,
            }
            self._record_command(run_id, command_id, 'lease_recovery', result_payload)
            return _lease_recovery_result_from_payload(run_id, {'kind': 'lease_recovery', **result_payload})

    def _seen_command(self, run_id: str, command_id: str, *, kind: str,
                      require_run: bool = True) -> tuple[Any, ControllerState]:
        validate_nonempty(command_id, 'command_id')
        state = self.state(run_id)
        if require_run and not state.run_exists:
            raise ValueError(f'unknown run: {run_id}')
        seen = state.command_results.get(command_id, _MISSING)
        if seen is not _MISSING and seen.get('kind') != kind:
            raise ValueError(f"command_id reused for {seen.get('kind')}: {command_id}")
        return seen, state

    def _status_command(
            self,
            run_id: str,
            command_id: str,
            kind: str,
            expected: RunStatus,
            event_type: str) -> RunState:
        seen, state = self._seen_command(run_id, command_id, kind=kind)
        if seen is not _MISSING:
            return replace(state.run, status=seen.get('status', state.run.status))
        self._append(
            run_id, event_type, {
                'command_id': command_id}) if state.run.status == expected else self._record_command(
            run_id, command_id, kind, {
                'status': state.run.status})
        return self.state(run_id).run

    def _finish_attempt(self, attempt: Attempt, status: AttemptStatus, *, commit_status: CommitStatus | None = None,
                        reason: str = '', output_refs: dict[ArtifactKey, ArtifactRef] | None = None) -> AttemptResult:
        payload: dict[str, Any] = {'attempt_id': attempt.attempt_id}
        if reason:
            payload['reason'] = reason
        if output_refs is not None:
            payload['output_refs'] = output_refs
        self._append(attempt.run_id, f'attempt.{status}', payload)
        self._maybe_finish_run(attempt.run_id)
        return AttemptResult(attempt.attempt_id, status, commit_status, reason)

    def _record_failure(self, attempt: Attempt, result: AttemptExecutionResult,
                        commit_status: CommitStatus | None = None) -> AttemptResult:
        reason = result.error_message or result.error_type or 'execution_failed'
        self._append(attempt.run_id, 'attempt.failed', {'attempt_id': attempt.attempt_id, 'reason': reason})
        state = self.state(attempt.run_id)
        if state.active_plan is not None and self._retryable_latest(state, state.attempts[attempt.attempt_id]):
            self._schedule_retry(attempt.run_id, state.active_plan, state.attempts[attempt.attempt_id])
        return AttemptResult(attempt.attempt_id, 'failed', commit_status, reason)

    def _record_lease_exhausted(self, attempt: Attempt) -> AttemptResult:
        self._append(attempt.run_id, 'attempt.failed', {
            'attempt_id': attempt.attempt_id,
            'claim_id': attempt.claim_id,
            'reason': 'lease_recovery_exhausted',
        })
        state = self.state(attempt.run_id)
        if state.active_plan is not None and self._retryable_latest(state, state.attempts[attempt.attempt_id]):
            self._schedule_retry(attempt.run_id, state.active_plan, state.attempts[attempt.attempt_id])
        return AttemptResult(attempt.attempt_id, 'failed', reason='lease_recovery_exhausted')

    def _schedule_retry(self, run_id: str, plan: PlanInstance, attempt: Attempt) -> Attempt:
        retry = self._new_attempt(run_id, plan, self._plan_op_by_id(
            plan.plan, attempt.op_id), attempt.attempt_number + 1)
        self._append(run_id, 'attempt.retry_scheduled', {'attempt_id': attempt.attempt_id})
        self._append(run_id, 'attempt.created', {'attempt': retry})
        return retry

    def _retryable_latest(self, state: ControllerState, attempt: Attempt) -> bool:
        return (
            attempt.plan_version == state.run.active_plan_version
            and attempt.attempt_number < self.retry_policy.max_attempts
            and state.latest_attempt_by_op.get((attempt.plan_version, attempt.op_id)) == attempt
        )

    def _ready_attempts(self, state: ControllerState) -> list[Attempt]:
        active = state.run.active_plan_version
        if state.run.status != 'running' or active is None:
            return []
        claimed_outputs = {artifact_id for attempt in state.attempts_by_status.get('claimed', ())
                           if attempt.plan_version == active for artifact_id in attempt.output_artifact_keys}
        return [
            attempt for attempt in state.attempts_by_status.get(
                'pending', ()) if attempt.plan_version == active and not any(
                (dep := state.latest_attempt_by_op.get(
                    (active, op_id))) is None or dep.status != 'completed' for op_id in attempt.depends_on) and not any(
                    artifact_id in claimed_outputs for artifact_id in attempt.output_artifact_keys)]

    def _resolve_inputs_at_claim(self, state: ControllerState, attempt: Attempt,
                                 plan_op: PlanOp) -> dict[ArtifactKey, ArtifactRef]:
        refs = dict(plan_op.input_key_versions)
        for key in plan_op.planned_input_keys:
            producer = state.producer_by_artifact.get((attempt.plan_version, key))
            if producer is not None and (ref := producer.output_refs.get(key)) is not None:
                refs[key] = ref
        return refs

    def _plan_op_for_attempt(self, state: ControllerState, attempt: Attempt) -> PlanOp:
        return self._plan_op_by_id(state.plans[attempt.plan_version].plan, attempt.op_id)

    @staticmethod
    def _plan_op_by_id(plan: ExecutionPlan, op_id: str) -> PlanOp:
        try:
            return next(plan_op for plan_op in _plan_ops(plan) if plan_op.op_id == op_id)
        except StopIteration:
            raise ValueError(f'plan op not found: {op_id}') from None

    def _maybe_finish_run(self, run_id: str) -> None:
        state = self.state(run_id)
        if state.run.status not in {'running', 'cancel_requested'} or state.run.active_plan_version is None:
            return
        statuses = {attempt.status for (version, _), attempt in state.latest_attempt_by_op.items()
                    if version == state.run.active_plan_version}
        if state.run.status == 'cancel_requested' and statuses and not statuses.intersection(ATTEMPT_OPEN):
            self._append(run_id, 'run.cancelled')
        elif statuses.intersection({'failed', 'stale'}):
            self._append(run_id, 'run.failed')
        elif statuses == {'completed'}:
            self._append(run_id, 'run.completed')

    def _new_attempt(self, run_id: str, plan: PlanInstance, plan_op: PlanOp, attempt_number: int) -> Attempt:
        return Attempt(
            f'plan{plan.plan_version}:{plan_op.op_id}:{attempt_number}',
            run_id,
            plan.plan_version,
            plan.epoch,
            plan_op.op_id,
            {},
            plan_op.output_keys,
            plan_op.depends_on,
            attempt_number=attempt_number)

    def _append(self, run_id: str, event_type: str, payload: dict[str, Any] | None = None) -> int:
        return self.event_log.append(ControllerEvent(event_type, run_id, payload or {}))

    def _record_command(self, run_id: str, command_id: str, kind: str, result: Any) -> None:
        self._append(run_id, 'command.recorded', {'command_id': command_id, 'result': {'kind': kind, **result}})

    def _attempt_for_claim(self, claim: AttemptClaim) -> Attempt:
        attempt = self.state(claim.run_id).attempts.get(claim.attempt_id)
        if attempt is None or attempt.claim_id != claim.claim_id:
            raise ValueError(f'unknown claim_id: {claim.claim_id}')
        return attempt

    def _attempt_or_stale_result(self, claim: AttemptClaim) -> tuple[Attempt, None] | tuple[None, AttemptResult]:
        attempt = self.state(claim.run_id).attempts.get(claim.attempt_id)
        if attempt is None:
            raise ValueError(f'unknown attempt_id: {claim.attempt_id}')
        if attempt.claim_id != claim.claim_id:
            return None, AttemptResult(claim.attempt_id, 'stale', reason='stale_claim')
        return attempt, None


def _replay(run_id: str, events: Iterable[ControllerEvent]) -> ControllerState:
    data: dict[str, Any] = {'run': RunState(run_id), 'run_exists': False, 'plans': {
    }, 'attempts': {}, 'command_results': {}}
    for event in sorted(events, key=lambda item: item.seq):
        payload = event.payload
        if event.event_type == 'run.created':
            data['run_exists'] = True
            data['run'] = replace(data['run'], status='pending')
        elif event.event_type in RUN_EVENTS:
            _record_command(data, event, {'kind': RUN_COMMAND_KINDS.get(
                event.event_type, event.event_type), 'status': RUN_EVENTS[event.event_type]})
            data['run'] = replace(data['run'], status=RUN_EVENTS[event.event_type])
        elif event.event_type == 'plan.submitted':
            plan = payload['plan']
            data['plans'][plan.plan_version] = plan
            _record_command(data, event, {'kind': 'submit_plan', 'plan_version': plan.plan_version})
            data['run'] = replace(data['run'], active_plan_version=plan.plan_version, epoch=plan.epoch)
        elif event.event_type == 'attempt.created':
            attempt = payload['attempt']
            data['attempts'][attempt.attempt_id] = attempt
        elif event.event_type == 'attempt.claimed':
            _update_attempt(data['attempts'], payload['attempt_id'], status='claimed', claim_id=payload['claim_id'],
                            resolved_input_refs=payload.get('resolved_input_refs', {}),
                            worker_id=str(payload.get('worker_id') or ''),
                            lease_expires_at=float(payload.get('lease_expires_at') or 0),
                            claim_generation=int(payload.get('claim_generation') or 0))
        elif event.event_type == 'attempt.heartbeat':
            _update_attempt_if_claim_matches(data['attempts'], payload['attempt_id'], payload.get('claim_id'),
                                             lease_expires_at=float(payload.get('lease_expires_at') or 0))
        elif event.event_type == 'attempt.requeued':
            _update_attempt_if_claim_matches(
                data['attempts'],
                payload['attempt_id'],
                payload.get('claim_id'),
                status='pending',
                claim_id='',
                worker_id='',
                lease_expires_at=0.0,
                resolved_input_refs={},
                reason=str(
                    payload.get('reason') or ''),
                lease_recovery_count=_next_recovery_count(
                    data['attempts'],
                    payload['attempt_id']))
        elif event.event_type in ATTEMPT_EVENTS:
            updates = {'status': ATTEMPT_EVENTS[event.event_type], 'reason': str(payload.get('reason') or ''),
                       **({'output_refs': payload['output_refs']} if 'output_refs' in payload else {})}
            if 'claim_id' in payload:
                _update_attempt_if_claim_matches(
                    data['attempts'], payload['attempt_id'], payload.get('claim_id'), **updates)
            else:
                _update_attempt(data['attempts'], payload['attempt_id'], **updates)
        elif event.event_type == 'command.recorded':
            _record_command(data, event, payload.get('result'))
    return _build_state(data)


def _record_command(data: dict[str, Any], event: ControllerEvent, result: Any) -> None:
    if command_id := str(event.payload.get('command_id') or ''):
        data['command_results'][command_id] = result


def _build_state(data: dict[str, Any]) -> ControllerState:
    latest: dict[tuple[int, str], Attempt] = {}
    producer: dict[tuple[int, ArtifactKey], Attempt] = {}
    by_status: dict[AttemptStatus, list[Attempt]] = {}
    for attempt in data['attempts'].values():
        key = (attempt.plan_version, attempt.op_id)
        by_status.setdefault(attempt.status, []).append(attempt)
        if key not in latest or attempt.attempt_number > latest[key].attempt_number:
            latest[key] = attempt
        if attempt.status == 'completed':
            producer.update({(attempt.plan_version, artifact_id): attempt for artifact_id in attempt.output_refs})
    return ControllerState(data['run'], data['run_exists'], data['plans'], data['attempts'], data['command_results'],
                           latest, producer, {status: tuple(items) for status, items in by_status.items()})


def _plan_ops(plan: ExecutionPlan) -> tuple[PlanOp, ...]:
    return tuple(chain.from_iterable(plan.layers))


def _update_attempt(attempts: dict[str, Attempt], attempt_id: str, **changes: Any) -> None:
    if attempt_id in attempts:
        attempts[attempt_id] = replace(attempts[attempt_id], **changes)


def _update_attempt_if_claim_matches(attempts: dict[str, Attempt],
                                     attempt_id: str, expected_claim_id: Any, **changes: Any) -> None:
    attempt = attempts.get(str(attempt_id))
    if attempt is not None and attempt.claim_id == str(expected_claim_id or ''):
        attempts[attempt.attempt_id] = replace(attempt, **changes)


def _next_recovery_count(attempts: dict[str, Attempt], attempt_id: str) -> int:
    attempt = attempts.get(str(attempt_id))
    return 0 if attempt is None else attempt.lease_recovery_count + 1


def _terminal_result(attempt: Attempt) -> AttemptResult | None:
    return AttemptResult(
        attempt.attempt_id,
        attempt.status,
        reason=attempt.reason) if attempt.status in ATTEMPT_TERMINAL else None


def _artifact_keys(values: set[ArtifactKey]) -> set[ArtifactKey]:
    if any(not isinstance(value, ArtifactKey) for value in values):
        raise TypeError('plan targets must be ArtifactKey values')
    return set(values)


def _normalize_ref_key_map(values: dict[ArtifactKey, ArtifactRef]) -> dict[ArtifactKey, ArtifactRef]:
    if any(not isinstance(key, ArtifactKey) for key in values):
        raise TypeError('output refs must be keyed by ArtifactKey')
    return dict(values)


def _validate_claim_request(worker_id: str, lease_expires_at: float) -> None:
    if (not worker_id and lease_expires_at != 0) or (worker_id and lease_expires_at <= 0):
        raise ValueError(
            "claim must be non-leased (worker_id='', lease_expires_at=0) "
            'or leased (worker_id set, lease_expires_at > 0)',
        )


def _validate_scan_window(seq: int, limit: int) -> None:
    if seq < 0:
        raise ValueError('seq must be >= 0')
    if limit < 1:
        raise ValueError('limit must be >= 1')


def _lease_recovery_result_from_payload(run_id: str, payload: dict[str, Any]) -> LeaseRecoveryResult:
    return LeaseRecoveryResult(
        str(payload.get('run_id') or run_id),
        tuple(str(item) for item in payload.get('recovered_attempt_ids', ())),
        tuple(str(item) for item in payload.get('cancelled_attempt_ids', ())),
        tuple(str(item) for item in payload.get('failed_attempt_ids', ())),
        tuple(str(item) for item in payload.get('skipped_attempt_ids', ())),
    )
