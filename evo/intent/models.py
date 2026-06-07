from __future__ import annotations

from dataclasses import dataclass, field
from typing import Literal, Protocol

from ..artifacts import ArtifactRef
from ..operations import OperationRunRef

ConfirmationPolicy = Literal['none', 'required']
IntentKind = Literal['chat', 'query', 'flow_control', 'artifact_change', 'config_change', 'confirmation', 'conditional', 'unsupported']
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
    def parse(self, request: IntentRequest, capabilities: list[dict]) -> list[AtomicIntent]: ...
