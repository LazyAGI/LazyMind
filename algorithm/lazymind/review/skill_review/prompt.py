# flake8: noqa: E501

from __future__ import annotations

import json
from typing import Any


def skill_extraction_gate_prompt(trajectory: str) -> str:
    return f"""
You are an expert Agent Experience Evaluation Engine, your task is to decide whether this trajectory should enter the skill mining pipeline. 
The goal is NOT to preserve conversation history; it is to find reusable procedural knowledge, reasoning patterns, execution strategies, correction behaviors, or failure patterns that can generalize to future tasks.

# Extraction Threshold (Strict)

A trajectory SHOULD be extracted ONLY IF it satisfies MOST of the following conditions:

- the task required non-trivial multi-step reasoning or execution
- the agent dynamically adjusted strategy, retrieval direction, or planning
- the trajectory contains reusable procedural patterns rather than task-specific facts
- at least one critical decision, correction, refinement, or constraint-handling process affected the final outcome
- the agent state meaningfully changed during execution
- the trajectory demonstrates a reusable way to solve or diagnose a class of problems
- the execution path cannot be trivially replaced by a single response or direct retrieval

Typical high-value signals include:
- retrieval refinement after initial failure
- iterative evidence validation
- conflict resolution between multiple sources
- adaptive tool selection
- decomposition of complex tasks
- recovery from incorrect assumptions or failed execution
- constraint-aware replanning
- reusable failure diagnosis patterns

Do NOT extract trajectories that are mainly:
- casual conversation
- simple factual Q&A
- one-shot responses
- direct rewriting or translation
- straightforward tool execution without reasoning
- linear retrieval with no strategy evolution
- repetitive operational interactions
- trajectories dominated by task-specific content instead of reusable procedures
- sessions where the final outcome mainly depended on memorized knowledge rather than execution strategy

# Important

Do not judge by task success, trajectory length, or number of tool calls alone. A failed trajectory can be valuable if it teaches a reusable failure or recovery pattern. Use a conservative standard: if reusable procedural value is weak or ambiguous, return should_extract=false.

# Output Format

Return ONLY valid JSON:
{{
  "should_extract": true,
  "confidence": 0.92,
  "value_type": ["reasoning_pattern", "retrieval_pattern", "constraint_handling"],
  "reason": "The trajectory contains reusable retrieval refinement and adaptive replanning behaviors that causally contributed to task completion."
}}

value_type candidates: success_pattern, failure_pattern, reasoning_pattern, retrieval_pattern, tool_usage_pattern, planning_pattern, constraint_handling, no_value.

# Trajectory
{trajectory}
"""


def cluster_signature_prompt(trajectory: str) -> str:
    return f"""
You are an expert Agent Memory Abstraction Engine.

Your task is to extract a compact "cluster_signature" for future task clustering and skill mining.

# Objective

Extract only the reusable task structure needed to decide whether multiple drafts should become one skill.

The output should describe:
1. The reusable task intent
2. The high-level reusable procedure
3. The applicability boundary for the skill

# Requirements

- Preserve reusable workflow structure, not case-specific details
- Describe the broad reusable skill family, not the narrow observed case
- Keep wording general enough for reusable skill mining, but not vague
- Remove names, ids, dates, locations, prices, exact quantities, and incidental tool errors
- Do not mention exact tool names unless they define the reusable task
- Do not include every observed root cause in the intent; prefer a task-family description
- Do not include fallback options, alternative resolutions, or customer choice variants in the intent unless they define a materially different workflow
- Merge adjacent diagnostics into broader steps when they belong to the same troubleshooting workflow
- Use 3-6 procedure steps
- Boundaries must be one concise paragraph describing the positive applicability scope and only materially different workflows it should not cover
- Do not exclude nearby variants that the same reusable procedure can handle
- Do not exclude alternative remediation options, fallback paths, optional checks, or customer preference variants that belong to the same task family
- Do not exclude cases based on incidental episode outcomes, such as whether a tool succeeded or failed
- Avoid vague phrases like "help the user" or "solve the issue"
- output should be in the same language as the trajectory

# Output Format

Return ONLY valid JSON:
{{
  "intent": "...",
  "procedure": ["...", "...", "..."],
  "boundaries": "..."
}}

# Trajectory
{trajectory}
"""


def refined_trajectory_prompt(trajectory: str) -> str:
    return f"""
You are an expert Skill-oriented Trajectory Refinement Engine.

Extract the MINIMAL EFFECTIVE TRAJECTORY from the raw execution trajectory. The result will be used to generate reusable agent skills, so each step must be an abstract skill-level step, not a raw conversation summary. Use the same language as the trajectory.

# Core Method: Reverse Causal Chain

Reason backward from the final answer/result:
- What evidence, decision, correction, or constraint made the outcome possible?
- What earlier step produced that state?
- Which action changed the agent's understanding or execution direction enough to enable the next critical step?

Keep only steps on this causal chain. Do not preserve a step merely because it happened in the timeline.

# Step Granularity

A retained step should:
- represent a reusable reasoning or execution pattern
- be higher-level than one message or tool call
- focus on intent, strategy, state transition, or critical decision
- merge multiple low-level actions when they serve the same purpose

Keep a step ONLY IF it preserved a task-critical constraint, changed understanding, changed execution strategy, produced critical evidence, corrected an important mistake, directly contributed to success/failure, or introduced a reusable reasoning/action pattern.

Remove steps that are repetitive, exploratory but useless, operationally trivial, low-information, duplicated retries, pure message restatements, or raw tool calls with no strategic meaning.

BAD:
- "The user asked a question."
- "The assistant called search."

GOOD:
- "Clarify the task boundary before choosing an execution path."
- "Validate conflicting evidence before committing to the final answer."

# Field Rules

- action: describe the abstract operation and reusable pattern; do not copy/paraphrase user input.
- state: describe the critical state produced, why it mattered, what remains unsatisfied, and any similar but incorrect alternative when relevant.

# Output Format

Return ONLY valid JSON:
{{
  "steps": [
    {{
      "step_index": 1,
      "action": "...",
      "state": "..."
    }}
  ]
}}

# Trajectory
{trajectory}
"""


def pending_skill_draft_prompt(skill_name: str, skill_content: str) -> str:
    return f"""
You are an expert Skill Review Refactoring Engine, your task is to convert an existing pending skill into a reusable skill draft.
The pending skill is already structured, so extract only the three core parts needed by the skill mining pipeline:

1. cluster_signature
2. refined_trajectory
3. guidelines

# Requirements

- Use the title and content to identify the reusable intent, procedure, and applicability boundary.
- Split the skill content into meaningful operational steps for refined_trajectory.
- Summarize the guidance embedded in each step into concise guidelines.
- Keep the output abstract and reusable; do not copy Markdown headings mechanically.
- Do not include implementation metadata, ids, review status, or database fields.
- Output should be in the same language as the skill content.

# Output Format

Return ONLY valid JSON:
{{
  "cluster_signature": {{
    "intent": "...",
    "procedure": ["...", "...", "..."],
    "boundaries": "..."
  }},
  "refined_trajectory": {{
    "steps": [
      {{
        "step_index": 1,
        "action": "...",
        "state": "..."
      }}
    ]
  }},
  "guidelines": {{
    "success_patterns": [
      {{
        "related_step": 1,
        "guideline": "..."
      }}
    ],
    "failure_patterns": [
      {{
        "related_step": 1,
        "guideline": "..."
      }}
    ]
  }}
}}

# Skill Title
{skill_name}

# Skill Content
{skill_content}
"""


def guidelines_prompt(
    trajectory: str,
    refined_trajectory: dict
) -> str:
    return f"""
You are an expert Skill Experience Extraction Engine, your task is to extract reusable strategic guidelines from the trajectory, the output should be in the same language as the trajectory

# Objective

Extract:
1. Success patterns that improved task performance
2. Failure patterns that caused inefficiency, errors, or bad decisions

The extracted guidelines will later become reusable skill knowledge.

# Important

Guidelines must be reusable, transferable, strategy-level, and actionable. They must not be tied to concrete entities, raw data, or one specific case.
Avoid low-level operational instructions, trajectory narration, obvious statements or generic advice without actionable meaning.

Pattern definitions:
- success pattern: effective strategy, decision heuristic, retrieval/execution pattern, verification behavior, or planning behavior.
- failure pattern: reasoning mistake, premature conclusion, ineffective retrieval, missing verification, redundant exploration, tool misuse, or context misunderstanding.

Each guideline should link to the most relevant refined trajectory step.

# Output Format

Return ONLY valid JSON:
{{
  "success_patterns": [
    {{
      "related_step": 1,
      "guideline": "..."
    }}
  ],
  "failure_patterns": [
    {{
      "related_step": 2,
      "guideline": "..."
    }}
  ]
}}

# Refined Trajectory
{refined_trajectory}

# Raw Trajectory
{trajectory}
"""


def draft_prompt(trajectory: dict[str, Any]) -> str:
    return (
        'You extract a reusable skill draft from one agent trajectory.\n'
        'Return JSON only with keys: cluster_signature, refined_trajectory, guidelines.\n'
        'cluster_signature has intent, procedure, boundaries.\n'
        'refined_trajectory has steps: step_index, role, action, state, tool_name, skill_name.\n'
        'guidelines has success_patterns and failure_patterns, each item has related_step and guideline.\n\n'
        f'TRAJECTORY:\n{json.dumps(trajectory, ensure_ascii=False, indent=2)}'
    )


def cluster_prompt(drafts: list[dict[str, Any]]) -> str:
    return (
        'Cluster skill draft signatures into reusable skill families.\n'
        'Return JSON only: {"clusters":[{"task_scope":"...","draft_indexes":[0]}]}.\n\n'
        'Merge drafts when they share the same reusable task intent, high-level procedure, and applicability scope.\n'
        'Do not split drafts merely because one case has an extra root cause, a different outcome, a tool failure, '
        'a different language/style, or a narrower boundary statement.\n'
        'Do not split drafts merely because one includes an extra fallback option, alternative remediation path, '
        'plan/customer choice variant, or broader/narrower wording.\n'
        'Keep drafts separate only when an agent would need a materially different procedure or the combined skill '
        'would become ambiguous.\n'
        'A singleton cluster is allowed only when no existing cluster can handle that draft without changing the core procedure.\n'
        'If a draft differs only by an extra fallback option, broader wording, or an alternative customer choice, '
        'merge it into the closest broader cluster.\n'
        'Every draft index must appear exactly once. Use the provided draft_index values.\n\n'
        f'DRAFT_SIGNATURES:\n{json.dumps(drafts, ensure_ascii=False, indent=2)}'
    )


def outline_prompt(task_scope: str, refined_trajectories: list[dict[str, Any]]) -> str:
    return f"""
You are an expert Skill Abstraction Engine for autonomous agents.
Given multiple refined trajectories from the same semantic cluster, synthesize ONE reusable Skill Outline.
Use the same language as the trajectories for all natural-language fields. The only exception is `skill_name`, which must be concise English kebab-case.

## Objective

Abstract the common execution pattern rather than summarizing individual trajectories.
The Skill should capture reusable execution logic that can generalize to similar future tasks, including:
* when the skill applies
* what objective it achieves
* reusable execution stages
* key decision points
* expected state after each stage

## Abstraction Principles

* A Skill represents one reusable capability: broader than a single execution trace but narrower than an entire workflow.
* Generalize intentions instead of concrete actions.
* Merge semantically equivalent behaviors across trajectories.
* Preserve causal dependencies, prerequisites, state progression, and meaningful decisions.
* Keep only reusable patterns.
* Remove tool names, parameters, entities, retries, implementation details, and user-specific information.
* Do not invent behaviors unsupported by the trajectories.

## Output Fields

### skill_name

Generate a concise English kebab-case name describing the reusable capability.

### applicable_scenario

Summarize:

* the reusable trigger
* required prerequisites
* the nearest exclusions that distinguish this Skill from adjacent Skills

The description should be concise, reusable, and retrieval-friendly.

### sop.steps

Represent the Skill as reusable execution stages.

Each step contains:

* **step_name**: concise procedural stage name.
* **action_goal**: the purpose of the stage and the progress it enables.
* **branch_conditions**: include only genuine decision points. Avoid trivial transitions such as "continue" or "proceed". Each branch contains:

  * `condition`
  * `next_action`
* **expected_state**: the observable state indicating the stage is complete. Mention when the stage may be skipped if appropriate.

## Output Philosophy

Prefer evidence over generic best practices.

Never invent reusable logic that is not supported by the trajectories.


# Output Schema

Return ONLY valid JSON:
{{
  "skill_name": "...",
  "applicable_scenario": "...",
  "sop": {{
    "steps": [
      {{
        "step_name": "...",
        "action_goal": "...",
        "branch_conditions": [
          {{
            "condition": "...",
            "next_action": "..."
          }}
        ],
        "expected_state": "..."
      }}
    ]
  }}
}}

# Input Data

TASK_SCOPE:
{task_scope}

REFINED_TRAJECTORIES:
{refined_trajectories.model_dump_json(indent=2)}"""


def candidate_prompt(outline: dict[str, Any], guidelines: dict[str, Any]) -> str:
    return f"""You are an expert Skill Materializer for autonomous agents.
Your task is to transform a **Skill Outline** into a complete reusable `SKILL.md`.
The Skill Outline already defines the workflow. Your job is **not** to redesign or expand the workflow, but to enrich each procedure step with reusable operational knowledge distilled from the provided success and failure guidelines.
The output should read like a human-authored operational playbook rather than a collection of extracted observations.

# Inputs
You receive:
1. A Skill Outline
   * skill_name
   * applicable_scenario
   * SOP
2. Success patterns collected from many trajectories.
3. Failure patterns collected from many trajectories.

The guidelines are intentionally noisy:

* they may overlap;
* they may describe the same idea differently;
* they may belong to different SOP steps;
* they may contain trajectory-specific details that should not appear in the final skill.

Your job is to extract the reusable operational knowledge.

# Core Principles

Treat the Skill Outline as the authoritative workflow definition.

Do **not** modify:

* the skill scope;
* the applicable scenario;
* the overall procedure order;
* the logical progression between steps.

Your responsibility is to make each existing step more executable, reliable, and reusable.

# Guideline Alignment

The provided guidelines are **not pre-aligned** with the SOP.

For each guideline:

1. Determine which SOP step it best supports based on semantic meaning.
2. Attach it to the single most appropriate step.
3. Ignore trajectory-specific ordering or metadata.

Do not create new procedure steps simply because a guideline does not perfectly match an existing one.

# Guideline Consolidation

Each SOP step may receive many success and failure patterns.

For each step:

* identify the common operational intent shared by the attached guidelines;
* merge semantically equivalent guidance;
* remove duplicated, overly specific, or trajectory-dependent information;
* rewrite the remaining knowledge into concise, reusable operational guidance.

The final step should read as if written by an experienced engineer, not assembled from multiple trajectories.

# Writing the Procedure

Preserve the original SOP structure.

For each procedure step:

* organize the content around the step's objective;
* describe how the step is typically executed;
* naturally integrate useful success practices into the execution flow;
* integrate failure avoidance only where it improves decisions or prevents common mistakes;

Avoid repeating similar guidance across multiple steps.

# Scope Control

The applicable_scenario defines the reusable boundary of this skill.

Do not broaden its scope.

Discard guidelines that belong to adjacent workflows or depend on one-off environments, specific tools, identifiers, datasets, or implementation artifacts.

Retain reusable reasoning, decision logic, and operational practices.

# Frontmatter

Generate a complete `SKILL.md` document.

The YAML frontmatter should include:

* name
* description
* category

The description should clearly express:

* when the skill applies;
* what following the skill enables;
* the nearest scenario that should **not** use this skill.
* consistent with the applicable_scenario.

The category should:
* be a concise lowercase classification for the skill, such as `research`, `coding`, `writing`, `data-analysis`, `tool-use`, `planning`, `debugging`, `review`, or `general`
* describe the reusable task family, not the source trajectory, user, project, or implementation detail

# Markdown Structure

The generated document should contain:

* YAML frontmatter
* Procedure (or Steps)

Optionally include:

* Recovery and Edge Cases
* Quality Checks

If recovery guidance only applies to one step, integrate it into that step instead of creating a separate section.

# Final Review

Before producing the result, verify that:

* every SOP step is preserved;
* no new top-level procedure step has been introduced;
* every guideline has either been integrated into an appropriate step or intentionally discarded as out of scope;
* duplicated guidance has been consolidated;
* the resulting document is coherent, executable, and reusable rather than a summary of trajectory fragments.

# Output Schema

Return ONLY valid JSON:
{{
  "skill_name": "...",
  "applicable_scenario": "...",
  "content": "..."
}}

# Input Data

SKILL_OUTLINE:
{outline.model_dump_json(indent=2)}

STEP_GUIDELINES:
{guidelines.model_dump_json(indent=2)}"""


def resolution_prompt(candidate: dict[str, Any], called_skills: dict[str, str]) -> str:
    return (
        'Resolve whether the candidate skill should be saved as a new skill or used '
        'to patch one of the called skills.\n\n'
        'You receive:\n'
        '1. CANDIDATE_SKILL: a newly mined candidate skill.\n'
        '2. CALLED_SKILLS: existing skills used in the source trajectories, as a map '
        'from skill name to full skill content.\n\n'
        'Choose type="patch" only when the candidate clearly improves, corrects, or '
        'extends an existing called skill. Otherwise choose type="new".\n\n'
        'Return ONLY valid JSON with these keys:\n'
        '- type: "new" or "patch"\n'
        '- patch_skill_name: required when type="patch"; the called skill name to patch\n'
        '- summary: for patch, describe the intent of this modification; for new, use null\n'
        '- patched_skill: when type="patch", the full patched SKILL.md content; when type="new", use an empty string\n\n'
        f'CALLED_SKILLS:\n{json.dumps(called_skills, ensure_ascii=False, indent=2)}\n\n'
        f'CANDIDATE_SKILL:\n{json.dumps(candidate, ensure_ascii=False, indent=2)}'
    )
