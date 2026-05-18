from __future__ import annotations

from chat.components.skill_review.llm import SkillReviewLLM
from chat.components.skill_review.schemas import (
    ContextualDescription,
    GuidelineSet,
    RefinedTrajectory,
    SkillDraft,
    SuccessGuideline,
    Trajectory,
)
from chat.prompts.skill_review import craft_prompt


def build_skill_craft(trajectory: Trajectory, llm: SkillReviewLLM) -> SkillDraft:
    try:
        payload = llm.complete_json(craft_prompt(trajectory.model_dump()))
        return SkillDraft.model_validate(payload)
    except Exception:
        return _fallback_craft(trajectory)


def _fallback_craft(trajectory: Trajectory) -> SkillDraft:
    user_steps = [step for step in trajectory.steps if step.role == 'user']
    assistant_steps = [step for step in trajectory.steps if step.role == 'assistant']
    goal = user_steps[0].action if user_steps else 'Unknown task goal'
    summary = assistant_steps[-1].action if assistant_steps else 'No assistant result found.'
    key_steps = [
        step for step in trajectory.steps
        if step.role in {'user', 'assistant', 'tool'} and step.action
    ][:12]
    return SkillDraft(
        contextual_description=ContextualDescription(
            task_goal=goal[:500],
            applicable_scenario='Tasks similar to this session trajectory.',
            execution_summary=summary[:800],
            key_result=summary[:500],
            environment={
                'called_tools': trajectory.called_tools,
                'called_skills': trajectory.called_skills,
            },
        ),
        refined_trajectory=RefinedTrajectory(steps=key_steps),
        guidelines=GuidelineSet(
            success_patterns=[
                SuccessGuideline(
                    related_step=key_steps[-1].step_index if key_steps else None,
                    guideline='Keep only the actions that directly change the final result, and preserve tool outputs that affect the answer.',
                )
            ],
            failure_patterns=[],
        ),
    )
