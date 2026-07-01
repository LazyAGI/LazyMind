from __future__ import annotations

from typing import Annotated, Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class EmptyArgs(StrictModel):
    pass


class NoActionAckArgs(StrictModel):
    summary: str = ''


class ChatArgs(StrictModel):
    topic: str = ''
    reply_intent: str = ''


class CaseRefArgs(StrictModel):
    case_ref: str = ''
    selector: str = ''
    cursor: str = ''
    max_chars: int = Field(default=1200, ge=200, le=4000)


class ReadReportArgs(StrictModel):
    artifact_ref: str = ''
    section: str = ''
    selector: str = ''
    checkpoint_ref: str = ''
    cursor: str = ''
    max_chars: int = Field(default=1200, ge=200, le=4000)


class PatchArtifactArgs(StrictModel):
    artifact_ref: str
    json_pointer: str
    value: Any

    @model_validator(mode='after')
    def require_patch_target(self) -> 'PatchArtifactArgs':
        if not self.artifact_ref.strip():
            raise ValueError('patch_artifact requires artifact_ref')
        if not self.json_pointer.strip():
            raise ValueError('patch_artifact requires json_pointer')
        return self


class ApprovalArgs(StrictModel):
    approval_token: str = ''


class RunControlArgs(StrictModel):
    action: Literal['continue', 'pause', 'cancel', 'retry_failed']


class RerunCaseArgs(StrictModel):
    case_ref: str = ''


class BoundedRunArgs(StrictModel):
    target_step_ref: str = ''
    stop_before_step_ref: str = ''
    pause_after_step_ref: str = ''

    @model_validator(mode='after')
    def require_boundary(self) -> 'BoundedRunArgs':
        if not any((self.target_step_ref, self.stop_before_step_ref, self.pause_after_step_ref)):
            raise ValueError('bounded_run requires at least one step boundary')
        return self


class NoActionAckIntent(StrictModel):
    kind: Literal['no_action_ack']
    args: NoActionAckArgs = Field(default_factory=NoActionAckArgs)


class ChatIntent(StrictModel):
    kind: Literal['chat']
    args: ChatArgs = Field(default_factory=ChatArgs)


class StatusIntent(StrictModel):
    kind: Literal['status_query']
    args: EmptyArgs = Field(default_factory=EmptyArgs)


class ReadCaseIntent(StrictModel):
    kind: Literal['read_case_result']
    args: CaseRefArgs


class ReadReportIntent(StrictModel):
    kind: Literal['read_report_section']
    args: ReadReportArgs = Field(default_factory=ReadReportArgs)


class ExplainGateIntent(StrictModel):
    kind: Literal['explain_current_gate']
    args: ReadReportArgs = Field(default_factory=ReadReportArgs)


class RunControlIntentFrame(StrictModel):
    kind: Literal['run_control']
    args: RunControlArgs


class BoundedRunIntent(StrictModel):
    kind: Literal['bounded_run']
    args: BoundedRunArgs


class RerunCaseIntent(StrictModel):
    kind: Literal['rerun_case']
    args: RerunCaseArgs


class PatchArtifactIntent(StrictModel):
    kind: Literal['patch_artifact']
    args: PatchArtifactArgs


class ApprovalIntent(StrictModel):
    kind: Literal['approval']
    args: ApprovalArgs = Field(default_factory=ApprovalArgs)
    action: Literal['approve', 'reject', 'cancel']


MessageIntentPayload = Annotated[
    NoActionAckIntent
    | ChatIntent
    | StatusIntent
    | ReadCaseIntent
    | ReadReportIntent
    | ExplainGateIntent
    | RunControlIntentFrame
    | BoundedRunIntent
    | RerunCaseIntent
    | PatchArtifactIntent
    | ApprovalIntent,
    Field(discriminator='kind'),
]


class SourceSpan(StrictModel):
    text: str = Field(min_length=1)


class IntentFrame(StrictModel):
    intent: MessageIntentPayload
    source: SourceSpan
    confidence: float = Field(default=1.0, ge=0.0, le=1.0)
    reason: str = ''


class ActiveAgenda(StrictModel):
    text: str = ''


class MessagePlan(StrictModel):
    schema_version: Literal['message_intent.v2.1']
    status: Literal['next_ops', 'clarification', 'done']
    current: IntentFrame | None
    active_agenda: ActiveAgenda
    clarification: str
    confidence: float = Field(ge=0.0, le=1.0)

    @model_validator(mode='after')
    def validate_status(self) -> 'MessagePlan':
        if self.status == 'next_ops' and self.current is None:
            raise ValueError('status next_ops requires current')
        if self.status != 'next_ops' and self.current is not None:
            raise ValueError('current is only allowed for status next_ops')
        if self.status == 'clarification' and not self.clarification.strip():
            raise ValueError('clarification status requires clarification text')
        if self.status == 'done' and self.active_agenda.text.strip():
            raise ValueError('done status requires empty active_agenda')
        return self


class ResolvedIntent(StrictModel):
    kind: str
    case_id: str = ''
    case_ref: str = ''
    case_ids: tuple[str, ...] = ()
    artifact_id: str = ''
    json_pointer: str = ''
    value: Any = None
    approval_token: str = ''
    reason: str = ''
    raw_args: dict[str, Any] = Field(default_factory=dict)
