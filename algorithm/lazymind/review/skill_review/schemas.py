from __future__ import annotations

from datetime import datetime
from typing import Any, Dict, List, Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, model_validator


class SkillReviewRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    requestid: str = Field(..., min_length=1)
    start_time: datetime
    end_time: datetime
    user_id: Optional[str] = None
    min_user_turns: int = Field(default=2, ge=0)
    min_tool_turns: int = Field(default=5, ge=0)
    artifact_dir: Optional[str] = None
    pending_skill_ids: List[str] = Field(default_factory=list)
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


class ContextualDescription(BaseModel):
    task_goal: str = ''
    applicable_scenario: str = ''
    execution_summary: str = ''
    key_result: str = ''
    environment: Dict[str, Any] = Field(default_factory=dict)


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
    contextual_description: ContextualDescription
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


class CandidateSkill(BaseModel):
    skill_name: str
    category: str = 'general'
    source_trajectories: List[str] = Field(default_factory=list)
    source_skills: Dict[str, str] = Field(default_factory=dict)
    applicable_scenario: str
    content: str
    outline: SkillOutline


class CandidateSkillLLMOutput(BaseModel):
    skill_name: str
    category: str = 'general'
    applicable_scenario: str
    content: str


class SkillReviewResolution(BaseModel):
    id: str = Field(..., min_length=1)
    skill_name: str = Field(..., min_length=1)
    type: Literal['new', 'patch']
    review_status: Literal['pending', 'accepted', 'rejected', 'expired'] = 'pending'
    userid: str = ''
    requestid: str = ''
    skill_content: str = ''
    summary: Optional[str] = None
    time: str = ''


class UserSkillReviewResult(BaseModel):
    user_id: str
    status: Literal['completed', 'skipped', 'failed', 'running']
    qualified: bool
    session_count: int = 0
    qualified_session_count: int = 0
    trigger: Dict[str, Any] = Field(default_factory=dict)
    candidates: List[SkillReviewResolution] = Field(default_factory=list)
    error: Optional[str] = None


class SkillReviewBatchResult(BaseModel):
    success: bool
    inserted_count: int = 0
    error: Optional[str] = None


class SkillReviewRunStat(BaseModel):
    id: str = Field(..., min_length=1)
    requestid: str = ''
    userid: str = ''
    status: Literal['completed', 'skipped', 'failed', 'running']
    started_at: str
    duration_ms: int = 0
    summary: Dict[str, Any] = Field(default_factory=dict)
