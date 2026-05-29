from __future__ import annotations

import json
import traceback
from concurrent.futures import ThreadPoolExecutor, as_completed

from tqdm import tqdm

from lazyllm import AutoModel, LOG

# Shared executor that caps concurrent draft builds at 10 across all threads.
_DRAFT_EXECUTOR = ThreadPoolExecutor(max_workers=4)

from chat.components.skill_review.schemas import (
    ContextualDescription,
    GuidelineSet,
    RefinedTrajectory,
    SkillDraft,
    SuccessGuideline,
    Trajectory,
    TrajectoryStep,
)
from chat.components.skill_review.json_call import call_json
from chat.prompts.skill_review import (
    contextual_description_prompt,
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


def build_skill_drafts(trajectories: list[Trajectory], llm: AutoModel) -> list[SkillDraft]:
    """Build drafts for all trajectories concurrently (max 10 at a time) with a progress bar."""
    # # --
    # with open('/Users/jisiyuan/Project/LazyRAG/tmp/lazyrag-skill-dev2/unknown_user/03_draft.json', 'r') as f:
    #     drafts = json.load(f)
    # return [SkillDraft(**draft) for draft in drafts]
    # # --
    
    
    futures = {
        _DRAFT_EXECUTOR.submit(build_skill_draft, traj, llm): (index, traj)
        for index, traj in enumerate(trajectories)
    }
    results: list[SkillDraft | None] = [None] * len(trajectories)
    with tqdm(total=len(futures), desc='building skill drafts', unit='trajectory') as bar:
        for fut in as_completed(futures):
            index, traj = futures[fut]
            try:
                results[index] = fut.result()
            except Exception:
                traceback.print_exc()
                raise
            bar.set_postfix(session=traj.session_id[:16])
            bar.update(1)
    # Preserve original ordering.
    res = [draft for draft in results if draft is not None]
    LOG.info(f'built {len(res)} skill drafts from {len(trajectories)} trajectories')
    return res


def build_skill_draft(trajectory: Trajectory, llm: AutoModel) -> SkillDraft | None:
    try:
        trajectory_text = trajectory.steps_text

        gate = _build_skill_extraction_gate(trajectory, llm)
        if not gate.get('should_extract'):
            LOG.info(
                f"skip skill draft for trajectory {trajectory.session_id}: "
                f"{gate.get('reason') or 'skill extraction gate returned false'}"
            )
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
