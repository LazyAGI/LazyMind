from __future__ import annotations

from collections import deque
from collections.abc import Callable, Iterable
from dataclasses import replace
from typing import Any

from ..artifacts.models import ArtifactRef, ImpactReport
from .models import (ArtifactSetRequirement, OperationRun, OperationRunChange, OperationRunChangeKind,
                     OperationRunObserver, OperationRunRef, OperationRunSnapshot, OperationRunStatus, OperationSpec,
                     ScheduleBlocker, ScheduleState)


_ALLOWED_TRANSITIONS: dict[OperationRunStatus, set[OperationRunStatus]] = {
    'pending': {'running', 'checkpointed'}, 'running': {'checkpointed', 'ended'},
    'checkpointed': {'pending'}, 'ended': set(),
}
_DONE_OUTCOMES = {'success', 'superseded'}


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
        operation_ids = [spec.operation_id for spec in specs]
        if len(operation_ids) != len(set(operation_ids)): raise ValueError('duplicate operation_id in operation graph')
        missing = sorted({dep for spec in specs for dep in spec.depends_on if dep not in set(operation_ids)})
        if missing: raise ValueError(f"unknown operation dependency: {', '.join(missing)}")
        ordered = self._topological_specs(specs)
        for spec in specs:
            self._specs[spec.operation_id] = spec
        for spec in ordered:
            depends_on = [self._active_run_by_operation_id[operation_id] for operation_id in spec.depends_on]
            self.create_run(spec, inputs=[], depends_on=depends_on)

    def create_run(self, spec: OperationSpec, inputs: list[ArtifactRef], *,
                   depends_on: list[OperationRunRef] | None = None, parent: OperationRunRef | None = None,
                   source_message_id: str | None = None) -> OperationRunRef:
        spec = _spec_with_required_inputs(spec, inputs)
        self._specs.setdefault(spec.operation_id, spec)
        ref = OperationRunRef(self._next_run_id(spec.operation_id))
        previous_active = self._active_run_by_operation_id.get(spec.operation_id)
        if depends_on is None:
            depends_on = [self._active_run_by_operation_id[operation_id] for operation_id in spec.depends_on]
        run = OperationRun(ref=ref, spec=spec, attempt=self._next_attempt(spec.operation_id), parent=parent,
                           source_message_id=source_message_id, depends_on=list(depends_on),
                           input_refs=_merge_input_refs(inputs, spec.required_artifact_refs))
        self._bind_required_artifact_inputs(run, emit=False, allow_latest=True)
        self._ensure_writer_order(run)
        self._runs[ref] = run
        self._active_run_by_operation_id[spec.operation_id] = ref
        try:
            self._topological_run_refs()
        except Exception:
            self._runs.pop(ref, None)
            if previous_active is None: self._active_run_by_operation_id.pop(spec.operation_id, None)
            else: self._active_run_by_operation_id[spec.operation_id] = previous_active
            raise
        self._emit(OperationRunChange('created', None, self._snapshot(run)))
        return ref

    def replace_dependency(self, old_parent: OperationRunRef, new_parent: OperationRunRef,
                           child: OperationRunRef) -> None:
        self.get_run(new_parent)
        child_run = self.get_run(child)
        before = self._snapshot(child_run)
        previous = list(child_run.depends_on)
        child_run.depends_on = [new_parent if dep == old_parent else dep for dep in child_run.depends_on]
        try:
            self._topological_run_refs()
        except Exception:
            child_run.depends_on = previous
            raise
        if before.depends_on != [str(dep) for dep in child_run.depends_on]:
            self._emit(OperationRunChange('dependencies_updated', before, self._snapshot(child_run)))
        self._refresh_inputs_for_replaced_dependency(child_run, old_parent, new_parent)

    def supersede(self, old: OperationRunRef, new: OperationRunRef, reason: str) -> None:
        if old == new: raise ValueError('operation cannot supersede itself')
        old_run = self.get_run(old)
        new_run = self.get_run(new)
        if old_run.status != 'ended': raise ValueError(f'cannot supersede unfinished operation: {old}')
        if new_run.status != 'ended' or new_run.outcome != 'success':
            raise ValueError(f'replacement operation must finish successfully before supersede: {new}')
        if new_run.parent is not None and new_run.parent != old:
            raise ValueError(f'replacement operation already has a different parent: {new}')
        if old_run.superseded_by is not None:
            if old_run.superseded_by == new: return
            raise ValueError(f'operation already superseded: {old}')
        old_before = self._snapshot(old_run)
        new_before = self._snapshot(new_run)
        old_run.status = 'ended'
        old_run.superseded_by = new
        old_run.supersede_reason = reason
        old_run.outcome = 'superseded'
        if new_run.parent is None: new_run.parent = old
        self._active_run_by_operation_id[old_run.spec.operation_id] = new
        for run in self._runs.values():
            if run.ref == new or run.status == 'ended' or run.output_refs: continue
            self.replace_dependency(old, new, run.ref)
        self._topological_run_refs()
        self._emit(OperationRunChange('superseded', old_before, self._snapshot(old_run), reason=reason))
        if new_before.parent != str(new_run.parent or ''):
            self._emit(OperationRunChange('dependencies_updated', new_before, self._snapshot(new_run), reason=reason))

    def ready_runs(self) -> list[OperationRunRef]:
        ready: list[OperationRunRef] = []
        ordered = self._topological_run_refs()
        order = {ref: index for index, ref in enumerate(ordered)}
        depths: dict[OperationRunRef, int] = {}
        for ref in ordered:
            run = self._runs[ref]
            if run.status != 'pending' or run.superseded_by: continue
            if self._dependencies_satisfied(run) and self._required_artifacts_available(run): ready.append(ref)
        return sorted(ready, key=lambda ref: (-self._dependency_depth(ref, depths), order[ref]))

    def can_run(self, ref: OperationRunRef) -> bool:
        run = self.get_run(ref)
        if run.status in {'ended', 'running'}: return True
        if run.status != 'pending' or run.superseded_by: return False
        return self._dependencies_satisfied(run) and self._required_artifacts_available(run)

    def schedule_state(self) -> ScheduleState:
        ready = self.ready_runs()
        failed = [ScheduleBlocker(str(ref), 'failed_operation') for ref in self._topological_run_refs()
                  if not self._runs[ref].superseded_by and self._runs[ref].status == 'ended'
                  and self._runs[ref].outcome not in _DONE_OUTCOMES]
        blockers = failed + [self._blocker(ref) for ref in self.run_refs({'pending'}) if ref not in ready]
        blockers = [blocker for blocker in blockers if blocker is not None]
        running = self.run_refs({'running'})
        checkpointed = self.run_refs({'checkpointed'})
        active_runs = [run for run in self._runs.values() if not run.superseded_by]
        complete = bool(active_runs) and not ready and not running and not checkpointed and not blockers and all(
            run.status == 'ended' and run.outcome in _DONE_OUTCOMES for run in active_runs)
        return ScheduleState(ready=ready, running=running, checkpointed=checkpointed, blockers=blockers,
                             complete=complete)

    def affected_runs(self, impact: ImpactReport) -> list[OperationRunRef]:
        affected_refs = set(impact.changed) | set(impact.impacted)
        run_refs: set[OperationRunRef] = set()
        for ref, run in self._runs.items():
            if run.superseded_by: continue
            if any(output in impact.impacted for output in run.output_refs): run_refs.add(ref)
            elif any(input_ref in affected_refs for input_ref in self.inputs_for(ref)): run_refs.add(ref)
        return self._topological_run_refs(run_refs)

    def get_run(self, ref: OperationRunRef) -> OperationRun:
        try:
            return self._runs[ref]
        except KeyError as exc:
            raise KeyError(str(ref)) from exc

    def run_refs(self, statuses: set[OperationRunStatus] | None = None) -> list[OperationRunRef]:
        refs = self._topological_run_refs()
        if statuses is None: return refs
        return [ref for ref in refs if self._runs[ref].status in statuses]

    def active_run_for(self, operation_id: str) -> OperationRunRef | None:
        return self._active_run_by_operation_id.get(operation_id)

    def restore_run(self, snapshot: OperationRunSnapshot) -> OperationRunRef:
        ref = OperationRunRef(snapshot.operation_run_id)
        if ref in self._runs: raise ValueError(f'operation run already exists: {ref}')
        run = _run_from_snapshot(snapshot)
        previous_active = self._active_run_by_operation_id.get(run.spec.operation_id)
        self._runs[ref] = run
        self._active_run_by_operation_id[run.spec.operation_id] = ref
        for output in run.output_refs:
            self.register_artifact(output)
        try:
            self._topological_run_refs()
        except Exception:
            self._runs.pop(ref, None)
            if previous_active is None: self._active_run_by_operation_id.pop(run.spec.operation_id, None)
            else: self._active_run_by_operation_id[run.spec.operation_id] = previous_active
            raise
        return ref

    def start_run(self, ref: OperationRunRef) -> None:
        self._transition(ref, 'running', kind='started')

    def bind_inputs(self, ref: OperationRunRef, inputs: list[ArtifactRef]) -> None:
        run = self.get_run(ref)
        before = self._snapshot(run)
        run.spec = _spec_with_required_inputs(run.spec, inputs)
        run.input_refs = _merge_input_refs(inputs, run.spec.required_artifact_refs)
        self._bind_required_artifact_inputs(run, emit=False)
        after = self._snapshot(run)
        if before.input_refs != after.input_refs or before.required_artifact_refs != after.required_artifact_refs:
            self._emit(OperationRunChange('inputs_bound', before, after))

    def rebind_input_refs(self, ref: OperationRunRef,
                          replacements: dict[str, ArtifactRef]) -> dict[str, dict[str, str]]:
        run = self.get_run(ref)
        changes = {
            **_changed_replacements(run.spec.required_artifact_refs, replacements),
            **_changed_replacements(run.input_refs, replacements),
            **_changed_replacements(_param_artifact_refs(run.spec.params), replacements),
        }
        params = _replace_param_refs(run.spec.params, changes)
        if not changes and params == run.spec.params: return {}
        before = self._snapshot(run)
        run.input_refs = _replace_refs_by_artifact_id(run.input_refs, replacements)
        new_required = _replace_refs_by_artifact_id(run.spec.required_artifact_refs, replacements)
        run.spec = replace(run.spec, params=params, required_artifact_refs=new_required)
        run.input_refs = _merge_input_refs(run.input_refs, run.spec.required_artifact_refs)
        after = self._snapshot(run)
        if (before.input_refs != after.input_refs or before.required_artifact_refs != after.required_artifact_refs
                or before.params != after.params):
            self._emit(OperationRunChange('inputs_bound', before, after))
        return changes

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

    def retry_with_downstream(self, ref: OperationRunRef, *, source_message_id: str | None = None,
                              spec_overrides: dict[str, OperationSpec] | None = None) -> list[OperationRunRef]:
        spec_overrides = spec_overrides or {}
        replacements: list[tuple[OperationRunRef, OperationRunRef]] = []
        for old_ref in [ref, *self._active_downstream_refs(ref)]:
            old_run = self.get_run(old_ref)
            if old_run.status in {'pending', 'checkpointed'} and not old_run.output_refs:
                for old, new in replacements:
                    if old in old_run.depends_on: self.replace_dependency(old, new, old_ref)
                    else: self._refresh_inputs_for_replaced_dependency(old_run, old, new, add_dependency=True)
                continue
            new_ref = self._create_retry_run(old_ref, replacements, source_message_id=source_message_id,
                                             spec_override=spec_overrides.get(str(old_ref)))
            replacements.append((old_ref, new_ref))
        return [new for _, new in replacements]

    def settle_retry_replacements(self, replacements: list[OperationRunRef], *,
                                  reason: str = 'retry succeeded') -> None:
        for new_ref in replacements:
            new_run = self.get_run(new_ref)
            if new_run.status != 'ended' or new_run.outcome != 'success' or new_run.parent is None: continue
            old_run = self.get_run(new_run.parent)
            if old_run.status != 'ended' or old_run.superseded_by: continue
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
        if current is None or ref.version > current.version: self._latest_artifact_by_id[ref.artifact_id] = ref

    def inputs_for(self, ref: OperationRunRef) -> list[ArtifactRef]:
        run = self.get_run(ref)
        self._bind_required_artifact_inputs(run)
        refs = {input_ref.artifact_id: input_ref for input_ref in run.input_refs}
        for requirement in run.spec.required_artifact_sets:
            for parent in self._parents_matching_requirement(run, requirement):
                for artifact_ref in parent.output_refs:
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
        if OperationRunRef(operation_id) not in self._runs: return operation_id
        suffix = 2
        while OperationRunRef(f'{operation_id}#{suffix}') in self._runs:
            suffix += 1
        return f'{operation_id}#{suffix}'

    def _next_attempt(self, operation_id: str) -> int:
        return 1 + sum(run.spec.operation_id == operation_id for run in self._runs.values())

    def _ensure_writer_order(self, candidate: OperationRun) -> None:
        artifact_id = candidate.spec.tags.get('writes_artifact_id')
        if not artifact_id: return
        for ref, run in self._runs.items():
            if run.superseded_by or run.spec.tags.get('writes_artifact_id') != artifact_id: continue
            if candidate.parent == ref: continue
            if candidate.spec.write_policy != 'versioned' or run.spec.write_policy != 'versioned':
                raise ValueError(f'artifact writer already exists for {artifact_id}: {ref} -> {candidate.ref}')
            if run.status == 'ended': continue
            if ref not in candidate.depends_on:
                raise ValueError(f'unordered versioned writer for artifact {artifact_id}: {ref} -> {candidate.ref}')

    def _create_retry_run(self, old_ref: OperationRunRef, replacements: list[tuple[OperationRunRef, OperationRunRef]],
                          *, source_message_id: str | None,
                          spec_override: OperationSpec | None = None) -> OperationRunRef:
        old_run = self.get_run(old_ref)
        replaceable_ids = self._replaceable_retry_artifact_ids(old_run, replacements)
        spec = spec_override or old_run.spec
        if replaceable_ids: spec = _spec_requiring_artifact_ids(spec, replaceable_ids)
        if spec.operation_id != old_run.spec.operation_id:
            raise ValueError('retry override must keep the original operation_id')
        by_old = dict(replacements)
        depends_on = [by_old.get(dep, dep) for dep in old_run.depends_on]
        input_ids = {ref.artifact_id for ref in old_run.input_refs}
        for old_dep, new_dep in replacements:
            if input_ids & self._writer_artifact_ids(self.get_run(old_dep)) and new_dep not in depends_on:
                depends_on.append(new_dep)
        writes_artifact_id = spec.tags.get('writes_artifact_id')
        if writes_artifact_id:
            deps = set(depends_on)
            depends_on += [ref for ref, run in self._runs.items()
                           if not run.superseded_by and run.status != 'ended' and ref not in deps
                           and run.spec.tags.get('writes_artifact_id') == writes_artifact_id]
        inputs = [ref for ref in old_run.input_refs if ref.artifact_id not in replaceable_ids]
        return self.create_run(spec, inputs=inputs, depends_on=depends_on, parent=old_ref,
                               source_message_id=source_message_id or old_run.source_message_id)

    def _replaceable_retry_artifact_ids(self, old_run: OperationRun,
                                        replacements: list[tuple[OperationRunRef, OperationRunRef]]) -> set[str]:
        input_ids = {ref.artifact_id for ref in old_run.input_refs}
        replaced_ids: set[str] = set()
        for old_dep, new_dep in replacements:
            dependency_edge = old_dep in old_run.depends_on or new_dep in old_run.depends_on
            old_writer_ids = self._writer_artifact_ids(self.get_run(old_dep))
            if not dependency_edge and not input_ids & old_writer_ids: continue
            replaced_ids |= old_writer_ids | self._writer_artifact_ids(self.get_run(new_dep))
        return replaced_ids & input_ids

    def _bind_required_artifact_inputs(self, run: OperationRun, *, emit: bool = True,
                                       allow_latest: bool = False) -> None:
        before = self._snapshot(run) if emit else None
        refs = _merge_input_refs(run.input_refs, run.spec.required_artifact_refs)
        bound_ids = {ref.artifact_id for ref in refs}
        for artifact_id in run.spec.required_artifact_ids:
            if artifact_id in bound_ids: continue
            ref = self._required_artifact_ref(run, artifact_id, allow_latest=allow_latest)
            if ref is None: continue
            refs.append(ref)
            bound_ids.add(artifact_id)
        run.input_refs = _merge_input_refs(refs, run.spec.required_artifact_refs)
        if emit and before and before.input_refs != [str(ref) for ref in run.input_refs]:
            self._emit(OperationRunChange('inputs_bound', before, self._snapshot(run)))

    def _required_artifact_ref(self, run: OperationRun, artifact_id: str, *, allow_latest: bool) -> ArtifactRef | None:
        dependency_ref = self._dependency_output_ref(run, artifact_id)
        if dependency_ref is not None: return dependency_ref
        if self._has_dependency_writer(run, artifact_id) or not allow_latest: return None
        return self._latest_artifact_by_id.get(artifact_id)

    def _dependency_output_ref(self, run: OperationRun, artifact_id: str) -> ArtifactRef | None:
        refs = [output for parent in self._resolved_dependency_runs(run) for output in parent.output_refs
                if output.artifact_id == artifact_id]
        return max(refs, key=lambda item: item.version) if refs else None

    def _has_dependency_writer(self, run: OperationRun, artifact_id: str) -> bool:
        return any(parent.spec.tags.get('writes_artifact_id') == artifact_id
                   for parent in self._resolved_dependency_runs(run))

    def _refresh_inputs_for_replaced_dependency(self, run: OperationRun, old_parent: OperationRunRef,
                                                new_parent: OperationRunRef, *, add_dependency: bool = False) -> None:
        writer_ids = (self._writer_artifact_ids(self.get_run(old_parent))
                      | self._writer_artifact_ids(self.get_run(new_parent)))
        replaceable_ids = writer_ids & {ref.artifact_id for ref in run.input_refs}
        if not replaceable_ids: return
        before = self._snapshot(run)
        run.spec = _spec_requiring_artifact_ids(run.spec, replaceable_ids)
        run.input_refs = [ref for ref in run.input_refs if ref.artifact_id not in replaceable_ids]
        if add_dependency and new_parent not in run.depends_on:
            run.depends_on.append(new_parent)
            try:
                self._topological_run_refs()
            except Exception:
                run.depends_on = [dep for dep in run.depends_on if dep != new_parent]
                run.input_refs = [ArtifactRef.parse(value) for value in before.input_refs]
                required = [ArtifactRef.parse(value) for value in before.required_artifact_refs]
                run.spec = replace(run.spec, required_artifact_refs=required,
                                   required_artifact_ids=list(before.required_artifact_ids))
                raise
        self._bind_required_artifact_inputs(run, emit=False)
        after = self._snapshot(run)
        if before.depends_on != after.depends_on:
            self._emit(OperationRunChange('dependencies_updated', before, after))
        elif (before.input_refs != after.input_refs or before.required_artifact_refs != after.required_artifact_refs
                or before.required_artifact_ids != after.required_artifact_ids):
            self._emit(OperationRunChange('inputs_bound', before, after))

    @staticmethod
    def _writer_artifact_ids(run: OperationRun) -> set[str]:
        ids = {output.artifact_id for output in run.output_refs}
        artifact_id = run.spec.tags.get('writes_artifact_id')
        if artifact_id: ids.add(artifact_id)
        return ids

    def _resolved_dependency_runs(self, run: OperationRun) -> list[OperationRun]:
        parents: list[OperationRun] = []
        for parent_ref in run.depends_on:
            parent = self.get_run(parent_ref)
            if parent.superseded_by: parent = self.get_run(parent.superseded_by)
            parents.append(parent)
        return parents

    def _active_downstream_refs(self, ref: OperationRunRef) -> list[OperationRunRef]:
        target_outputs = {output.artifact_id for output in self.get_run(ref).output_refs}
        downstream: set[OperationRunRef] = set()
        queue = deque([ref])
        while queue:
            current = queue.popleft()
            for candidate_ref in self._topological_run_refs():
                if candidate_ref == ref or candidate_ref in downstream: continue
                candidate = self.get_run(candidate_ref)
                if candidate.superseded_by: continue
                if not self._depends_on_or_uses(candidate, current, target_outputs): continue
                downstream.add(candidate_ref)
                queue.append(candidate_ref)
        return self._topological_run_refs(downstream)

    def _depends_on_or_uses(self, run: OperationRun, parent_ref: OperationRunRef, parent_output_ids: set[str]) -> bool:
        if parent_ref in run.depends_on: return True
        if parent_output_ids and any(ref.artifact_id in parent_output_ids for ref in run.input_refs): return True
        return bool(parent_output_ids & set(run.spec.required_artifact_ids))

    def _dependencies_satisfied(self, run: OperationRun) -> bool:
        return all(self.get_run(dep).status == 'ended' and self.get_run(dep).outcome in _DONE_OUTCOMES
                   for dep in run.depends_on)

    def _blocker(self, ref: OperationRunRef) -> ScheduleBlocker | None:
        run = self.get_run(ref)
        dependency_blockers = [
            str(dep) for dep in run.depends_on
            if self.get_run(dep).status != 'ended' or self.get_run(dep).outcome not in _DONE_OUTCOMES]
        if dependency_blockers:
            return ScheduleBlocker(str(ref), 'dependency_not_satisfied', depends_on=dependency_blockers)
        missing_artifacts = self._missing_required_artifact_ids(run)
        missing_sets = [requirement.name for requirement in run.spec.required_artifact_sets
                        if not self._artifact_set_available(run, requirement)]
        if missing_artifacts or missing_sets:
            return ScheduleBlocker(str(ref), 'missing_artifact', missing_artifact_ids=missing_artifacts,
                                   missing_artifact_sets=missing_sets)
        return None

    def _required_artifacts_available(self, run: OperationRun) -> bool:
        return not self._missing_required_artifact_ids(run) and all(
            self._artifact_set_available(run, requirement) for requirement in run.spec.required_artifact_sets)

    def _missing_required_artifact_ids(self, run: OperationRun) -> list[str]:
        explicit = {ref.artifact_id for ref in run.input_refs}
        return [artifact_id for artifact_id in run.spec.required_artifact_ids
                if artifact_id not in explicit and self._dependency_output_ref(run, artifact_id) is None]

    def _artifact_set_available(self, run: OperationRun, requirement: ArtifactSetRequirement) -> bool:
        parents = self._parents_matching_requirement(run, requirement)
        outputs = [output for parent in parents for output in parent.output_refs]
        return len(outputs) >= requirement.min_count and all(parent.output_refs for parent in parents)

    def _parents_matching_requirement(self, run: OperationRun,
                                      requirement: ArtifactSetRequirement) -> list[OperationRun]:
        return [self.get_run(parent_ref) for parent_ref in run.depends_on
                if _tag_value(self.get_run(parent_ref).spec, requirement.producer_tag) == requirement.producer_value]

    def _snapshot(self, run: OperationRun) -> OperationRunSnapshot:
        return OperationRunSnapshot(
            operation_run_id=str(run.ref), operation_id=run.spec.operation_id,
            operation_type=run.spec.operation_type, status=run.status, attempt=run.attempt,
            category=run.spec.category, flow_tag=run.spec.flow_tag, stage_tag=run.spec.stage_tag,
            input_refs=[str(ref) for ref in run.input_refs], output_refs=[str(ref) for ref in run.output_refs],
            depends_on=[str(ref) for ref in run.depends_on], parent=str(run.parent or ''),
            source_message_id=run.source_message_id or '', superseded_by=str(run.superseded_by or ''),
            supersede_reason=run.supersede_reason, outcome=run.outcome, tags=dict(run.spec.tags),
            params=dict(run.spec.params),
            required_artifact_refs=[str(ref) for ref in run.spec.required_artifact_refs],
            required_artifact_ids=list(run.spec.required_artifact_ids),
            required_artifact_sets=[dict(name=item.name, producer_tag=item.producer_tag,
                                         producer_value=item.producer_value, min_count=item.min_count)
                                    for item in run.spec.required_artifact_sets],
            write_policy=run.spec.write_policy,
        )

    def _emit(self, change: OperationRunChange) -> None:
        for observer in self._observers:
            observer.on_operation_run_change(change)

    def _topological_specs(self, specs: list[OperationSpec]) -> list[OperationSpec]:
        by_id = {spec.operation_id: spec for spec in specs}
        ordered = _topological_order(list(by_id), lambda operation_id: by_id[operation_id].depends_on)
        if len(ordered) != len(specs): raise ValueError('operation graph must be a DAG')
        return [by_id[operation_id] for operation_id in ordered]

    def _topological_run_refs(self, subset: set[OperationRunRef] | None = None) -> list[OperationRunRef]:
        selected = list(self._runs) if subset is None else [ref for ref in self._runs if ref in subset]
        ordered = _topological_order(selected, lambda ref: self._runs[ref].depends_on)
        if len(ordered) != len(selected): raise ValueError('operation runs must be a DAG')
        return ordered

    def _dependency_depth(self, ref: OperationRunRef, memo: dict[OperationRunRef, int]) -> int:
        if ref in memo: return memo[ref]
        run = self.get_run(ref)
        memo[ref] = 0 if not run.depends_on else 1 + max(self._dependency_depth(dep, memo) for dep in run.depends_on)
        return memo[ref]


def _topological_order(nodes: list, parents_of: Callable[[Any], Iterable]) -> list:
    indegree = {node: 0 for node in nodes}
    children: dict[Any, list] = {node: [] for node in nodes}
    for node in nodes:
        for parent in parents_of(node):
            if parent not in indegree: continue
            indegree[node] += 1
            children[parent].append(node)
    queue = deque(node for node, count in indegree.items() if count == 0)
    ordered = []
    while queue:
        node = queue.popleft()
        ordered.append(node)
        for child in children[node]:
            indegree[child] -= 1
            if indegree[child] == 0: queue.append(child)
    return ordered


def _tag_value(spec: OperationSpec, key: str) -> str | None:
    if key == 'category': return spec.category
    if key == 'flow': return spec.flow_tag
    if key == 'stage': return spec.stage_tag
    return spec.tags.get(key)


def _run_from_snapshot(snapshot: OperationRunSnapshot) -> OperationRun:
    return OperationRun(
        ref=OperationRunRef(snapshot.operation_run_id),
        spec=OperationSpec(
            operation_id=snapshot.operation_id, operation_type=snapshot.operation_type,
            category=snapshot.category, flow_tag=snapshot.flow_tag, stage_tag=snapshot.stage_tag,
            required_artifact_refs=[ArtifactRef.parse(value) for value in snapshot.required_artifact_refs],
            required_artifact_ids=list(snapshot.required_artifact_ids),
            required_artifact_sets=[ArtifactSetRequirement(**item) for item in snapshot.required_artifact_sets],
            write_policy=snapshot.write_policy, tags=dict(snapshot.tags), params=dict(snapshot.params),
        ),
        status=snapshot.status,
        attempt=snapshot.attempt,
        parent=OperationRunRef(snapshot.parent) if snapshot.parent else None,
        source_message_id=snapshot.source_message_id or None,
        input_refs=_merge_input_refs(
            [ArtifactRef.parse(value) for value in snapshot.input_refs],
            [ArtifactRef.parse(value) for value in snapshot.required_artifact_refs],
        ),
        output_refs=[ArtifactRef.parse(value) for value in snapshot.output_refs],
        depends_on=[OperationRunRef(value) for value in snapshot.depends_on],
        superseded_by=OperationRunRef(snapshot.superseded_by) if snapshot.superseded_by else None,
        supersede_reason=snapshot.supersede_reason,
        outcome=snapshot.outcome,
    )


def _merge_input_refs(refs: list[ArtifactRef], pinned_refs: list[ArtifactRef]) -> list[ArtifactRef]:
    merged: dict[str, ArtifactRef] = {}
    for ref in [*pinned_refs, *refs]:
        merged.setdefault(ref.artifact_id, ref)
    return list(merged.values())


def _spec_with_required_inputs(spec: OperationSpec, inputs: list[ArtifactRef]) -> OperationSpec:
    refs = _merge_input_refs(inputs, spec.required_artifact_refs)
    return spec if refs == spec.required_artifact_refs else replace(spec, required_artifact_refs=refs)


def _spec_requiring_artifact_ids(spec: OperationSpec, artifact_ids: set[str]) -> OperationSpec:
    required_refs = [ref for ref in spec.required_artifact_refs if ref.artifact_id not in artifact_ids]
    required_ids = list(spec.required_artifact_ids)
    required_ids += [artifact_id for artifact_id in sorted(artifact_ids) if artifact_id not in set(required_ids)]
    return replace(spec, required_artifact_refs=required_refs, required_artifact_ids=required_ids)


def _replace_refs_by_artifact_id(refs: list[ArtifactRef], replacements: dict[str, ArtifactRef]) -> list[ArtifactRef]:
    return [replacements.get(ref.artifact_id, ref) for ref in refs]


def _changed_replacements(refs: list[ArtifactRef], replacements: dict[str, ArtifactRef]) -> dict[str, dict[str, str]]:
    changes: dict[str, dict[str, str]] = {}
    for ref in refs:
        replacement = replacements.get(ref.artifact_id)
        if replacement is None or replacement == ref: continue
        changes[ref.artifact_id] = {'artifact_id': ref.artifact_id, 'old_ref': str(ref), 'new_ref': str(replacement)}
    return changes


def _param_artifact_refs(value) -> list[ArtifactRef]:
    if isinstance(value, str):
        try:
            return [ArtifactRef.parse(value)]
        except ValueError:
            return []
    if isinstance(value, list): return [ref for item in value for ref in _param_artifact_refs(item)]
    if isinstance(value, dict): return [ref for item in value.values() for ref in _param_artifact_refs(item)]
    return []


def _replace_param_refs(value, changes: dict[str, dict[str, str]]):
    if not changes: return value
    by_old = {change['old_ref']: change['new_ref'] for change in changes.values()}
    if isinstance(value, str): return by_old.get(value, value)
    if isinstance(value, list): return [_replace_param_refs(item, changes) for item in value]
    if isinstance(value, dict): return {key: _replace_param_refs(item, changes) for key, item in value.items()}
    return value
