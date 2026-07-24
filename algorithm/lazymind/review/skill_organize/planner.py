from __future__ import annotations

from lazyllm import LOG

from lazymind.review.skill_organize.prompts import organize_plan_prompt
from lazymind.review.skill_organize.schemas import SkillOrganizePlan, SkillSummary, SourceSkill
from lazymind.review.skill_organize.validator import validate_plan
from lazymind.review.skill_review.json_call import call_json


def build_organize_plan(
    summaries: list[SkillSummary],
    source_skills: list[SourceSkill],
    llm,
    *,
    max_retries: int = 3,
) -> SkillOrganizePlan:
    prompt = organize_plan_prompt([item.model_dump() for item in summaries])
    last_error: Exception | None = None
    for attempt in range(max_retries):
        try:
            payload = call_json(llm, prompt, SkillOrganizePlan, max_retries=1)
            plan = SkillOrganizePlan.model_validate(payload)
            validate_plan(plan, source_skills)
            return plan
        except Exception as exc:
            last_error = exc
            LOG.warning(f'[SkillOrganize] plan attempt {attempt + 1} failed: {exc}')
    raise ValueError(f'failed to build valid organize plan: {last_error}') from last_error
