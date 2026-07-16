from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, model_validator

from lazymind.review.skill_organize.config import MAX_SKILL_ORGANIZE_LIMIT


class SkillOrganizeRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    requestid: str = Field(..., min_length=1)
    user_id: str = Field(..., min_length=1)
    skills: List[str] = Field(default_factory=list, max_length=MAX_SKILL_ORGANIZE_LIMIT)
    artifact_dir: Optional[str] = None
    model_configs: Dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode='after')
    def validate_payload(self) -> 'SkillOrganizeRequest':
        self.requestid = self.requestid.strip()
        self.user_id = self.user_id.strip()
        raw_skills = [str(item).strip() for item in self.skills if str(item).strip()]
        if not raw_skills:
            raise ValueError("'skills' must contain at least one skill key in category/name format.")
        normalized_skills = [_normalize_category_name_key(item) for item in raw_skills]
        invalid_skills = [item for item, normalized in zip(raw_skills, normalized_skills) if normalized is None]
        if invalid_skills:
            raise ValueError(f"'skills' entries must use category/name format: {invalid_skills}")
        self.skills = [item for item in normalized_skills if item is not None]
        if len(set(self.skills)) != len(self.skills):
            raise ValueError("'skills' must not contain duplicate entries.")
        return self


class SourceSkill(BaseModel):
    name: str
    category: str = ''
    content: str


class SkillSummary(BaseModel):
    name: str
    category: str = ''
    description: str = ''
    core_steps: List[str] = Field(default_factory=list)


class SkillPlan(BaseModel):
    type: Literal['keep', 'refactor', 'merge', 'delete_duplicate']
    source_names: List[str] = Field(default_factory=list)
    target_name: str = ''
    target_category: str = ''
    target_description: str = ''
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
    name: str
    category: str = ''
    content: str


class MaterializedSkillContent(BaseModel):
    content: str


class SkillFsDraft(BaseModel):
    delete_names: List[str] = Field(default_factory=list)
    upsert_skills: List[SkillFsDraftItem] = Field(default_factory=list)


class SkillOrganizeResult(BaseModel):
    success: bool
    requestid: str
    taskid: str = ''
    inserted_count: int = 0
    artifact_dir: str = ''
    error: Optional[str] = None


def _normalize_category_name_key(value: str) -> Optional[str]:
    parts = [part.strip() for part in value.split('/')]
    if len(parts) != 2 or not all(parts):
        return None
    return f'{parts[0]}/{parts[1]}'
