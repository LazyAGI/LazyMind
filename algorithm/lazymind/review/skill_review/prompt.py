# flake8: noqa: E501

from __future__ import annotations

import json
from typing import Any


def skill_extraction_gate_prompt(trajectory: str) -> str:
    return f"""
You are an expert Agent Experience Evaluation Engine.

Your task is to determine whether the trajectory contains reusable experience valuable enough for future skill extraction.

# Objective

Evaluate whether this trajectory should enter the skill mining pipeline.

The goal of skill extraction is NOT to preserve conversation history.

The goal is to discover reusable procedural knowledge, reasoning patterns, execution strategies, correction behaviors, or failure patterns that may generalize to future tasks.

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

# Do NOT extract trajectories that are mainly:

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

Do NOT judge based only on:
- task success
- trajectory length
- number of tool calls

A failed trajectory may still contain valuable reusable experience.

Focus on:
- procedural reuse potential
- reasoning value
- future generalizability

Use a conservative standard:
If reusable procedural value is weak or ambiguous, return should_extract = false.

# Output Format

Return ONLY valid JSON:

{{
  "should_extract": true,
  "confidence": 0.92,
  "value_type": [
    "reasoning_pattern",
    "retrieval_pattern",
    "constraint_handling"
  ],
  "reason": "The trajectory contains reusable retrieval refinement and adaptive replanning behaviors that causally contributed to task completion."
}}

# value_type candidates

- success_pattern
- failure_pattern
- reasoning_pattern
- retrieval_pattern
- tool_usage_pattern
- planning_pattern
- constraint_handling
- no_value

# Trajectory

{trajectory}
"""


def contextual_description_prompt(trajectory: str) -> str:
    return f"""
You are an expert Agent Memory Abstraction Engine.

Your task is to summarize the trajectory into a structured "contextual_description" for future task clustering and skill mining.

# Objective

Extract the high-level task context and execution outcome from the trajectory.

The output should describe:
1. What the agent tried to achieve
2. In what scenario/environment
3. What strategy/process it used
4. What key result/outcome was obtained
5. What tools/skills/environment were involved

# Requirements

- Focus on task-level abstraction instead of low-level actions
- Preserve only reusable and clusterable information
- Remove noisy operational details
- Avoid case-specific wording
- The description should be generic enough to group similar tasks together
- If the task failed, explicitly explain the failure reason briefly
- output should be in the same language as the trajectory

# Output Format

Return ONLY valid JSON:

{{
  "task_goal": "...",
  "applicable_scenario": "...",
  "execution_summary": "...",
  "key_result": "..."
}}

# Trajectory

{trajectory}
"""


def refined_trajectory_prompt(trajectory: str) -> str:
    return f"""
You are an expert Skill-oriented Trajectory Refinement Engine for autonomous agents.

Your task is to extract the MINIMAL EFFECTIVE TRAJECTORY from the raw execution trajectory.

The extracted steps will later be used to generate reusable agent skills.
Therefore, each step must be an ABSTRACT SKILL-LEVEL STEP, not a raw message summary.
output should be in the same language as the trajectory

# Core Objective

Identify only the key abstract steps that causally contributed to the final outcome.
**Reverse Causal Chain**: Refine the trajectory by reasoning backward from the final outcome.

Start from the final answer/result, then ask:
- What key evidence, decision, or correction made this outcome possible?
- What previous step produced that evidence, decision, or correction?
- Which earlier action changed the agent's state enough to enable the next critical step?

Only keep steps that appear on this backward causal chain.

Do NOT preserve a step merely because it happened earlier in the timeline.
If a step did not causally enable a later critical step, remove it.

This is NOT:
- a chronological summary
- a message-by-message compression
- a replay of messages or tool calls

This IS:
- a causal path extraction
- a reusable skill-step abstraction

# Step Granularity

A step should:
- represent a reusable reasoning or execution pattern
- be higher-level than a single message or tool call
- focus on intent, strategy, state transition, or critical decisions
- merge multiple low-level actions if they serve the same purpose

Do NOT create a step just because:
- the user sent a message
- the assistant replied
- a tool was called
- information appeared in the conversation

# Refinement Principles

Keep a step ONLY IF it:
- changed the agent's understanding
- changed execution strategy or direction
- retrieved or produced critical evidence
- corrected an important mistake
- directly contributed to success or failure
- introduced a reusable reasoning/action pattern

Remove steps that are:
- repetitive
- exploratory but useless
- operationally trivial
- low-information
- duplicated retries
- pure message restatements

# Action Field

The "action" field should describe:
- the abstract operation performed by the agent
- the reusable reasoning or execution pattern

Do NOT:
- copy/paraphrase user input
- describe raw conversation turns
- describe low-level tool operations unless strategically important

# State Field

The "state" field should describe:
- why this step mattered
- what new understanding, evidence, constraint, or decision state was produced
- how it affected subsequent execution

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
You are an expert Skill Review Refactoring Engine.

Your task is to convert an existing pending skill into a reusable skill draft.

# Objective

The pending skill is already structured, so extract only the three core parts needed by
the skill mining pipeline:

1. contextual_description
2. refined_trajectory
3. guidelines

# Requirements

- Use the title to identify the intended scenario, task goal, and capability.
- Split the skill content into meaningful operational steps for refined_trajectory.
- Summarize the guidance embedded in each step into concise guidelines.
- Keep the output abstract and reusable; do not copy Markdown headings mechanically.
- Do not include implementation metadata, ids, review status, or database fields.
- Output should be in the same language as the skill content.

# Output Format

Return ONLY valid JSON:

{{
  "contextual_description": {{
    "task_goal": "...",
    "applicable_scenario": "...",
    "execution_summary": "...",
    "key_result": "..."
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
You are an expert Skill Experience Extraction Engine.

Your task is to extract reusable strategic guidelines from the trajectory.
output should be in the same language as the trajectory

# Objective

Extract:
1. Success patterns that improved task performance
2. Failure patterns that caused inefficiency, errors, or bad decisions

The extracted guidelines will later become reusable skill knowledge.

# Important

Guidelines must be:
- reusable
- transferable
- strategy-level
- not case-specific
- not tied to concrete entities or data

Avoid:
- low-level operational instructions
- trajectory narration
- obvious statements
- generic advice without actionable meaning

# Success Pattern Definition

A success pattern is:
- an effective strategy
- a useful decision heuristic
- a reliable retrieval/execution pattern
- an effective verification behavior
- a useful planning behavior

# Failure Pattern Definition

A failure pattern is:
- a common reasoning mistake
- premature conclusions
- ineffective retrieval behavior
- missing verification
- redundant exploration
- tool misuse
- context misunderstanding

# related_step

Each guideline should be linked to the MOST relevant refined trajectory step.

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


def outline_prompt(task_scope: str, refined_trajectories: list[dict[str, Any]]) -> str:
    return f"""
You are an expert Skill Abstraction Engine for autonomous agents.

Your task is to synthesize a reusable Skill Outline from multiple refined trajectories belonging to the same task cluster.
output should be in the same language as the trajectory

# Objective

Extract the COMMON EXECUTION STRUCTURE shared across successful trajectories.

You are NOT summarizing trajectories.

You are abstracting a reusable SOP.

The output should describe:
- what the agent is trying to achieve at each stage
- how execution progresses
- where branching decisions occur
- what state should be achieved before moving forward

# Important Principles

## 1. Abstract actions into reusable procedural steps

BAD:
- "Search document A"
- "Read message from user"
- "Call tool X with parameter Y"

GOOD:
- "Retrieve missing evidence"
- "Validate retrieved information"
- "Refine retrieval strategy"
- "Compare candidate solutions"

Steps should represent reusable operational intentions,
NOT concrete trajectory events.

---

## 2. Merge semantically equivalent behaviors

Different trajectories may use:
- different tools
- different query wording
- different execution orders

If they serve the same execution purpose,
you should merge them into one abstract SOP step.

---

## 3. Preserve causal structure

The SOP should reflect:
- dependency between stages
- progression of agent state
- key decision points

Avoid flat chronological summaries.

---

## 4. Keep only stable and reusable patterns

Do NOT include:
- accidental behaviors
- noisy retries
- one-off observations
- user-specific details
- tool parameters
- concrete file names / entities

Only retain patterns likely to generalize.

---

# Input

You will receive:
1. task_scope
2. multiple refined trajectories

Each refined trajectory already contains:
- only causally important steps
- minimal effective execution path

# Output Schema

Return ONLY valid JSON.

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

## step_name
Short procedural stage name.

GOOD:
- Analyze Task Constraints
- Retrieve Supporting Evidence
- Validate Consistency
- Refine Execution Plan

BAD:
- Search BM25
- Read user message
- Use SQL tool

---

## action_goal
Describe:
- why this step exists
- what capability it provides
- what progress it enables

Focus on operational intent.

---

## branch_conditions
Only include meaningful decision points.

Examples:
- insufficient evidence retrieved
- conflicting results detected
- retrieval confidence too low
- execution path blocked

---

## expected_state
Describe the expected agent state after the step succeeds.

Examples:
- key constraints are identified
- sufficient evidence is collected
- candidate solution is validated
- execution uncertainty is reduced

# Input Data

TASK_SCOPE:
{task_scope}

REFINED_TRAJECTORIES:
{refined_trajectories.model_dump_json(indent=2)}"""


def candidate_prompt(outline: dict[str, Any], guidelines: dict[str, Any]) -> str:
    return f"""You are an expert Skill Composer for autonomous agents.

Your task is to transform a Skill Outline into a fully executable Candidate Skill.
output should be in the same language as the trajectory

You will receive an abstract SOP and noisy success/failure guidelines.
Your job is to synthesize them into a complete Agent Skills `SKILL.md` document that follows the agentskills.io standard used by Anthropic-style skills.

# Objective

Convert abstract SOP structure and trajectory-level experiences into reusable operational knowledge.

The final skill should help an agent:
- execute more reliably
- avoid common mistakes
- make better decisions
- self-check execution quality

The final document must read like a human-authored skill, not like a database dump. The `content` field must contain the full `SKILL.md` file content, including YAML frontmatter and Markdown instructions.

# Important Principles

## 1. Do NOT rewrite the SOP

The Skill Outline already defines:
- execution stages
- progression logic
- branching structure

Your job is to enrich each step,
NOT regenerate the workflow.

---

## 2. Integrate guidelines into prose

You will receive:
- success_patterns
- failure_patterns

These are noisy trajectory-level observations.

You must:
- merge related guidelines by meaning
- deduplicate them
- organize them under the relevant SOP step
- turn them into fluent operational guidance
- explain the intent and tradeoff when useful

Do NOT copy guideline lists directly into the output.
Do NOT create separate "Guidelines", "Success patterns", and "Failure patterns" bullet blocks under every step.
Do NOT preserve every guideline just because it appears in the input.

BAD:
- Goal: ...
- Guidelines:
  - ...
  - ...
- Success patterns:
  - ...
- Failure patterns:
  - ...

GOOD:
- Clarify the task goal before acting. Distinguish whether the user wants to test data content, tool behavior, or workflow behavior; this prevents downstream actions from targeting the wrong object. If the goal is already explicit, proceed directly instead of adding unnecessary confirmation.

---

## 3. Keep guidance procedural and actionable

Every enhancement should help execution.

Avoid:
- abstract philosophy
- vague advice
- trajectory summaries
- mechanical bullet aggregation
- raw guideline wording when it can be merged

Prefer:
- operational heuristics
- decision criteria
- failure prevention
- validation logic

---

## 4. Write a standards-compliant Agent Skill document

Use Markdown in the "content" field. The content is the entire `SKILL.md` file, not a summary and not a JSON representation of the skill.

Required structure:
- YAML frontmatter delimited by `---`
- `name` and `description` fields in frontmatter
- Markdown instructions after the closing `---`

Frontmatter requirements:
- `name` must be lowercase letters, numbers, and hyphens only
- `name` must be no more than 64 characters
- `name` must not start or end with a hyphen
- `description` must state when to use the skill and what reusable capability it provides
- keep frontmatter concise; do not put trajectory history in metadata

Recommended Markdown structure:
- H1 title
- "When To Use"
- "Procedure" or "Steps"
- Optional "Recovery And Edge Cases"
- Optional "Quality Checks"

Within each step:
- start with the step purpose
- include 2-4 integrated bullets or short paragraphs
- weave success and failure guidance into the same explanation
- include checks only when they clarify whether the step is complete

Avoid overly long step sections. Prefer concise, synthesized guidance.

---

## 5. Emphasize reliability

The skill should improve:
- robustness
- recovery ability
- decision quality
- execution consistency

# Input

You will receive:
1. Skill Outline
2. candidate success_patterns
3. candidate failure_patterns

# Output Schema

Return ONLY valid JSON.

{{
  "skill_name": "...",
  "applicable_scenario": "...",
  "content": "..."
}}

# Field Requirements

## content
A complete SKILL.md-style Markdown document.

The content must:
- be the full content of a valid `SKILL.md` file
- start with YAML frontmatter containing at least `name` and `description`
- use a portable skill name that follows lowercase hyphenated naming rules
- preserve the outline's procedure order
- synthesize guidelines into natural step guidance
- include recovery advice where failure patterns imply a branch
- include self-checks as integrated quality criteria
- keep source trajectory ids and implementation metadata out of the Markdown body
- avoid copying the input field names as section labels inside every step
- avoid dumping raw success/failure pattern lists

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
