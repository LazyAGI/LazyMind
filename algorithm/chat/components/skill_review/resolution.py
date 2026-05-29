from __future__ import annotations

from uuid import uuid4
from lazyllm import LOG

from chat.components.skill_review.json_call import call_json
from chat.components.skill_review.schemas import (
    CandidateSkill,
    SkillReviewResolution,
)
from chat.prompts.skill_review import resolution_prompt


_RESOLUTION_DECISION_SCHEMA = {
    'title': 'skill_review_resolution_decision',
    'type': 'object',
    'properties': {
        'type': {'type': 'string', 'enum': ['new', 'patch']},
        'patch_skill_name': {'type': 'string'},
        'suggestion': {'type': 'string'},
        'patched_skill': {'type': 'string'},
    },
    'required': ['type', 'suggestion'],
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
    suggestion = str(payload.get('suggestion') or '').strip()
    if resolution_type != 'patch':
        return _new_resolution(candidate, suggestion=suggestion)

    patch_skill_name = str(payload.get('patch_skill_name') or '').strip()
    patched_skill = str(payload.get('patched_skill') or '').strip()
    LOG.info(f'patched_skill: {patched_skill}')
    if not patch_skill_name:
        raise ValueError('patch resolution requires patch_skill_name')
    if not patched_skill:
        raise ValueError('patch resolution requires patched_skill')
    return SkillReviewResolution(
        id=str(uuid4()),
        skill_name=patch_skill_name,
        type='patch',
        skill_content=patched_skill,
        suggestion=suggestion,
    )


def _new_resolution(candidate: CandidateSkill, *, suggestion: str = '') -> SkillReviewResolution:
    if not suggestion:
        suggestion = (
            f'Create a new skill named {candidate.skill_name} because it covers '
            f'the reviewed scenario: {candidate.applicable_scenario}'
        )
    return SkillReviewResolution(
        id=str(uuid4()),
        skill_name=candidate.skill_name,
        type='new',
        skill_content=candidate.content,
        suggestion=suggestion,
    )


def _normalize_resolution_type(value, fallback: str) -> str:
    normalized = str(value or '').strip().lower()
    if normalized in {'new', 'create'}:
        return 'new'
    if normalized in {'patch', 'modify', 'replace', 'merge'}:
        return 'patch'
    return fallback
