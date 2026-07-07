from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, model_validator

from lazymind.review.skill_organize.config import MAX_SKILL_ORGANIZE_LIMIT


class SkillOrganizeRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    requestid: str = Field(..., min_length=1)
    user_id: str = Field(..., min_length=1)
    skills: List[str] = Field(default_factory=list, max_length=MAX_SKILL_ORGANIZE_LIMIT)
    fs_base_url: str = ''
    artifact_dir: Optional[str] = None
    model_configs: Dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode='after')
    def validate_payload(self) -> 'SkillOrganizeRequest':
        self.requestid = self.requestid.strip()
        self.user_id = self.user_id.strip()
        self.fs_base_url = self.fs_base_url.strip()
        self.skills = [str(item).strip() for item in self.skills if str(item).strip()]
        if not self.skills:
            raise ValueError("'skills' must contain at least one skill name or path.")
        if len(set(self.skills)) != len(self.skills):
            raise ValueError("'skills' must not contain duplicate entries.")
        return self


class SourceSkill(BaseModel):
    name: str
    path: str
    category: str = ''
    content: str


class SkillSummary(BaseModel):
    name: str
    path: str
    category: str = ''
    description: str = ''
    applicable_scenario: str = ''
    core_steps: List[str] = Field(default_factory=list)


class SkillPlan(BaseModel):
    type: Literal['keep', 'refactor', 'merge', 'delete_duplicate']
    source_names: List[str] = Field(default_factory=list)
    source_paths: List[str] = Field(default_factory=list)
    target_name: str = ''
    target_path: str = ''
    target_category: str = ''
    target_description: str = ''
    target_applicable_scenario: str = ''
    step_handling_policy: Literal[
        'keep_steps',
        'minimally_adjust_steps',
        'merge_and_deduplicate_existing_steps',
        'none',
    ] = 'none'
    reason: str = ''


class SkillOrganizePlan(BaseModel):
    plans: List[SkillPlan] = Field(default_factory=list)


class SkillFsDraftItem(BaseModel):
    path: str
    content: str


class MaterializedSkillContent(BaseModel):
    content: str


class SkillFsDraft(BaseModel):
    delete_paths: List[str] = Field(default_factory=list)
    upsert_skills: List[SkillFsDraftItem] = Field(default_factory=list)


class SkillOrganizeResult(BaseModel):
    success: bool
    requestid: str
    taskid: str = ''
    inserted_count: int = 0
    artifact_dir: str = ''
    error: Optional[str] = None
