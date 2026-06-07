from __future__ import annotations

from collections import deque

from ..artifacts.models import ArtifactRef, ImpactReport
from .models import (
    ArtifactSetRequirement,
    OperationRun,
    OperationRunChange,
    OperationRunChangeKind,
    OperationRunObserver,
    OperationRunRef,
    OperationRunSnapshot,
    OperationRunStatus,
    OperationSpec,
    RerunPlan,
    ScheduleBlocker,
    ScheduleState,
)


_ALLOWED_TRANSITIONS: dict[OperationRunStatus, set[OperationRunStatus]] = {
    'pending': {'running', 'checkpointed'},
    'running': {'checkpointed', 'ended'},
    'checkpointed': {'pending'},
    'ended': set(),
}


class OperationGraph:
    """In-memory DAG for operation dependencies, readiness, replacement, and rerun planning."""

    def __init__(self):
        self._specs: dict[str, OperationSpec] = {}
        self._runs: dict[OperationRunRef, OperationRun] = {}
        self._active_run_by_operation_id: dict[str, OperationRunRef] = {}
        self._latest_artifact_by_id: dict[str, ArtifactRef] = {}
        self._observers: list[OperationRunObserver] = []

    def add_observer(self, observer: OperationRunObserver) -> None:
        self._observers.append(observer)

    def register_default_graph(self, specs: list[OperationSpec]) -> None:
        self._validate_specs(specs)
        for spec in specs:
            self._specs[spec.operation_id] = spec
        for spec in self._topological_specs(specs):
            depends_on = [self._active_run_by_operation_id[operation_id] for operation_id in spec.depends_on]
            self.create_run(spec, inputs=[], depends_on=depends_on)

    def create_run(
        self,
        spec: OperationSpec,
        inputs: list[ArtifactRef],
        *,
        depends_on: list[OperationRunRef] | None = None,
        parent: OperationRunRef | None = None,
        source_message_id: str | None = None,
    ) -> OperationRunRef:
        self._specs.setdefault(spec.operation_id, spec)
        ref = OperationRunRef(self._next_run_id(spec.operation_id))
        previous_active = self._active_run_by_operation_id.get(spec.operation_id)
        run = OperationRun(
            ref=ref,
            spec=spec,
            attempt=self._next_attempt(spec.operation_id),
            parent=parent,
            source_message_id=source_message_id,
            input_refs=list(inputs),
            depends_on=list(depends_on if depends_on is not None else self._resolve_spec_dependencies(spec)),
        )
        self._ensure_writer_order(run)
        self._runs[ref] = run
        self._active_run_by_operation_id[spec.operation_id] = ref
        try:
            self._ensure_acyclic_runs()
        except Exception:
            self._runs.pop(ref, None)
            if previous_active is None:
                self._active_run_by_operation_id.pop(spec.operation_id, None)
            else:
                self._active_run_by_operation_id[spec.operation_id] = previous_active
            raise
        self._emit(OperationRunChange('created', None, self._snapshot(run)))
        return ref

    def insert_run(self, run: OperationRun, after: OperationRunRef | None = None) -> None:
        if run.ref in self._runs:
            raise ValueError(f'operation run already exists: {run.ref}')
        previous_active = self._active_run_by_operation_id.get(run.spec.operation_id)
        self._runs[run.ref] = run
        self._active_run_by_operation_id[run.spec.operation_id] = run.ref
        try:
            if after:
                self._add_dependency(after, run.ref)
            else:
                self._ensure_acyclic_runs()
            self._emit(OperationRunChange('created', None, self._snapshot(run)))
        except Exception:
            self._runs.pop(run.ref, None)
            if previous_active is None:
                self._active_run_by_operation_id.pop(run.spec.operation_id, None)
            else:
                self._active_run_by_operation_id[run.spec.operation_id] = previous_active
            raise

    def append_parallel_run(self, run: OperationRun, *, after: OperationRunRef, before: OperationRunRef) -> None:
        if run.ref in self._runs:
            raise ValueError(f'operation run already exists: {run.ref}')
        previous_active = self._active_run_by_operation_id.get(run.spec.operation_id)
        self._runs[run.ref] = run
        self._active_run_by_operation_id[run.spec.operation_id] = run.ref
        try:
            self._add_dependency(after, run.ref)
            before_change = self._add_dependency(run.ref, before)
            self._emit(OperationRunChange('created', None, self._snapshot(run)))
            if before_change:
                self._emit(before_change)
        except Exception:
            if run.ref in self._runs:
                self._runs[run.ref].depends_on = [dep for dep in self._runs[run.ref].depends_on if dep != after]
            if before in self._runs:
                self._runs[before].depends_on = [dep for dep in self._runs[before].depends_on if dep != run.ref]
            self._runs.pop(run.ref, None)
            if previous_active is None:
                self._active_run_by_operation_id.pop(run.spec.operation_id, None)
            else:
                self._active_run_by_operation_id[run.spec.operation_id] = previous_active
            self._ensure_acyclic_runs()
            raise

    def add_dependency(self, parent: OperationRunRef, child: OperationRunRef) -> None:
        change = self._add_dependency(parent, child)
        if change:
            self._emit(change)

    def _add_dependency(self, parent: OperationRunRef, child: OperationRunRef) -> OperationRunChange | None:
        self.get_run(parent)
        child_run = self.get_run(child)
        if parent in child_run.depends_on:
            return None
        before = self._snapshot(child_run)
        child_run.depends_on.append(parent)
        try:
            self._ensure_acyclic_runs()
        except Exception:
            child_run.depends_on = [dep for dep in child_run.depends_on if dep != parent]
            raise
        return OperationRunChange('dependencies_updated', before, self._snapshot(child_run))

    def remove_dependency(self, parent: OperationRunRef, child: OperationRunRef) -> None:
        self.get_run(parent)
        child_run = self.get_run(child)
        before = self._snapshot(child_run)
        child_run.depends_on = [dep for dep in child_run.depends_on if dep != parent]
        if before.depends_on != [str(dep) for dep in child_run.depends_on]:
            self._emit(OperationRunChange('dependencies_updated', before, self._snapshot(child_run)))

    def replace_dependency(
        self,
        old_parent: OperationRunRef,
        new_parent: OperationRunRef,
        child: OperationRunRef,
    ) -> None:
        self.get_run(new_parent)
        child_run = self.get_run(child)
        before = self._snapshot(child_run)
        previous = list(child_run.depends_on)
        child_run.depends_on = [new_parent if dep == old_parent else dep for dep in child_run.depends_on]
        try:
            self._ensure_acyclic_runs()
        except Exception:
            child_run.depends_on = previous
            raise
        if before.depends_on != [str(dep) for dep in child_run.depends_on]:
            self._emit(OperationRunChange('dependencies_updated', before, self._snapshot(child_run)))

    def supersede(self, old: OperationRunRef, new: OperationRunRef, reason: str) -> None:
        if old == new:
            raise ValueError('operation cannot supersede itself')
        old_run = self.get_run(old)
        new_run = self.get_run(new)
        if old_run.status != 'ended':
            raise ValueError(f'cannot supersede unfinished operation: {old}')
        if new_run.status != 'ended' or new_run.outcome != 'success':
            raise ValueError(f'replacement operation must finish successfully before supersede: {new}')
        if new_run.parent is not None and new_run.parent != old:
            raise ValueError(f'replacement operation already has a different parent: {new}')
        if old_run.superseded_by is not None:
            if old_run.superseded_by == new:
                return
            raise ValueError(f'operation already superseded: {old}')
        old_before = self._snapshot(old_run)
        new_before = self._snapshot(new_run)
        old_run.status = 'ended'
        old_run.superseded_by = new
        old_run.supersede_reason = reason
        old_run.outcome = 'superseded'
        if new_run.parent is None:
            new_run.parent = old
        self._active_run_by_operation_id[old_run.spec.operation_id] = new
        for run in self._runs.values():
            if run.ref == new:
                continue
            if run.status == 'ended' or run.output_refs:
                continue
            self.replace_dependency(old, new, run.ref)
        self._ensure_acyclic_runs()
        self._emit(OperationRunChange('superseded', old_before, self._snapshot(old_run), reason=reason))
        if new_before.parent != str(new_run.parent or ''):
            self._emit(OperationRunChange('dependencies_updated', new_before, self._snapshot(new_run), reason=reason))

    def ready_runs(self) -> list[OperationRunRef]:
        ready: list[OperationRunRef] = []
        for ref in self._topological_run_refs():
            run = self._runs[ref]
            if run.status != 'pending' or run.superseded_by:
                continue
            if self._dependencies_satisfied(run) and self._required_artifacts_available(run):
                ready.append(ref)
        return ready

    def can_run(self, ref: OperationRunRef) -> bool:
        run = self.get_run(ref)
        if run.status == 'ended':
            return True
        if run.status == 'running':
            return True
        if run.status != 'pending' or run.superseded_by:
            return False
        return self._dependencies_satisfied(run) and self._required_artifacts_available(run)

    def schedule_state(self) -> ScheduleState:
        ready = self.ready_runs()
        running = self.run_refs({'running'})
        checkpointed = self.run_refs({'checkpointed'})
        failed = [
            ScheduleBlocker(str(ref), 'failed_operation')
            for ref in self._topological_run_refs()
            if not self._runs[ref].superseded_by
            and self._runs[ref].status == 'ended'
            and self._runs[ref].outcome not in {'success', 'superseded'}
        ]
        blockers = failed + [self._blocker(ref) for ref in self.run_refs({'pending'}) if ref not in ready]
        active_runs = [run for run in self._runs.values() if not run.superseded_by]
        complete = bool(active_runs) and not ready and not running and not checkpointed and not blockers and all(
            run.status == 'ended' and run.outcome in {'success', 'superseded'} for run in active_runs
        )
        return ScheduleState(
            ready=ready,
            running=running,
            checkpointed=checkpointed,
            blockers=[blocker for blocker in blockers if blocker is not None],
            complete=complete,
        )

    def affected_runs(self, impact: ImpactReport) -> list[OperationRunRef]:
        affected_refs = set(impact.changed) | set(impact.impacted)
        run_refs: set[OperationRunRef] = set()
        for ref, run in self._runs.items():
            if run.superseded_by:
                continue
            if any(output in impact.impacted for output in run.output_refs):
                run_refs.add(ref)
                continue
            if any(input_ref in affected_refs for input_ref in self.inputs_for(ref)):
                run_refs.add(ref)
        return self._topological_run_refs(run_refs)

    def build_rerun_plan(self, impact: ImpactReport) -> RerunPlan:
        return RerunPlan(
            changed_refs=set(impact.changed),
            impacted_refs=set(impact.impacted),
            operation_refs=self.affected_runs(impact),
        )

    def runs_by_tag(self, key: str, value: str) -> list[OperationRunRef]:
        return [ref for ref in self._topological_run_refs() if _tag_value(self._runs[ref].spec, key) == value]

    def get_run(self, ref: OperationRunRef) -> OperationRun:
        try:
            return self._runs[ref]
        except KeyError as exc:
            raise KeyError(str(ref)) from exc

    def run_refs(self, statuses: set[OperationRunStatus] | None = None) -> list[OperationRunRef]:
        refs = self._topological_run_refs()
        if statuses is None:
            return refs
        return [ref for ref in refs if self._runs[ref].status in statuses]

    def active_run_for(self, operation_id: str) -> OperationRunRef | None:
        return self._active_run_by_operation_id.get(operation_id)

    def writer_for(self, artifact_id: str) -> OperationRunRef | None:
        for ref in self._topological_run_refs():
            run = self._runs[ref]
            if run.superseded_by or run.status == 'ended':
                continue
            if run.spec.tags.get('writes_artifact_id') == artifact_id:
                return ref
        return None

    def restore_run(self, snapshot: OperationRunSnapshot) -> OperationRunRef:
        ref = OperationRunRef(snapshot.operation_run_id)
        if ref in self._runs:
            raise ValueError(f'operation run already exists: {ref}')
        run = _run_from_snapshot(snapshot)
        previous_active = self._active_run_by_operation_id.get(run.spec.operation_id)
        self._runs[ref] = run
        self._active_run_by_operation_id[run.spec.operation_id] = ref
        for output in run.output_refs:
            self.register_artifact(output)
        try:
            self._ensure_acyclic_runs()
        except Exception:
            self._runs.pop(ref, None)
            if previous_active is None:
                self._active_run_by_operation_id.pop(run.spec.operation_id, None)
            else:
                self._active_run_by_operation_id[run.spec.operation_id] = previous_active
            raise
        return ref

    def start_run(self, ref: OperationRunRef) -> None:
        self._transition(ref, 'running', kind='started')

    def bind_inputs(self, ref: OperationRunRef, inputs: list[ArtifactRef]) -> None:
        run = self.get_run(ref)
        before = self._snapshot(run)
        run.input_refs = list(inputs)
        if before.input_refs != [str(item) for item in run.input_refs]:
            self._emit(OperationRunChange('inputs_bound', before, self._snapshot(run)))

    def checkpoint_run(self, ref: OperationRunRef) -> None:
        self._transition(ref, 'checkpointed', kind='checkpointed')

    def reset_run(self, ref: OperationRunRef) -> None:
        run = self.get_run(ref)
        before = self._snapshot(run)
        self._assert_transition(run.status, 'pending')
        run.status = 'pending'
        run.output_refs = []
        run.outcome = ''
        self._emit(OperationRunChange('reset', before, self._snapshot(run)))

    def retry_with_downstream(
        self,
        ref: OperationRunRef,
        *,
        source_message_id: str | None = None,
        spec_overrides: dict[str, OperationSpec] | None = None,
    ) -> list[OperationRunRef]:
        spec_overrides = spec_overrides or {}
        replacements: list[tuple[OperationRunRef, OperationRunRef]] = []
        for old_ref in [ref, *self._active_downstream_refs(ref)]:
            old_run = self.get_run(old_ref)
            if old_run.status in {'pending', 'checkpointed'} and not old_run.output_refs:
                self._replace_retry_dependencies(replacements, old_ref)
                continue
            replacements.append(
                (
                    old_ref,
                    self._create_retry_run(
                        old_ref,
                        replacements,
                        source_message_id=source_message_id,
                        spec_override=spec_overrides.get(str(old_ref)),
                    ),
                )
            )
        return [new for _, new in replacements]

    def settle_retry_replacements(
        self,
        replacements: list[OperationRunRef],
        *,
        reason: str = 'retry succeeded',
    ) -> None:
        for new_ref in replacements:
            new_run = self.get_run(new_ref)
            if new_run.status != 'ended' or new_run.outcome != 'success' or new_run.parent is None:
                continue
            old_run = self.get_run(new_run.parent)
            if old_run.status != 'ended':
                continue
            if old_run.superseded_by:
                continue
            self.supersede(new_run.parent, new_ref, reason)

    def end_run(self, ref: OperationRunRef, outputs: list[ArtifactRef], *, outcome: str = 'success') -> None:
        run = self.get_run(ref)
        before = self._snapshot(run)
        self._assert_transition(run.status, 'ended')
        run.status = 'ended'
        run.output_refs = list(outputs)
        run.outcome = outcome
        for output in outputs:
            self.register_artifact(output)
        self._emit(OperationRunChange('ended', before, self._snapshot(run)))

    def register_artifact(self, ref: ArtifactRef) -> None:
        current = self._latest_artifact_by_id.get(ref.artifact_id)
        if current is None or ref.version > current.version:
            self._latest_artifact_by_id[ref.artifact_id] = ref

    def inputs_for(self, ref: OperationRunRef) -> list[ArtifactRef]:
        run = self.get_run(ref)
        refs = {input_ref.artifact_id: input_ref for input_ref in run.input_refs}
        for artifact_id in run.spec.required_artifact_ids:
            if artifact_id in self._latest_artifact_by_id:
                refs.setdefault(artifact_id, self._latest_artifact_by_id[artifact_id])
        for artifact_ref in self._artifact_set_inputs(run):
            refs.setdefault(artifact_ref.artifact_id, artifact_ref)
        return list(refs.values())

    def _transition(self, ref: OperationRunRef, status: OperationRunStatus, *, kind: OperationRunChangeKind) -> None:
        run = self.get_run(ref)
        before = self._snapshot(run)
        self._assert_transition(run.status, status)
        run.status = status
        self._emit(OperationRunChange(kind, before, self._snapshot(run)))

    def _assert_transition(self, current: OperationRunStatus, new: OperationRunStatus) -> None:
        if new not in _ALLOWED_TRANSITIONS[current]:
            raise ValueError(f'invalid operation status transition: {current} -> {new}')

    def _next_run_id(self, operation_id: str) -> str:
        if OperationRunRef(operation_id) not in self._runs:
            return operation_id
        suffix = 2
        while OperationRunRef(f'{operation_id}#{suffix}') in self._runs:
            suffix += 1
        return f'{operation_id}#{suffix}'

    def _next_attempt(self, operation_id: str) -> int:
        return 1 + sum(run.spec.operation_id == operation_id for run in self._runs.values())

    def _resolve_spec_dependencies(self, spec: OperationSpec) -> list[OperationRunRef]:
        return [self._active_run_by_operation_id[operation_id] for operation_id in spec.depends_on]

    def _ensure_writer_order(self, candidate: OperationRun) -> None:
        artifact_id = candidate.spec.tags.get('writes_artifact_id')
        if not artifact_id:
            return
        for ref, run in self._runs.items():
            if run.superseded_by or run.status == 'ended':
                continue
            if run.spec.tags.get('writes_artifact_id') != artifact_id:
                continue
            if ref not in candidate.depends_on:
                raise ValueError(f'unordered active writer for artifact {artifact_id}: {ref} -> {candidate.ref}')

    def _create_retry_run(
        self,
        old_ref: OperationRunRef,
        replacements: list[tuple[OperationRunRef, OperationRunRef]],
        *,
        source_message_id: str | None,
        spec_override: OperationSpec | None = None,
    ) -> OperationRunRef:
        old_run = self.get_run(old_ref)
        spec = spec_override or old_run.spec
        if spec.operation_id != old_run.spec.operation_id:
            raise ValueError('retry override must keep the original operation_id')
        depends_on = [self._replacement_for(dep, replacements) for dep in old_run.depends_on]
        depends_on.extend(self._active_writer_dependencies(spec.tags.get('writes_artifact_id'), depends_on))
        return self.create_run(
            spec,
            inputs=[] if old_run.output_refs else list(old_run.input_refs),
            depends_on=depends_on,
            parent=old_ref,
            source_message_id=source_message_id or old_run.source_message_id,
        )

    def _replacement_for(
        self,
        ref: OperationRunRef,
        replacements: list[tuple[OperationRunRef, OperationRunRef]],
    ) -> OperationRunRef:
        for old, new in replacements:
            if old == ref:
                return new
        return ref

    def _replace_retry_dependencies(
        self,
        replacements: list[tuple[OperationRunRef, OperationRunRef]],
        ref: OperationRunRef,
    ) -> None:
        run = self.get_run(ref)
        for old, new in replacements:
            if old in run.depends_on:
                self.replace_dependency(old, new, ref)

    def _active_writer_dependencies(
        self,
        artifact_id: str | None,
        depends_on: list[OperationRunRef],
    ) -> list[OperationRunRef]:
        if not artifact_id:
            return []
        deps = set(depends_on)
        return [
            ref for ref, run in self._runs.items()
            if not run.superseded_by
            and run.status != 'ended'
            and run.spec.tags.get('writes_artifact_id') == artifact_id
            and ref not in deps
        ]

    def _active_downstream_refs(self, ref: OperationRunRef) -> list[OperationRunRef]:
        target_outputs = {output.artifact_id for output in self.get_run(ref).output_refs}
        downstream: set[OperationRunRef] = set()
        queue = deque([ref])
        while queue:
            current = queue.popleft()
            for candidate_ref in self._topological_run_refs():
                if candidate_ref == ref or candidate_ref in downstream:
                    continue
                candidate = self.get_run(candidate_ref)
                if candidate.superseded_by:
                    continue
                if not self._depends_on_or_uses(candidate, current, target_outputs):
                    continue
                downstream.add(candidate_ref)
                queue.append(candidate_ref)
        return self._topological_run_refs(downstream)

    def _depends_on_or_uses(self, run: OperationRun, parent_ref: OperationRunRef, parent_output_ids: set[str]) -> bool:
        if parent_ref in run.depends_on:
            return True
        if parent_output_ids and any(ref.artifact_id in parent_output_ids for ref in run.input_refs):
            return True
        return bool(parent_output_ids & set(run.spec.required_artifact_ids))

    def _dependencies_satisfied(self, run: OperationRun) -> bool:
        return all(
            self.get_run(dep).status == 'ended' and self.get_run(dep).outcome in {'success', 'superseded'}
            for dep in run.depends_on
        )

    def _blocker(self, ref: OperationRunRef) -> ScheduleBlocker | None:
        run = self.get_run(ref)
        dependency_blockers = [
            str(dep)
            for dep in run.depends_on
            if self.get_run(dep).status != 'ended' or self.get_run(dep).outcome not in {'success', 'superseded'}
        ]
        missing_artifacts = self._missing_required_artifact_ids(run)
        missing_sets = self._missing_required_artifact_sets(run)
        if dependency_blockers:
            return ScheduleBlocker(str(ref), 'dependency_not_satisfied', depends_on=dependency_blockers)
        if missing_artifacts or missing_sets:
            return ScheduleBlocker(
                str(ref),
                'missing_artifact',
                missing_artifact_ids=missing_artifacts,
                missing_artifact_sets=missing_sets,
            )
        return None

    def _required_artifacts_available(self, run: OperationRun) -> bool:
        return not self._missing_required_artifact_ids(run) and all(
            self._artifact_set_available(run, requirement)
            for requirement in run.spec.required_artifact_sets
        )

    def _missing_required_artifact_ids(self, run: OperationRun) -> list[str]:
        explicit = {ref.artifact_id for ref in run.input_refs}
        available = explicit | set(self._latest_artifact_by_id)
        return [artifact_id for artifact_id in run.spec.required_artifact_ids if artifact_id not in available]

    def _missing_required_artifact_sets(self, run: OperationRun) -> list[str]:
        return [
            requirement.name
            for requirement in run.spec.required_artifact_sets
            if not self._artifact_set_available(run, requirement)
        ]

    def _artifact_set_inputs(self, run: OperationRun) -> list[ArtifactRef]:
        refs: list[ArtifactRef] = []
        for requirement in run.spec.required_artifact_sets:
            refs.extend(self._outputs_matching_requirement(run, requirement))
        return refs

    def _outputs_matching_requirement(
        self,
        run: OperationRun,
        requirement: ArtifactSetRequirement,
    ) -> list[ArtifactRef]:
        refs: list[ArtifactRef] = []
        for parent in self._parents_matching_requirement(run, requirement):
            refs.extend(parent.output_refs)
        return refs

    def _artifact_set_available(self, run: OperationRun, requirement: ArtifactSetRequirement) -> bool:
        parents = self._parents_matching_requirement(run, requirement)
        outputs = [output for parent in parents for output in parent.output_refs]
        return len(outputs) >= requirement.min_count and all(parent.output_refs for parent in parents)

    def _parents_matching_requirement(
        self,
        run: OperationRun,
        requirement: ArtifactSetRequirement,
    ) -> list[OperationRun]:
        parents: list[OperationRun] = []
        for parent_ref in run.depends_on:
            parent = self.get_run(parent_ref)
            if _tag_value(parent.spec, requirement.producer_tag) == requirement.producer_value:
                parents.append(parent)
        return parents

    def _snapshot(self, run: OperationRun) -> OperationRunSnapshot:
        return OperationRunSnapshot(
            operation_run_id=str(run.ref),
            operation_id=run.spec.operation_id,
            operation_type=run.spec.operation_type,
            status=run.status,
            attempt=run.attempt,
            category=run.spec.category,
            flow_tag=run.spec.flow_tag,
            stage_tag=run.spec.stage_tag,
            input_refs=[str(ref) for ref in run.input_refs],
            output_refs=[str(ref) for ref in run.output_refs],
            depends_on=[str(ref) for ref in run.depends_on],
            parent=str(run.parent or ''),
            source_message_id=run.source_message_id or '',
            superseded_by=str(run.superseded_by or ''),
            supersede_reason=run.supersede_reason,
            outcome=run.outcome,
            tags=dict(run.spec.tags),
            params=dict(run.spec.params),
            required_artifact_ids=list(run.spec.required_artifact_ids),
            required_artifact_sets=[
                dict(
                    name=item.name,
                    producer_tag=item.producer_tag,
                    producer_value=item.producer_value,
                    min_count=item.min_count,
                )
                for item in run.spec.required_artifact_sets
            ],
        )

    def _emit(self, change: OperationRunChange) -> None:
        for observer in self._observers:
            observer.on_operation_run_change(change)

    def _validate_specs(self, specs: list[OperationSpec]) -> None:
        operation_ids = [spec.operation_id for spec in specs]
        if len(operation_ids) != len(set(operation_ids)):
            raise ValueError('duplicate operation_id in operation graph')
        known = set(operation_ids)
        missing = sorted({dep for spec in specs for dep in spec.depends_on if dep not in known})
        if missing:
            raise ValueError(f"unknown operation dependency: {', '.join(missing)}")
        self._topological_specs(specs)

    def _topological_specs(self, specs: list[OperationSpec]) -> list[OperationSpec]:
        by_id = {spec.operation_id: spec for spec in specs}
        indegree = {operation_id: 0 for operation_id in by_id}
        children = {operation_id: [] for operation_id in by_id}
        for spec in specs:
            for parent in spec.depends_on:
                indegree[spec.operation_id] += 1
                children[parent].append(spec.operation_id)
        queue = deque(operation_id for operation_id, count in indegree.items() if count == 0)
        ordered: list[OperationSpec] = []
        while queue:
            operation_id = queue.popleft()
            ordered.append(by_id[operation_id])
            for child in children[operation_id]:
                indegree[child] -= 1
                if indegree[child] == 0:
                    queue.append(child)
        if len(ordered) != len(specs):
            raise ValueError('operation graph must be a DAG')
        return ordered

    def _ensure_acyclic_runs(self) -> None:
        self._topological_run_refs()

    def _topological_run_refs(self, subset: set[OperationRunRef] | None = None) -> list[OperationRunRef]:
        selected = list(self._runs) if subset is None else [ref for ref in self._runs if ref in subset]
        indegree = {ref: 0 for ref in selected}
        children = {ref: [] for ref in selected}
        for ref in selected:
            for parent in self._runs[ref].depends_on:
                if parent not in indegree:
                    continue
                indegree[ref] += 1
                children[parent].append(ref)
        queue = deque(ref for ref, count in indegree.items() if count == 0)
        ordered: list[OperationRunRef] = []
        while queue:
            ref = queue.popleft()
            ordered.append(ref)
            for child in children[ref]:
                indegree[child] -= 1
                if indegree[child] == 0:
                    queue.append(child)
        if len(ordered) != len(selected):
            raise ValueError('operation runs must be a DAG')
        return ordered


def _tag_value(spec: OperationSpec, key: str) -> str | None:
    if key == 'category':
        return spec.category
    if key == 'flow':
        return spec.flow_tag
    if key == 'stage':
        return spec.stage_tag
    return spec.tags.get(key)


def _run_from_snapshot(snapshot: OperationRunSnapshot) -> OperationRun:
    return OperationRun(
        ref=OperationRunRef(snapshot.operation_run_id),
        spec=OperationSpec(
            operation_id=snapshot.operation_id,
            operation_type=snapshot.operation_type,
            category=snapshot.category,
            flow_tag=snapshot.flow_tag,
            stage_tag=snapshot.stage_tag,
            required_artifact_ids=list(snapshot.required_artifact_ids),
            required_artifact_sets=[ArtifactSetRequirement(**item) for item in snapshot.required_artifact_sets],
            tags=dict(snapshot.tags),
            params=dict(snapshot.params),
        ),
        status=snapshot.status,
        attempt=snapshot.attempt,
        parent=OperationRunRef(snapshot.parent) if snapshot.parent else None,
        source_message_id=snapshot.source_message_id or None,
        input_refs=[ArtifactRef.parse(value) for value in snapshot.input_refs],
        output_refs=[ArtifactRef.parse(value) for value in snapshot.output_refs],
        depends_on=[OperationRunRef(value) for value in snapshot.depends_on],
        superseded_by=OperationRunRef(snapshot.superseded_by) if snapshot.superseded_by else None,
        supersede_reason=snapshot.supersede_reason,
        outcome=snapshot.outcome,
    )
