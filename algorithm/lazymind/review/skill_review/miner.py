from __future__ import annotations

import json
from concurrent.futures import as_completed
from pathlib import Path
from typing import Any

from lazyllm import LOG, ThreadPoolExecutor

from lazymind.review.skill_review.config import (
    DEFAULT_STAGE_WORKERS,
    STAGE_CANDIDATE,
    STAGE_FILES,
    STAGE_OUTLINE,
)
from lazymind.review.skill_review.schemas import (
    CandidateSkill,
    CandidateSkillLLMOutput,
    GuidelineSet,
    SkillOutline,
    TaskCluster,
)
from lazymind.review.skill_review.json_call import call_json
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file
from lazymind.review.skill_review.prompt import candidate_prompt, outline_prompt


_OUTLINE_RESPONSE_SCHEMA: dict[str, Any] = {
    'title': 'skill_outline_response',
    'type': 'object',
    'properties': {
        'skill_name': {'type': 'string'},
        'applicable_scenario': {'type': 'string'},
        'sop': {
            'type': 'object',
            'properties': {
                'steps': {
                    'type': 'array',
                    'items': {
                        'type': 'object',
                        'properties': {
                            'step_name': {'type': 'string'},
                            'action_goal': {'type': 'string'},
                            'branch_conditions': {
                                'type': 'array',
                                'items': {
                                    'type': 'object',
                                    'properties': {
                                        'condition': {'type': 'string'},
                                        'next_action': {'type': 'string'},
                                    },
                                    'required': ['condition', 'next_action'],
                                },
                            },
                            'expected_state': {'type': 'string'},
                        },
                        'required': ['step_name', 'action_goal'],
                    },
                },
            },
            'required': ['steps'],
        },
    },
    'required': ['skill_name', 'applicable_scenario', 'sop'],
}


def build_skill_outline(cluster: TaskCluster, llm) -> SkillOutline | None:
    refined_trajectories = [
        draft.refined_trajectory.model_dump()
        for draft in cluster.drafts
    ]
    payload = call_json(
        llm,
        outline_prompt(
            task_scope=cluster.task_scope,
            refined_trajectories=_JsonDumpable(refined_trajectories),
        ),
        _OUTLINE_RESPONSE_SCHEMA,
    )
    normalized = _normalize_outline_payload(payload)
    if normalized is None:
        return None
    return SkillOutline.model_validate(normalized)


def build_candidate_skill(
    cluster: TaskCluster,
    outline: SkillOutline,
    llm,
) -> CandidateSkill:
    guidelines = _collect_guidelines(cluster)
    payload = call_json(llm, candidate_prompt(outline, guidelines), CandidateSkillLLMOutput)
    normalized = _normalize_candidate_payload(
        payload,
        outline,
        source_trajectories=_collect_source_trajectories(cluster),
        source_skills=_collect_source_skills(cluster),
    )
    return CandidateSkill.model_validate(normalized)


def build_skill_outlines(
    clusters: list[TaskCluster],
    llm,
    *,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    artifact_dir: Path | None = None,
) -> tuple[list[tuple[TaskCluster, SkillOutline]], dict]:
    started_at = start_stage()
    results: list[tuple[TaskCluster, SkillOutline] | None] = [None] * len(clusters)
    errors: list[dict] = []
    with ThreadPoolExecutor(max_workers=max(1, max_workers)) as executor:
        futures = {
            executor.submit(build_skill_outline, cluster, llm): (index, cluster)
            for index, cluster in enumerate(clusters)
        }
        for fut in as_completed(futures):
            index, cluster = futures[fut]
            item_id = _cluster_item_id(index, cluster)
            try:
                outline = fut.result()
                if outline is not None:
                    results[index] = (cluster, outline)
            except Exception as exc:
                LOG.warning(f'failed to build outline for {item_id}: {exc}')
                errors.append(stage_error(STAGE_OUTLINE, item_id, exc))
    outline_pairs = [item for item in results if item is not None]
    if artifact_dir is not None:
        write_json_file(artifact_dir / STAGE_FILES[STAGE_OUTLINE], [outline for _, outline in outline_pairs])
    return outline_pairs, finish_stage_report(
        STAGE_OUTLINE,
        started_at,
        input_count=len(clusters),
        output_count=len(outline_pairs),
        errors=errors,
        status='failed' if not outline_pairs else 'completed',
    )


def build_candidate_skills(
    outline_pairs: list[tuple[TaskCluster, SkillOutline]],
    llm,
    *,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    artifact_dir: Path | None = None,
) -> tuple[list[CandidateSkill], dict]:
    started_at = start_stage()
    results: list[CandidateSkill | None] = [None] * len(outline_pairs)
    errors: list[dict] = []
    with ThreadPoolExecutor(max_workers=max(1, max_workers)) as executor:
        futures = {
            executor.submit(build_candidate_skill, cluster, outline, llm): (index, cluster, outline)
            for index, (cluster, outline) in enumerate(outline_pairs)
        }
        for fut in as_completed(futures):
            index, cluster, outline = futures[fut]
            item_id = outline.skill_name or _cluster_item_id(index, cluster)
            try:
                results[index] = fut.result()
            except Exception as exc:
                LOG.warning(f'failed to build candidate for {item_id}: {exc}')
                errors.append(stage_error(STAGE_CANDIDATE, item_id, exc))
    candidates = [item for item in results if item is not None]
    if artifact_dir is not None:
        write_json_file(artifact_dir / STAGE_FILES[STAGE_CANDIDATE], candidates)
        _write_candidate_skill_files(artifact_dir, candidates)
    return candidates, finish_stage_report(
        STAGE_CANDIDATE,
        started_at,
        input_count=len(outline_pairs),
        output_count=len(candidates),
        errors=errors,
        status='failed' if not candidates else 'completed',
    )


class _JsonDumpable:
    def __init__(self, value: Any) -> None:
        self.value = value

    def model_dump_json(self, *, indent: int | None = None) -> str:
        return json.dumps(self.value, ensure_ascii=False, indent=indent)


def _normalize_outline_payload(payload: dict[str, Any]) -> dict[str, Any] | None:
    sop = payload.get('sop')
    raw_steps = sop.get('steps') if isinstance(sop, dict) else sop
    if not isinstance(raw_steps, list):
        raise ValueError('outline payload must contain sop.steps as a list')

    steps = []
    for index, raw_step in enumerate(raw_steps, start=1):
        if not isinstance(raw_step, dict):
            raise ValueError(f'outline step {index} must be an object')
        steps.append({
            'step_name': str(raw_step.get('step_name') or f'Step {index}'),
            'action_goal': str(raw_step.get('action_goal') or ''),
            'branch_conditions': _normalize_branch_conditions(raw_step.get('branch_conditions')),
        })

    if not steps:
        LOG.warning('outline payload does not contain sop steps; skip candidate generation')
        return None
    skill_name = str(payload.get('skill_name') or '').strip()
    applicable_scenario = str(payload.get('applicable_scenario') or '').strip()
    if not skill_name:
        raise ValueError('outline payload must contain skill_name')
    if not applicable_scenario:
        raise ValueError('outline payload must contain applicable_scenario')
    return {
        'skill_name': skill_name,
        'applicable_scenario': applicable_scenario,
        'sop': steps,
    }


def _normalize_branch_conditions(value: Any) -> list[str]:
    if not isinstance(value, list):
        return []
    conditions = []
    for item in value:
        if isinstance(item, str):
            condition = item.strip()
        elif isinstance(item, dict):
            condition_text = str(item.get('condition') or '').strip()
            next_action = str(item.get('next_action') or item.get('response') or '').strip()
            condition = f'{condition_text}: {next_action}' if next_action else condition_text
        else:
            condition = str(item).strip()
        if condition:
            conditions.append(condition)
    return conditions


def _normalize_candidate_payload(
    payload: dict[str, Any],
    outline: SkillOutline,
    source_trajectories: list[str],
    source_skills: dict[str, str],
) -> dict[str, Any]:
    skill_name = str(payload.get('skill_name') or '').strip()
    applicable_scenario = str(payload.get('applicable_scenario') or '').strip()
    content = str(payload.get('content') or '').strip()
    if not skill_name:
        raise ValueError('candidate payload must contain skill_name')
    if not applicable_scenario:
        raise ValueError('candidate payload must contain applicable_scenario')
    if not content:
        raise ValueError('candidate payload must contain content')
    return {
        'skill_name': skill_name,
        'category': str(payload.get('category') or 'general'),
        'source_trajectories': source_trajectories,
        'source_skills': source_skills,
        'applicable_scenario': applicable_scenario,
        'content': content + '\n',
        'outline': outline.model_dump(),
    }


def _collect_source_trajectories(cluster: TaskCluster) -> list[str]:
    seen: set[str] = set()
    result: list[str] = []
    for draft in cluster.drafts:
        session_id = str(draft.session_id or '').strip()
        if not session_id or session_id in seen:
            continue
        seen.add(session_id)
        result.append(session_id)
    return result


def _collect_source_skills(cluster: TaskCluster) -> dict[str, str]:
    result: dict[str, str] = {}
    for draft in cluster.drafts:
        raw_skills = draft.source_skills
        if not raw_skills:
            environment = draft.contextual_description.environment or {}
            raw_skills = environment.get('called_skills') if isinstance(environment, dict) else {}
        if isinstance(raw_skills, dict):
            items = raw_skills.items()
        elif isinstance(raw_skills, list):
            items = ((str(skill or '').strip(), '') for skill in raw_skills)
        else:
            continue
        for raw_name, raw_content in items:
            skill_name = str(raw_name or '').strip()
            if not skill_name or skill_name in result:
                continue
            result[skill_name] = str(raw_content or '')
    return result


def _collect_guidelines(cluster: TaskCluster) -> GuidelineSet:
    success = []
    failure = []
    for draft in cluster.drafts:
        success.extend(draft.guidelines.success_patterns)
        failure.extend(draft.guidelines.failure_patterns)
    return GuidelineSet(success_patterns=success, failure_patterns=failure)


def _cluster_item_id(index: int, cluster: TaskCluster) -> str:
    sources = [
        draft.session_id
        for draft in cluster.drafts
        if draft.session_id
    ]
    return f'cluster_{index}:{"|".join(sources[:5])}'


def _write_candidate_skill_files(artifact_dir: Path, candidates: list[CandidateSkill]) -> None:
    skill_dir = artifact_dir / 'skills'
    skill_dir.mkdir(parents=True, exist_ok=True)
    for index, candidate in enumerate(candidates, start=1):
        filename = f'{index:02d}_{_safe_filename(candidate.skill_name)}.md'
        path = skill_dir / filename
        tmp = path.with_suffix(path.suffix + '.tmp')
        tmp.write_text(candidate.content, encoding='utf-8')
        tmp.replace(path)


def _safe_filename(value: str) -> str:
    safe = ''.join(ch if ch.isalnum() or ch in ('-', '_', '.') else '_' for ch in value.strip())
    return safe or 'skill'
