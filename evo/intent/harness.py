from __future__ import annotations

from dataclasses import asdict, dataclass, replace
from typing import Any

from ..artifacts import ArtifactDraft, ArtifactRef
from ..checkpoints import CheckpointRef
from ..operations import OperationRunRef
from ..store import Event, EvoStore
from .capabilities import CapabilityRegistry
from .compiler import MUTATION_KINDS, CompiledIntent, compile_intents
from .factory import IntentOperationFactory
from .models import AtomicIntent, IntentDecisionAction, IntentHarnessResult, IntentKind, IntentParser, IntentPlan, IntentRequest, OperationProposal, ValidationIssue
from .schema import validate_params

ARTIFACT_READ_QUERY_CAPABILITIES = {
    'read_artifact_query',
    'read_repair_artifact',
    'read_coarse_artifact_query',
    'read_fine_artifact_query',
}

MISSING_TARGET_PROPOSAL_CAPABILITIES = {
    'fine_classify_case',
    'build_repair_loop_plan',
    'start_repair_loop',
    'continue_repair_loop',
}


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
            result = _issue_result([], [_issue('', 'parse_failed', 'clarify', f'intent parse failed: {exc}')])
            self._emit('intent.parsed', request, {'intents': [], 'error': {'type': exc.__class__.__name__, 'message': str(exc)}})
            self._commit_trace(request, result)
            self._emit_result(request, result)
            return result
        self._emit('intent.parsed', request, {'intents': [asdict(intent) for intent in intents]})
        parser_issues = list(getattr(self.parser, 'issues', []))
        if getattr(self.parser, 'action', '') == 'no_operations':
            result = IntentHarnessResult('no_operations', intents)
            self._commit_trace(request, result)
            self._emit_result(request, result)
            return result
        if parser_issues:
            result = _issue_result(intents, parser_issues)
            self._commit_trace(request, result)
            self._emit_result(request, result)
            return result
        intents, normalize_issues = self._normalize_intents(intents, capabilities)
        if normalize_issues:
            result = _issue_result(intents, normalize_issues)
            self._commit_trace(request, result)
            self._emit_result(request, result)
            return result
        issues = self._precompile_issues(intents, capabilities)
        if issues:
            result = _issue_result(intents, issues)
            self._commit_trace(request, result)
            self._emit_result(request, result)
            return result
        compilation = compile_intents(request, intents)
        if compilation.issues:
            result = _issue_result(intents, compilation.issues)
            self._commit_trace(request, result)
            self._emit_result(request, result)
            return result
        plan_issues = self._plan_issues(compilation.plans)
        if plan_issues:
            result = _issue_result(intents, plan_issues)
            self._commit_trace(request, result)
            self._emit_result(request, result)
            return result
        proposals = self._create_proposals(compilation.ordered)
        conditional_refs = self._commit_conditionals(request, intents)
        result = IntentHarnessResult(
            _result_action(proposals),
            intents,
            proposals=proposals,
        )
        self._commit_trace(request, result, conditional_refs=conditional_refs)
        self._emit_result(request, result)
        return result

    def resolve_deferred(self, conditional_ref: ArtifactRef) -> IntentHarnessResult:
        graph = self.store.artifact_graph(self.run_id)
        conditional = graph.get(conditional_ref)
        if conditional.get('status') != 'waiting':
            return IntentHarnessResult('no_operations', [])
        try:
            answer_ref = graph.latest_ref(f"intent_answer_{conditional['predicate']['source_intent_id']}")
            answer = graph.get(answer_ref)
        except (FileNotFoundError, KeyError):
            return IntentHarnessResult('no_operations', [])
        matched, actual = _eval_predicate(answer, conditional['predicate'])
        selected = 'then' if matched else 'else'
        intents = [AtomicIntent(**item) for item in conditional[f'{selected}_intents']]
        result = self._handle_parsed(IntentRequest(conditional['source_message_id'], '', conditional['checkpoint_id']), intents)
        graph.commit_artifact(
            ArtifactDraft(
                conditional_ref.artifact_id,
                'ConditionalIntent',
                {**conditional, 'status': 'resolved', 'actual_value': actual, 'matched': matched, 'selected_branch': selected,
                 'selected_intent_ids': [intent.intent_id for intent in intents],
                 'operation_refs': [str(item.operation_ref) for item in result.proposals],
                 'issues': [asdict(issue) for issue in result.issues]},
                f"intent_harness:{conditional['source_message_id']}",
                input_refs=[conditional_ref, answer_ref],
                role='audit',
            )
        )
        return result

    def _handle_parsed(self, request: IntentRequest, intents: list[AtomicIntent]) -> IntentHarnessResult:
        if not intents:
            return IntentHarnessResult('no_operations', [])
        capabilities = self.capability_registry.execution_context(self.store, self.run_id, self.checkpoint_ref)
        intents, normalize_issues = self._normalize_intents(intents, capabilities)
        if normalize_issues:
            return _issue_result(intents, normalize_issues)
        issues = self._precompile_issues(intents, capabilities)
        if issues:
            return _issue_result(intents, issues)
        compilation = compile_intents(request, intents)
        if compilation.issues:
            return _issue_result(intents, compilation.issues)
        plan_issues = self._plan_issues(compilation.plans)
        if plan_issues:
            return _issue_result(intents, plan_issues)
        proposals = self._create_proposals(compilation.ordered)
        return IntentHarnessResult(_result_action(proposals), intents, proposals=proposals)

    def _precompile_issues(self, intents: list[AtomicIntent], capabilities: list[dict]) -> list[ValidationIssue]:
        if not intents:
            return [_issue('', 'empty_message', 'clarify', 'message did not contain a supported intent')]
        valid_kinds = set(IntentKind.__args__)
        allowed = {capability['capability_id']: capability for capability in capabilities}
        issues: list[ValidationIssue] = []
        writers_by_artifact: dict[str, list[AtomicIntent]] = {}
        for intent in intents:
            if intent.kind not in valid_kinds:
                issues.append(_issue(intent.intent_id, 'invalid_intent_kind', 'clarify', f'invalid intent kind: {intent.kind}'))
                continue
            if intent.confidence < self.min_confidence:
                issues.append(_issue(intent.intent_id, 'low_confidence', 'clarify', f'low confidence intent: {intent.intent_id}'))
            if intent.kind == 'unsupported':
                issues.append(_issue(intent.intent_id, 'unsupported_intent', 'clarify', f'unsupported intent: {intent.intent_id}'))
            if intent.kind == 'conditional':
                issues.extend(_conditional_issues(intent, intents))
            if intent.kind == 'query':
                issues.extend(self._query_issues(intent, allowed))
            if intent.kind == 'chat':
                issues.extend(self._chat_issues(intent, allowed))
            if intent.kind in MUTATION_KINDS:
                issues.extend(self._mutation_issues(intent, allowed))
                artifact_id = _artifact_id(intent)
                if artifact_id:
                    writers_by_artifact.setdefault(artifact_id, []).append(intent)
        issues.extend(_conditional_dependency_issues(intents))
        issues.extend(_writer_order_issues(writers_by_artifact))
        return issues

    def _chat_issues(self, intent: AtomicIntent, allowed: dict[str, dict]) -> list[ValidationIssue]:
        capability_id = str(intent.target.get('capability_id') or 'respond_to_user')
        if capability_id not in allowed:
            return [_issue(intent.intent_id, 'capability_not_allowed', 'reject', f'capability not allowed at checkpoint {self.checkpoint_ref}: {capability_id}')]
        return validate_params(
            intent.intent_id,
            {**intent.params, 'query_intent_id': intent.intent_id},
            allowed[capability_id].get('params_schema', {}),
        )

    def _mutation_issues(self, intent: AtomicIntent, allowed: dict[str, dict]) -> list[ValidationIssue]:
        issues: list[ValidationIssue] = []
        if not intent.action:
            issues.append(_issue(intent.intent_id, 'missing_action', 'clarify', f'mutation intent missing action: {intent.intent_id}'))
        capability_id = intent.target.get('capability_id')
        if not capability_id:
            issues.append(_issue(intent.intent_id, 'missing_capability', 'clarify', f'mutation intent missing capability_id: {intent.intent_id}'))
            return issues
        if capability_id not in allowed:
            issues.append(_issue(intent.intent_id, 'capability_not_allowed', 'reject', f'capability not allowed at checkpoint {self.checkpoint_ref}: {capability_id}'))
            return issues
        issues.extend(validate_params(intent.intent_id, intent.params, allowed[capability_id].get('params_schema', {})))
        return issues

    def _query_issues(self, intent: AtomicIntent, allowed: dict[str, dict]) -> list[ValidationIssue]:
        capability_id = _query_capability_id(intent)
        if capability_id not in allowed:
            return [_issue(intent.intent_id, 'capability_not_allowed', 'reject', f'capability not allowed at checkpoint {self.checkpoint_ref}: {capability_id}')]
        issues = validate_params(intent.intent_id, _query_validation_params(intent), allowed[capability_id].get('params_schema', {}))
        if issues:
            return issues
        if capability_id == 'read_run_status_query':
            run_id = str(intent.target.get('run_id') or self.run_id)
            if not (self.store.run_dir(run_id) / 'run.json').exists():
                return [_issue(intent.intent_id, 'unknown_run', 'clarify', f'unknown run target: {run_id}')]
            return []
        operation_id = intent.target.get('operation_run_id')
        if capability_id == 'read_operation_query':
            if not operation_id:
                return [_issue(intent.intent_id, 'missing_query_target', 'clarify', f'query intent missing target: {intent.intent_id}')]
            try:
                self.store.read_operation(self.run_id, str(operation_id))
            except FileNotFoundError:
                return [_issue(intent.intent_id, 'unknown_operation', 'clarify', f'unknown operation target: {operation_id}')]
            return []
        ref = _artifact_ref(intent)
        if ref is not None:
            try:
                self.store.artifact_graph(self.run_id).schema_name(ref)
            except KeyError:
                if capability_id not in ARTIFACT_READ_QUERY_CAPABILITIES:
                    return [_issue(intent.intent_id, 'unknown_artifact', 'clarify', f'unknown artifact target: {ref}')]
        if ref is None and not _artifact_id(intent) and not intent.target.get('artifact_ids'):
            return [_issue(intent.intent_id, 'missing_query_target', 'clarify', f'query intent missing target: {intent.intent_id}')]
        return []

    def _normalize_intents(self, intents: list[AtomicIntent], capabilities: list[dict]) -> tuple[list[AtomicIntent], list[ValidationIssue]]:
        intents = _expand_inline_conditionals(intents)
        allowed = {capability['capability_id']: capability for capability in capabilities}
        future = _future_artifacts(intents, allowed)
        issues: list[ValidationIssue] = []
        normalized: list[AtomicIntent] = []
        for intent in intents:
            if intent.kind == 'response':
                intent = replace(intent, kind='chat')
            target = dict(intent.target)
            if intent.kind == 'query' and not target.get('operation_run_id') and intent.params.get('operation_run_id'):
                target['operation_run_id'] = intent.params['operation_run_id']
            if intent.kind == 'query' and not target.get('run_id') and intent.params.get('run_id'):
                target['run_id'] = intent.params['run_id']
            capability_id = _query_capability_id(replace(intent, target=target))
            capability = allowed.get(capability_id, {})
            operation_type = capability.get('creates_operation_type') or capability.get('operation_type')
            if intent.kind == 'query' and operation_type == 'ReadArtifactQueryOperation':
                issues.extend(self._normalize_artifact_targets(intent, target, require_target=not bool(target.get('operation_run_id'))))
            elif intent.kind == 'query' and capability_id == 'read_run_status_query' and not target.get('run_id'):
                target['run_id'] = self.run_id
            elif intent.kind in MUTATION_KINDS and target.get('capability_id') in allowed:
                capability = allowed[target['capability_id']]
                require_target = _requires_message_target(capability)
                issues.extend(self._normalize_artifact_targets(
                    intent,
                    target,
                    require_target=require_target,
                    target_schemas=capability.get('target_artifact_schemas') or [],
                    future_artifacts=future.get(intent.intent_id, set()),
                    allow_missing_target=_allow_missing_target(intent, capabilities),
                ))
            normalized.append(self._inherit_active_params(replace(intent, target=target), allowed))
        return normalized, issues

    def _inherit_active_params(self, intent: AtomicIntent, allowed: dict[str, dict]) -> AtomicIntent:
        if intent.kind not in MUTATION_KINDS:
            return intent
        capability = allowed.get(str(intent.target.get('capability_id') or ''))
        operation_id = str(intent.target.get('operation_id') or '')
        active = self.operation_factory.operation_graph.active_run_for(operation_id) if operation_id else None
        if not capability or active is None:
            return intent
        spec = self.operation_factory.operation_graph.get_run(active).spec
        keys = _param_keys(capability.get('params_schema') or {})
        params = {
            key: value for key, value in spec.params.items()
            if key in keys and key not in {'capability_id', 'source_message_id'}
        }
        return replace(intent, params={**params, **intent.params}) if params else intent

    def _normalize_artifact_targets(
        self,
        intent: AtomicIntent,
        target: dict,
        *,
        require_target: bool,
        target_schemas: list[str] | None = None,
        future_artifacts: set[str] | None = None,
        allow_missing_target: bool = False,
    ) -> list[ValidationIssue]:
        issues: list[ValidationIssue] = []
        refs: list[ArtifactRef] = []
        raw_input_refs = target.get('input_refs')
        if raw_input_refs:
            for value in raw_input_refs:
                try:
                    ref = value if isinstance(value, ArtifactRef) else ArtifactRef.parse(value)
                except ValueError:
                    issues.append(_issue(intent.intent_id, 'invalid_artifact_ref', 'clarify', f'invalid artifact target: {value}'))
                    continue
                try:
                    self.store.artifact_graph(self.run_id).schema_name(ref)
                except KeyError:
                    issues.append(_issue(intent.intent_id, 'unknown_artifact', 'clarify', f'unknown artifact target: {ref}'))
                else:
                    refs.append(ref)
            target['input_refs'] = [str(ref) for ref in refs]
            return issues
        raw_ids = target.get('artifact_ids')
        if raw_ids:
            for artifact_id in raw_ids:
                ref, issue = self._latest_ref(intent.intent_id, str(artifact_id))
                if issue:
                    issues.append(issue)
                else:
                    refs.append(ref)
            if refs:
                target['input_refs'] = [str(ref) for ref in refs]
            return issues
        ref = _artifact_ref(intent)
        if ref is not None:
            try:
                self.store.artifact_graph(self.run_id).schema_name(ref)
            except KeyError:
                if not (allow_missing_target or _query_capability_id(intent) in ARTIFACT_READ_QUERY_CAPABILITIES):
                    return [_issue(intent.intent_id, 'unknown_artifact', 'clarify', f'unknown artifact target: {ref}')]
                target['artifact_missing'] = True
            target['artifact_ref'] = str(ref)
            return []
        ref_keys = ('eval_report_ref', 'eval_dataset_ref', 'source_report_ref', 'source_snapshot_ref',
                    'case_preparation_ref', 'rag_answer_ref', 'coarse_classification_ref',
                    'classification_report_ref', 'repair_loop_plan_ref')
        first_ref = ''
        target_schema_set = set(target_schemas or [])
        for key in ref_keys:
            if intent.params.get(key):
                try:
                    ref = ArtifactRef.parse(str(intent.params[key]))
                    schema_name = self.store.artifact_graph(self.run_id).schema_name(ref)
                except (KeyError, ValueError):
                    artifact_id = str(intent.params[key]).split('@', 1)[0]
                    if artifact_id in (future_artifacts or set()):
                        first_ref = first_ref or str(intent.params[key])
                        continue
                    if allow_missing_target:
                        target['artifact_ref'] = str(intent.params[key])
                        target['artifact_missing'] = True
                        return []
                    return [_issue(intent.intent_id, 'unknown_artifact', 'clarify', f'unknown artifact target: {intent.params[key]}')]
                first_ref = first_ref or str(ref)
                if not target_schema_set or schema_name in target_schema_set:
                    target['artifact_ref'] = str(ref)
                    return []
        if first_ref and not target_schema_set:
            target['artifact_ref'] = first_ref
            return []
        artifact_id = str(target.get('artifact_id') or intent.params.get('case_id') or '')
        if artifact_id:
            ref, issue = self._latest_ref(intent.intent_id, artifact_id)
            if issue:
                if not (allow_missing_target or _query_capability_id(intent) in ARTIFACT_READ_QUERY_CAPABILITIES):
                    return [issue]
                target['artifact_ref'] = f'{artifact_id}@v1'
                target['artifact_missing'] = True
                target.pop('artifact_id', None)
                return []
            target['artifact_ref'] = str(ref)
            target.pop('artifact_id', None)
            return []
        if require_target:
            return [_issue(intent.intent_id, 'missing_artifact_target', 'clarify', f'intent missing artifact target: {intent.intent_id}')]
        return []

    def _latest_ref(self, intent_id: str, artifact_id: str) -> tuple[ArtifactRef | None, ValidationIssue | None]:
        try:
            return self.store.artifact_graph(self.run_id).latest_ref(artifact_id), None
        except KeyError:
            return None, _issue(intent_id, 'unknown_artifact', 'clarify', f'unknown artifact target: {artifact_id}')
        except ValueError:
            return None, _issue(intent_id, 'invalid_artifact_id', 'clarify', f'invalid artifact target: {artifact_id}')

    def _plan_issues(self, plans: list[IntentPlan]) -> list[ValidationIssue]:
        issues: list[ValidationIssue] = []
        for plan in plans:
            try:
                capability = self.capability_registry.validate(self.store, self.run_id, self.checkpoint_ref, plan)
            except (PermissionError, ValueError, KeyError) as exc:
                issues.append(_issue(plan.operation_id, 'invalid_plan', 'reject', str(exc)))
                continue
            artifact_id = _writes_artifact_id(capability, plan)
            for writer in _active_writers_for(self.operation_factory.operation_graph, artifact_id):
                if writer not in plan.depends_on:
                    plan.depends_on.append(writer)
        return issues

    def _create_proposals(self, ordered: list[CompiledIntent]) -> list[OperationProposal]:
        proposals: list[OperationProposal] = []
        operation_by_intent: dict[str, OperationRunRef] = {}
        for item in ordered:
            if item.plan is None:
                continue
            depends_on = list(item.plan.depends_on)
            for intent_id in item.operation_dependencies:
                ref = operation_by_intent[intent_id]
                if ref not in depends_on:
                    depends_on.append(ref)
            plan = IntentPlan(
                capability_id=item.plan.capability_id,
                operation_id=item.plan.operation_id,
                params=item.plan.params,
                input_refs=item.plan.input_refs,
                required_artifact_ids=item.plan.required_artifact_ids,
                depends_on=depends_on,
                parent=item.plan.parent,
                source_message_id=item.plan.source_message_id,
            )
            proposal = self.operation_factory.create_operation(self.run_id, self.checkpoint_ref, plan)
            operation_by_intent[item.intent.intent_id] = proposal.operation_ref
            proposals.append(proposal)
        return proposals

    def _commit_conditionals(self, request: IntentRequest, intents: list[AtomicIntent]) -> list[ArtifactRef]:
        refs: list[ArtifactRef] = []
        for intent in intents:
            if intent.kind != 'conditional':
                continue
            branches = intent.branches
            ref = self.store.artifact_graph(self.run_id).commit_artifact(
                ArtifactDraft(
                    f'conditional_intent_{intent.intent_id}',
                    'ConditionalIntent',
                    {
                        'conditional_intent_id': intent.intent_id,
                        'source_message_id': request.message_id,
                        'checkpoint_id': request.checkpoint_id,
                        'status': 'waiting',
                        'query_intent_ids': sorted({branches['if']['source_intent_id']}),
                        'predicate': branches['if'],
                        'then_intents': branches.get('then', []),
                        'else_intents': branches.get('else', []),
                        'actual_value': None,
                        'matched': None,
                        'selected_branch': '',
                        'selected_intent_ids': [],
                        'operation_refs': [],
                        'issues': [],
                    },
                    f'intent_harness:{request.message_id}',
                    input_refs=[ref for ref in (request.message_ref, request.parse_ref) if ref],
                    role='audit',
                )
            )
            refs.append(ref)
        return refs

    def _commit_trace(self, request: IntentRequest, result: IntentHarnessResult, conditional_refs: list[ArtifactRef] | None = None) -> None:
        payload = {
            'message_id': request.message_id,
            'message': request.message,
            'checkpoint_id': request.checkpoint_id,
            'message_ref': str(request.message_ref or ''),
            'parse_ref': str(request.parse_ref or ''),
            'intents': [asdict(intent) for intent in result.intents],
            'issues': [asdict(issue) for issue in result.issues],
            'operation_refs': [str(proposal.operation_ref) for proposal in result.proposals],
            'conditional_refs': [str(ref) for ref in (conditional_refs or [])],
            'result_action': result.action,
            'binding_notes': _binding_notes(result.intents),
        }
        self.store.artifact_graph(self.run_id).commit_artifact(
            ArtifactDraft(
                f'intent_trace_{request.message_id}',
                'IntentTrace',
                payload,
                f'intent_harness:{request.message_id}',
                input_refs=[ref for ref in (request.message_ref, request.parse_ref) if ref],
                role='audit',
            )
        )

    def _emit_result(self, request: IntentRequest, result: IntentHarnessResult) -> None:
        event_type = 'intent.rejected' if result.action == 'reject' else 'intent.completed'
        self._emit(
            event_type,
            request,
            {
                'result_action': result.action,
                'operation_refs': [str(proposal.operation_ref) for proposal in result.proposals],
                'issues': [asdict(issue) for issue in result.issues],
            },
        )

    def _emit(self, event_type: str, request: IntentRequest, payload: dict) -> None:
        self.store.append_event(
            Event(
                event_type,
                self.run_id,
                {'message_id': request.message_id, 'checkpoint_id': request.checkpoint_id, **payload},
            )
        )


def _issue_result(intents: list[AtomicIntent], issues: list[ValidationIssue]) -> IntentHarnessResult:
    action: IntentDecisionAction = 'reject' if any(issue.severity == 'reject' for issue in issues) else 'ask_clarification'
    return IntentHarnessResult(action, intents, reasons=[issue.message for issue in issues], issues=issues)


def _result_action(proposals: list[OperationProposal]) -> IntentDecisionAction:
    if proposals:
        return 'propose_operations'
    return 'no_operations'


def _requires_message_target(capability: dict) -> bool:
    if not capability.get('target_artifact_schemas'):
        return False
    system = capability.get('system_param_contract') or {}
    return not any(str(name).endswith('_ref') for name in system)


def _allow_missing_target(intent: AtomicIntent, capabilities: list[dict]) -> bool:
    capability_id = str(intent.target.get('capability_id') or '')
    allowed = [item.get('capability_id') for item in capabilities]
    return len(allowed) == 1 and allowed[0] == capability_id and capability_id in MISSING_TARGET_PROPOSAL_CAPABILITIES


def _artifact_ref(intent: AtomicIntent) -> ArtifactRef | None:
    value = intent.target.get('artifact_ref')
    if value:
        if isinstance(value, ArtifactRef):
            return value
        if '@v' not in str(value):
            return None
        return ArtifactRef.parse(value)
    artifact_id = intent.target.get('artifact_id') or intent.params.get('case_id')
    version = intent.target.get('version')
    if artifact_id and version:
        return ArtifactRef(str(artifact_id), int(version))
    return None


def _artifact_id(intent: AtomicIntent) -> str:
    value = intent.target.get('artifact_ref')
    if value and not isinstance(value, ArtifactRef) and '@v' not in str(value):
        return str(value)
    ref = _artifact_ref(intent)
    return ref.artifact_id if ref else str(intent.target.get('artifact_id') or intent.params.get('case_id') or '')


def _binding_notes(intents: list[AtomicIntent]) -> list[dict]:
    produced_by_intent: dict[str, str] = {}
    notes: list[dict] = []
    for intent in intents:
        artifact_id = _artifact_id(intent)
        late_bound = bool(artifact_id and any(produced_by_intent.get(dep) == artifact_id for dep in intent.depends_on))
        if late_bound:
            notes.append(
                {
                    'intent_id': intent.intent_id,
                    'artifact_id': artifact_id,
                    'binding': 'runtime_latest_after_dependencies',
                    'pre_bind_ref': str(_artifact_ref(intent) or ''),
                }
            )
        if intent.kind in MUTATION_KINDS:
            produced_by_intent[intent.intent_id] = artifact_id
    return notes


def _issue(intent_id: str, code: str, severity: str, message: str) -> ValidationIssue:
    return ValidationIssue(code, intent_id, severity, message)


def _query_capability_id(intent: AtomicIntent) -> str:
    if intent.target.get('capability_id'):
        return str(intent.target['capability_id'])
    if intent.target.get('operation_run_id'):
        return 'read_operation_query'
    if intent.target.get('run_id') or intent.target.get('run_status'):
        return 'read_run_status_query'
    return 'read_artifact_query'


def _query_validation_params(intent: AtomicIntent) -> dict:
    params = {'query_intent_id': intent.intent_id}
    if _query_capability_id(intent) == 'read_operation_query':
        params['operation_run_id'] = intent.target.get('operation_run_id', '')
    if _query_capability_id(intent) == 'read_run_status_query':
        params['run_id'] = intent.target.get('run_id', '')
    return params


def _expand_inline_conditionals(intents: list[AtomicIntent]) -> list[AtomicIntent]:
    expanded: list[AtomicIntent] = []
    for intent in intents:
        branches = intent.branches or {}
        if intent.kind != 'conditional' and isinstance(branches.get('if'), dict):
            expanded.append(replace(intent, branches={}))
            source = str(branches['if'].get('source_intent_id') or intent.intent_id)
            clean = {
                **branches,
                'then': [_branch_intent(item, source) for item in branches.get('then', []) if isinstance(item, dict)],
                'else': [_branch_intent(item, source) for item in branches.get('else', []) if isinstance(item, dict)],
            }
            expanded.append(AtomicIntent(f'branch_{intent.intent_id}', 'conditional', 'branch', branches=clean, depends_on=[intent.intent_id], confidence=intent.confidence, risk=intent.risk))
        else:
            expanded.append(intent)
    return expanded


def _branch_intent(item: dict, source_intent_id: str) -> dict:
    return {**item, 'kind': 'chat' if item.get('kind') == 'response' else item.get('kind'), 'depends_on': [dep for dep in item.get('depends_on', []) if dep != source_intent_id]}


def _conditional_issues(intent: AtomicIntent, intents: list[AtomicIntent]) -> list[ValidationIssue]:
    branches = intent.branches or {}
    predicate = branches.get('if')
    issues: list[ValidationIssue] = []
    by_id = {item.intent_id: item for item in intents}
    if not isinstance(predicate, dict):
        return [_issue(intent.intent_id, 'missing_condition', 'clarify', f'conditional intent missing predicate: {intent.intent_id}')]
    source = str(predicate.get('source_intent_id') or '')
    if source not in by_id:
        issues.append(_issue(intent.intent_id, 'unknown_condition_source', 'reject', f'conditional source intent not found: {source}'))
    elif by_id[source].kind != 'query':
        issues.append(_issue(intent.intent_id, 'invalid_condition_source', 'reject', f'conditional source must be query intent: {source}'))
    if predicate.get('op') not in {'eq', 'ne', 'exists', 'not_exists', 'in', 'not_in'}:
        issues.append(_issue(intent.intent_id, 'unsupported_predicate_op', 'reject', f"unsupported predicate op: {predicate.get('op')}"))
    if not str(predicate.get('path') or '').startswith('answer.'):
        issues.append(_issue(intent.intent_id, 'unsupported_predicate_path', 'reject', f"unsupported predicate path: {predicate.get('path')}"))
    if not isinstance(branches.get('then', []), list) or not isinstance(branches.get('else', []), list):
        issues.append(_issue(intent.intent_id, 'invalid_branch_type', 'reject', f'conditional branches then/else must be arrays: {intent.intent_id}'))
    if not branches.get('then') and not branches.get('else'):
        issues.append(_issue(intent.intent_id, 'empty_branches', 'reject', f'conditional intent has no branch intents: {intent.intent_id}'))
    ids = [item.get('intent_id') for branch in (branches.get('then') or [], branches.get('else') or []) for item in [branch] if isinstance(item, dict)]
    if len(ids) != len(set(ids)):
        issues.append(_issue(intent.intent_id, 'duplicate_branch_intent_id', 'reject', f'duplicate branch intent_id in conditional: {intent.intent_id}'))
    return issues


def _conditional_dependency_issues(intents: list[AtomicIntent]) -> list[ValidationIssue]:
    conditional_ids = {intent.intent_id for intent in intents if intent.kind == 'conditional'}
    return [
        _issue(intent.intent_id, 'top_level_depends_on_conditional', 'reject', f'top-level intent cannot depend on conditional intent: {dep}')
        for intent in intents
        for dep in intent.depends_on
        if intent.kind != 'conditional' and dep in conditional_ids
    ]


def _eval_predicate(answer: dict[str, Any], predicate: dict[str, Any]) -> tuple[bool, Any]:
    exists, actual = _path_value(answer, str(predicate['path']))
    op = predicate['op']
    expected = predicate.get('value')
    if op == 'exists':
        return exists, actual
    if op == 'not_exists':
        return not exists, actual
    if not exists:
        raise ValueError(f"predicate path not found: {predicate['path']}")
    if op == 'eq':
        return actual == expected, actual
    if op == 'ne':
        return actual != expected, actual
    if op == 'in':
        return actual in (expected or []), actual
    if op == 'not_in':
        return actual not in (expected or []), actual
    raise ValueError(f'unsupported predicate op: {op}')


def _path_value(payload: dict[str, Any], path: str) -> tuple[bool, Any]:
    current: Any = payload
    for part in path.split('.'):
        if not isinstance(current, dict) or part not in current:
            return False, None
        current = current[part]
    return True, current


def _writer_order_issues(writers_by_artifact: dict[str, list[AtomicIntent]]) -> list[ValidationIssue]:
    issues: list[ValidationIssue] = []
    for artifact_id, writers in writers_by_artifact.items():
        for index, left in enumerate(writers):
            for right in writers[index + 1 :]:
                if not _depends_path(left.intent_id, right.intent_id, writers) and not _depends_path(right.intent_id, left.intent_id, writers):
                    issues.append(
                        _issue(
                            right.intent_id,
                            'ambiguous_artifact_mutation',
                            'clarify',
                            f'multiple unordered mutation intents target artifact: {artifact_id}',
                        )
                    )
    return issues


def _active_writers_for(operation_graph, artifact_id: str) -> list:
    if not artifact_id:
        return []
    writers = []
    for ref in operation_graph.run_refs():
        run = operation_graph.get_run(ref)
        if run.superseded_by or run.status == 'ended':
            continue
        if run.spec.tags.get('writes_artifact_id') == artifact_id:
            writers.append(ref)
    return writers


def _depends_path(child_id: str, parent_id: str, intents: list[AtomicIntent]) -> bool:
    by_id = {intent.intent_id: intent for intent in intents}
    stack = list(by_id[child_id].depends_on)
    seen: set[str] = set()
    while stack:
        current = stack.pop()
        if current == parent_id:
            return True
        if current in seen or current not in by_id:
            continue
        seen.add(current)
        stack.extend(by_id[current].depends_on)
    return False


def _future_artifacts(intents: list[AtomicIntent], capabilities: dict[str, dict]) -> dict[str, set[str]]:
    by_id = {intent.intent_id: intent for intent in intents}
    produced = {}
    for intent in intents:
        capability_id = str(intent.target.get('capability_id') or '')
        plan = IntentPlan(capability_id, str(intent.target.get('operation_id') or ''), intent.params,
                          input_refs=_input_refs(intent))
        produced[intent.intent_id] = _writes_artifact_id(capabilities.get(capability_id, {}), plan)
    return {
        intent.intent_id: {produced[dep] for dep in _deps(intent.intent_id, by_id) if produced.get(dep)}
        for intent in intents
    }


def _deps(intent_id: str, by_id: dict[str, AtomicIntent]) -> set[str]:
    out, stack = set(), list(by_id[intent_id].depends_on) if intent_id in by_id else []
    while stack:
        dep = stack.pop()
        if dep in out or dep not in by_id:
            continue
        out.add(dep)
        stack.extend(by_id[dep].depends_on)
    return out


def _input_refs(intent: AtomicIntent) -> list[ArtifactRef]:
    return [ref if isinstance(ref, ArtifactRef) else ArtifactRef.parse(ref)
            for ref in (intent.target.get('input_refs') or [])]


def _writes_artifact_id(capability: dict | Any, plan: IntentPlan) -> str:
    writable_schema = capability.writable_artifact_schema if hasattr(capability, 'writable_artifact_schema') else (
        capability.get('writable_artifact_schema') or ''
    )
    if not writable_schema or writable_schema in {'IntentAnswer', 'JudgeResult'}:
        return ''
    if plan.params.get('output_id'):
        return str(plan.params['output_id'])
    template = _operation_template(capability)
    if template.get('tags', {}).get('writes_artifact_id'):
        return str(template['tags']['writes_artifact_id'])
    if writable_schema == 'CasePreparation' and plan.params.get('output_case_id'):
        return f"case_preparation_{plan.params['output_case_id']}"
    if writable_schema == 'DatasetCase':
        case_id = _dataset_case_id(plan)
        if case_id:
            return case_id
    fixed = {
        'CorpusLoadReport': 'corpus_load_report',
        'CorpusSnapshot': 'corpus_snapshot',
        'EvalDataset': 'eval_dataset',
        'ClassificationReport': 'classification_report',
        'ABTestComparison': 'abtest_comparison',
        'RepairLoopPlan': 'repair_loop_plan',
    }
    if writable_schema in fixed:
        return fixed[writable_schema]
    if plan.params.get('case_id'):
        return str(plan.params['case_id'])
    if plan.input_refs:
        return plan.input_refs[0].artifact_id
    return str(plan.params.get('case_id') or '')


def _param_keys(schema: dict) -> set[str]:
    return set((schema.get('properties') or {}).keys())


def _dataset_case_id(plan: IntentPlan) -> str:
    if plan.params.get('case_id'):
        return str(plan.params['case_id'])
    artifact_id = str(plan.params.get('case_preparation_ref') or '').split('@', 1)[0]
    return artifact_id.removeprefix('case_preparation_') if artifact_id.startswith('case_preparation_') else ''


def _operation_template(capability: dict | Any) -> dict:
    examples = capability.examples if hasattr(capability, 'examples') else capability.get('examples', [])
    for example in examples:
        template = example.get('operation_spec') if isinstance(example, dict) else None
        if isinstance(template, dict):
            return template
    return {}
