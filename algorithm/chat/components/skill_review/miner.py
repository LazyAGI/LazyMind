from __future__ import annotations

import re

from chat.components.skill_review.llm import SkillReviewLLM
from chat.components.skill_review.schemas import (
    CandidateSkill,
    GuidelineSet,
    SkillOutline,
    SkillOutlineStep,
    TaskCluster,
)
from chat.prompts.skill_review import candidate_prompt, outline_prompt


def build_skill_outline(cluster: TaskCluster, llm: SkillReviewLLM) -> SkillOutline:
    try:
        payload = llm.complete_json(outline_prompt(cluster.model_dump()))
        return SkillOutline.model_validate(payload)
    except Exception:
        return _fallback_outline(cluster)


def build_candidate_skill(
    cluster: TaskCluster,
    outline: SkillOutline,
    llm: SkillReviewLLM,
) -> CandidateSkill:
    guidelines = _collect_guidelines(cluster)
    try:
        payload = llm.complete_json(candidate_prompt(outline.model_dump(), guidelines.model_dump()))
        payload['outline'] = outline.model_dump()
        return CandidateSkill.model_validate(payload)
    except Exception:
        return _fallback_candidate(outline, guidelines)


def _fallback_outline(cluster: TaskCluster) -> SkillOutline:
    craft = cluster.crafts[0]
    steps = []
    for step in craft.refined_trajectory.steps[:8]:
        steps.append(
            SkillOutlineStep(
                step_name=f'Step {len(steps) + 1}',
                action_goal=step.action[:240],
                branch_conditions=[],
                expected_state=step.state or 'The task advances toward the expected result.',
            )
        )
    if not steps:
        steps.append(
            SkillOutlineStep(
                step_name='Understand the task',
                action_goal='Clarify the task goal and identify the useful tools or skills.',
                branch_conditions=[],
                expected_state='The task has a concrete execution plan.',
            )
        )
    return SkillOutline(
        skill_name=_skill_name_from_text(craft.contextual_description.task_goal),
        applicable_scenario=craft.contextual_description.applicable_scenario or cluster.task_scope,
        sop=steps,
    )


def _fallback_candidate(outline: SkillOutline, guidelines: GuidelineSet) -> CandidateSkill:
    lines = [
        '---',
        f'name: {outline.skill_name}',
        f'description: {outline.applicable_scenario[:200]}',
        '---',
        '',
        f'# {outline.skill_name}',
        '',
        '## When To Use',
        outline.applicable_scenario,
        '',
        '## Steps',
    ]
    for idx, step in enumerate(outline.sop, start=1):
        lines.extend([
            f'{idx}. {step.step_name}',
            f'   - Goal: {step.action_goal}',
            f'   - Expected state: {step.expected_state}',
        ])
        if step.branch_conditions:
            lines.append(f'   - Branches: {"; ".join(step.branch_conditions)}')
    if guidelines.success_patterns:
        lines.extend(['', '## Success Patterns'])
        for item in guidelines.success_patterns:
            lines.append(f'- {item.guideline}')
    if guidelines.failure_patterns:
        lines.extend(['', '## Failure Patterns'])
        for item in guidelines.failure_patterns:
            lines.append(f'- {item.guideline}')
    return CandidateSkill(
        skill_name=outline.skill_name,
        category='general',
        applicable_scenario=outline.applicable_scenario,
        content='\n'.join(lines).strip() + '\n',
        outline=outline,
    )


def _collect_guidelines(cluster: TaskCluster) -> GuidelineSet:
    success = []
    failure = []
    for craft in cluster.crafts:
        success.extend(craft.guidelines.success_patterns)
        failure.extend(craft.guidelines.failure_patterns)
    return GuidelineSet(success_patterns=success, failure_patterns=failure)


def _skill_name_from_text(text: str) -> str:
    words = re.findall(r'[A-Za-z0-9]+', text or '')
    if words:
        return '-'.join(words[:5]).lower()
    return 'reviewed-skill'
