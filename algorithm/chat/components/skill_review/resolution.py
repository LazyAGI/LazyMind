from __future__ import annotations

from uuid import uuid4

from chat.components.skill_review.schemas import CandidateSkill, SkillReviewResolution, Trajectory
from chat.prompts.skill_review import resolution_prompt


def resolve_skill_action(
    candidate: CandidateSkill,
    trajectory: Trajectory,
    llm,
) -> SkillReviewResolution:
    resolution_type = 'patch' if trajectory.called_skills else 'new'
    try:
        payload = llm.complete_json(resolution_prompt(candidate.model_dump(), trajectory.called_skills))
        resolution_type = _normalize_resolution_type(payload.get('type') or payload.get('action'), resolution_type)
        suggestion = payload.get('suggestion') or payload.get('reason') or ''
    except Exception:
        suggestion = 'Create a new reusable skill from the reviewed trajectories.'
        if trajectory.called_skills:
            suggestion = (
                f'Patch existing skill {trajectory.called_skills[0]} with the candidate '
                'steps, success patterns, failure avoidance notes, and self-checks.'
            )
    return SkillReviewResolution(
        id=str(uuid4()),
        skill_name=candidate.skill_name,
        type=resolution_type,
        skill_content=candidate.model_dump(),
        suggestion=str(suggestion or ''),
    )


def _normalize_resolution_type(value, fallback: str) -> str:
    normalized = str(value or '').strip().lower()
    if normalized in {'new', 'create'}:
        return 'new'
    if normalized in {'patch', 'modify', 'replace', 'merge'}:
        return 'patch'
    return fallback
