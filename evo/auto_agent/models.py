from __future__ import annotations

from enum import Enum
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field

from .intervention import AutoIntervention

AutoActionKind = Literal[
    'noop',
    'start_flow',
    'continue_flow',
    'pause_flow',
    'cancel_flow',
    'retry_failed',
    'send_message',
    'approve_pending',
    'reject_pending',
    'cancel_pending',
    'stop_agent',
]


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class CommandStatus(str, Enum):
    OK = 'ok'
    RUNNING = 'running'
    ERROR = 'error'


class PortCommandResult(StrictModel):
    status: CommandStatus
    raw: dict[str, Any] = Field(default_factory=dict)
    error: str = ''


class AutoAgentConfig(StrictModel):
    enabled: bool = True
    tick_interval_s: float = Field(default=1.0, ge=0.1, le=300.0)
    start_when_idle: bool = True
    auto_continue: bool = True
    max_continue_actions: int = Field(default=20, ge=1, le=1000)
    retry_failed_enabled: bool = True
    retry_failed_max_per_step: int = Field(default=2, ge=0, le=20)
    max_action_failures: int = Field(default=3, ge=1, le=20)
    rerun_case_enabled: bool = True
    rerun_case_max_per_ref: int = Field(default=1, ge=0, le=20)
    patch_artifact_enabled: bool = True
    patch_judge_score_enabled: bool = True
    auto_approve: Literal['never', 'evidence_backed', 'all_mutations'] = 'evidence_backed'
    auto_cancel_enabled: bool = False
    pause_on_risk: bool = True


class ActiveApproval(StrictModel):
    approval_token: str
    intent_kind: str
    risk_level: str
    status: str = 'active'
    expected_refs: tuple[str, ...] = ()
    expires_at: float = 0.0


class AutoObservation(StrictModel):
    thread_id: str
    mode: str = 'interactive'
    status: str = 'idle'
    current_step: str = ''
    completed_steps: tuple[str, ...] = ()
    stale_steps: tuple[str, ...] = ()
    pending_checkpoint: dict[str, Any] | None = None
    latest_refs: dict[str, str] = Field(default_factory=dict)
    facts: dict[str, Any] = Field(default_factory=dict)
    active_approval: ActiveApproval | None = None
    hash: str = ''


class AutoAction(StrictModel):
    kind: AutoActionKind
    reason: str
    target: str = ''
    message: str = ''
    command_id: str = ''
    approval_token: str = ''
    intervention: AutoIntervention | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)


class AutoDecision(StrictModel):
    observation_hash: str
    action: AutoAction
    reason: str


class AutoActionRecord(StrictModel):
    action_id: str
    kind: AutoActionKind
    target: str = ''
    status: str = ''
    reason: str = ''
    response: dict[str, Any] = Field(default_factory=dict)
    created_at: float


class AutoAgentState(StrictModel):
    thread_id: str
    running: bool = False
    config: AutoAgentConfig = Field(default_factory=AutoAgentConfig)
    stop_reason: str = ''
    last_observation_hash: str = ''
    last_decision: dict[str, Any] = Field(default_factory=dict)
    completed_action_ids: tuple[str, ...] = ()
    action_failure_counts: dict[str, int] = Field(default_factory=dict)
    continue_count: int = 0
    retry_counts: dict[str, int] = Field(default_factory=dict)
    intervention_counts: dict[str, int] = Field(default_factory=dict)
    auto_pending_approvals: tuple[str, ...] = ()
    records: tuple[AutoActionRecord, ...] = ()
