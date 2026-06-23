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
You are an expert Agent Memory Abstraction Engine, your task is to summarize the trajectory into a structured "contextual_description" for future task clustering and skill mining.

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

Synthesize one reusable Skill Outline from multiple refined trajectories in the same task cluster. Use the same language as the trajectories, except skill_name must always be an ASCII slug.

# Objective

Extract the COMMON EXECUTION STRUCTURE shared across trajectories. You are not summarizing individual trajectories; you are abstracting a reusable SOP that describes:
- what the agent is trying to achieve at each stage
- how execution progresses
- where branching decisions occur
- what state should be achieved before moving forward

# Abstraction Rules

- Steps must represent reusable operational intentions, not concrete events.
- Merge semantically equivalent behaviors even if tools, query wording, or order differ.
- Preserve causal structure: dependencies, state progression, and key decision points.
- Keep only stable patterns likely to generalize.
- Exclude accidental behavior, noisy retries, one-off observations, user-specific details, tool parameters, and concrete file names/entities.

BAD:
- "Search document A"
- "Call tool X with parameter Y"

GOOD:
- "Retrieve missing evidence"
- "Validate consistency before finalizing"

# Skill Name Rules

skill_name must:
- match ^[a-z0-9]+(?:-[a-z0-9]+)*$
- be <= 64 characters
- use lowercase ASCII letters, digits, and hyphens only
- contain no spaces, underscores, dots, slashes, uppercase letters, or non-ASCII characters

# Mandatory SOP Constraints

Every SOP MUST begin with step_name = "Anchor on Task Goal".
- action_goal: extract objectives, explicit constraints, and completion criteria so all later work can be checked against them.
- branch_conditions must include incompatible constraints -> abort and state the conflict.
- branch_conditions must include task fits SOP -> use extracted criteria as the checklist.
- expected_state: all objectives, constraints, and completion criteria are explicitly listed and understood.

Every SOP can end with step_name = "Verify Completion".
- action_goal: confirm every criterion from the anchoring step has been satisfied before declaring success or failure.
- branch_conditions must include all criteria satisfied -> report success with evidence.
- branch_conditions must include any criterion unmet -> report the unmet criterion and do not claim success.
- expected_state: every anchoring criterion has been independently checked.

For every SOP step:
- include at least one meaningful branch_condition.
- avoid tautologies such as "continue", "try again", or "if successful proceed".
- expected_state should describe the completed state and, when useful, when the step can be skipped.

Across the SOP, include all of these gates at least once:
- goal-completion gate: current state already satisfies the task -> skip remaining work.
- constraint-boundary gate: this action would violate a known constraint -> take corrective action instead.
- information-quality gate: available information is insufficient for a decision -> gather more before proceeding.

BAD branch_conditions:
- "Continue to next step."
- "If successful, proceed."

GOOD branch_conditions:
- "If available evidence is insufficient for a decision, gather or validate more evidence before choosing."
- "If a requested action violates an explicit constraint, stop that action and select a compliant alternative."

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

# Step Writing Rules

- step_name: short procedural stage name.
- action_goal: explain why the step exists, what capability it provides, and what progress it enables.
- branch_conditions: real decision points with different next actions.
- expected_state: observable state after successful completion, including skip condition when useful.

# Input Data

TASK_SCOPE:
{task_scope}

REFINED_TRAJECTORIES:
{refined_trajectories.model_dump_json(indent=2)}"""


def candidate_prompt(outline: dict[str, Any], guidelines: dict[str, Any]) -> str:
    return f"""You are an expert Skill Composer for autonomous agents.

Transform a Skill Outline into a complete executable Agent Skill. Use the same language as the trajectories for prose, but skill_name and YAML frontmatter name must always be ASCII slugs.

You receive:
1. an abstract SOP
2. noisy success_patterns and failure_patterns

Your job is to synthesize them into a human-authored `SKILL.md` document. The `content` field must contain the full file content, including YAML frontmatter and Markdown instructions.

# Core Objectives

- Preserve the outline's execution order, stage progression, and branching structure.
- Enrich each SOP step with deduplicated, integrated operational guidance.
- Make the skill specific enough that an agent can decide when to use it and when NOT to use it.
- Improve reliability, recovery, decision quality, and self-checking.
- Do not merge different action spaces into one skill, especially read-only workflows with state-changing workflows.

# Guideline Integration

Merge related guidelines by meaning, deduplicate them, organize them under relevant SOP steps, and turn them into fluent procedural guidance. Explain intent/tradeoff when useful. Do not copy raw guideline lists or preserve every guideline just because it appears in input.

BAD:
- "Guidelines: ..., Success patterns: ..., Failure patterns: ..."
- "Step 2 says to validate; validate things carefully."

GOOD:
- "Before acting on retrieved evidence, compare it against the task constraints; if the evidence cannot support every required criterion, gather more instead of finalizing."

# SKILL.md Structure

The content must be a complete `SKILL.md`-style Markdown document:
- YAML frontmatter delimited by `---`
- frontmatter includes `name` and `description`
- Markdown instructions after the closing `---`

Frontmatter rules:
- `name` exactly equals JSON skill_name.
- `name` must match ^[a-z0-9]+(?:-[a-z0-9]+)*$, be <=64 chars, and use lowercase ASCII letters/digits/hyphens only.
- `description` must follow: "When [ONE specific triggering condition] - [what the agent gains by following this skill] (NOT [most confusable alternative scenario])"
- The (NOT ...) clause is required and should provide a concrete exclusion test.

BAD description:
- "When you need to process structured data - provides systematic data handling"

GOOD description:
- "When you need to validate structured data against a known schema - provides systematic constraint checking (NOT for exploratory data browsing or ad-hoc queries)"

Required Markdown sections in order:
1. H1 title
2. "When To Use"
3. "Do Not Use When"
4. "Procedure" or "Steps"
5. Optional "Recovery And Edge Cases"
6. Optional "Quality Checks"

# Scope Boundary

"When To Use" must describe exactly one observable triggering scenario and what must be true before invoking the skill. Narrow the trigger when possible to avoid false-positive matches.

"Do Not Use When" must include at least two exclusions:
- nearest-neighbor exclusion: a superficially similar task with a different objective or action space
- constraint-mismatch exclusion: access, scope, or task constraints make this skill's approach unsuitable

The frontmatter NOT clause should echo the nearest-neighbor exclusion. Scope rejection should appear before substantive action.

# Procedure Quality

Within each step:
- start with the step purpose
- include 2-4 concise bullets or short paragraphs
- weave success and failure guidance into the same explanation
- include checks only when they clarify completion or constraint satisfaction
- include recovery advice where failure patterns imply a branch
- if constraints conflict, the constraint that limits action space takes priority

Avoid abstract philosophy, vague advice, trajectory summaries, mechanical bullet aggregation, source trajectory ids, implementation metadata, and raw input field names as repeated section labels.

# Pre-Output Self-Critique

Silently revise before output:
- Would the skill reject out-of-scope tasks early enough?
- Is the most likely false-positive match explicitly excluded?
- Does the skill still work when the task has more constraints than the source trajectories?
- Do JSON skill_name and frontmatter name match all slug rules?
- Is the output a complete SKILL.md file, not a summary?

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
