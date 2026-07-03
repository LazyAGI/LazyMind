from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator


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
                  'unknown_field', 'immutable_field', 'cross_thread_reference']
    message: str = Field(max_length=500)


class PendingApproval(StrictModel):
    approval_token: str = Field(max_length=80)
    expires_at: float
    origin_message_id: str = Field(max_length=160)
    base_observation_hash: str = Field(min_length=64, max_length=64)
    intent_ref: MessageContentRef
    compiled_ref: MessageContentRef


class PlannedAction(StrictModel):
    kind: Literal['flow', 'query', 'mutation', 'config_patch', 'approval', 'clarify', 'final']
    command: Literal['', 'continue', 'pause', 'resume', 'cancel', 'retry'] = ''
    query: Literal['', 'progress_snapshot', 'read_step_root', 'read_case_artifact'] = ''
    mutation: Literal['', 'edit_artifact', 'rerun_case_stage', 'rerun_step', 'invalidate_from_step'] = ''
    target: Literal['', 'run_config', 'source_config', 'target_config', 'eval_policy',
                    'repair_policy', 'candidate_config'] = ''
    decision: Literal['', 'approve', 'reject', 'amend', 'replace', 'unclear'] = ''
    step: str = ''
    until_step: str = ''
    case_id: str = ''
    case_kind: str = ''
    stage: str = ''
    artifact_ref: list[Any] = Field(default_factory=list)
    pointer: str = ''
    value: Any = None
    approval_token: str = ''
    message: str = ''

    @model_validator(mode='after')
    def validate_action(self) -> 'PlannedAction':
        ok = {
            'flow': bool(self.command),
            'query': self.query == 'progress_snapshot'
            or (self.query == 'read_step_root' and self.step)
            or (self.query == 'read_case_artifact' and self.case_id and self.case_kind),
            'mutation': (self.mutation == 'rerun_case_stage' and self.case_id and self.stage)
            or (self.mutation in {'rerun_step', 'invalidate_from_step'} and self.step)
            or (self.mutation == 'edit_artifact' and self.artifact_ref and self.pointer),
            'config_patch': bool(self.target and self.pointer),
            'approval': bool(self.decision),
            'clarify': True,
            'final': True,
        }
        if not ok[self.kind]:
            raise ValueError(f'{self.kind} action is missing required fields')
        fields = {
            'flow': {'command', 'until_step'}, 'query': {'query', 'step', 'case_id', 'case_kind'},
            'mutation': {'mutation', 'step', 'case_id', 'stage', 'artifact_ref', 'pointer', 'value'},
            'config_patch': {'target', 'pointer', 'value', 'message'},
            'approval': {'decision', 'approval_token', 'message'}, 'clarify': {'message'}, 'final': {'message'},
        }[self.kind] | {'kind'}
        dirty = {
            name for name in self.model_fields
            if name not in fields and getattr(self, name) not in ('', None, [])
        }
        if dirty:
            raise ValueError(f'{self.kind} action has unrelated fields: {sorted(dirty)}')
        return self


class TurnPlan(StrictModel):
    turn_decision: Literal['next_action', 'needs_input', 'needs_approval', 'final']
    active_agenda: list[str] = Field(default_factory=list)
    next_action: PlannedAction | None = None
    user_message_effect: Literal['append', 'amend', 'replace', 'cancel', 'none'] = 'none'

    @model_validator(mode='after')
    def validate_decision(self) -> 'TurnPlan':
        action = self.next_action
        if self.turn_decision == 'next_action':
            if action is None or action.kind in {'clarify', 'final'}:
                raise ValueError('next_action requires an executable action')
        elif self.turn_decision == 'needs_input':
            self.next_action = action or PlannedAction(kind='clarify')
            if self.next_action.kind != 'clarify':
                raise ValueError('needs_input requires clarify')
        elif self.turn_decision == 'final':
            self.next_action = action or PlannedAction(kind='final')
            if self.next_action.kind != 'final':
                raise ValueError('final requires final')
        elif action is None or action.kind in {'clarify', 'final'}:
            raise ValueError('needs_approval requires approval or executable action')
        return self


class MessageTurnResult(StrictModel):
    thread_id: str
    turn_id: str
    message_id: str
    turn_decision: Literal['needs_input', 'needs_approval', 'action_executed', 'query_answered', 'final', 'rejected']
    assistant_text: str = ''
    observation_ref: MessageContentRef | None = None
    pending_approval_ref: MessageContentRef | None = None
    action_receipt_ref: MessageContentRef | None = None
