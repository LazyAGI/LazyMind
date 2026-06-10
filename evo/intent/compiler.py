from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field

from ..artifacts import ArtifactRef
from .models import AtomicIntent, IntentPlan, IntentRequest, ValidationIssue

MUTATION_KINDS = {'artifact_change', 'flow_control', 'config_change'}
ARTIFACT_QUERY_CAPABILITIES = {
    'read_artifact_query', 'read_repair_artifact', 'read_coarse_artifact_query', 'read_fine_artifact_query'}
FIXED_CAPABILITY_WRITES = {
    'load_corpus': 'corpus_load_report', 'build_corpus_snapshot': 'corpus_snapshot',
    'assemble_dataset': 'eval_dataset', 'assemble_classification_report': 'classification_report',
    'compare_abtest_result': 'abtest_comparison', 'cutover_candidate_algorithm': 'candidate_algorithm_cutover',
    'build_repair_loop_plan': 'repair_loop_plan',
}


@dataclass(frozen=True)
class CompiledIntent:
    intent: AtomicIntent
    plan: IntentPlan | None
    operation_dependencies: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class IntentCompilation:
    ordered: list[CompiledIntent]
    issues: list[ValidationIssue]

    @property
    def plans(self) -> list[IntentPlan]:
        return [item.plan for item in self.ordered if item.plan is not None]


def compile_intents(request: IntentRequest, intents: list[AtomicIntent]) -> IntentCompilation:
    issues = _dag_issues(intents)
    if issues: return IntentCompilation([], issues)
    produced_by_intent: dict[str, str] = {}
    compiled: list[CompiledIntent] = []
    for intent in _topological_intents(intents):
        operation_deps = [dep for dep in intent.depends_on
                          if _by_id(intents, dep).kind in MUTATION_KINDS | {'query', 'chat'}]
        if intent.kind == 'query':
            capability_id = _query_capability_id(intent)
            required = (_query_artifact_ids(intent)
                        if capability_id in ARTIFACT_QUERY_CAPABILITIES and operation_deps else [])
            plan = IntentPlan(capability_id, _operation_id(intent),
                              {**_query_params(intent), 'query_intent_id': intent.intent_id,
                               'source_message_id': request.message_id},
                              input_refs=[] if operation_deps else _input_refs(intent),
                              required_artifact_ids=required, source_message_id=request.message_id)
        elif intent.kind == 'chat':
            plan = IntentPlan(str(intent.target.get('capability_id') or 'respond_to_user'), _operation_id(intent),
                              {**intent.params, 'query_intent_id': intent.intent_id,
                               'source_message_id': request.message_id},
                              source_message_id=request.message_id, depends_on=[])
        elif intent.kind in MUTATION_KINDS:
            artifact_id = _artifact_id(intent)
            input_refs = _input_refs(intent)
            late_ids = [ref.artifact_id for ref in input_refs
                        if any(produced_by_intent.get(dep) == ref.artifact_id for dep in operation_deps)]
            if not late_ids and artifact_id and any(
                    produced_by_intent.get(dep) == artifact_id for dep in operation_deps):
                late_ids = [artifact_id]
            required = late_ids or ([] if input_refs or not artifact_id else [artifact_id])
            plan = IntentPlan(intent.target['capability_id'], _operation_id(intent),
                              {**intent.params, 'source_message_id': request.message_id},
                              input_refs=[] if late_ids else input_refs,
                              required_artifact_ids=required, source_message_id=request.message_id)
            produced_by_intent[intent.intent_id] = _write_artifact_id(intent) or artifact_id
            compiled.append(CompiledIntent(intent, plan, operation_deps))
            continue
        else:
            compiled.append(CompiledIntent(intent, None))
            continue
        produced_by_intent[intent.intent_id] = ''
        compiled.append(CompiledIntent(intent, plan, operation_deps))
    return IntentCompilation(compiled, [])


def _dag_issues(intents: list[AtomicIntent]) -> list[ValidationIssue]:
    issues: list[ValidationIssue] = []
    ids = [intent.intent_id for intent in intents]
    known: set[str] = set()
    for intent_id in ids:
        if intent_id in known:
            issues.append(_issue(intent_id, 'duplicate_intent_id', f'duplicate intent_id: {intent_id}'))
        known.add(intent_id)
    for intent in intents:
        for dep in intent.depends_on:
            if dep == intent.intent_id:
                issues.append(_issue(intent.intent_id, 'self_dependency',
                                     f'intent cannot depend on itself: {intent.intent_id}'))
            elif dep not in known:
                issues.append(_issue(intent.intent_id, 'unknown_dependency', f'unknown intent dependency: {dep}'))
            elif intent.kind in MUTATION_KINDS and _by_id(intents, dep).kind == 'unsupported':
                issues.append(_issue(intent.intent_id, 'unsupported_dependency',
                                     f'mutation depends on unsupported intent: {dep}'))
    if not issues:
        try:
            _topological_intents(intents)
        except ValueError:
            issues.append(_issue('', 'cyclic_dependency', 'intent dependencies must be a DAG'))
    return issues


def _topological_intents(intents: list[AtomicIntent]) -> list[AtomicIntent]:
    by_id = {intent.intent_id: intent for intent in intents}
    indegree = {intent_id: 0 for intent_id in by_id}
    children: dict[str, list[str]] = {intent_id: [] for intent_id in by_id}
    for intent in intents:
        for dep in intent.depends_on:
            if dep not in by_id: continue
            indegree[intent.intent_id] += 1
            children[dep].append(intent.intent_id)
    queue = deque(intent.intent_id for intent in intents if indegree[intent.intent_id] == 0)
    ordered: list[AtomicIntent] = []
    while queue:
        intent_id = queue.popleft()
        ordered.append(by_id[intent_id])
        for child in children[intent_id]:
            indegree[child] -= 1
            if indegree[child] == 0: queue.append(child)
    if len(ordered) != len(intents): raise ValueError('intent dependencies must be a DAG')
    return ordered


def _by_id(intents: list[AtomicIntent], intent_id: str) -> AtomicIntent:
    return next(intent for intent in intents if intent.intent_id == intent_id)


def _query_capability_id(intent: AtomicIntent) -> str:
    if intent.target.get('capability_id'): return str(intent.target['capability_id'])
    if intent.target.get('operation_run_id'): return 'read_operation_query'
    if intent.target.get('run_id') or intent.target.get('run_status'): return 'read_run_status_query'
    return 'read_artifact_query'


def _query_params(intent: AtomicIntent) -> dict:
    capability_id = _query_capability_id(intent)
    if capability_id == 'read_operation_query':
        return {'operation_run_id': intent.target.get('operation_run_id', '')}
    if capability_id == 'read_run_status_query': return {'run_id': intent.target.get('run_id', '')}
    if capability_id in ARTIFACT_QUERY_CAPABILITIES:
        ref = _artifact_ref(intent)
        return {'artifact_ref': str(ref)} if ref is not None else {}
    return {}


def _operation_id(intent: AtomicIntent) -> str:
    return intent.target.get('operation_id') or f'intent.{intent.action}.{intent.intent_id}'


def _input_refs(intent: AtomicIntent) -> list[ArtifactRef]:
    if intent.target.get('artifact_missing'): return []
    raw_refs = intent.target.get('input_refs')
    if raw_refs:
        return [ref if isinstance(ref, ArtifactRef) else ArtifactRef.parse(ref) for ref in raw_refs]
    ref = _artifact_ref(intent)
    return [] if ref is None else [ref]


def _artifact_ref(intent: AtomicIntent) -> ArtifactRef | None:
    value = intent.target.get('artifact_ref')
    if value:
        if isinstance(value, ArtifactRef): return value
        return ArtifactRef.parse(value) if '@v' in str(value) else None
    artifact_id = intent.target.get('artifact_id') or intent.params.get('case_id')
    version = intent.target.get('version')
    return ArtifactRef(str(artifact_id), int(version)) if artifact_id and version else None


def _artifact_id(intent: AtomicIntent) -> str:
    value = intent.target.get('artifact_ref')
    if value and not isinstance(value, ArtifactRef) and '@v' not in str(value): return str(value)
    ref = _artifact_ref(intent)
    return ref.artifact_id if ref else str(intent.target.get('artifact_id') or intent.params.get('case_id') or '')


def _write_artifact_id(intent: AtomicIntent) -> str:
    params = intent.params
    capability_id = str(intent.target.get('capability_id') or '')
    if params.get('output_id'): return str(params['output_id'])
    if capability_id == 'prepare_dataset_case' and params.get('output_case_id'):
        return f"case_preparation_{params['output_case_id']}"
    if capability_id == 'generate_dataset_case': return _dataset_case_id(params)
    return FIXED_CAPABILITY_WRITES.get(capability_id, '')


def _dataset_case_id(params: dict) -> str:
    if params.get('case_id'): return str(params['case_id'])
    artifact_id = str(params.get('case_preparation_ref') or '').split('@', 1)[0]
    return artifact_id.removeprefix('case_preparation_') if artifact_id.startswith('case_preparation_') else ''


def _query_artifact_ids(intent: AtomicIntent) -> list[str]:
    raw_ids = intent.target.get('artifact_ids')
    if raw_ids: return [str(item) for item in raw_ids]
    artifact_id = _artifact_id(intent)
    return [artifact_id] if artifact_id else []


def _issue(intent_id: str, code: str, message: str) -> ValidationIssue:
    return ValidationIssue(code, intent_id, 'reject', message)
