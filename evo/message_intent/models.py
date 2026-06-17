from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field

IntentKind = Literal[
    'status_query',
    'list_failed_cases',
    'read_case_result',
    'read_report_section',
    'explain_current_gate',
    'continue_flow',
    'pause_flow',
    'cancel_flow',
    'retry_failed',
    'rerun_case',
    'patch_artifact',
    'approve_pending',
    'reject_pending',
    'cancel_pending',
    'general_chat',
    'unsupported',
]


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class PlannerIntent(StrictModel):
    kind: IntentKind
    case_id: str = ''
    case_ref: str = ''
    case_ids: tuple[str, ...] = ()
    artifact_id: str = ''
    field: str = ''
    value: Any = None
    approval_token: str = ''
    reason: str = ''


class PlannerOutput(StrictModel):
    status: Literal['intent', 'done', 'clarification']
    consumed_text: str = ''
    consumed_message_ids: tuple[str, ...] = ()
    consumed_prefix_len: int = 0
    intent: PlannerIntent = Field(default_factory=lambda: PlannerIntent(kind='unsupported'))
    confidence: float = Field(default=1.0, ge=0.0, le=1.0)
    needs_clarification: str = ''


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
