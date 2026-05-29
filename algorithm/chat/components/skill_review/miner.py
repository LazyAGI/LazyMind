from __future__ import annotations

import json
from typing import Any

from lazyllm import LOG

from chat.components.skill_review.schemas import (
    CandidateSkill,
    CandidateSkillLLMOutput,
    GuidelineSet,
    SkillOutline,
    TaskCluster,
)
from chat.components.skill_review.json_call import call_json
from chat.prompts.skill_review import candidate_prompt, outline_prompt


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
    # # ---
    # with open('tmp/lazyrag-skill-dev3/user_unknown_user_time_20260527111333/05_outline.json', 'r') as f:
    #     outlines = json.load(f)
    # return [SkillOutline(**outline) for outline in outlines]
    # # ---
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
    # # ---
    # with open('/Users/jisiyuan/Project/LazyRAG/tmp/lazyrag-skill-dev3/user_unknown_user_time_20260527111333/06_candidate.json', 'r') as f:
    #     candidates = json.load(f)
    # return [CandidateSkill(**candidate) for candidate in candidates]
    # # ---
    guidelines = _collect_guidelines(cluster)
    payload = call_json(llm, candidate_prompt(outline, guidelines), CandidateSkillLLMOutput)
    normalized = _normalize_candidate_payload(
        payload,
        outline,
        source_trajectories=_collect_source_trajectories(cluster),
        source_skills=_collect_source_skills(cluster),
    )
    return CandidateSkill.model_validate(normalized)


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
