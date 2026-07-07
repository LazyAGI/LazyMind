from __future__ import annotations

from concurrent.futures import as_completed

from lazyllm import LOG, ThreadPoolExecutor

from lazymind.review.skill_organize.config import DEFAULT_MATERIALIZE_WORKERS
from lazymind.review.skill_organize.prompts import materialize_draft_prompt
from lazymind.review.skill_organize.schemas import (
    MaterializedSkillContent,
    SkillFsDraft,
    SkillFsDraftItem,
    SkillOrganizePlan,
    SkillPlan,
    SourceSkill,
)
from lazymind.review.skill_organize.validator import validate_fs_draft
from lazymind.review.skill_review.json_call import call_json


def materialize_fs_draft(
    plan: SkillOrganizePlan,
    source_skills: list[SourceSkill],
    llm,
    *,
    max_retries: int = 3,
    max_workers: int = DEFAULT_MATERIALIZE_WORKERS,
) -> SkillFsDraft:
    all_delete_paths: list[str] = []
    all_upserts = []
    by_path = {item.path: item for item in source_skills}
    partials: list[SkillFsDraft | None] = [None] * len(plan.plans)
    errors: list[str] = []

    with ThreadPoolExecutor(max_workers=max(1, max_workers)) as executor:
        futures = {
            executor.submit(_materialize_plan_item, item, by_path, llm, max_retries=max_retries): (index, item)
            for index, item in enumerate(plan.plans)
        }
        for fut in as_completed(futures):
            index, item = futures[fut]
            try:
                partials[index] = fut.result()
            except Exception as exc:
                LOG.warning(f'[SkillOrganize] failed to materialize plan item {index} {item.source_paths}: {exc}')
                errors.append(f'plans[{index}] {item.source_paths}: {exc}')

    if errors:
        raise ValueError('failed to materialize fs draft: ' + '; '.join(errors))

    for partial in partials:
        if partial is None:
            continue
        all_delete_paths.extend(partial.delete_paths)
        all_upserts.extend(partial.upsert_skills)
    draft = SkillFsDraft(delete_paths=all_delete_paths, upsert_skills=all_upserts)
    validate_fs_draft(draft, source_skills)
    return draft


def _materialize_plan_item(
    plan: SkillPlan,
    by_path: dict[str, SourceSkill],
    llm,
    *,
    max_retries: int,
) -> SkillFsDraft:
    if plan.type == 'keep':
        return SkillFsDraft()
    if plan.type == 'delete_duplicate':
        return SkillFsDraft(delete_paths=list(plan.source_paths))

    sources = [by_path[path] for path in plan.source_paths]
    upsert = _materialize_upsert_skill(plan, sources, llm, max_retries=max_retries)
    delete_paths = _merge_delete_paths(plan) if plan.type == 'merge' else []
    return SkillFsDraft(delete_paths=delete_paths, upsert_skills=[upsert])


def _materialize_upsert_skill(
    plan: SkillPlan,
    sources: list[SourceSkill],
    llm,
    *,
    max_retries: int,
) -> SkillFsDraftItem:
    prompt = materialize_draft_prompt(
        _prompt_plan(plan),
        [_prompt_source_skill(item) for item in sources],
    )
    last_error: Exception | None = None
    for attempt in range(max_retries):
        try:
            payload = call_json(llm, prompt, MaterializedSkillContent, max_retries=1)
            materialized = MaterializedSkillContent.model_validate(payload)
            item = SkillFsDraftItem(path=plan.target_path, content=materialized.content)
            validate_fs_draft(SkillFsDraft(upsert_skills=[item]), sources)
            return item
        except Exception as exc:
            last_error = exc
            LOG.warning(
                f'[SkillOrganize] materialize {plan.type} '
                f'{plan.source_paths} attempt {attempt + 1} failed: {exc}'
            )
    raise ValueError(f'failed to materialize valid upsert skill for {plan.source_paths}: {last_error}') from last_error


def _merge_delete_paths(plan: SkillPlan) -> list[str]:
    return [path for path in plan.source_paths if path != plan.target_path]


def _prompt_plan(plan: SkillPlan) -> dict:
    return {
        'type': plan.type,
        'source_names': plan.source_names,
        'target_name': plan.target_name,
        'target_category': plan.target_category,
        'target_description': plan.target_description,
        'target_applicable_scenario': plan.target_applicable_scenario,
        'step_handling_policy': plan.step_handling_policy,
        'reason': plan.reason,
    }


def _prompt_source_skill(skill: SourceSkill) -> dict:
    return {
        'name': skill.name,
        'content': skill.content,
    }
