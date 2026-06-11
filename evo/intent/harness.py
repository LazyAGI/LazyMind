from __future__ import annotations

from dataclasses import asdict, dataclass, replace
from typing import Any

from ..artifacts import ArtifactDraft, ArtifactRef
from ..checkpoints import CheckpointManager, CheckpointRef
from ..operations import OperationGraph, OperationRunRef, OperationSpec
from ..store import Event, EvoStore
from .compiler import (
    MUTATION_KINDS, CompiledIntent, _artifact_id, _artifact_ref, _dataset_case_id, _query_capability_id,
    compile_intents,
)
from .models import (
    AtomicIntent, CapabilitySpec, IntentDecisionAction, IntentHarnessResult, IntentKind, IntentParser, IntentPlan,
    IntentRequest, OperationProposal, ValidationIssue, validate_params,
)

MISSING_TARGET_PROPOSAL_CAPABILITIES = {
    'fine_classify_case', 'build_repair_loop_plan', 'start_repair_loop', 'continue_repair_loop'}
FIXED_WRITES = {
    'CorpusLoadReport': 'corpus_load_report', 'CorpusSnapshot': 'corpus_snapshot', 'EvalDataset': 'eval_dataset',
    'ClassificationReport': 'classification_report', 'ABTestComparison': 'abtest_comparison',
    'RepairLoopPlan': 'repair_loop_plan',
}
_VIEW_KEYS = (
    'capability_id', 'creates_operation_type', 'title', 'description', 'use_when', 'avoid_when', 'intent_use_when',
    'intent_avoid_when', 'task_type', 'semantic_schema', 'effects', 'target_artifact_schemas',
    'writable_artifact_schema', 'params_schema', 'examples', 'risk_level', 'confirmation_policy', 'batch_policy',
    'cross_stage_policy',
)
_PREDICATE_OPS = {'eq', 'ne', 'exists', 'not_exists', 'in', 'not_in'}


class CapabilityRegistry:
    def __init__(self, specs: list[CapabilitySpec]):
        self._specs = {spec.capability_id: spec for spec in specs}

    def get(self, capability_id: str) -> CapabilitySpec:
        if capability_id not in self._specs: raise ValueError(f'unknown capability: {capability_id}')
        return self._specs[capability_id]

    def capability_ids(self) -> list[str]:
        return sorted(self._specs)

    def allowed_for_checkpoint(self, store: EvoStore, run_id: str,
                               checkpoint_ref: CheckpointRef) -> list[CapabilitySpec]:
        data = store.read_json(store.run_dir(run_id) / 'checkpoints' / f'{checkpoint_ref.checkpoint_id}.json')
        return [self.get(capability_id) for capability_id in data.get('allowed_capabilities', [])]

    def planning_context(self, store: EvoStore, run_id: str, checkpoint_ref: CheckpointRef) -> list[dict]:
        return [_view(spec, False) for spec in self.allowed_for_checkpoint(store, run_id, checkpoint_ref)]

    def execution_context(self, store: EvoStore, run_id: str, checkpoint_ref: CheckpointRef) -> list[dict]:
        return [_view(spec, True) for spec in self.allowed_for_checkpoint(store, run_id, checkpoint_ref)]

    def validate(self, store: EvoStore, run_id: str, checkpoint_ref: CheckpointRef, plan: IntentPlan) -> CapabilitySpec:
        allowed_ids = {spec.capability_id for spec in self.allowed_for_checkpoint(store, run_id, checkpoint_ref)}
        if plan.capability_id not in allowed_ids:
            raise PermissionError(f'capability not allowed at checkpoint {checkpoint_ref}: {plan.capability_id}')
        spec = self.get(plan.capability_id)
        if spec.target_artifact_schemas:
            graph = store.artifact_graph(run_id)
            for ref in plan.input_refs:
                _check_target_schema(spec, graph.schema_name(ref))
            for artifact_id in plan.required_artifact_ids:
                _check_target_schema(spec, graph.schema_name(graph.latest_ref(artifact_id)))
        return spec


class IntentOperationFactory:
    def __init__(self, *, store: EvoStore, operation_graph: OperationGraph, capability_registry: CapabilityRegistry,
                 checkpoint_manager: CheckpointManager | None = None,):
        self.store = store
        self.operation_graph = operation_graph
        self.capability_registry = capability_registry
        self.checkpoint_manager = checkpoint_manager

    def create_operation(self, run_id: str, checkpoint_ref: CheckpointRef, plan: IntentPlan) -> OperationProposal:
        capability = self.capability_registry.validate(self.store, run_id, checkpoint_ref, plan)
        operation_type = capability.creates_operation_type
        if not operation_type and capability.task_type == 'control_task': operation_type = 'RuntimeControlOperation'
        if plan.params.get('operation_type') and plan.params['operation_type'] != operation_type:
            raise PermissionError('intent plan cannot override capability operation_type')
        writes_artifact_id = _writes_artifact_id(capability, plan, use_required=True)
        template = _operation_template(capability)
        tags = {'capability_id': capability.capability_id}
        if writes_artifact_id: tags['writes_artifact_id'] = writes_artifact_id
        required_ids = [*template.get('required_artifact_ids', []), *plan.required_artifact_ids]
        ref = self.operation_graph.create_run(
            OperationSpec(
                operation_id=plan.operation_id, operation_type=operation_type,
                category=str(template.get('category') or 'intent'),
                flow_tag=str(template.get('flow_tag') or 'intent'),
                stage_tag=str(template.get('stage_tag') or capability.capability_id),
                depends_on=[str(item) for item in template.get('depends_on', [])],
                required_artifact_refs=list(plan.input_refs),
                required_artifact_ids=list(dict.fromkeys(str(item) for item in required_ids if str(item))),
                # Intent mutations rewrite existing artifacts as new versions by design.
                write_policy='versioned' if writes_artifact_id else 'single',
                tags={**tags, **dict(template.get('tags') or {})},
                params={**plan.params, 'capability_id': capability.capability_id},
            ),
            inputs=list(plan.input_refs), depends_on=list(plan.depends_on), parent=plan.parent,
            source_message_id=plan.source_message_id,
        )
        if not (capability.confirmation_policy == 'required' or capability.risk_level == 'high'
                or capability.cross_stage_policy == 'allowed_with_runtime_confirmation'):
            return OperationProposal(ref)
        if self.checkpoint_manager is None: raise RuntimeError('confirmation policy requires CheckpointManager')
        confirmation = self.checkpoint_manager.create_checkpoint(
            run_id, None, f'confirm {ref}', allowed_capabilities=[capability.capability_id],
            next_operations=[OperationRunRef(str(ref))],
        )
        return OperationProposal(ref, requires_confirmation=True, confirmation_checkpoint_id=confirmation.checkpoint_id)


@dataclass(frozen=True)
class IntentHarness:
    store: EvoStore
    run_id: str
    checkpoint_ref: CheckpointRef
    parser: IntentParser
    capability_registry: CapabilityRegistry
    operation_factory: IntentOperationFactory
    min_confidence: float = 0.6

    def handle(self, request: IntentRequest) -> IntentHarnessResult:
        self._emit('intent.received', request, {'message': request.message})
        capabilities = self.capability_registry.execution_context(self.store, self.run_id, self.checkpoint_ref)
        try:
            intents = self.parser.parse(request, capabilities)
        except Exception as exc:
            self._emit('intent.parsed', request,
                       {'intents': [], 'error': {'type': exc.__class__.__name__, 'message': str(exc)}})
            issue = _clarify('', 'parse_failed', f'intent parse failed: {exc}')
            return self._finish(request, _issue_result([], [issue]))
        self._emit('intent.parsed', request, {'intents': [asdict(intent) for intent in intents]})
        parser_issues = list(getattr(self.parser, 'issues', []))
        if getattr(self.parser, 'action', '') == 'no_operations':
            return self._finish(request, IntentHarnessResult('no_operations', intents))
        if parser_issues: return self._finish(request, _issue_result(intents, parser_issues))
        result = self._pipeline(request, intents, capabilities)
        if result.issues: return self._finish(request, result)
        return self._finish(request, result, self._commit_conditionals(request, result.intents))

    def resolve_deferred(self, conditional_ref: ArtifactRef) -> IntentHarnessResult:
        graph = self.store.artifact_graph(self.run_id)
        conditional = graph.get(conditional_ref)
        if conditional.get('status') != 'waiting': return IntentHarnessResult('no_operations', [])
        try:
            answer_ref = graph.latest_ref(f"intent_answer_{conditional['predicate']['source_intent_id']}")
            answer = graph.get(answer_ref)
        except (FileNotFoundError, KeyError):
            return IntentHarnessResult('no_operations', [])
        matched, actual = _eval_predicate(answer, conditional['predicate'])
        selected = 'then' if matched else 'else'
        intents = [AtomicIntent(**item) for item in conditional[f'{selected}_intents']]
        request = IntentRequest(conditional['source_message_id'], '', conditional['checkpoint_id'])
        if intents:
            caps = self.capability_registry.execution_context(self.store, self.run_id, self.checkpoint_ref)
            result = self._pipeline(request, intents, caps)
        else:
            result = IntentHarnessResult('no_operations', [])
        payload = {
            **conditional, 'status': 'resolved', 'actual_value': actual, 'matched': matched,
            'selected_branch': selected, 'selected_intent_ids': [intent.intent_id for intent in intents],
            'operation_refs': [str(item.operation_ref) for item in result.proposals],
            'issues': [asdict(issue) for issue in result.issues],
        }
        graph.commit_artifact(ArtifactDraft(
            conditional_ref.artifact_id, 'ConditionalIntent', payload,
            f"intent_harness:{conditional['source_message_id']}", input_refs=[conditional_ref, answer_ref],
            role='audit',
        ))
        return result

    def _pipeline(self, request: IntentRequest, intents: list[AtomicIntent],
                  capabilities: list[dict]) -> IntentHarnessResult:
        intents, issues = self._normalize_intents(intents, capabilities)
        issues = issues or self._precompile_issues(intents, capabilities)
        if issues: return _issue_result(intents, issues)
        compilation = compile_intents(request, intents)
        if compilation.issues: return _issue_result(intents, compilation.issues)
        plan_issues = self._plan_issues(compilation.plans)
        if plan_issues: return _issue_result(intents, plan_issues)
        proposals = self._create_proposals(compilation.ordered)
        action: IntentDecisionAction = 'propose_operations' if proposals else 'no_operations'
        return IntentHarnessResult(action, intents, proposals=proposals)

    def _finish(self, request: IntentRequest, result: IntentHarnessResult,
                conditional_refs: list[ArtifactRef] | None = None) -> IntentHarnessResult:
        self._commit_trace(request, result, conditional_refs)
        self._emit('intent.rejected' if result.action == 'reject' else 'intent.completed', request, {
            'result_action': result.action,
            'operation_refs': [str(proposal.operation_ref) for proposal in result.proposals],
            'issues': [asdict(issue) for issue in result.issues],
        })
        return result

    def _precompile_issues(self, intents: list[AtomicIntent], capabilities: list[dict]) -> list[ValidationIssue]:
        if not intents: return [_clarify('', 'empty_message', 'message did not contain a supported intent')]
        valid_kinds = set(IntentKind.__args__)
        allowed = {capability['capability_id']: capability for capability in capabilities}
        issues: list[ValidationIssue] = []
        writers_by_artifact: dict[str, list[AtomicIntent]] = {}
        for intent in intents:
            iid = intent.intent_id
            if intent.kind not in valid_kinds:
                issues.append(_clarify(iid, 'invalid_intent_kind', f'invalid intent kind: {intent.kind}'))
                continue
            if intent.confidence < self.min_confidence:
                issues.append(_clarify(iid, 'low_confidence', f'low confidence intent: {iid}'))
            if intent.kind == 'unsupported':
                issues.append(_clarify(iid, 'unsupported_intent', f'unsupported intent: {iid}'))
            if intent.kind == 'conditional': issues.extend(_conditional_issues(intent, intents))
            if intent.kind == 'query': issues.extend(self._query_issues(intent, allowed))
            if intent.kind == 'chat': issues.extend(self._chat_issues(intent, allowed))
            if intent.kind in MUTATION_KINDS:
                issues.extend(self._mutation_issues(intent, allowed))
                artifact_id = _artifact_id(intent)
                if artifact_id: writers_by_artifact.setdefault(artifact_id, []).append(intent)
        conditional_ids = {item.intent_id for item in intents if item.kind == 'conditional'}
        issues.extend(_reject(item.intent_id, 'top_level_depends_on_conditional',
                              f'top-level intent cannot depend on conditional intent: {dep}')
                      for item in intents for dep in item.depends_on
                      if item.kind != 'conditional' and dep in conditional_ids)
        for artifact_id, writers in writers_by_artifact.items():
            for index, left in enumerate(writers):
                for right in writers[index + 1:]:
                    if not (_depends_path(left.intent_id, right.intent_id, writers)
                            or _depends_path(right.intent_id, left.intent_id, writers)):
                        issues.append(_clarify(right.intent_id, 'ambiguous_artifact_mutation',
                                               f'multiple unordered mutation intents target artifact: {artifact_id}'))
        return issues

    def _chat_issues(self, intent: AtomicIntent, allowed: dict[str, dict]) -> list[ValidationIssue]:
        capability_id = str(intent.target.get('capability_id') or 'respond_to_user')
        if capability_id not in allowed: return [self._not_allowed(intent, capability_id)]
        return validate_params(intent.intent_id, {**intent.params, 'query_intent_id': intent.intent_id},
                               allowed[capability_id].get('params_schema', {}))

    def _mutation_issues(self, intent: AtomicIntent, allowed: dict[str, dict]) -> list[ValidationIssue]:
        issues: list[ValidationIssue] = []
        iid = intent.intent_id
        if not intent.action:
            issues.append(_clarify(iid, 'missing_action', f'mutation intent missing action: {iid}'))
        capability_id = intent.target.get('capability_id')
        if not capability_id:
            return issues + [_clarify(iid, 'missing_capability', f'mutation intent missing capability_id: {iid}')]
        if capability_id not in allowed: return issues + [self._not_allowed(intent, capability_id)]
        issues.extend(validate_params(iid, intent.params, allowed[capability_id].get('params_schema', {})))
        return issues

    def _query_issues(self, intent: AtomicIntent, allowed: dict[str, dict]) -> list[ValidationIssue]:
        capability_id = _query_capability_id(intent)
        if capability_id not in allowed: return [self._not_allowed(intent, capability_id)]
        params = {'query_intent_id': intent.intent_id}
        if capability_id == 'read_operation_query':
            params['operation_run_id'] = intent.target.get('operation_run_id', '')
        if capability_id == 'read_run_status_query': params['run_id'] = intent.target.get('run_id', '')
        issues = validate_params(intent.intent_id, params, allowed[capability_id].get('params_schema', {}))
        if issues: return issues
        if capability_id == 'read_run_status_query':
            run_id = str(intent.target.get('run_id') or self.run_id)
            if not (self.store.run_dir(run_id) / 'run.json').exists():
                return [_clarify(intent.intent_id, 'unknown_run', f'unknown run target: {run_id}')]
            return []
        operation_id = intent.target.get('operation_run_id')
        if capability_id == 'read_operation_query':
            if not operation_id: return [_missing_query_target(intent)]
            try:
                self.store.read_operation(self.run_id, str(operation_id))
            except FileNotFoundError:
                return [_clarify(intent.intent_id, 'unknown_operation', f'unknown operation target: {operation_id}')]
            return []
        ref = _artifact_ref(intent)
        if ref is not None:
            try:
                self.store.artifact_graph(self.run_id).schema_name(ref)
            except KeyError:
                return [_clarify(intent.intent_id, 'unknown_artifact', f'unknown artifact target: {ref}')]
        if ref is None and not _artifact_id(intent) and not intent.target.get('artifact_ids'):
            return [_missing_query_target(intent)]
        return []

    def _not_allowed(self, intent: AtomicIntent, capability_id: Any) -> ValidationIssue:
        return _reject(intent.intent_id, 'capability_not_allowed',
                       f'capability not allowed at checkpoint {self.checkpoint_ref}: {capability_id}')

    def _normalize_intents(self, intents: list[AtomicIntent],
                           capabilities: list[dict]) -> tuple[list[AtomicIntent], list[ValidationIssue]]:
        intents = _expand_inline_conditionals(intents)
        allowed = {capability['capability_id']: capability for capability in capabilities}
        future = _future_artifacts(intents, allowed)
        issues: list[ValidationIssue] = []
        normalized: list[AtomicIntent] = []
        for intent in intents:
            if intent.kind == 'response': intent = replace(intent, kind='chat')
            target = dict(intent.target)
            if intent.kind == 'query' and not target.get('operation_run_id') and intent.params.get('operation_run_id'):
                target['operation_run_id'] = intent.params['operation_run_id']
            if intent.kind == 'query' and not target.get('run_id') and intent.params.get('run_id'):
                target['run_id'] = intent.params['run_id']
            capability_id = _query_capability_id(replace(intent, target=target))
            capability = allowed.get(capability_id, {})
            operation_type = capability.get('creates_operation_type') or capability.get('operation_type')
            if intent.kind == 'query' and operation_type == 'ReadArtifactQueryOperation':
                require_target = not bool(target.get('operation_run_id'))
                issues.extend(self._normalize_artifact_targets(intent, target, require_target=require_target))
            elif intent.kind == 'query' and capability_id == 'read_run_status_query' and not target.get('run_id'):
                target['run_id'] = self.run_id
            elif intent.kind in MUTATION_KINDS and target.get('capability_id') in allowed:
                capability = allowed[target['capability_id']]
                issues.extend(self._normalize_artifact_targets(
                    intent, target, require_target=_requires_message_target(capability),
                    target_schemas=capability.get('target_artifact_schemas') or [],
                    artifact_ref_param_names=_artifact_ref_param_names(capability),
                    future_artifacts=future.get(intent.intent_id, set()),
                    allow_missing_target=_allow_missing_target(intent, capabilities),
                ))
            normalized.append(self._inherit_active_params(replace(intent, target=target), allowed))
        return normalized, issues

    def _inherit_active_params(self, intent: AtomicIntent, allowed: dict[str, dict]) -> AtomicIntent:
        if intent.kind not in MUTATION_KINDS: return intent
        capability = allowed.get(str(intent.target.get('capability_id') or ''))
        operation_id = str(intent.target.get('operation_id') or '')
        active = self.operation_factory.operation_graph.active_run_for(operation_id) if operation_id else None
        if not capability or active is None: return intent
        spec = self.operation_factory.operation_graph.get_run(active).spec
        keys = set((capability.get('params_schema') or {}).get('properties') or {})
        params = {key: value for key, value in spec.params.items()
                  if key in keys and key not in {'capability_id', 'source_message_id'}}
        return replace(intent, params={**params, **intent.params}) if params else intent

    def _normalize_artifact_targets(self, intent: AtomicIntent, target: dict, *, require_target: bool,
                                    target_schemas: list[str] | None = None,
                                    artifact_ref_param_names: list[str] | None = None,
                                    future_artifacts: set[str] | None = None,
                                    allow_missing_target: bool = False) -> list[ValidationIssue]:
        issues: list[ValidationIssue] = []
        refs: list[ArtifactRef] = []
        if target.get('input_refs'):
            for value in target['input_refs']:
                try:
                    ref = value if isinstance(value, ArtifactRef) else ArtifactRef.parse(value)
                except ValueError:
                    issues.append(_clarify(intent.intent_id, 'invalid_artifact_ref',
                                           f'invalid artifact target: {value}'))
                    continue
                if self._schema_name(ref) is None:
                    issues.append(_unknown_artifact(intent.intent_id, ref))
                else:
                    refs.append(ref)
            target['input_refs'] = [str(ref) for ref in refs]
            return issues
        if target.get('artifact_ids'):
            for artifact_id in target['artifact_ids']:
                ref, issue = self._latest_ref(intent.intent_id, str(artifact_id))
                issues.append(issue) if issue else refs.append(ref)
            if refs: target['input_refs'] = [str(ref) for ref in refs]
            return issues
        ref = _artifact_ref(intent)
        if ref is not None:
            if self._schema_name(ref) is None:
                if not allow_missing_target: return [_unknown_artifact(intent.intent_id, ref)]
                target['artifact_missing'] = True
            target['artifact_ref'] = str(ref)
            return []
        param_refs: list[tuple[ArtifactRef, str]] = []
        future_ref = ''
        target_schema_set = set(target_schemas or [])
        for raw_ref in _param_artifact_refs(intent.params, artifact_ref_param_names or []):
            try:
                ref = ArtifactRef.parse(str(raw_ref))
                schema_name = self.store.artifact_graph(self.run_id).schema_name(ref)
            except (KeyError, ValueError):
                artifact_id = str(raw_ref).split('@', 1)[0]
                if artifact_id in (future_artifacts or set()):
                    future_ref = future_ref or str(raw_ref)
                    continue
                if allow_missing_target:
                    target['artifact_ref'] = str(raw_ref)
                    target['artifact_missing'] = True
                    return []
                return [_unknown_artifact(intent.intent_id, raw_ref)]
            param_refs.append((ref, schema_name))
        if param_refs:
            _set_input_refs(target, [ref for ref, _schema_name in param_refs])
            target_ref = next((ref for ref, schema_name in param_refs
                               if not target_schema_set or schema_name in target_schema_set), None)
            if target_ref is not None:
                target['artifact_ref'] = str(target_ref)
                return []
        if future_ref and not target_schema_set:
            target['artifact_ref'] = future_ref
            return []
        artifact_id = str(target.get('artifact_id') or intent.params.get('case_id') or '')
        if artifact_id:
            ref, issue = self._latest_ref(intent.intent_id, artifact_id)
            if issue:
                if not allow_missing_target: return [issue]
                target['artifact_ref'] = f'{artifact_id}@v1'
                target['artifact_missing'] = True
            else:
                target['artifact_ref'] = str(ref)
            target.pop('artifact_id', None)
            return []
        if require_target:
            return [_clarify(intent.intent_id, 'missing_artifact_target',
                             f'intent missing artifact target: {intent.intent_id}')]
        return []

    def _schema_name(self, ref: ArtifactRef) -> str | None:
        try:
            return self.store.artifact_graph(self.run_id).schema_name(ref)
        except KeyError:
            return None

    def _latest_ref(self, intent_id: str, artifact_id: str) -> tuple[ArtifactRef | None, ValidationIssue | None]:
        try:
            return self.store.artifact_graph(self.run_id).latest_ref(artifact_id), None
        except KeyError:
            return None, _unknown_artifact(intent_id, artifact_id)
        except ValueError:
            return None, _clarify(intent_id, 'invalid_artifact_id', f'invalid artifact target: {artifact_id}')

    def _plan_issues(self, plans: list[IntentPlan]) -> list[ValidationIssue]:
        issues: list[ValidationIssue] = []
        for plan in plans:
            try:
                capability = self.capability_registry.validate(self.store, self.run_id, self.checkpoint_ref, plan)
            except (PermissionError, ValueError, KeyError) as exc:
                issues.append(_reject(plan.operation_id, 'invalid_plan', str(exc)))
                continue
            artifact_id = _writes_artifact_id(capability, plan)
            if not artifact_id: continue
            graph = self.operation_factory.operation_graph
            for ref in graph.run_refs():
                run = graph.get_run(ref)
                if run.superseded_by or run.status == 'ended': continue
                if run.spec.tags.get('writes_artifact_id') == artifact_id and ref not in plan.depends_on:
                    plan.depends_on.append(ref)
        return issues

    def _create_proposals(self, ordered: list[CompiledIntent]) -> list[OperationProposal]:
        proposals: list[OperationProposal] = []
        operation_by_intent: dict[str, OperationRunRef] = {}
        for item in ordered:
            if item.plan is None: continue
            depends_on = list(item.plan.depends_on)
            for intent_id in item.operation_dependencies:
                ref = operation_by_intent[intent_id]
                if ref not in depends_on: depends_on.append(ref)
            plan = replace(item.plan, depends_on=depends_on)
            proposal = self.operation_factory.create_operation(self.run_id, self.checkpoint_ref, plan)
            operation_by_intent[item.intent.intent_id] = proposal.operation_ref
            proposals.append(proposal)
        return proposals

    def _commit_conditionals(self, request: IntentRequest, intents: list[AtomicIntent]) -> list[ArtifactRef]:
        refs: list[ArtifactRef] = []
        for intent in intents:
            if intent.kind != 'conditional': continue
            branches = intent.branches
            payload = {
                'conditional_intent_id': intent.intent_id, 'source_message_id': request.message_id,
                'checkpoint_id': request.checkpoint_id, 'status': 'waiting',
                'query_intent_ids': sorted({branches['if']['source_intent_id']}), 'predicate': branches['if'],
                'then_intents': branches.get('then', []), 'else_intents': branches.get('else', []),
                'actual_value': None, 'matched': None, 'selected_branch': '', 'selected_intent_ids': [],
                'operation_refs': [], 'issues': [],
            }
            refs.append(self._commit_audit(f'conditional_intent_{intent.intent_id}', 'ConditionalIntent', payload,
                                           request))
        return refs

    def _commit_trace(self, request: IntentRequest, result: IntentHarnessResult,
                      conditional_refs: list[ArtifactRef] | None = None) -> None:
        payload = {
            'message_id': request.message_id, 'message': request.message, 'checkpoint_id': request.checkpoint_id,
            'message_ref': str(request.message_ref or ''), 'parse_ref': str(request.parse_ref or ''),
            'intents': [asdict(intent) for intent in result.intents],
            'issues': [asdict(issue) for issue in result.issues],
            'operation_refs': [str(proposal.operation_ref) for proposal in result.proposals],
            'conditional_refs': [str(ref) for ref in (conditional_refs or [])],
            'result_action': result.action,
            'binding_notes': _binding_notes(result.intents),
        }
        self._commit_audit(f'intent_trace_{request.message_id}', 'IntentTrace', payload, request)

    def _commit_audit(self, artifact_id: str, schema: str, payload: dict, request: IntentRequest) -> ArtifactRef:
        return self.store.artifact_graph(self.run_id).commit_artifact(ArtifactDraft(
            artifact_id, schema, payload, f'intent_harness:{request.message_id}',
            input_refs=[ref for ref in (request.message_ref, request.parse_ref) if ref], role='audit',
        ))

    def _emit(self, event_type: str, request: IntentRequest, payload: dict) -> None:
        self.store.append_event(Event(
            event_type, self.run_id,
            {'message_id': request.message_id, 'checkpoint_id': request.checkpoint_id, **payload},
        ))


def _issue_result(intents: list[AtomicIntent], issues: list[ValidationIssue]) -> IntentHarnessResult:
    has_reject = any(issue.severity == 'reject' for issue in issues)
    action: IntentDecisionAction = 'reject' if has_reject else 'ask_clarification'
    return IntentHarnessResult(action, intents, reasons=[issue.message for issue in issues], issues=issues)


def _clarify(intent_id: str, code: str, message: str) -> ValidationIssue:
    return ValidationIssue(code, intent_id, 'clarify', message)


def _reject(intent_id: str, code: str, message: str) -> ValidationIssue:
    return ValidationIssue(code, intent_id, 'reject', message)


def _unknown_artifact(intent_id: str, ref: Any) -> ValidationIssue:
    return _clarify(intent_id, 'unknown_artifact', f'unknown artifact target: {ref}')


def _missing_query_target(intent: AtomicIntent) -> ValidationIssue:
    return _clarify(intent.intent_id, 'missing_query_target', f'query intent missing target: {intent.intent_id}')


def _requires_message_target(capability: dict) -> bool:
    if not capability.get('target_artifact_schemas'): return False
    return not any(str(name).endswith('_ref') for name in capability.get('system_param_contract') or {})


def _allow_missing_target(intent: AtomicIntent, capabilities: list[dict]) -> bool:
    capability_id = str(intent.target.get('capability_id') or '')
    allowed = [item.get('capability_id') for item in capabilities]
    return len(allowed) == 1 and allowed[0] == capability_id and capability_id in MISSING_TARGET_PROPOSAL_CAPABILITIES


def _artifact_ref_param_names(capability: dict) -> list[str]:
    return [str(name) for name in capability.get('system_param_contract') or {}
            if name.endswith('_ref') or name.endswith('_refs')]


def _param_artifact_refs(params: dict[str, Any], names: list[str]) -> list[str]:
    refs: list[str] = []
    for name in names:
        value = params.get(name)
        for item in (value if isinstance(value, list) else [value]):
            text = str(item or '').strip()
            if '@v' in text: refs.append(text)
    return refs


def _set_input_refs(target: dict, refs: list[ArtifactRef]) -> None:
    existing = [ref if isinstance(ref, ArtifactRef) else ArtifactRef.parse(str(ref))
                for ref in (target.get('input_refs') or [])]
    by_id: dict[str, ArtifactRef] = {}
    for ref in [*existing, *refs]:
        by_id.setdefault(ref.artifact_id, ref)
    target['input_refs'] = [str(ref) for ref in by_id.values()]


def _binding_notes(intents: list[AtomicIntent]) -> list[dict]:
    produced_by_intent: dict[str, str] = {}
    notes: list[dict] = []
    for intent in intents:
        artifact_id = _artifact_id(intent)
        if artifact_id and any(produced_by_intent.get(dep) == artifact_id for dep in intent.depends_on):
            notes.append({'intent_id': intent.intent_id, 'artifact_id': artifact_id,
                          'binding': 'runtime_latest_after_dependencies',
                          'pre_bind_ref': str(_artifact_ref(intent) or '')})
        if intent.kind in MUTATION_KINDS: produced_by_intent[intent.intent_id] = artifact_id
    return notes


def _expand_inline_conditionals(intents: list[AtomicIntent]) -> list[AtomicIntent]:
    expanded: list[AtomicIntent] = []
    for intent in intents:
        branches = intent.branches or {}
        if intent.kind == 'conditional' or not isinstance(branches.get('if'), dict):
            expanded.append(intent)
            continue
        expanded.append(replace(intent, branches={}))
        source = str(branches['if'].get('source_intent_id') or intent.intent_id)
        clean = {
            **branches,
            'then': [_branch_intent(item, source) for item in branches.get('then', []) if isinstance(item, dict)],
            'else': [_branch_intent(item, source) for item in branches.get('else', []) if isinstance(item, dict)],
        }
        expanded.append(AtomicIntent(
            f'branch_{intent.intent_id}', 'conditional', 'branch', branches=clean, depends_on=[intent.intent_id],
            confidence=intent.confidence, risk=intent.risk,
        ))
    return expanded


def _branch_intent(item: dict, source_intent_id: str) -> dict:
    return {
        **item, 'kind': 'chat' if item.get('kind') == 'response' else item.get('kind'),
        'depends_on': [dep for dep in item.get('depends_on', []) if dep != source_intent_id],
    }


def _conditional_issues(intent: AtomicIntent, intents: list[AtomicIntent]) -> list[ValidationIssue]:
    iid = intent.intent_id
    branches = intent.branches or {}
    predicate = branches.get('if')
    issues: list[ValidationIssue] = []
    by_id = {item.intent_id: item for item in intents}
    if not isinstance(predicate, dict):
        return [_clarify(iid, 'missing_condition', f'conditional intent missing predicate: {iid}')]
    source = str(predicate.get('source_intent_id') or '')
    if source not in by_id:
        issues.append(_reject(iid, 'unknown_condition_source', f'conditional source intent not found: {source}'))
    elif by_id[source].kind != 'query':
        issues.append(_reject(iid, 'invalid_condition_source', f'conditional source must be query intent: {source}'))
    if predicate.get('op') not in _PREDICATE_OPS:
        issues.append(_reject(iid, 'unsupported_predicate_op', f"unsupported predicate op: {predicate.get('op')}"))
    if not str(predicate.get('path') or '').startswith('answer.'):
        path = predicate.get('path')
        issues.append(_reject(iid, 'unsupported_predicate_path', f'unsupported predicate path: {path}'))
    if not isinstance(branches.get('then', []), list) or not isinstance(branches.get('else', []), list):
        issues.append(_reject(iid, 'invalid_branch_type', f'conditional branches then/else must be arrays: {iid}'))
    if not branches.get('then') and not branches.get('else'):
        issues.append(_reject(iid, 'empty_branches', f'conditional intent has no branch intents: {iid}'))
    ids = [item.get('intent_id') for branch in (branches.get('then') or [], branches.get('else') or [])
           for item in [branch] if isinstance(item, dict)]
    if len(ids) != len(set(ids)):
        issues.append(_reject(iid, 'duplicate_branch_intent_id', f'duplicate branch intent_id in conditional: {iid}'))
    return issues


def _eval_predicate(answer: dict[str, Any], predicate: dict[str, Any]) -> tuple[bool, Any]:
    exists, actual = _path_value(answer, str(predicate['path']))
    op = predicate['op']
    expected = predicate.get('value')
    if op == 'exists': return exists, actual
    if op == 'not_exists': return not exists, actual
    if not exists: raise ValueError(f"predicate path not found: {predicate['path']}")
    if op == 'eq': return actual == expected, actual
    if op == 'ne': return actual != expected, actual
    if op == 'in': return actual in (expected or []), actual
    if op == 'not_in': return actual not in (expected or []), actual
    raise ValueError(f'unsupported predicate op: {op}')


def _path_value(payload: dict[str, Any], path: str) -> tuple[bool, Any]:
    current: Any = payload
    for part in path.split('.'):
        if not isinstance(current, dict) or part not in current: return False, None
        current = current[part]
    return True, current


def _depends_path(child_id: str, parent_id: str, intents: list[AtomicIntent]) -> bool:
    by_id = {intent.intent_id: intent for intent in intents}
    stack = list(by_id[child_id].depends_on)
    seen: set[str] = set()
    while stack:
        current = stack.pop()
        if current == parent_id: return True
        if current in seen or current not in by_id: continue
        seen.add(current)
        stack.extend(by_id[current].depends_on)
    return False


def _future_artifacts(intents: list[AtomicIntent], capabilities: dict[str, dict]) -> dict[str, set[str]]:
    by_id = {intent.intent_id: intent for intent in intents}
    produced = {}
    for intent in intents:
        capability_id = str(intent.target.get('capability_id') or '')
        plan = IntentPlan(capability_id, str(intent.target.get('operation_id') or ''), intent.params,
                          input_refs=[ref if isinstance(ref, ArtifactRef) else ArtifactRef.parse(ref)
                                      for ref in (intent.target.get('input_refs') or [])])
        produced[intent.intent_id] = _writes_artifact_id(capabilities.get(capability_id, {}), plan)
    return {intent.intent_id: {produced[dep] for dep in _deps(intent.intent_id, by_id) if produced.get(dep)}
            for intent in intents}


def _deps(intent_id: str, by_id: dict[str, AtomicIntent]) -> set[str]:
    out, stack = set(), list(by_id[intent_id].depends_on) if intent_id in by_id else []
    while stack:
        dep = stack.pop()
        if dep in out or dep not in by_id: continue
        out.add(dep)
        stack.extend(by_id[dep].depends_on)
    return out


def _cap_field(capability: Any, key: str) -> Any:
    return capability.get(key) if isinstance(capability, dict) else getattr(capability, key)


def _writes_artifact_id(capability: Any, plan: IntentPlan, *, use_required: bool = False) -> str:
    writable_schema = _cap_field(capability, 'writable_artifact_schema') or ''
    if not writable_schema or writable_schema in {'IntentAnswer', 'JudgeResult'}: return ''
    if plan.params.get('output_id'): return str(plan.params['output_id'])
    template = _operation_template(capability)
    if template.get('tags', {}).get('writes_artifact_id'): return str(template['tags']['writes_artifact_id'])
    if writable_schema == 'CasePreparation' and plan.params.get('output_case_id'):
        return f"case_preparation_{plan.params['output_case_id']}"
    if writable_schema == 'DatasetCase' and _dataset_case_id(plan.params): return _dataset_case_id(plan.params)
    if writable_schema in FIXED_WRITES: return FIXED_WRITES[writable_schema]
    if plan.params.get('case_id'): return str(plan.params['case_id'])
    if plan.input_refs: return plan.input_refs[0].artifact_id
    if use_required and len(plan.required_artifact_ids) == 1: return plan.required_artifact_ids[0]
    return str(plan.params.get('case_id') or '')


def _operation_template(capability: Any) -> dict:
    for example in _cap_field(capability, 'examples') or []:
        template = example.get('operation_spec') if isinstance(example, dict) else None
        if isinstance(template, dict): return template
    return {}


def _check_target_schema(spec: CapabilitySpec, schema_name: str) -> None:
    if schema_name not in spec.target_artifact_schemas:
        raise PermissionError(f'capability {spec.capability_id} cannot target schema: {schema_name}')


def _view(spec: CapabilitySpec, include_system_contract: bool) -> dict:
    keys = _VIEW_KEYS + (('system_param_contract',) if include_system_contract else ())
    out = {}
    for key in keys:
        value = getattr(spec, key.removeprefix('intent_'))
        out[key] = list(value) if isinstance(value, list) else dict(value) if isinstance(value, dict) else value
    return out
