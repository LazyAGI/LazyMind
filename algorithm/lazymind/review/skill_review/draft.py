from __future__ import annotations

from concurrent.futures import as_completed
from pathlib import Path

from tqdm import tqdm

from lazyllm import AutoModel, LOG, ThreadPoolExecutor

from lazymind.review.skill_review.schemas import (
    ClusterSignature,
    GuidelineSet,
    RefinedTrajectory,
    SkillDraft,
    Trajectory,
)
from lazymind.review.skill_review.config import DEFAULT_STAGE_WORKERS, STAGE_DRAFT, STAGE_FILES
from lazymind.review.skill_review.json_call import call_json
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file
from lazymind.review.skill_review.prompt import (
    cluster_signature_prompt,
    guidelines_prompt,
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


def build_skill_drafts(
    trajectories: list[Trajectory],
    llm: AutoModel,
    *,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    artifact_dir: Path | None = None,
) -> tuple[list[SkillDraft], dict]:
    """Build drafts from qualified trajectories."""
    started_at = start_stage()
    jobs = _draft_jobs(trajectories, llm)
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
    input_count = metadata['trajectory']['input_count']
    LOG.info(
        f'[SkillReview] built {len(drafts)} skill drafts from {input_count} trajectories; '
        f'errors={len(errors)}'
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


def _draft_jobs(
    trajectories: list[Trajectory],
    llm: AutoModel,
) -> list[dict]:
    return [
        {
            'kind': 'trajectory',
            'item_id': trajectory.session_id,
            'build': lambda trajectory=trajectory: _build_trajectory_draft(trajectory, llm),
        }
        for trajectory in trajectories
    ]


def _draft_report_metadata(
    jobs: list[dict],
    results: list[SkillDraft | None],
    errors: list[dict],
) -> dict:
    metadata = {
        'trajectory': {'input_count': 0, 'output_count': 0, 'error_count': 0},
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
        gate = _build_skill_extraction_gate(trajectory, llm)
        if not gate.get('should_extract'):
            LOG.info(f'[SkillReview] skip skill draft for trajectory {trajectory.session_id}')
            return None

        cluster_signature = _build_cluster_signature(trajectory, llm)
        refined_trajectory = _build_refined_trajectory(trajectory, llm)
        guidelines = _build_guidelines(trajectory, refined_trajectory, llm)

        return SkillDraft(
            session_id=trajectory.session_id,
            cluster_signature=cluster_signature,
            refined_trajectory=refined_trajectory,
            guidelines=guidelines,
            source_trajectory=trajectory.session_id,
            source_skills=trajectory.called_skills,
        )
    except Exception as exc:
        raise exc


def _build_skill_extraction_gate(
    trajectory: Trajectory,
    llm: AutoModel,
) -> dict:
    parsed = call_json(
        llm,
        skill_extraction_gate_prompt(trajectory.steps_text),
        _SKILL_EXTRACTION_GATE_SCHEMA,
    )
    should_extract = parsed.get('should_extract')
    if not isinstance(should_extract, bool):
        raise ValueError(f'skill extraction gate response must contain boolean {should_extract} {parsed}')
    return parsed


def _build_cluster_signature(
    trajectory: Trajectory,
    llm: AutoModel,
) -> ClusterSignature:
    parsed = call_json(
        llm,
        cluster_signature_prompt(trajectory.steps_text),
        ClusterSignature,
    )
    return ClusterSignature.model_validate(parsed)


def _build_refined_trajectory(
    trajectory: Trajectory,
    llm: AutoModel,
) -> RefinedTrajectory:
    parsed = call_json(
        llm,
        refined_trajectory_prompt(trajectory.steps_text),
        RefinedTrajectory,
    )
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
    trajectory: Trajectory,
    refined_trajectory: RefinedTrajectory,
    llm: AutoModel,
) -> GuidelineSet:
    parsed = call_json(
        llm,
        guidelines_prompt(trajectory=trajectory.steps_text,
                          refined_trajectory=refined_trajectory.model_dump()),
        GuidelineSet,
    )
    return GuidelineSet.model_validate(parsed)
