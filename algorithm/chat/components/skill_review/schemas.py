from __future__ import annotations

from datetime import datetime
from typing import Any, Dict, List, Literal, Optional

from pydantic import BaseModel, Field, model_validator


class SkillReviewRequest(BaseModel):
    start_time: datetime
    end_time: datetime
    min_user_turns: int = Field(default=2, ge=0)
    min_tool_turns: int = Field(default=5, ge=0)

    @model_validator(mode='after')
    def validate_time_range(self) -> 'SkillReviewRequest':
        if self.start_time > self.end_time:
            raise ValueError('start_time must be earlier than or equal to end_time')
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
    called_skills: List[str] = Field(default_factory=list)
    steps: List[TrajectoryStep] = Field(default_factory=list)
    steps_text: str = ''
    qualified: bool = False
    skip_reason: Optional[str] = None


class ContextualDescription(BaseModel):
    task_goal: str = ''
    applicable_scenario: str = ''
    execution_summary: str = ''
    key_result: str = ''
    environment: Dict[str, Any] = Field(default_factory=dict)


class RefinedTrajectory(BaseModel):
    steps: List[TrajectoryStep] = Field(default_factory=list)


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
    contextual_description: ContextualDescription
    refined_trajectory: RefinedTrajectory
    guidelines: GuidelineSet


class TaskCluster(BaseModel):
    task_scope: str
    drafts: List[SkillDraft] = Field(default_factory=list)


class SkillOutlineStep(BaseModel):
    step_name: str
    action_goal: str
    branch_conditions: List[str] = Field(default_factory=list)
    expected_state: str = ''


class SkillOutline(BaseModel):
    skill_name: str
    applicable_scenario: str
    sop: List[SkillOutlineStep] = Field(default_factory=list)


class CandidateSkill(BaseModel):
    skill_name: str
    category: str = 'general'
    applicable_scenario: str
    content: str
    outline: SkillOutline


class SkillReviewResolution(BaseModel):
    id: str = Field(..., min_length=1)
    skill_name: str = Field(..., min_length=1)
    type: Literal['new', 'patch']
    skill_content: Dict[str, Any]
    suggestion: Optional[str] = None


class UserSkillReviewResult(BaseModel):
    user_id: str
    status: Literal['completed', 'skipped', 'failed', 'running']
    qualified: bool
    session_count: int = 0
    qualified_session_count: int = 0
    trigger: Dict[str, Any] = Field(default_factory=dict)
    candidates: List[SkillReviewResolution] = Field(default_factory=list)
    artifacts: Dict[str, Any] = Field(default_factory=dict)
    error: Optional[str] = None


class SkillReviewBatchResult(BaseModel):
    success: bool
    inserted_count: int = 0
    error: Optional[str] = None


class StageManifest(BaseModel):
    session_id: str
    status: Literal['completed', 'skipped', 'failed', 'running'] = 'running'
    current_stage: Optional[str] = None
    completed_stages: List[str] = Field(default_factory=list)
    input_hash: str = ''
    error: Optional[str] = None
    created_at: str
    updated_at: str
