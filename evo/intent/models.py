from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal, Protocol

from ..artifacts import ArtifactRef
from ..operations import OperationRunRef

ConfirmationPolicy = Literal['none', 'required']
IntentKind = Literal[
    'chat', 'query', 'flow_control', 'artifact_change', 'config_change', 'confirmation', 'conditional', 'unsupported',
]
IntentRisk = Literal['low', 'medium', 'high']
IntentDecisionAction = Literal['propose_operations', 'ask_clarification', 'reject', 'no_operations']
ValidationSeverity = Literal['clarify', 'reject']


@dataclass(frozen=True)
class CapabilitySpec:
    capability_id: str
    creates_operation_type: str
    target_artifact_schemas: list[str] = field(default_factory=list)
    readable_artifact_ids: list[str] = field(default_factory=list)
    writable_artifact_schema: str = ''
    allowed_tools: list[str] = field(default_factory=list)
    confirmation_policy: ConfirmationPolicy = 'none'
    title: str = ''
    description: str = ''
    use_when: list[str] = field(default_factory=list)
    avoid_when: list[str] = field(default_factory=list)
    task_type: str = 'single_operation_task'
    semantic_schema: dict = field(default_factory=dict)
    system_param_contract: dict = field(default_factory=dict)
    effects: list[str] = field(default_factory=list)
    batch_policy: str = ''
    cross_stage_policy: str = ''
    params_schema: dict = field(default_factory=dict)
    examples: list[dict] = field(default_factory=list)
    risk_level: IntentRisk = 'low'


@dataclass(frozen=True)
class IntentRequest:
    message_id: str
    message: str
    checkpoint_id: str
    message_ref: ArtifactRef | None = None
    parse_ref: ArtifactRef | None = None


@dataclass(frozen=True)
class IntentPlan:
    capability_id: str
    operation_id: str
    params: dict
    input_refs: list[ArtifactRef] = field(default_factory=list)
    required_artifact_ids: list[str] = field(default_factory=list)
    depends_on: list[OperationRunRef] = field(default_factory=list)
    parent: OperationRunRef | None = None
    source_message_id: str | None = None


@dataclass(frozen=True)
class OperationProposal:
    operation_ref: OperationRunRef
    requires_confirmation: bool = False
    confirmation_checkpoint_id: str = ''


@dataclass(frozen=True)
class AtomicIntent:
    intent_id: str
    kind: IntentKind
    action: str
    target: dict = field(default_factory=dict)
    params: dict = field(default_factory=dict)
    confidence: float = 1.0
    risk: IntentRisk = 'low'
    depends_on: list[str] = field(default_factory=list)
    branches: dict = field(default_factory=dict)


@dataclass(frozen=True)
class ValidationIssue:
    code: str
    intent_id: str
    severity: ValidationSeverity
    message: str


@dataclass(frozen=True)
class IntentHarnessResult:
    action: IntentDecisionAction
    intents: list[AtomicIntent]
    proposals: list[OperationProposal] = field(default_factory=list)
    reasons: list[str] = field(default_factory=list)
    issues: list[ValidationIssue] = field(default_factory=list)


class IntentParser(Protocol):
    def parse(self, request: IntentRequest, capabilities: list[dict]) -> list[AtomicIntent]:
        ...


_PARAM_TYPES = {'string': str, 'number': (int, float), 'integer': int, 'boolean': bool, 'object': dict, 'array': list}


def validate_params(intent_id: str, params: dict, schema: dict) -> list[ValidationIssue]:
    if not schema: return []
    if schema.get('type') == 'object' and not isinstance(params, dict):
        return [_issue(intent_id, 'invalid_type', 'params must be object')]
    issues = [_issue(intent_id, 'missing_required_param', f'missing required param: {name}')
              for name in schema.get('required', []) if name not in params]
    properties = schema.get('properties', {})
    if schema.get('additionalProperties') is False:
        issues += [_issue(intent_id, 'unknown_param', f'unknown param: {name}')
                   for name in sorted(set(params) - set(properties))]
    for name, value in params.items():
        if name in properties: issues.extend(_validate_value(intent_id, name, value, properties[name]))
    return issues


def _validate_value(intent_id: str, name: str, value: Any, schema: dict) -> list[ValidationIssue]:
    expected = schema.get('type')
    if expected and not _matches_type(value, expected):
        return [_issue(intent_id, 'invalid_param_type', f'{name} must be {expected}')]
    issues = []
    if 'enum' in schema and value not in schema['enum']:
        values = ', '.join(map(str, schema['enum']))
        issues.append(_issue(intent_id, 'invalid_enum', f'{name} must be one of: {values}'))
    if isinstance(value, str) and 'minLength' in schema and len(value) < int(schema['minLength']):
        issues.append(_issue(intent_id, 'min_length', f"{name} length must be >= {schema['minLength']}"))
    return issues


def _matches_type(value: Any, expected: str) -> bool:
    if expected in {'number', 'integer'} and isinstance(value, bool): return False
    kind = _PARAM_TYPES.get(expected)
    return True if kind is None else isinstance(value, kind)


def _issue(intent_id: str, code: str, message: str) -> ValidationIssue:
    return ValidationIssue(code, intent_id, 'clarify', message)
