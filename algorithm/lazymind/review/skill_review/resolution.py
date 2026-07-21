from __future__ import annotations

from concurrent.futures import as_completed
from pathlib import Path
import re
from uuid import uuid4

from lazyllm import LOG, ThreadPoolExecutor

from lazymind.common.skill_document import require_valid_skill_document
from lazymind.common.skill_storage_key import parse_skill_storage_key
from lazymind.review.skill_review.config import DEFAULT_STAGE_WORKERS, STAGE_FILES, STAGE_RESOLUTION
from lazymind.review.skill_review.json_call import call_json
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file
from lazymind.review.skill_review.schemas import (
    CandidateSkill,
    SkillReviewResolution,
)
from lazymind.review.skill_review.prompt import merge_skill_patch_prompt, resolution_prompt


_RESOLUTION_DECISION_SCHEMA = {
    'title': 'skill_review_resolution_decision',
    'type': 'object',
    'properties': {
        'type': {'type': 'string', 'enum': ['new', 'patch']},
        'patch_skill_key': {'type': 'string'},
        'reason': {'type': ['string', 'null']},
    },
    'required': ['type'],
}

_PATCH_MERGE_SCHEMA = {
    'title': 'skill_review_patch_merge',
    'type': 'object',
    'properties': {
        'summary': {'type': ['string', 'null']},
        'skill_name': {'type': 'string'},
        'skill_content': {'type': 'string'},
        'patched_skill': {'type': 'string'},
    },
    'required': ['skill_name', 'skill_content'],
}


def resolve_skill_action(
    candidate: CandidateSkill,
    llm,
    *,
    skill_manager=None,
    skill_summaries: str = '',
) -> SkillReviewResolution:
    if not skill_manager or not skill_summaries.strip():
        return _new_resolution(candidate)

    available_skill_keys = _extract_skill_keys(skill_summaries)
    payload = call_json(
        llm,
        resolution_prompt(candidate.model_dump(), skill_summaries),
        _RESOLUTION_DECISION_SCHEMA,
    )
    resolution_type = _normalize_resolution_type(payload.get('type') or payload.get('action'), 'new')
    reason = str(payload.get('reason') or payload.get('summary') or payload.get('suggestion') or '').strip()
    if resolution_type != 'patch':
        return _new_resolution(candidate)

    patch_skill_key = str(payload.get('patch_skill_key') or '').strip()
    if not patch_skill_key:
        raise ValueError('patch resolution requires patch_skill_key')
    if patch_skill_key not in available_skill_keys:
        raise ValueError(f'patch_skill_key {patch_skill_key!r} is not in global skill summaries')
    category, name = parse_skill_storage_key(patch_skill_key)
    patch_skill_key = f'{category}/{name}'

    existing_skill_content = _read_skill_content(skill_manager, patch_skill_key)
    merge_payload = call_json(
        llm,
        merge_skill_patch_prompt(
            candidate.model_dump(),
            target_skill_key=patch_skill_key,
            existing_skill_content=existing_skill_content,
            decision_reason=reason,
        ),
        _PATCH_MERGE_SCHEMA,
    )
    summary = str(merge_payload.get('summary') or reason).strip()
    patched_skill_name = str(merge_payload.get('skill_name') or '').strip()
    patched_skill = str(merge_payload.get('skill_content') or merge_payload.get('patched_skill') or '').strip()
    if not patched_skill_name:
        raise ValueError('patch resolution requires skill_name')
    if not patched_skill:
        raise ValueError('patch resolution requires skill_content')
    _validate_patched_skill_name(patched_skill, patched_skill_name)
    return SkillReviewResolution(
        id=str(uuid4()),
        skill_name=patched_skill_name,
        target_skill_key=patch_skill_key,
        type='patch',
        skill_content=patched_skill,
        summary=summary or None,
    )


def resolve_skill_actions(
    candidates: list[CandidateSkill],
    llm,
    *,
    skill_manager=None,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    artifact_dir: Path | None = None,
) -> tuple[list[SkillReviewResolution], dict]:
    started_at = start_stage()
    results: list[SkillReviewResolution | None] = [None] * len(candidates)
    errors: list[dict] = []
    skill_summaries = _list_skill_summaries(skill_manager)
    with ThreadPoolExecutor(max_workers=max(1, max_workers)) as executor:
        futures = {
            executor.submit(
                resolve_skill_action,
                candidate,
                llm,
                skill_manager=skill_manager,
                skill_summaries=skill_summaries,
            ): (index, candidate)
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
        status='failed' if candidates and not resolutions else 'completed',
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


def _list_skill_summaries(skill_manager) -> str:
    if skill_manager is None:
        return ''
    try:
        value = skill_manager.list_skill()
    except Exception as exc:
        LOG.warning(f'failed to list global skills for resolution: {exc}')
        return ''
    if isinstance(value, str):
        return value.strip()
    return str(value or '').strip()


def _read_skill_content(skill_manager, skill_name: str) -> str:
    try:
        payload = skill_manager.get_skill(skill_name)
    except Exception as exc:
        raise ValueError(f'failed to read skill {skill_name}: {exc}') from exc
    if not isinstance(payload, dict):
        raise ValueError(f'get_skill({skill_name}) returned invalid payload')
    status = str(payload.get('status') or '').strip().lower()
    if status and status != 'ok':
        raise ValueError(f'get_skill({skill_name}) failed with status={status}')
    content = str(payload.get('content') or '').strip()
    if not content:
        raise ValueError(f'get_skill({skill_name}) returned empty content')
    return content


def _validate_patched_skill_name(skill_content: str, skill_name: str) -> None:
    require_valid_skill_document(skill_content, expected_name=skill_name)


def _extract_skill_keys(skill_summaries: str) -> set[str]:
    keys: set[str] = set()
    for match in re.finditer(r'^\s*-\s+\*\*([^*]+)\*\*', skill_summaries, flags=re.MULTILINE):
        value = match.group(1).strip()
        if value:
            keys.add(value)
    return keys
