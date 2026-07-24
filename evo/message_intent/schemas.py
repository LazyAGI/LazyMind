from __future__ import annotations

from typing import Annotated, Any, Literal

from pydantic import BaseModel, ConfigDict, Field, TypeAdapter, model_validator


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class MessageContentRef(StrictModel):
    uri: str = Field(max_length=512)
    sha256: str = Field(min_length=64, max_length=64)
    byte_size: int = Field(ge=0)


class MessageRequest(StrictModel):
    message_id: str = Field(default='', max_length=160)
    text: str = Field(min_length=1, max_length=20000)


class ConfigValidationIssue(StrictModel):
    path: str = Field(max_length=240)
    code: Literal['missing_required', 'invalid_type', 'invalid_url', 'out_of_range',
                  'unknown_field', 'invalid_value', 'immutable_field', 'cross_thread_reference']
    message: str = Field(max_length=500)


class PendingApproval(StrictModel):
    approval_token: str = Field(max_length=80)
    expires_at: float
    origin_message_id: str = Field(max_length=160)
    base_observation_hash: str = Field(min_length=64, max_length=64)
    intent_ref: MessageContentRef
    compiled_ref: MessageContentRef


class FlowAction(StrictModel):
    kind: Literal['flow']
    command: Literal['continue', 'pause', 'resume', 'cancel', 'retry']
    until_step: str = ''


class QueryAction(StrictModel):
    kind: Literal['query']
    query: Literal['progress_snapshot', 'read_step_root', 'read_case_artifact']
    step: str = ''
    case_id: str = ''
    case_kind: str = ''

    @model_validator(mode='after')
    def validate_action(self) -> 'QueryAction':
        ok = self.query == 'progress_snapshot' or (self.query == 'read_step_root' and self.step) or (
            self.query == 'read_case_artifact' and self.case_id and self.case_kind
        )
        if not ok:
            raise ValueError('query action is missing required fields')
        return self


class MutationAction(StrictModel):
    kind: Literal['mutation']
    mutation: Literal['edit_artifact', 'rerun_case_stage', 'rerun_step', 'invalidate_from_step']
    step: str = ''
    case_id: str = ''
    stage: str = ''
    artifact_ref: list[Any] = Field(default_factory=list)
    pointer: str = ''
    value: Any = None

    @model_validator(mode='after')
    def validate_action(self) -> 'MutationAction':
        ok = (self.mutation == 'rerun_case_stage' and self.case_id and self.stage) or (
            self.mutation in {'rerun_step', 'invalidate_from_step'} and self.step
        ) or (self.mutation == 'edit_artifact' and self.artifact_ref and self.pointer)
        if not ok:
            raise ValueError('mutation action is missing required fields')
        return self


class ConfigPatchAction(StrictModel):
    kind: Literal['config_patch']
    target: Literal['run_config', 'source_config', 'target_config', 'eval_policy',
                    'repair_policy', 'candidate_config']
    pointer: str
    value: Any = None
    message: str = ''


class ApprovalAction(StrictModel):
    kind: Literal['approval']
    decision: Literal['approve', 'reject', 'amend', 'replace', 'unclear']
    approval_token: str = ''
    message: str = ''


class ClarifyAction(StrictModel):
    kind: Literal['clarify']
    message: str = ''


class FinalAction(StrictModel):
    kind: Literal['final']
    message: str = ''


PlannedAction = Annotated[
    FlowAction | QueryAction | MutationAction | ConfigPatchAction | ApprovalAction | ClarifyAction | FinalAction,
    Field(discriminator='kind'),
]
PlannedActionAdapter = TypeAdapter(PlannedAction)


def parse_planned_action(value: Any) -> PlannedAction:
    return PlannedActionAdapter.validate_python(value)


class TurnPlan(StrictModel):
    turn_decision: Literal['next_action', 'needs_input', 'needs_approval', 'final']
    active_agenda: list[str] = Field(default_factory=list)
    next_action: PlannedAction | None = None
    user_message_effect: Literal['append', 'amend', 'replace', 'cancel', 'none'] = 'none'
    assistant_text: str = Field(default='', max_length=1000)

    @model_validator(mode='after')
    def validate_decision(self) -> 'TurnPlan':
        action = self.next_action
        if self.turn_decision == 'next_action':
            if action is None or action.kind in {'clarify', 'final'}:
                raise ValueError('next_action requires an executable action')
        elif self.turn_decision == 'needs_input':
            self.next_action = action or ClarifyAction(message=self.assistant_text)
            if self.next_action.kind != 'clarify':
                raise ValueError('needs_input requires clarify')
            if not self.next_action.message:
                self.next_action.message = self.assistant_text
        elif self.turn_decision == 'final':
            self.next_action = action or FinalAction(message=self.assistant_text)
            if self.next_action.kind != 'final':
                raise ValueError('final requires final')
            if not self.next_action.message:
                self.next_action.message = self.assistant_text
        elif action is None or action.kind in {'clarify', 'final'}:
            raise ValueError('needs_approval requires approval or executable action')
        return self


class MessageTurnResult(StrictModel):
    thread_id: str
    turn_id: str
    message_id: str
    command_id: str = ''
    turn_decision: Literal[
        'needs_input',
        'needs_approval',
        'action_submitted',
        'action_executed',
        'query_answered',
        'final',
        'rejected',
    ]
    assistant_text: str = ''
    observation_ref: MessageContentRef | None = None
    pending_approval_ref: MessageContentRef | None = None
    action_receipt_ref: MessageContentRef | None = None


class MessageHistoryItem(StrictModel):
    turn_id: str
    message_id: str
    command_id: str = ''
    status: str
    user_text: str = ''
    assistant_text: str = ''
    turn_decision: str = ''
    observation_ref: MessageContentRef | None = None
    pending_approval_ref: MessageContentRef | None = None
    action_receipt_ref: MessageContentRef | None = None


class MessageHistoryResponse(StrictModel):
    thread_id: str
    items: list[MessageHistoryItem]
    next_page_token: str = ''
