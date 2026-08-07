# Workflow package format v1

The package currently retains the physical filename `workflow.yaml` for storage
compatibility. Its domain content and all Agent-facing language are Workflow.

## Required tree

```text
workflow.yaml
scenario/state.yml
scenario/scenario.md
```

Optional paths are `scenario/layout.json` and `scripts/<relative-path>`. Absolute
paths and `..` traversal are invalid.

## `workflow.yaml`

Minimum shape:

```yaml
id: research-report
name: Research Report
description: Produce an evidence-grounded report with reviewable outputs.
when_to_use: Use when the user requests a researched report; do not use for a quick factual answer.
steps:
  - id: collect
    label: Collect evidence
  - id: draft
    label: Draft report
slots:
  - id: evidence
    label: Evidence
    type: json
    cardinality: list
  - id: report
    label: Report
    type: text
    cardinality: single
```

Rules:

- `id`, `name`, `description`, and step declarations are required.
- Step ids must exactly match `state.yml` step keys.
- Slot/material types are `text`, `image`, `file`, or `json`; cardinality is
  `single` or `list`.
- Mark a material `external: true` only when the user supplies it separately.
- User query, task description, topic, prompt, and instructions are not materials;
  steps receive them through execution context.
- Every non-external material has exactly one producing step.
- Optional UI tabs may reference declared slot ids; UI never changes graph state.

## `scenario/state.yml`

```yaml
initial: __start__
transitions:
  __start__:
    - to: collect
  collect:
    - to: draft
  draft:
    - to: __end__
steps:
  collect:
    label: Collect evidence
    prompt: |
      Collect traceable evidence for {{user_input}}.
      Save every declared output and stop.
    tools: [web_search]
    outputs:
      - material: evidence
    acceptance_criteria: Evidence contains sources and directly supports the request.
  draft:
    label: Draft report
    prompt: |
      Write the report from {{evidence}} for {{user_input}}.
      Apply {{runtime_instruction}}, save the report output, and stop.
    inputs:
      - material: evidence
        required: true
    outputs:
      - material: report
    acceptance_criteria: Report addresses the request and every material claim is supported.
```

Rules:

- `initial` is `__start__`; `__start__` and `__end__` are reserved virtual nodes.
- Every transition target and source must be valid; terminal paths reach `__end__`.
- A Ready step's required inputs must be produced by control ancestors.
- Required inputs are AND. One level of `alternatives` provides OR for a single
  required input. Optional inputs cannot declare alternatives.
- `route: choice` means the Agent selects one applicable outgoing condition;
  default/all may expose independent applicable successors.
- `skip_if` accepts one level of material-based `all` or `any`; do not place free
  natural-language policy in it.
- Prompt placeholders are `{{user_input}}`, `{{runtime_instruction}}`, and declared
  input material ids only.
- Tools must exist in the conversion context catalog. Credentials and model config
  never appear in tool declarations or prompts.
- Every declared output is required. Acceptance criteria must be observable from
  the output rather than internal reasoning.

## `scenario/scenario.md`

Use descriptive prose for the ChatAgent:

```markdown
# Research Report

## Appropriate use
Use for multi-source reports requiring evidence and a reviewable final document.
Do not use for quick answers without a report deliverable.

## Workflow
1. `collect` gathers traceable evidence.
2. `draft` produces the report from that evidence.

## Inputs and outputs
The user supplies the research request. The Workflow produces evidence and report Artifacts.
```

Do not repeat framework advance/retry rules here; those belong to this Agent Kit.

## Safety and publication

- Prefer registered framework tools over scripts.
- Never embed secrets, local absolute paths, temporary URLs, private reasoning,
  Host model configuration, or direct database operations.
- Scripts require matching deterministic audit hashes and approved classifications.
- Compiler and publish diagnostics are authoritative; a syntactically valid YAML
  file can still be invalid due to graph, lineage, tool, or safety constraints.
