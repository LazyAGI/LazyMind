from __future__ import annotations

from typing import Annotated, Any, Dict, List, Literal, Optional

from pydantic import AfterValidator, BaseModel, ConfigDict, Field, model_validator

from lazymind.common.skill.document import require_skill_name
from lazymind.common.skill.storage_key import parse_skill_key
from lazymind.review.skill_organize.config import MAX_SKILL_ORGANIZE_LIMIT


class SkillOrganizeRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    requestid: str = Field(..., min_length=1)
    user_id: str = Field(..., min_length=1)
    skills: List[str] = Field(default_factory=list, max_length=MAX_SKILL_ORGANIZE_LIMIT)
    mode: Literal['light', 'deep'] = 'light'
    artifact_dir: Optional[str] = None
    model_configs: Dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode='after')
    def validate_payload(self) -> 'SkillOrganizeRequest':
        self.requestid = self.requestid.strip()
        self.user_id = self.user_id.strip()
        raw_skills = [str(item).strip() for item in self.skills if str(item).strip()]
        if not raw_skills:
            raise ValueError("'skills' must contain at least one skill key in category/name format.")
        self.skills = [
            f'{category}/{name}'
            for category, name in (parse_skill_key(item) for item in raw_skills)
        ]
        if self.mode == 'deep' and any(parse_skill_key(key)[0] != 'internal' for key in self.skills):
            raise ValueError('deep organization only supports internal skills')
        if len(set(self.skills)) != len(self.skills):
            raise ValueError("'skills' must not contain duplicate entries.")
        return self


class SourceSkill(BaseModel):
    model_config = ConfigDict(use_enum_values=True)

    key: str
    category: Annotated[str, AfterValidator(require_skill_name)]
    name: str
    content: str
    search_metadata: Optional[Dict[str, Any]] = None


class SkillSummary(BaseModel):
    model_config = ConfigDict(use_enum_values=True)

    key: str
    category: Annotated[str, AfterValidator(require_skill_name)]
    name: str
    description: str = ''
    core_steps: List[str] = Field(default_factory=list)
    search_metadata: Dict[str, Any] = Field(default_factory=dict)


class SearchMetadata(BaseModel):
    model_config = ConfigDict(extra='forbid')

    field: Optional[str] = None
    tags: Optional[List[str]] = None
    aliases: Optional[List[str]] = None
    keywords: Optional[List[str]] = None


class SkillPlan(BaseModel):
    model_config = ConfigDict(extra='forbid')

    type: Literal['keep', 'refactor', 'merge', 'delete_duplicate']
    source_keys: List[str] = Field(default_factory=list)
    target_source_key: str = ''
    target_name: str = ''
    target_description: str = ''
    target_metadata: SearchMetadata = Field(default_factory=SearchMetadata)
    step_handling_policy: Literal[
        'keep_steps',
        'minimally_adjust_steps',
        'merge_and_deduplicate_existing_steps',
        'none',
    ] = 'none'
    reason: str = ''


class SkillOrganizePlan(BaseModel):
    model_config = ConfigDict(extra='forbid')

    plans: List[SkillPlan] = Field(default_factory=list)


class SkillFsDraftItem(BaseModel):
    model_config = ConfigDict(extra='forbid')

    source_key: str
    target_key: str
    content: str
    search_metadata: SearchMetadata = Field(default_factory=SearchMetadata)


class MaterializedSkillContent(BaseModel):
    model_config = ConfigDict(extra='forbid')

    content: str


class SkillFsDraft(BaseModel):
    model_config = ConfigDict(extra='forbid')

    delete_keys: List[str] = Field(default_factory=list)
    upsert_skills: List[SkillFsDraftItem] = Field(default_factory=list)


class SkillOrganizeResult(BaseModel):
    success: bool
    requestid: str
    taskid: str = ''
    inserted_count: int = 0
    artifact_dir: str = ''
    error: Optional[str] = None
