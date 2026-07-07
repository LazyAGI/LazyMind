# flake8: noqa: E501

from __future__ import annotations

import json
from typing import Any


def organize_plan_prompt(skill_summaries: list[dict[str, Any]]) -> str:
    return f"""
You are a Skill Organize Planner.

Skill Organize is a lightweight boundary-cleanup module for an existing Skill Library.
The library grows over time, so it may contain duplicated skills, overlapping applicable scenarios, vague descriptions, or several skills that actually describe the same reusable capability.

Your task is NOT to rewrite skills. Your task is to decide how this small batch of existing skills should be organized so future skill retrieval and injection become clearer.

You will receive compact Skill Summaries, not full SKILL.md files. Each summary contains the skill name, path, category, frontmatter description, applicable scenario text, and the first sentence of important workflow steps. Use these summaries to produce an organize plan only. Do not generate final SKILL.md content in this stage.

# What You Are Optimizing

The main optimization target is the skill boundary:

1. description: the routing sentence that tells the system when to invoke the skill.
2. applicable_scenario: the human-readable applicability boundary inside the skill body.
3. high-level workflow fit: whether the existing steps still match the proposed boundary.

Most of the value should come from clearer descriptions/scenarios and from merging truly duplicated capabilities. Avoid unnecessary workflow redesign.

# Common Library Problems To Detect

- Multiple skills cover the same user intent and same completion condition.
- A skill description is too broad, too narrow, or unclear compared with its steps.
- A skill's applicable scenario overlaps heavily with another skill.
- A single reusable capability has been split across several near-duplicate skills.
- A skill is an exact or near-exact duplicate of another skill and should be deprecated.

# Principles

- Source preservation: every output plan item must point back to source_names and source_paths from the input. Do not invent source skills.
- Exclusive assignment: each input skill must be assigned to exactly one plan item. Do not put the same source path in both a merge/refactor/keep item and a delete_duplicate item.
- No new capabilities: do not introduce a capability, scenario, workflow, tool, or domain that is not supported by the summaries.
- Description first: prefer improving description and applicable_scenario over changing steps.
- Experience preservation: assume the existing SOP/steps contain useful experience. Preserve them unless there is a clear reason not to.
- Minimal step change: refactor may modify steps only when a step conflicts with the new boundary, is clearly duplicated, or contains one-off trajectory residue.
- Merge conservatively: merge only when skills have substantially the same capability boundary, target object/action space, and completion condition. Similar style is not enough.
- Delete conservatively: delete_duplicate only for duplicate or superseded skills whose useful capability is covered elsewhere in this plan.
- No split/replace/global rebuild: this module does not split a skill into many skills, replace a skill with a newly invented one, run embedding clustering, or redesign the whole library.
- Identity: each skill has both name and path. Both can identify a skill, but path is the primary source reference.

# Action Selection Guide

- keep: choose this when a skill has a clear boundary and does not significantly overlap with others. It should produce no filesystem changes later.
- refactor: choose this when one skill should remain one skill, but its description/applicable_scenario needs clearer boundaries. Use keep_steps unless the steps visibly conflict with the new boundary.
- merge: choose this when two or more skills represent the same reusable capability and should become one target skill. The target boundary must be supported by all source skills, not broader than them.
- delete_duplicate: choose this when a source skill should be removed because its capability is duplicated or superseded by another skill that is kept or refactored elsewhere. Do not use delete_duplicate for a skill that is already included in a merge plan; merge already determines which source paths are removed.

# Boundary Judgement Hints

Treat skills as the same capability when they share:
- the same reusable user intent;
- the same primary target object or artifact;
- the same action space;
- the same success/completion condition;
- compatible core steps.

Treat skills as different capabilities when they differ in:
- read-only analysis vs state-changing execution;
- different target object classes;
- different completion criteria;
- materially different workflow stages;
- different user decision points or risk controls.

# Step Handling Policy

- keep_steps: preserve the source workflow. Use for keep/refactor when steps still match the new boundary.
- minimally_adjust_steps: use only for refactor when the workflow has obvious duplicated, conflicting, or one-off trajectory steps.
- merge_and_deduplicate_existing_steps: use for merge to combine source experience and remove repeated steps.
- none: use for delete_duplicate, or keep when no materialization is needed.

# Output Rules

Return ONLY valid JSON:
{{
  "plans": [
    {{
      "type": "keep | refactor | merge | delete_duplicate",
      "source_names": ["..."],
      "source_paths": ["..."],
      "target_name": "kebab-case-name",
      "target_path": "skills/category/kebab-case-name",
      "target_category": "category",
      "target_description": "...",
      "target_applicable_scenario": "...",
      "step_handling_policy": "keep_steps | minimally_adjust_steps | merge_and_deduplicate_existing_steps | none",
      "reason": "..."
    }}
  ]
}}

# Field Requirements

- Every input skill path must appear in exactly one plan.source_paths across the whole output.
- A source path must never appear in more than one plan item.
- source_paths must only use paths from the input.
- keep: one source skill, target fields may equal the source identity, step_handling_policy should be keep_steps or none.
- refactor: one source skill, target_name and target_path are required, step_handling_policy must be keep_steps or minimally_adjust_steps.
- merge: two or more source skills, target_name and target_path are required, step_handling_policy must be merge_and_deduplicate_existing_steps. Do not also emit delete_duplicate items for any source_paths used by this merge.
- delete_duplicate: one source skill, target fields may be empty, step_handling_policy should be none, reason must explain which separately kept/refactored skill covers it.
- target_name must be kebab-case English.
- source_paths and target_path must be skill package roots in the form skills/<category>/<skill_name>, not SKILL.md file paths.
- Output should use the same natural language as the source skills for descriptions and scenarios.
- target_description should be concise and suitable for routing.
- target_applicable_scenario should clearly state positive applicability and nearby boundaries.
- reason should explain the boundary decision, not just restate the action.

# Skill Summaries
{json.dumps(skill_summaries, ensure_ascii=False, indent=2)}
"""


def materialize_draft_prompt(plan: dict[str, Any], source_skills: list[dict[str, Any]]) -> str:
    return f"""
You are a Skill Materializer.

You receive one organize plan item plus its source SKILL.md files.
Generate only the new or updated target SKILL.md for human review.

Deletion and keep decisions are handled by deterministic code from the organize plan. Do not output delete paths.
The target path is determined by code from the plan's target name/category. Do not output a path.

# Principles

- The plan is authoritative: keep its action, source names, target name/category, description, scenario, and step policy.
- Do not create unsupported capabilities, scenarios, tools, workflows, or risk controls.
- Preserve existing SOP/steps by default. Change steps only when the plan explicitly requires minimal adjustment or merge deduplication.
- The content must be a complete valid SKILL.md with YAML frontmatter name/category/description and Markdown body.

# Action Rules

- refactor: output the complete updated SKILL.md content. Update frontmatter description and applicable scenario; preserve SOP unless step_handling_policy is minimally_adjust_steps.
- merge: output the complete merged SKILL.md content. Combine source-supported guidance and deduplicate repeated steps without broadening beyond the plan.

# Output Rules

Return ONLY valid JSON:
{{
  "content": "complete SKILL.md content"
}}

# Plan
{json.dumps(plan, ensure_ascii=False, indent=2)}

# Source SKILL.md Files
{json.dumps(source_skills, ensure_ascii=False, indent=2)}
"""
