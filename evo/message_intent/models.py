from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

IntentKind = str


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class EmptyArgs(StrictModel):
    pass


class GeneralChatArgs(StrictModel):
    topic: str = ''
    reply_intent: str = ''


class CaseRefArgs(StrictModel):
    case_ref: str = ''
    selector: str = ''
    cursor: str = ''
    max_chars: int = 1200


class ReadReportSectionArgs(StrictModel):
    artifact_ref: str = ''
    section: str = ''
    selector: str = ''
    checkpoint_ref: str = ''
    cursor: str = ''
    max_chars: int = 1200


class PatchArtifactArgs(StrictModel):
    case_ref: str = ''
    field: str
    value: Any


class ApprovalArgs(StrictModel):
    approval_token: str = ''


class UnsupportedArgs(StrictModel):
    reason: str = ''


class BoundedContinueArgs(StrictModel):
    target_step_ref: str = ''
    stop_before_step_ref: str = ''
    pause_after_step_ref: str = ''


NextOpsArgs = (
    EmptyArgs
    | GeneralChatArgs
    | CaseRefArgs
    | ReadReportSectionArgs
    | PatchArtifactArgs
    | ApprovalArgs
    | UnsupportedArgs
    | BoundedContinueArgs
)


@dataclass(frozen=True)
class OperationSpec:
    args_model: type[StrictModel]
    category: Literal['read', 'mutating', 'approval']
    prompt_args: str
    requires_case_ref: bool = False
    requires_patch_field: bool = False
    requires_boundary: bool = False
    runtime_supported: bool = True
    unsupported_reason: str = ''


OPERATION_SPECS: dict[str, OperationSpec] = {
    'general_chat': OperationSpec(
        GeneralChatArgs,
        'read',
        'general_chat: {"topic": string, "reply_intent": string}.',
    ),
    'status_query': OperationSpec(EmptyArgs, 'read', 'status_query: {}.'),
    'list_failed_cases': OperationSpec(EmptyArgs, 'read', 'list_failed_cases: {}.'),
    'read_case_result': OperationSpec(
        CaseRefArgs,
        'read',
        (
            'read_case_result: {"case_ref": string, "selector": string, '
            '"cursor": string, "max_chars": number}; keep raw user references if not normalized.'
        ),
        requires_case_ref=True,
    ),
    'read_report_section': OperationSpec(
        ReadReportSectionArgs,
        'read',
        (
            'read_report_section: {"artifact_ref": string, "section": string, '
            '"selector": string, "cursor": string, "max_chars": number}.'
        ),
    ),
    'explain_current_gate': OperationSpec(
        ReadReportSectionArgs,
        'read',
        (
            'explain_current_gate: {"artifact_ref": string, "section": string, '
            '"selector": string, "checkpoint_ref": string, "cursor": string, '
            '"max_chars": number}.'
        ),
        runtime_supported=False,
        unsupported_reason='当前 evo runtime 还不支持解释指定 gate/checkpoint；可以先读取报告或查看流程状态。',
    ),
    'continue_flow': OperationSpec(EmptyArgs, 'mutating', 'continue_flow: {}.'),
    'pause_flow': OperationSpec(EmptyArgs, 'mutating', 'pause_flow: {}.'),
    'cancel_flow': OperationSpec(EmptyArgs, 'mutating', 'cancel_flow: {}.'),
    'retry_failed': OperationSpec(EmptyArgs, 'mutating', 'retry_failed: {}.'),
    'rerun_case': OperationSpec(
        CaseRefArgs,
        'mutating',
        'rerun_case: {"case_ref": string}; keep raw user references if not normalized.',
        requires_case_ref=True,
    ),
    'patch_artifact': OperationSpec(
        PatchArtifactArgs,
        'mutating',
        'patch_artifact: {"case_ref": string, "field": string, "value": any}; keep user-facing field names.',
        requires_patch_field=True,
    ),
    'bounded_continue_flow': OperationSpec(
        BoundedContinueArgs,
        'mutating',
        (
            'bounded_continue_flow: {"target_step_ref": string, '
            '"stop_before_step_ref": string, "pause_after_step_ref": string}.'
        ),
        requires_boundary=True,
        runtime_supported=False,
        unsupported_reason='当前 evo runtime 还不支持带步骤边界的继续执行；为避免误执行，已暂停在确认前。',
    ),
    'approve_pending': OperationSpec(
        ApprovalArgs,
        'approval',
        'approve_pending: {"approval_token": string}; empty string is allowed.',
    ),
    'reject_pending': OperationSpec(
        ApprovalArgs,
        'approval',
        'reject_pending: {"approval_token": string}; empty string is allowed.',
    ),
    'cancel_pending': OperationSpec(
        ApprovalArgs,
        'approval',
        'cancel_pending: {"approval_token": string}; empty string is allowed.',
    ),
    'unsupported': OperationSpec(
        UnsupportedArgs,
        'read',
        'unsupported: {"reason": string}.',
    ),
}


INTENT_KINDS = tuple(OPERATION_SPECS)
ARGS_GUIDANCE = tuple(spec.prompt_args for spec in OPERATION_SPECS.values())
READ_ONLY_KINDS = frozenset(kind for kind, spec in OPERATION_SPECS.items() if spec.category == 'read')
MUTATING_KINDS = frozenset(kind for kind, spec in OPERATION_SPECS.items() if spec.category == 'mutating')
PENDING_RESOLUTION_KINDS = frozenset(kind for kind, spec in OPERATION_SPECS.items() if spec.category == 'approval')


class NextOps(StrictModel):
    kind: IntentKind
    args: NextOpsArgs = Field(default_factory=EmptyArgs)
    confidence: float = Field(default=1.0, ge=0.0, le=1.0)
    reason: str = ''

    @model_validator(mode='before')
    @classmethod
    def coerce_args_for_kind(cls, data: Any) -> Any:
        if not isinstance(data, Mapping):
            return data
        payload = dict(data)
        kind = str(payload.get('kind') or '')
        args = payload.get('args')
        if args is None:
            args = {}
        payload['args'] = _spec_for(kind).args_model.model_validate(args)
        return payload

    @model_validator(mode='after')
    def validate_args_for_kind(self) -> 'NextOps':
        spec = _spec_for(self.kind)
        args = self.args
        if not isinstance(args, spec.args_model):
            raise ValueError(f'{self.kind} expects {spec.args_model.__name__}')
        if isinstance(args, CaseRefArgs) and spec.requires_case_ref:
            if not args.case_ref.strip():
                raise ValueError(f'{self.kind} requires case_ref')
        if isinstance(args, PatchArtifactArgs) and spec.requires_patch_field:
            if not args.field.strip():
                raise ValueError('patch_artifact requires field')
        if isinstance(args, BoundedContinueArgs) and spec.requires_boundary:
            if not any((args.target_step_ref, args.stop_before_step_ref, args.pause_after_step_ref)):
                raise ValueError('bounded_continue_flow requires a boundary arg')
        return self


def _spec_for(kind: str) -> OperationSpec:
    try:
        return OPERATION_SPECS[kind]
    except KeyError as exc:
        raise ValueError(f'unsupported next_ops kind: {kind}') from exc


class RollingPlannerOutput(StrictModel):
    status: Literal['next_ops', 'done', 'clarification']
    next_ops: NextOps | None = None
    reminder: str = ''
    clarification: str = ''
    confidence: float = Field(default=1.0, ge=0.0, le=1.0)


class ResolvedIntent(StrictModel):
    kind: IntentKind
    case_id: str = ''
    case_ref: str = ''
    case_ids: tuple[str, ...] = ()
    artifact_id: str = ''
    field: str = ''
    value: Any = None
    approval_token: str = ''
    reason: str = ''
    raw_args: dict[str, Any] = Field(default_factory=dict)
