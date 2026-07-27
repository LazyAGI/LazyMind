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
    code: Literal[
        'missing_required', 'invalid_type', 'invalid_url', 'out_of_range',
        'unknown_field', 'invalid_value', 'immutable_field',
    ]
    message: str = Field(max_length=500)


class PendingConfirmation(StrictModel):
    confirmation_token: str = Field(max_length=80)
    expires_at: float
    origin_message_id: str = Field(max_length=160)
    base_observation_hash: str = Field(min_length=64, max_length=64)
    intent_ref: MessageContentRef


class FlowAction(StrictModel):
    kind: Literal['flow']
    command: Literal['start', 'approve', 'pause', 'resume', 'retry', 'cancel']
    stage: str = ''

    @model_validator(mode='after')
    def validate_stage(self) -> FlowAction:
        if self.command == 'approve' and not self.stage:
            raise ValueError('approve requires stage')
        if self.command not in {'approve', 'retry'} and self.stage:
            raise ValueError('stage is only valid for approve or retry')
        return self


class QueryAction(StrictModel):
    kind: Literal['query']
    query: Literal['progress', 'stage_result', 'artifact', 'artifact_history']
    stage: str = ''
    artifact_id: str = ''
    partition_key: str = ''
    version: int | None = Field(default=None, ge=1)

    @model_validator(mode='after')
    def validate_query(self) -> QueryAction:
        if self.query == 'progress':
            if self.stage or self.artifact_id or self.partition_key or self.version is not None:
                raise ValueError('progress does not accept a query target')
            return self
        if self.query == 'stage_result':
            if not self.stage:
                raise ValueError('stage_result requires stage')
            if self.artifact_id or self.partition_key or self.version is not None:
                raise ValueError('stage_result only accepts stage')
            return self
        if not self.artifact_id:
            raise ValueError(f'{self.query} requires artifact_id')
        if self.stage:
            raise ValueError(f'{self.query} does not accept stage')
        if self.version is not None and self.query != 'artifact':
            raise ValueError('version is only valid for artifact')
        return self


class ArtifactAction(StrictModel):
    kind: Literal['artifact']
    command: Literal['patch', 'replace', 'retry', 'rollback']
    artifact_id: str = Field(min_length=1)
    partition_key: str = ''
    pointer: str = ''
    value: Any = None
    version: int | None = Field(default=None, ge=1)

    @model_validator(mode='after')
    def validate_command(self) -> ArtifactAction:
        has_value = 'value' in self.model_fields_set
        if self.command == 'patch':
            if not self.pointer or not has_value:
                raise ValueError('patch requires pointer and value')
            if self.version is not None:
                raise ValueError('patch does not accept version')
            return self
        if self.command == 'replace':
            if not has_value:
                raise ValueError('replace requires value')
            if self.pointer or self.version is not None:
                raise ValueError('replace only accepts value')
            return self
        if self.command == 'rollback':
            if self.version is None:
                raise ValueError('rollback requires version')
            if self.pointer or (has_value and self.value is not None):
                raise ValueError('rollback only accepts version')
            return self
        if self.pointer or (has_value and self.value is not None) or self.version is not None:
            raise ValueError('retry only accepts an artifact key')
        return self


class CaseAction(StrictModel):
    kind: Literal['case']
    command: Literal['add', 'delete']
    case_id: str = Field(min_length=1)
    case: dict[str, Any] | None = None
    instruction: str = ''
    required_chunks: list[str] = Field(default_factory=list)

    @model_validator(mode='after')
    def validate_command(self) -> CaseAction:
        if self.command == 'add' and self.case is None and not self.instruction:
            raise ValueError('add case requires a complete case or instruction')
        if self.command == 'add' and self.case is not None and (
            self.instruction or self.required_chunks
        ):
            raise ValueError('complete case and generation guidance are mutually exclusive')
        if self.command == 'delete' and (
            self.case is not None or self.instruction or self.required_chunks
        ):
            raise ValueError('delete case only accepts case_id')
        return self


class ConfigPatchAction(StrictModel):
    kind: Literal['config_patch']
    target: Literal[
        'run_config', 'source_config', 'target_config', 'eval_policy',
        'repair_policy', 'candidate_config',
    ]
    pointer: str
    value: Any = None


class ConfirmationAction(StrictModel):
    kind: Literal['confirmation']
    decision: Literal['confirm', 'reject', 'amend', 'replace', 'unclear']
    confirmation_token: str = ''
    message: str = ''

    @model_validator(mode='after')
    def validate_confirmation(self) -> ConfirmationAction:
        if self.decision == 'confirm' and not self.confirmation_token:
            raise ValueError('confirm requires confirmation_token')
        return self


class ClarifyAction(StrictModel):
    kind: Literal['clarify']
    message: str = ''


class FinalAction(StrictModel):
    kind: Literal['final']
    message: str = ''


PlannedAction = Annotated[
    FlowAction | QueryAction | ArtifactAction | CaseAction | ConfigPatchAction
    | ConfirmationAction | ClarifyAction | FinalAction,
    Field(discriminator='kind'),
]
PlannedActionAdapter = TypeAdapter(PlannedAction)


def parse_planned_action(value: Any) -> PlannedAction:
    return PlannedActionAdapter.validate_python(value)


class TurnPlan(StrictModel):
    turn_decision: Literal['next_action', 'needs_input', 'final']
    active_agenda: list[str] = Field(default_factory=list)
    next_action: PlannedAction | None = None
    user_message_effect: Literal['append', 'amend', 'replace', 'cancel', 'none'] = 'none'
    assistant_text: str = Field(default='', max_length=1000)

    @model_validator(mode='after')
    def validate_decision(self) -> TurnPlan:
        action = self.next_action
        if self.turn_decision == 'next_action':
            if action is None or action.kind in {'clarify', 'final'}:
                raise ValueError('next_action requires an executable action')
            return self
        if self.turn_decision == 'needs_input':
            if action is None:
                self.next_action = ClarifyAction(message=self.assistant_text)
            elif action.kind != 'clarify':
                raise ValueError('needs_input requires clarify')
            return self
        if action is None:
            self.next_action = FinalAction(message=self.assistant_text)
        elif action.kind != 'final':
            raise ValueError('final requires final')
        return self


class MessageTurnResult(StrictModel):
    thread_id: str
    turn_id: str
    message_id: str
    command_id: str = ''
    turn_decision: Literal[
        'needs_input', 'needs_confirmation', 'action_executed',
        'query_answered', 'final', 'rejected',
    ]
    assistant_text: str = ''
    observation_ref: MessageContentRef | None = None
    pending_confirmation_ref: MessageContentRef | None = None
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
    pending_confirmation_ref: MessageContentRef | None = None
    action_receipt_ref: MessageContentRef | None = None


class MessageHistoryResponse(StrictModel):
    thread_id: str
    items: list[MessageHistoryItem]
    next_page_token: str = ''


__all__ = [
    'ArtifactAction', 'CaseAction', 'ClarifyAction', 'ConfigPatchAction',
    'ConfigValidationIssue', 'ConfirmationAction', 'FinalAction', 'FlowAction',
    'MessageContentRef', 'MessageHistoryItem', 'MessageHistoryResponse',
    'MessageRequest', 'MessageTurnResult', 'PendingConfirmation', 'PlannedAction',
    'QueryAction', 'TurnPlan', 'parse_planned_action',
]
