from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Literal

from ..artifacts import ArtifactRef
from ..internal_ids import stage_group
from .graph import OperationGraph
from .models import OperationRunRef

FlowStage = Literal['dataset', 'eval', 'analysis', 'repair', 'abtest']

STAGE_ORDER: tuple[FlowStage, ...] = ('dataset', 'eval', 'analysis', 'repair', 'abtest')


@dataclass(frozen=True)
class StageDefinition:
    stage: FlowStage
    output_artifact_id: str
    input_artifact_id: str = ''
    freshness_field: str = ''
    required_before: tuple[str, ...] = ()


@dataclass(frozen=True)
class EnsureStagePlan:
    missing_artifact_ids: tuple[str, ...] = ()
    stale_stages: tuple[str, ...] = ()
    blocked_reason: str = ''

    @property
    def blocked(self) -> bool:
        return bool(self.blocked_reason)


class FlowGraphDefinition:
    """Canonical evo stage graph and freshness rules."""

    def __init__(self) -> None:
        self._stages: dict[FlowStage, StageDefinition] = {
            'dataset': StageDefinition('dataset', 'eval_dataset'),
            'eval': StageDefinition('eval', 'eval_report', 'eval_dataset', 'eval_dataset_ref',
                                    ('eval_dataset',)),
            'analysis': StageDefinition('analysis', 'classification_report', 'eval_report', 'eval_report_ref',
                                        ('eval_report',)),
            'repair': StageDefinition('repair', 'repair_loop_plan', 'classification_report',
                                      'classification_report_ref', ('classification_report',)),
            'abtest': StageDefinition('abtest', 'abtest_comparison', 'candidate_eval_report', '',
                                      ('eval_report',)),
        }

    @property
    def stages(self) -> tuple[FlowStage, ...]:
        return STAGE_ORDER

    def normalize_stage(self, value: str) -> FlowStage | None:
        stage = stage_group(value)
        return stage if stage in self._stages else None

    def definition(self, stage: str) -> StageDefinition:
        normalized = self.normalize_stage(stage)
        if normalized is None: raise ValueError(f'unknown evo stage: {stage}')
        return self._stages[normalized]

    def downstream(self, stage: str) -> tuple[FlowStage, ...]:
        normalized = self.definition(stage).stage
        index = STAGE_ORDER.index(normalized)
        return STAGE_ORDER[index + 1:]

    def predecessors(self, stage: str) -> tuple[FlowStage, ...]:
        normalized = self.definition(stage).stage
        return STAGE_ORDER[:STAGE_ORDER.index(normalized)]

    def stage_span(self, start_stage: str, target_stage: str) -> tuple[FlowStage, ...]:
        start = STAGE_ORDER.index(self.definition(start_stage).stage)
        target = STAGE_ORDER.index(self.definition(target_stage).stage)
        return STAGE_ORDER[start:target + 1]

    def missing_prerequisites(self, artifacts: Any, stage: str) -> tuple[str, ...]:
        definition = self.definition(stage)
        return tuple(artifact_id for artifact_id in definition.required_before
                     if _latest_ref(artifacts, artifact_id) is None)

    def is_stage_fresh(self, artifacts: Any, stage: str) -> bool:
        definition = self.definition(stage)
        output_ref = _latest_ref(artifacts, definition.output_artifact_id)
        if output_ref is None: return False
        if definition.stage == 'dataset': return True
        if definition.stage == 'abtest':
            return _abtest_fresh(artifacts)
        if not definition.input_artifact_id or not definition.freshness_field:
            return True
        input_ref = _latest_ref(artifacts, definition.input_artifact_id)
        if input_ref is None: return False
        payload = artifacts.get(output_ref)
        return str(payload.get(definition.freshness_field) or '') == str(input_ref)

    def is_candidate_eval_fresh(self, artifacts: Any, eval_dataset_ref: ArtifactRef | str) -> bool:
        try:
            report_ref = artifacts.latest_ref('candidate_eval_report')
            payload = artifacts.get(report_ref)
        except KeyError:
            return False
        if str(payload.get('eval_dataset_ref') or '') != str(eval_dataset_ref):
            return False
        service_ref = _latest_ref(artifacts, 'candidate_service')
        return service_ref is None or service_ref in _artifact_upstream_closure(artifacts, report_ref)

    def is_candidate_service_stopped(self, artifacts: Any) -> bool:
        service_ref = _latest_ref(artifacts, 'candidate_service')
        stop_ref = _latest_ref(artifacts, 'candidate_service_stop')
        if service_ref is None or stop_ref is None: return False
        return str(artifacts.get(stop_ref).get('candidate_service_ref') or '') == str(service_ref)

    def plan_ensure_stage(self, artifacts: Any, stage: str) -> EnsureStagePlan:
        normalized = self.normalize_stage(stage)
        if normalized is None:
            return EnsureStagePlan(blocked_reason='unknown_stage')
        missing = []
        for predecessor in self.predecessors(normalized):
            missing.extend(self.missing_prerequisites(artifacts, predecessor))
            if not self.is_stage_fresh(artifacts, predecessor):
                definition = self.definition(predecessor)
                if _latest_ref(artifacts, definition.output_artifact_id) is None:
                    missing.append(definition.output_artifact_id)
                else:
                    return EnsureStagePlan(stale_stages=self.stage_span(predecessor, normalized))
        missing.extend(self.missing_prerequisites(artifacts, normalized))
        missing = tuple(dict.fromkeys(missing))
        if missing:
            return EnsureStagePlan(missing_artifact_ids=missing, blocked_reason='missing_prerequisite_artifact')
        stale = tuple(item for item in self.stage_span(normalized, normalized)
                      if not self.is_stage_fresh(artifacts, item))
        return EnsureStagePlan(stale_stages=stale)


def downstream_rebuild_roots(graph: OperationGraph, artifacts: Any,
                             changed_refs: list[ArtifactRef]) -> tuple[OperationRunRef, ...]:
    impact = artifacts.impact(changed_refs)
    affected = [ref for ref in graph.affected_runs(impact) if graph.get_run(ref).spec.category != 'intent']
    return _root_stage_refs(graph, affected)


def _root_stage_refs(graph: OperationGraph, refs: list[OperationRunRef]) -> tuple[OperationRunRef, ...]:
    ref_set = set(refs)
    roots = []
    for ref in refs:
        run = graph.get_run(ref)
        if not any(parent in ref_set for parent in run.depends_on):
            roots.append(ref)
    return tuple(roots or refs[:1])


def _latest_ref(artifacts: Any, artifact_id: str) -> ArtifactRef | None:
    try:
        return artifacts.latest_ref(artifact_id)
    except KeyError:
        return None


def _abtest_fresh(artifacts: Any) -> bool:
    comparison_ref = _latest_ref(artifacts, 'abtest_comparison')
    baseline_ref = _latest_ref(artifacts, 'eval_report')
    candidate_ref = _latest_ref(artifacts, 'candidate_eval_report')
    if comparison_ref is None or baseline_ref is None or candidate_ref is None: return False
    payload = artifacts.get(comparison_ref)
    return (str(payload.get('baseline_eval_report_ref') or '') == str(baseline_ref)
            and str(payload.get('candidate_eval_report_ref') or '') == str(candidate_ref))


def _artifact_upstream_closure(artifacts: Any, ref: ArtifactRef) -> set[ArtifactRef]:
    seen, stack = set(), list(artifacts.upstream(ref))
    while stack:
        current = stack.pop()
        if current in seen: continue
        seen.add(current)
        stack.extend(artifacts.upstream(current))
    return seen
