from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from threading import RLock
from types import MappingProxyType
from typing import Any, Literal, Protocol

from .artifact import ArtifactKey, ArtifactRef
from .controller import RunController, RunState
from .mutation import ArtifactMutationRequest, ArtifactMutationResult, ArtifactMutationService
from .reconciliation import ReconcileRequest, ReconcileResult, ReconciliationScheduler
from .utils import validate_nonempty

InterventionStatus = Literal['applied', 'failed']
InterventionKind = Literal['patch_and_reconcile', 'materialize']
RUN_NOT_ACCEPTING_PLAN = frozenset({'failed', 'cancelled', 'cancel_requested'})


@dataclass(frozen=True)
class PatchAndReconcileRequest:
    run_id: str
    command_id: str
    artifact: ArtifactKey
    value: Any
    expected_ref: ArtifactRef | None
    patch_source: str = 'intervention'
    include_downstream: bool = True
    pause_first: bool = False
    resume_after: bool = False
    reason: str = 'patch_and_reconcile'
    metadata: Mapping[str, Any] = field(default_factory=lambda: MappingProxyType({}))

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.command_id, 'command_id')
        object.__setattr__(self, 'metadata', _freeze_mapping(self.metadata))


@dataclass(frozen=True)
class MaterializeInterventionRequest:
    run_id: str
    command_id: str
    artifacts: tuple[ArtifactKey, ...]
    include_downstream: bool = True
    resume_after: bool = False
    reason: str = 'manual_materialize'

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.command_id, 'command_id')
        object.__setattr__(self, 'artifacts', _stable_artifacts(self.artifacts))


@dataclass(frozen=True)
class InterventionResult:
    status: InterventionStatus
    mutation_result: ArtifactMutationResult | None = None
    reconcile_result: ReconcileResult | None = None
    run: RunState | None = None
    reason: str = ''


@dataclass(frozen=True)
class InterventionRecord:
    kind: InterventionKind
    result: InterventionResult


class InterventionLog(Protocol):
    def get(self, command_id: str) -> InterventionRecord | None:
        ...

    def record(self, command_id: str, kind: InterventionKind, result: InterventionResult) -> None:
        ...


class InMemoryInterventionLog:
    def __init__(self) -> None:
        self._records: dict[str, InterventionRecord] = {}

    def get(self, command_id: str) -> InterventionRecord | None:
        return self._records.get(command_id)

    def record(self, command_id: str, kind: InterventionKind, result: InterventionResult) -> None:
        self._records[command_id] = InterventionRecord(kind, result)


class FlowInterventionCoordinator:
    """Single-process intervention coordinator; durable idempotency belongs in FC-7."""

    def __init__(
        self,
        *,
        controller: RunController,
        mutation_service: ArtifactMutationService,
        scheduler: ReconciliationScheduler,
        log: InterventionLog | None = None,
    ) -> None:
        self.controller = controller
        self.mutation_service = mutation_service
        self.scheduler = scheduler
        self.log = log or InMemoryInterventionLog()
        self._lock = RLock()

    def patch_and_reconcile(self, request: PatchAndReconcileRequest) -> InterventionResult:
        with self._lock:
            if replay := self._replay_or_reuse(request.command_id, 'patch_and_reconcile'):
                return replay

            preflight = self._preflight_patch(request)
            if preflight is not None:
                return self._record(request.command_id, 'patch_and_reconcile', preflight)

            if request.pause_first:
                try:
                    self.controller.pause(request.run_id, command_id=f'{request.command_id}:pause')
                except ValueError:
                    return self._record(
                        request.command_id,
                        'patch_and_reconcile',
                        self._result('failed', request.run_id, reason='control_failed'),
                    )

            mutation = self.mutation_service.mutate(
                ArtifactMutationRequest(
                    command_id=f'{request.command_id}:mutation',
                    artifact=request.artifact,
                    value=request.value,
                    expected_ref=request.expected_ref,
                    reason=request.reason,
                    metadata=_intervention_metadata(request),
                )
            )
            if mutation.status == 'failed':
                return self._record(
                    request.command_id,
                    'patch_and_reconcile',
                    self._result('failed', request.run_id, mutation_result=mutation, reason='mutation_failed'),
                )

            reconcile = self.scheduler.reconcile(
                ReconcileRequest(
                    request.run_id,
                    f'{request.command_id}:reconcile',
                    changed_artifacts=(request.artifact,),
                    reason=request.reason,
                    include_downstream=request.include_downstream,
                )
            )
            if reconcile.status == 'failed':
                return self._record(
                    request.command_id,
                    'patch_and_reconcile',
                    self._result(
                        'failed',
                        request.run_id,
                        mutation_result=mutation,
                        reconcile_result=reconcile,
                        reason='reconcile_failed',
                    ),
                )
            if reconcile.status == 'skipped' and reconcile.reason == 'no_affected_artifacts':
                return self._record(
                    request.command_id,
                    'patch_and_reconcile',
                    self._result(
                        'applied',
                        request.run_id,
                        mutation_result=mutation,
                        reconcile_result=reconcile,
                        reason='no_reconcile_needed',
                    ),
                )
            if reconcile.status != 'submitted':
                return self._record(
                    request.command_id,
                    'patch_and_reconcile',
                    self._result(
                        'failed',
                        request.run_id,
                        mutation_result=mutation,
                        reconcile_result=reconcile,
                        reason='reconcile_failed',
                    ),
                )

            if request.resume_after:
                try:
                    self.controller.resume(request.run_id, command_id=f'{request.command_id}:resume')
                except ValueError:
                    return self._record(
                        request.command_id,
                        'patch_and_reconcile',
                        self._result(
                            'failed',
                            request.run_id,
                            mutation_result=mutation,
                            reconcile_result=reconcile,
                            reason='control_failed',
                        ),
                    )

            return self._record(
                request.command_id,
                'patch_and_reconcile',
                self._result('applied', request.run_id, mutation_result=mutation, reconcile_result=reconcile),
            )

    def materialize(self, request: MaterializeInterventionRequest) -> InterventionResult:
        with self._lock:
            if replay := self._replay_or_reuse(request.command_id, 'materialize'):
                return replay

            preflight = self._preflight_materialize(request)
            if preflight is not None:
                return self._record(request.command_id, 'materialize', preflight)

            reconcile = self.scheduler.reconcile(
                ReconcileRequest(
                    request.run_id,
                    f'{request.command_id}:reconcile',
                    materialize_artifacts=request.artifacts,
                    reason=request.reason,
                    include_downstream=request.include_downstream,
                )
            )
            if reconcile.status != 'submitted':
                return self._record(
                    request.command_id,
                    'materialize',
                    self._result('failed', request.run_id, reconcile_result=reconcile, reason='reconcile_failed'),
                )

            if request.resume_after:
                try:
                    self.controller.resume(request.run_id, command_id=f'{request.command_id}:resume')
                except ValueError:
                    return self._record(
                        request.command_id,
                        'materialize',
                        self._result('failed', request.run_id, reconcile_result=reconcile, reason='control_failed'),
                    )

            return self._record(
                request.command_id,
                'materialize',
                self._result('applied', request.run_id, reconcile_result=reconcile),
            )

    def _replay_or_reuse(self, command_id: str, kind: InterventionKind) -> InterventionResult | None:
        record = self.log.get(command_id)
        if record is None:
            return None
        if record.kind != kind:
            return InterventionResult('failed', reason='command_id_reused')
        return record.result

    def _record(self, command_id: str, kind: InterventionKind, result: InterventionResult) -> InterventionResult:
        self.log.record(command_id, kind, result)
        return result

    def _preflight_patch(self, request: PatchAndReconcileRequest) -> InterventionResult | None:
        if request.expected_ref is None:
            return InterventionResult('failed', reason='expected_ref_required')
        if request.expected_ref.key != request.artifact:
            return InterventionResult('failed', reason='expected_ref_key_mismatch')
        declares = getattr(self.scheduler, 'declares_artifact_key', None)
        if callable(declares) and not declares(request.artifact):
            return InterventionResult('failed', reason='unknown_target')
        if not self._run_accepts_plan(request.run_id):
            return self._result('failed', request.run_id, reason='run_not_accepting_plan')
        preflight = self.scheduler.reconcile(
            ReconcileRequest(
                request.run_id,
                f'{request.command_id}:preflight_reconcile',
                changed_artifacts=(request.artifact,),
                reason=request.reason,
                include_downstream=request.include_downstream,
                dry_run=True,
            )
        )
        if preflight.status == 'failed':
            return self._result(
                'failed',
                request.run_id,
                reconcile_result=preflight,
                reason='reconcile_preflight_failed',
            )
        return None

    def _preflight_materialize(self, request: MaterializeInterventionRequest) -> InterventionResult | None:
        if not request.artifacts:
            return InterventionResult('failed', reason='empty_selection')
        if not self._run_accepts_plan(request.run_id):
            return self._result('failed', request.run_id, reason='run_not_accepting_plan')
        return None

    def _run_accepts_plan(self, run_id: str) -> bool:
        state = self.controller.state(run_id)
        return not state.run_exists or state.run.status not in RUN_NOT_ACCEPTING_PLAN

    def _result(
        self,
        status: InterventionStatus,
        run_id: str,
        *,
        mutation_result: ArtifactMutationResult | None = None,
        reconcile_result: ReconcileResult | None = None,
        reason: str = '',
    ) -> InterventionResult:
        return InterventionResult(
            status,
            mutation_result,
            reconcile_result,
            _existing_run(self.controller, run_id),
            reason,
        )


def _stable_artifacts(artifacts: tuple[ArtifactKey, ...]) -> tuple[ArtifactKey, ...]:
    return tuple(sorted(set(artifacts)))


def _freeze_mapping(values: Mapping[Any, Any]) -> Mapping[Any, Any]:
    return MappingProxyType(dict(values))


def _intervention_metadata(request: PatchAndReconcileRequest) -> Mapping[str, Any]:
    metadata = dict(request.metadata)
    metadata.update(
        {
            'intervention_command_id': request.command_id,
            'intervention_run_id': request.run_id,
            'patch_source': request.patch_source,
        }
    )
    return MappingProxyType(metadata)


def _existing_run(controller: RunController, run_id: str) -> RunState | None:
    state = controller.state(run_id)
    return state.run if state.run_exists else None
