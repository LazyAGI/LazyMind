from __future__ import annotations

from concurrent.futures import as_completed
from pathlib import Path
from uuid import uuid4

from lazyllm import LOG, ThreadPoolExecutor

from lazymind.review.skill_review.config import DEFAULT_STAGE_WORKERS, STAGE_FILES, STAGE_RESOLUTION
from lazymind.review.skill_review.json_call import call_json
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file
from lazymind.review.skill_review.schemas import (
    CandidateSkill,
    SkillReviewResolution,
)
from lazymind.review.skill_review.prompt import resolution_prompt


_RESOLUTION_DECISION_SCHEMA = {
    'title': 'skill_review_resolution_decision',
    'type': 'object',
    'properties': {
        'type': {'type': 'string', 'enum': ['new', 'patch']},
        'patch_skill_name': {'type': 'string'},
        'summary': {'type': ['string', 'null']},
        'patched_skill': {'type': 'string'},
    },
    'required': ['type'],
}


def resolve_skill_action(
    candidate: CandidateSkill,
    llm,
) -> SkillReviewResolution:
    called_skills = candidate.source_skills or {}
    if not called_skills:
        return _new_resolution(candidate)

    payload = call_json(
        llm,
        resolution_prompt(candidate.model_dump(), called_skills),
        _RESOLUTION_DECISION_SCHEMA,
    )
    resolution_type = _normalize_resolution_type(payload.get('type') or payload.get('action'), 'new')
    summary = str(payload.get('summary') or payload.get('suggestion') or '').strip()
    if resolution_type != 'patch':
        return _new_resolution(candidate)

    patch_skill_name = str(payload.get('patch_skill_name') or '').strip()
    patched_skill = str(payload.get('patched_skill') or '').strip()
    if not patch_skill_name:
        raise ValueError('patch resolution requires patch_skill_name')
    if not patched_skill:
        raise ValueError('patch resolution requires patched_skill')
    return SkillReviewResolution(
        id=str(uuid4()),
        skill_name=patch_skill_name,
        type='patch',
        skill_content=patched_skill,
        summary=summary or None,
    )


def resolve_skill_actions(
    candidates: list[CandidateSkill],
    llm,
    *,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    artifact_dir: Path | None = None,
) -> tuple[list[SkillReviewResolution], dict]:
    started_at = start_stage()
    results: list[SkillReviewResolution | None] = [None] * len(candidates)
    errors: list[dict] = []
    with ThreadPoolExecutor(max_workers=max(1, max_workers)) as executor:
        futures = {
            executor.submit(resolve_skill_action, candidate, llm): (index, candidate)
            for index, candidate in enumerate(candidates)
        }
        for fut in as_completed(futures):
            index, candidate = futures[fut]
            try:
                results[index] = fut.result()
            except Exception as exc:
                LOG.warning(f'failed to resolve candidate {candidate.skill_name}: {exc}')
                errors.append(stage_error(STAGE_RESOLUTION, candidate.skill_name, exc))
    resolutions = [item for item in results if item is not None]
    if artifact_dir is not None:
        write_json_file(artifact_dir / STAGE_FILES[STAGE_RESOLUTION], resolutions)
    return resolutions, finish_stage_report(
        STAGE_RESOLUTION,
        started_at,
        input_count=len(candidates),
        output_count=len(resolutions),
        errors=errors,
    )


def _new_resolution(candidate: CandidateSkill) -> SkillReviewResolution:
    return SkillReviewResolution(
        id=str(uuid4()),
        skill_name=candidate.skill_name,
        type='new',
        skill_content=candidate.content,
        summary=None,
    )


def _normalize_resolution_type(value, fallback: str) -> str:
    normalized = str(value or '').strip().lower()
    if normalized in {'new', 'create'}:
        return 'new'
    if normalized in {'patch', 'modify', 'replace', 'merge'}:
        return 'patch'
    return fallback
