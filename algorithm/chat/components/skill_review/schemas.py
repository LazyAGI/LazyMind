from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, model_validator


ReviewAction = Literal['create', 'modify', 'replace', 'merge', 'skip']
ReviewStatus = Literal['completed', 'skipped', 'failed', 'running']


class SkillReviewRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    # TODO: The final API contract is still open. These fields are kept for
    # compatibility with the first skeleton but are not required by the current
    # algorithm, which reads all sessions from read_session().
    min_user_turns: int = Field(default=3, ge=0)
    min_tool_turns: int = Field(default=2, ge=0)
    resume: bool = True
    force: bool = False
    llm_config: Optional[Dict[str, Any]] = None
    emb_config: Optional[Dict[str, Any]] = None


class SessionMessage(BaseModel):
    model_config = ConfigDict(extra='allow')

    role: str
    content: str = ''
    created_at: Optional[str] = None
    tool_name: Optional[str] = None
    skill_name: Optional[str] = None
    raw: Dict[str, Any] = Field(default_factory=dict)


class SessionData(BaseModel):
    session_id: str
    source_db: str
    tables: List[str] = Field(default_factory=list)
    messages: List[SessionMessage] = Field(default_factory=list)
    metadata: Dict[str, Any] = Field(default_factory=dict)


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
    crafts: List[SkillDraft] = Field(default_factory=list)


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


class SkillReviewDecision(BaseModel):
    action: ReviewAction
    reason: str
    confidence: float = Field(default=0.0, ge=0.0, le=1.0)
    target_skill: Optional[Dict[str, str]] = None
    suggestions: List[Dict[str, str]] = Field(default_factory=list)
    candidate: Optional[CandidateSkill] = None


class SkillReviewResult(BaseModel):
    session_id: str
    status: ReviewStatus
    qualified: bool
    trigger: Dict[str, Any] = Field(default_factory=dict)
    candidates: List[SkillReviewDecision] = Field(default_factory=list)
    artifacts: Dict[str, str] = Field(default_factory=dict)
    error: Optional[str] = None


class UserSkillReviewResult(BaseModel):
    user_id: str
    status: ReviewStatus
    qualified: bool
    session_count: int = 0
    qualified_session_count: int = 0
    trigger: Dict[str, Any] = Field(default_factory=dict)
    candidates: List[SkillReviewDecision] = Field(default_factory=list)
    artifacts: Dict[str, Any] = Field(default_factory=dict)
    error: Optional[str] = None


class SkillReviewBatchResult(BaseModel):
    review_id: str
    status: ReviewStatus
    qualified: bool
    user_count: int = 0
    qualified_user_count: int = 0
    candidates: List[SkillReviewDecision] = Field(default_factory=list)
    users: List[UserSkillReviewResult] = Field(default_factory=list)
    artifacts: Dict[str, Any] = Field(default_factory=dict)
    error: Optional[str] = None


class StageManifest(BaseModel):
    session_id: str
    status: ReviewStatus = 'running'
    current_stage: Optional[str] = None
    completed_stages: List[str] = Field(default_factory=list)
    input_hash: str = ''
    model_config_hash: str = ''
    error: Optional[str] = None
    created_at: str
    updated_at: str
