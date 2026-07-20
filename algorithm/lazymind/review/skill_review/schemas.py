from __future__ import annotations

from datetime import datetime
from typing import Any, Dict, List, Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, model_validator

from lazymind.common.skill_document import require_valid_skill_document
from lazymind.common.skill_storage_key import parse_skill_storage_key


class SkillReviewRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    requestid: str = Field(..., min_length=1)
    start_time: datetime
    end_time: datetime
    user_id: Optional[str] = None
    min_user_turns: int = Field(default=2, ge=0)
    min_tool_turns: int = Field(default=5, ge=0)
    artifact_dir: Optional[str] = None
    model_configs: Dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode='after')
    def validate_time_range(self) -> 'SkillReviewRequest':
        if self.start_time > self.end_time:
            raise ValueError('start_time must be earlier than or equal to end_time')
        normalized_user_id = str(self.user_id).strip() if self.user_id is not None else ''
        self.user_id = normalized_user_id or None
        return self


class TrajectoryStep(BaseModel):
    step_index: int
    role: str
    action: str
    state: str = ''
    tool_name: Optional[str] = None
    skill_name: Optional[str] = None


class Trajectory(BaseModel):
    session_id: str
    user_turns: int
    tool_turns: int
    called_tools: List[str] = Field(default_factory=list)
    called_skills: Dict[str, str] = Field(default_factory=dict)
    steps: List[TrajectoryStep] = Field(default_factory=list)
    steps_text: str = ''
    qualified: bool = False


class ClusterSignature(BaseModel):
    intent: str
    procedure: List[str] = Field(default_factory=list)
    boundaries: str


class RefinedTrajectory(BaseModel):
    steps: List[Any] = Field(default_factory=list)


class SuccessGuideline(BaseModel):
    related_step: Optional[int] = None
    guideline: str


class FailureGuideline(BaseModel):
    related_step: Optional[int] = None
    guideline: str


class GuidelineSet(BaseModel):
    success_patterns: List[SuccessGuideline] = Field(default_factory=list)
    failure_patterns: List[FailureGuideline] = Field(default_factory=list)


class SkillDraft(BaseModel):
    session_id: str
    cluster_signature: ClusterSignature
    refined_trajectory: RefinedTrajectory
    guidelines: GuidelineSet
    source_trajectory: str = ''
    source_skills: Dict[str, str] = Field(default_factory=dict)


class TaskCluster(BaseModel):
    task_scope: str
    drafts: List[SkillDraft] = Field(default_factory=list)


class SkillOutlineStep(BaseModel):
    step_name: str
    action_goal: str
    branch_conditions: List[str] = Field(default_factory=list)


class SkillOutline(BaseModel):
    skill_name: str
    applicable_scenario: str
    sop: List[SkillOutlineStep] = Field(default_factory=list)


def _validate_candidate_document(skill_name: str, content: str) -> None:
    require_valid_skill_document(content, expected_name=skill_name)


class CandidateSkill(BaseModel):
    skill_name: str
    source_trajectories: List[str] = Field(default_factory=list)
    source_skills: Dict[str, str] = Field(default_factory=dict)
    applicable_scenario: str
    content: str
    outline: SkillOutline

    @model_validator(mode='after')
    def validate_content(self) -> 'CandidateSkill':
        _validate_candidate_document(self.skill_name, self.content)
        return self


class CandidateSkillLLMOutput(BaseModel):
    skill_name: str
    applicable_scenario: str
    content: str

    @model_validator(mode='after')
    def validate_content(self) -> 'CandidateSkillLLMOutput':
        _validate_candidate_document(self.skill_name, self.content)
        return self


class SkillReviewResolution(BaseModel):
    id: str = Field(..., min_length=1)
    skill_name: str = Field(..., min_length=1)
    target_skill_key: str = ''
    type: Literal['new', 'patch']
    review_status: Literal['pending', 'accepted', 'rejected', 'expired'] = 'pending'
    userid: str = ''
    requestid: str = ''
    skill_content: str = ''
    summary: Optional[str] = None
    time: str = ''

    @model_validator(mode='after')
    def validate_target_skill_key(self) -> 'SkillReviewResolution':
        self.target_skill_key = self.target_skill_key.strip()
        if self.type == 'new' and self.target_skill_key:
            raise ValueError('target_skill_key must be empty for new skill resolutions')
        if self.type == 'patch' and not self.target_skill_key:
            raise ValueError('target_skill_key is required for patch skill resolutions')
        if self.type == 'patch':
            category, name = parse_skill_storage_key(self.target_skill_key)
            self.target_skill_key = f'{category}/{name}'
        return self


class UserSkillReviewResult(BaseModel):
    user_id: str
    status: Literal['completed', 'skipped', 'failed']
    qualified: bool
    session_count: int = 0
    qualified_session_count: int = 0
    trigger: Dict[str, Any] = Field(default_factory=dict)
    candidates: List[SkillReviewResolution] = Field(default_factory=list)
    error: Optional[str] = None


class SkillReviewBatchResult(BaseModel):
    success: bool
    inserted_count: int = 0
    taskid: str = ''
    error: Optional[str] = None


class SkillReviewRunStat(BaseModel):
    id: str = Field(..., min_length=1)
    requestid: str = ''
    userid: str = ''
    status: Literal[
        'pending',
        'completed',
        'skipped',
        'failed',
        'review_draft',
        'review_cluster',
        'review_miner',
        'review_solution',
        'review_apply',
        'organize_plan',
        'organize_draft',
        'organize_apply',
    ]
    started_at: str
    duration_ms: int = 0
    summary: Dict[str, Any] = Field(default_factory=dict)
