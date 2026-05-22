from __future__ import annotations

import json
from typing import Any


def draft_prompt(trajectory: dict[str, Any]) -> str:
    return (
        'You extract a reusable skill draft from one agent trajectory.\n'
        'Return JSON only with keys: contextual_description, refined_trajectory, guidelines.\n'
        'contextual_description has task_goal, applicable_scenario, execution_summary, key_result, environment.\n'
        'refined_trajectory has steps: step_index, role, action, state, tool_name, skill_name.\n'
        'guidelines has success_patterns and failure_patterns, each item has related_step and guideline.\n\n'
        f'TRAJECTORY:\n{json.dumps(trajectory, ensure_ascii=False, indent=2)}'
    )


def cluster_prompt(drafts: list[dict[str, Any]]) -> str:
    return (
        'Cluster skill drafts by task type. Return JSON only: {"clusters":[{"task_scope":"...","draft_indexes":[0]}]}.\n'
        f'DRAFTS:\n{json.dumps(drafts, ensure_ascii=False, indent=2)}'
    )


def outline_prompt(cluster: dict[str, Any]) -> str:
    return (
        'Create a reusable skill outline from this task cluster. Return JSON only with '
        'skill_name, applicable_scenario, sop where sop is an array of steps containing '
        'step_name, action_goal, branch_conditions, expected_state.\n'
        f'CLUSTER:\n{json.dumps(cluster, ensure_ascii=False, indent=2)}'
    )


def candidate_prompt(outline: dict[str, Any], guidelines: dict[str, Any]) -> str:
    return (
        'Write a complete SKILL.md candidate from this outline and guidelines. '
        'Return JSON only with skill_name, category, applicable_scenario, content.\n'
        f'OUTLINE:\n{json.dumps(outline, ensure_ascii=False, indent=2)}\n'
        f'GUIDELINES:\n{json.dumps(guidelines, ensure_ascii=False, indent=2)}'
    )


def resolution_prompt(candidate: dict[str, Any], called_skills: list[str]) -> str:
    return (
        'Resolve whether the candidate should be saved as a new skill or as a patch '
        'to an existing skill. Return JSON only with keys: type and suggestion. '
        'type must be either "new" or "patch". suggestion is a concise review note.\n'
        f'CALLED_SKILLS:\n{json.dumps(called_skills, ensure_ascii=False)}\n'
        f'CANDIDATE:\n{json.dumps(candidate, ensure_ascii=False, indent=2)}'
    )
