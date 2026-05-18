from __future__ import annotations

from chat.components.skill_review.llm import SkillReviewLLM
from chat.components.skill_review.schemas import CandidateSkill, SkillReviewDecision, Trajectory
from chat.prompts.skill_review import decision_prompt


def decide_skill_action(
    candidate: CandidateSkill,
    trajectory: Trajectory,
    llm: SkillReviewLLM,
) -> SkillReviewDecision:
    try:
        payload = llm.complete_json(decision_prompt(candidate.model_dump(), trajectory.called_skills))
        payload['candidate'] = candidate.model_dump()
        return SkillReviewDecision.model_validate(payload)
    except Exception:
        action = 'modify' if trajectory.called_skills else 'create'
        target_skill = None
        suggestions = []
        if trajectory.called_skills:
            target_skill = {'name': trajectory.called_skills[0]}
            suggestions.append({
                'title': f'Update skill from session {trajectory.session_id}',
                'content': 'Merge the candidate skill steps, success patterns, and failure avoidance notes into the existing skill.',
                'reason': 'The reviewed trajectory used an existing skill and produced reusable improvements.',
            })
        return SkillReviewDecision(
            action=action,
            reason='Fallback decision based on whether the reviewed trajectory called an existing skill.',
            confidence=0.3,
            target_skill=target_skill,
            suggestions=suggestions,
            candidate=candidate,
        )
