from __future__ import annotations

from dataclasses import dataclass
from itertools import chain
from typing import Literal

from .artifact import ArtifactKey, ArtifactVersionResolver
from .controller import PlanInstance, RunController
from .errors import DAGGraphError, MissingArtifactVersionError, UnknownTargetError
from .graph import DAGGraph
from .plan import ExecutionPlan
from .utils import validate_nonempty

ReconcileStatus = Literal['submitted', 'skipped', 'failed']


@dataclass(frozen=True)
class ReconcileRequest:
    run_id: str
    command_id: str
    changed_artifacts: tuple[ArtifactKey, ...] = ()
    materialize_artifacts: tuple[ArtifactKey, ...] = ()
    reason: str = 'artifact_reconcile'
    include_downstream: bool = True
    dry_run: bool = False

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.command_id, 'command_id')
        object.__setattr__(self, 'changed_artifacts', _stable_artifacts(self.changed_artifacts))
        object.__setattr__(self, 'materialize_artifacts', _stable_artifacts(self.materialize_artifacts))


@dataclass(frozen=True)
class ReconcileResult:
    status: ReconcileStatus
    changed_artifacts: tuple[ArtifactKey, ...]
    materialize_artifacts: tuple[ArtifactKey, ...]
    target_artifacts: tuple[ArtifactKey, ...] = ()
    plan_instance: PlanInstance | None = None
    reason: str = ''

    def __post_init__(self) -> None:
        object.__setattr__(self, 'changed_artifacts', _stable_artifacts(self.changed_artifacts))
        object.__setattr__(self, 'materialize_artifacts', _stable_artifacts(self.materialize_artifacts))
        object.__setattr__(self, 'target_artifacts', _stable_artifacts(self.target_artifacts))


class ReconciliationScheduler:
    def __init__(self, graph: DAGGraph, resolver: ArtifactVersionResolver, controller: RunController) -> None:
        self.graph = graph
        self.resolver = resolver
        self.controller = controller

    def declares_artifact_key(self, key: ArtifactKey) -> bool:
        return self.graph.declares_artifact_key(key)

    def reconcile(self, request: ReconcileRequest) -> ReconcileResult:
        if not request.changed_artifacts and not request.materialize_artifacts:
            return self._result(request, 'skipped', reason='empty_selection')

        try:
            affected = set().union(*(self.graph.affected_keys_of(key) for key in request.changed_artifacts))
            if not request.materialize_artifacts and not affected:
                return self._result(request, 'skipped', reason='no_affected_artifacts')

            plan, materialized = self._build_plan(request)
        except UnknownTargetError:
            return self._result(request, 'failed', reason='unknown_target')
        except MissingArtifactVersionError:
            return self._result(request, 'failed', reason='missing_input_version')
        except DAGGraphError:
            return self._result(request, 'failed', reason='plan_build_failed')

        targets = _outputs_of(plan)
        if request.dry_run:
            return self._result(
                request,
                'submitted',
                materialize_artifacts=materialized,
                target_artifacts=tuple(sorted(targets)),
            )
        try:
            instance = self.controller.submit_plan(
                request.run_id,
                plan,
                targets=targets,
                reason=request.reason,
                command_id=request.command_id,
            )
        except ValueError:
            return self._result(request, 'failed', reason='submit_failed')

        return self._result(
            request,
            'submitted',
            materialize_artifacts=materialized,
            target_artifacts=instance.target_artifacts,
            plan_instance=instance,
        )

    def _build_plan(self, request: ReconcileRequest):
        materialized = request.materialize_artifacts
        try:
            return self.graph.build_recompute_plan_for_keys(
                self.resolver,
                changed_keys=set(request.changed_artifacts),
                materialize_keys=set(request.materialize_artifacts),
                include_downstream=request.include_downstream,
            ), materialized
        except MissingArtifactVersionError:
            if request.materialize_artifacts:
                raise
            materialized = tuple(
                key
                for key in sorted(
                    set().union(*(self.graph.consumer_artifacts_of(key) for key in request.changed_artifacts))
                )
                if self._can_materialize(key)
            )
            if not materialized:
                raise
            return self.graph.build_recompute_plan_for_keys(
                self.resolver,
                materialize_keys=set(materialized),
                include_downstream=False,
            ), materialized

    def _can_materialize(self, key: ArtifactKey) -> bool:
        try:
            self.graph.build_recompute_plan_for_keys(
                self.resolver,
                materialize_keys={key},
                include_downstream=False,
            )
            return True
        except (DAGGraphError, MissingArtifactVersionError, UnknownTargetError):
            return False

    @staticmethod
    def _result(
        request: ReconcileRequest,
        status: ReconcileStatus,
        *,
        materialize_artifacts: tuple[ArtifactKey, ...] | None = None,
        target_artifacts: tuple[ArtifactKey, ...] = (),
        plan_instance: PlanInstance | None = None,
        reason: str = '',
    ) -> ReconcileResult:
        return ReconcileResult(
            status,
            request.changed_artifacts,
            request.materialize_artifacts if materialize_artifacts is None else materialize_artifacts,
            target_artifacts,
            plan_instance,
            reason,
        )


def _stable_artifacts(artifacts: tuple[ArtifactKey, ...]) -> tuple[ArtifactKey, ...]:
    return tuple(sorted(set(artifacts)))


def _outputs_of(plan: ExecutionPlan) -> set[ArtifactKey]:
    return {
        key
        for plan_op in chain.from_iterable(plan.layers)
        for key in plan_op.output_keys
    }
