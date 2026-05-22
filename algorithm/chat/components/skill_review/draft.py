from __future__ import annotations

import re
import json
from lazyllm import AutoModel

from chat.components.skill_review.schemas import (
    ContextualDescription,
    GuidelineSet,
    RefinedTrajectory,
    SkillDraft,
    SuccessGuideline,
    Trajectory,
)
from chat.prompts.skill_review import draft_prompt


def build_skill_draft(trajectory: Trajectory, llm: AutoModel) -> SkillDraft:
    try:
        text = llm(draft_prompt(trajectory.steps_text))
        fenced = re.search(r'```(?:json)?\s*(\{.*?\})\s*```', text, re.S)
        if fenced:
            text = fenced.group(1)
        else:
            start = text.find('{')
            end = text.rfind('}')
            if start >= 0 and end > start:
                text = text[start:end + 1]
        parsed = json.loads(text)
        return SkillDraft.model_validate(parsed)
    except Exception:
        return _fallback_draft(trajectory)


def _fallback_draft(trajectory: Trajectory) -> SkillDraft:
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


