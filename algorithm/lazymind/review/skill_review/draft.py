from __future__ import annotations

import json
from concurrent.futures import as_completed
from pathlib import Path

from tqdm import tqdm

from lazyllm import AutoModel, LOG, ThreadPoolExecutor

from lazymind.review.skill_review.schemas import (
    ContextualDescription,
    GuidelineSet,
    RefinedTrajectory,
    SkillDraft,
    Trajectory,
)
from lazymind.review.skill_review.config import DEFAULT_STAGE_WORKERS, STAGE_DRAFT, STAGE_FILES
from lazymind.review.skill_review.json_call import call_json
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file
from lazymind.review.skill_review.prompt import (
    contextual_description_prompt,
    guidelines_prompt,
    pending_skill_draft_prompt,
    refined_trajectory_prompt,
    skill_extraction_gate_prompt,
)


_SKILL_EXTRACTION_GATE_SCHEMA = {
    'title': 'skill_extraction_gate_response',
    'type': 'object',
    'properties': {
        'should_extract': {'type': 'boolean'},
        'confidence': {'type': 'number'},
        'value_type': {
            'type': 'array',
            'items': {'type': 'string'},
        },
        'reason': {'type': 'string'},
    },
    'required': ['should_extract', 'reason'],
}

_PENDING_SKILL_DRAFT_SCHEMA = {
    'title': 'pending_skill_draft_response',
    'type': 'object',
    'properties': {
        'contextual_description': ContextualDescription.model_json_schema(),
        'refined_trajectory': RefinedTrajectory.model_json_schema(),
        'guidelines': GuidelineSet.model_json_schema(),
    },
    'required': ['contextual_description', 'refined_trajectory', 'guidelines'],
}


def build_skill_drafts(
    trajectories: list[Trajectory],
    llm: AutoModel,
    *,
    pending_records: list[dict] | None = None,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    artifact_dir: Path | None = None,
) -> tuple[list[SkillDraft], dict]:
    """Build drafts from trajectories and pending skill-review records."""
    started_at = start_stage()
    pending_records = pending_records or []
    jobs = _draft_jobs(trajectories, pending_records, llm)
    results: list[SkillDraft | None] = [None] * len(jobs)
    errors: list[dict] = []

    with ThreadPoolExecutor(max_workers=max(1, max_workers)) as executor:
        futures = {
            executor.submit(job['build']): (index, job)
            for index, job in enumerate(jobs)
        }
        with tqdm(total=len(futures), desc='building skill drafts', unit='draft') as bar:
            for fut in as_completed(futures):
                index, job = futures[fut]
                try:
                    results[index] = fut.result()
                except Exception as exc:
                    LOG.warning(f"failed to build {job['kind']} draft for {job['item_id']}: {exc}")
                    errors.append(stage_error(STAGE_DRAFT, job['item_id'], exc))
                bar.set_postfix(item=job['item_id'][:16])
                bar.update(1)

    drafts = [draft for draft in results if draft is not None]
    if artifact_dir is not None:
        write_json_file(artifact_dir / STAGE_FILES[STAGE_DRAFT], drafts)

    metadata = _draft_report_metadata(jobs, results, errors)
    LOG.info(
        f"[SkillReview] built {len(drafts)} skill drafts from {metadata['trajectory']['input_count']} trajectories "
        f"and {metadata['pending_skill']['input_count']} pending skill reviews; errors={len(errors)}"
    )
    report = finish_stage_report(
        STAGE_DRAFT,
        started_at,
        input_count=len(jobs),
        output_count=len(drafts),
        errors=errors,
        status='failed' if jobs and not drafts else 'completed',
        metadata=metadata,
    )
    return drafts, report


def _draft_jobs(trajectories: list[Trajectory], pending_records: list[dict], llm: AutoModel) -> list[dict]:
    jobs = [
        {
            'kind': 'trajectory',
            'item_id': trajectory.session_id,
            'build': lambda trajectory=trajectory: _build_trajectory_draft(trajectory, llm),
        }
        for trajectory in trajectories
    ]
    jobs.extend(
        {
            'kind': 'pending_skill',
            'item_id': str(record.get('id') or index),
            'build': lambda record=record: _build_pending_skill_draft(record, llm),
        }
        for index, record in enumerate(pending_records)
    )
    return jobs


def _draft_report_metadata(
    jobs: list[dict],
    results: list[SkillDraft | None],
    errors: list[dict],
) -> dict:
    metadata = {
        'trajectory': {'input_count': 0, 'output_count': 0, 'error_count': 0},
        'pending_skill': {'input_count': 0, 'output_count': 0, 'error_count': 0},
    }
    for job, result in zip(jobs, results):
        kind = job['kind']
        metadata[kind]['input_count'] += 1
        if result is not None:
            metadata[kind]['output_count'] += 1
    for error in errors:
        item_id = str(error.get('item_id') or '')
        kind = next((job['kind'] for job in jobs if job['item_id'] == item_id), 'trajectory')
        metadata[kind]['error_count'] += 1
    return metadata


def _build_trajectory_draft(trajectory: Trajectory, llm: AutoModel) -> SkillDraft | None:
    try:
        trajectory_text = trajectory.steps_text

        gate = _build_skill_extraction_gate(trajectory, llm)
        if not gate.get('should_extract'):
            LOG.info(f'[SkillReview] skip skill draft for trajectory {trajectory.session_id}')
            return None

        contextual_description = _build_contextual_description(trajectory, llm)
        refined_trajectory = _build_refined_trajectory(trajectory, llm)
        guidelines = _build_guidelines(trajectory_text, refined_trajectory, llm)

        return SkillDraft(
            session_id=trajectory.session_id,
            contextual_description=contextual_description,
            refined_trajectory=refined_trajectory,
            guidelines=guidelines,
            source_trajectory=trajectory.session_id,
            source_skills=trajectory.called_skills,
        )
    except Exception as exc:
        raise exc


def _build_pending_skill_draft(record: dict, llm: AutoModel) -> SkillDraft:
    record_id = str(record.get('id') or '').strip()
    skill_name = str(record.get('skill_name') or '').strip()
    skill_content = _record_text(record.get('skill_content')).strip()
    if not record_id:
        raise ValueError('pending skill review record must contain id')
    if not skill_name:
        raise ValueError(f'pending skill review record {record_id} must contain skill_name')
    if not skill_content:
        raise ValueError(f'pending skill review record {record_id} must contain skill_content')

    payload = call_json(
        llm,
        pending_skill_draft_prompt(skill_name, skill_content),
        _PENDING_SKILL_DRAFT_SCHEMA,
    )
    contextual_description = ContextualDescription.model_validate(payload.get('contextual_description') or {})
    refined_trajectory = _normalize_refined_trajectory(payload.get('refined_trajectory'))
    guidelines = GuidelineSet.model_validate(payload.get('guidelines') or {})
    return SkillDraft(
        session_id=record_id,
        contextual_description=contextual_description,
        refined_trajectory=refined_trajectory,
        guidelines=guidelines,
        source_trajectory=record_id,
        source_skills={skill_name: skill_content},
    )


def _build_skill_extraction_gate(
    trajectory: Trajectory,
    llm: AutoModel,
) -> dict:
    parsed = call_json(llm, skill_extraction_gate_prompt(trajectory.steps_text), _SKILL_EXTRACTION_GATE_SCHEMA)
    should_extract = parsed.get('should_extract')
    if not isinstance(should_extract, bool):
        raise ValueError(f'skill extraction gate response must contain boolean {should_extract} {parsed}')
    return parsed


def _build_contextual_description(
    trajectory: Trajectory,
    llm: AutoModel,
) -> ContextualDescription:
    parsed = call_json(llm, contextual_description_prompt(trajectory.steps_text), ContextualDescription)
    parsed['environment'] = {
        'called_tools': trajectory.called_tools,
        'called_skills': trajectory.called_skills,
    }
    return ContextualDescription.model_validate(parsed)


def _build_refined_trajectory(
    trajectory: Trajectory,
    llm: AutoModel,
) -> RefinedTrajectory:
    parsed = call_json(llm, refined_trajectory_prompt(trajectory.steps_text), RefinedTrajectory)
    return _normalize_refined_trajectory(parsed)


def _normalize_refined_trajectory(parsed) -> RefinedTrajectory:
    raw_steps = parsed.get('steps') if isinstance(parsed, dict) else None
    if not isinstance(raw_steps, list):
        raw_steps = []
    normalized_steps = []
    for index, step in enumerate(raw_steps, start=1):
        if not isinstance(step, dict):
            continue

        step_index = step.get('step_index')
        if not isinstance(step_index, int):
            step_index = index

        normalized_steps.append(
            dict(
                step_index=step_index,
                action=str(step.get('action') or ''),
                state=str(step.get('state') or ''),
            ))
    return RefinedTrajectory(steps=normalized_steps)


def _build_guidelines(
    trajectory_text: str,
    refined_trajectory: RefinedTrajectory,
    llm: AutoModel,
) -> GuidelineSet:
    parsed = call_json(
        llm,
        guidelines_prompt(trajectory=trajectory_text,
                          refined_trajectory=refined_trajectory.model_dump()),
        GuidelineSet,
    )
    return GuidelineSet.model_validate(parsed)


def _record_text(value) -> str:
    if isinstance(value, str):
        return value
    if value is None:
        return ''
    return json.dumps(value, ensure_ascii=False, indent=2)
