# Create Workflow Skill

## Purpose

Use this skill to design, create, generate, convert, polish, or repair LazyMind
Workflow artifacts.

This skill supports two execution contexts:

- **ChatAgent authoring**: an end user asks in chat to create a new Workflow.
- **Platform task generation**: an internal non-streaming `llm-task` job asks for
  one phase of Workflow generation, conversion, repair, or metadata polish.

First identify the execution context, then follow only the matching section.

## Context Dispatch

Use **Platform Task Mode** when the prompt or input says any of the following:

- `task_type` starts with `workflow.`
- The caller requests a JSON schema such as `workflow_yaml`, `state_yaml`,
  `scenario_md`, `scripts`, `edits`, `design_brief`, or `remaining_warnings`.
- The caller says it is an internal platform job, backend task, non-streaming
  task, generation phase, repair phase, or conversion phase.

Use **ChatAgent Authoring Mode** only when a human user is currently asking in
chat to create a new Workflow draft.

If both modes appear applicable, prefer **Platform Task Mode**. Backend jobs must
never be turned into an interactive chat flow.

## ChatAgent Authoring Mode

### When To Use

Use this mode only when the user explicitly asks to create a new Workflow.

Trigger conditions:

- "帮我创建一个…插件"
- "新建一个插件"
- "我想做一个插件"
- Any similar request that clearly asks to author a brand-new Workflow.

Do not use this mode when:

- The user wants to modify or fix an existing Workflow.
- The user is asking about how Workflows work.
- The user mentions a Workflow in passing but is not requesting creation.
- The caller is a platform/background task.

### Step 1 - Generate A Summary

Read the user's request carefully. Output a natural-language summary in this
exact format:

```text
插件名称：<display name>
功能描述：<one or two sentences describing what the workflow does>
输入槽位：<slot_id>（<type>，<cardinality>）<label>  - one per line
输出槽位：<slot_id>（<type>，<cardinality>）<label>  - one per line
主要步骤：1. <step_id>  2. <step_id>  3. <step_id>  ...
```

Rules for the summary:

- Slot `type` must be one of: `text`, `image`, `file`, `json`.
- Slot `cardinality` must be `single` or `list`.
- Keep steps to 2-5.
- Step ids must be English snake_case verb phrases, such as `extract_clauses`.
- Be specific enough that the generator can produce a working Workflow without
  guessing.

After printing the summary, immediately call `ask_user`:

```json
{
  "questions": [
    {
      "id": "confirm",
      "type": "boolean",
      "text": "以上是插件方案，是否确认创建？如需调整请点击「修改」并说明修改意见"
    }
  ]
}
```

This suspends the turn and waits for user input.

### Step 2 - Handle User Response

- If the user clicks Yes, 是, 确认, or otherwise affirms, proceed to Step 3.
- If the user clicks No, 否, 修改, or provides corrections, update the summary
  and call `ask_user` again.
- Repeat until the user confirms.

### Step 3 - Create The Draft

Call `create_workflow_draft` with:

- `name`: the confirmed Workflow display name.
- `description`: a comprehensive description combining the functional
  description, slot details, and step list.
- `slots`: the slot lines from the summary, one per line.
- `steps`: the step ids from the summary, one per line.

After the tool succeeds, write a brief confirmation message that:

1. States the Workflow is being generated.
2. Includes `[点击这里打开插件编辑器](<editor_url>)` using `editor_url` from the
   tool result.
3. Notes that generation runs in the background.

Do not output YAML in ChatAgent Authoring Mode.

## Platform Task Mode

### Core Rules

- Return only the JSON object requested by the caller.
- Do not use Markdown fences around JSON or YAML.
- Do not ask the user questions.
- Do not call `ask_user`.
- Do not call `create_workflow_draft`.
- Do not explain the result unless the requested JSON schema includes an
  explanation field.
- YAML content must be returned as JSON string values.
- Never use placeholders such as empty ids, empty names, `TODO`, `step_1`, or
  `to: ""`.
- If the source is ambiguous, choose a conservative executable design.

### Artifact Contract

`workflow.yaml` must contain:

- `id`: a non-empty English kebab-case or snake_case identifier.
- `name`: a non-empty display name.
- `slots`: all external inputs and step outputs.
- `steps`: 2 to 8 concrete processing steps.
- Optional `ui.tabs` that places exposed output materials.

Each slot should include:

- `id`
- `type`: `text`, `image`, `file`, or `json`
- `cardinality`: `single` or `list` when list behavior matters
- `external: true` for user-provided inputs
- `exposed: true` for user-visible outputs
- `label`

`scenario/state.yml` must contain:

- `transitions.__start__` with a real first step id.
- One `steps.<step_id>` block for every `workflow.yaml.steps[].id`.
- A transition path from every step to `__end__`.
- No directed cycles. Do not model retry, quality feedback, or refinement loops
  as control edges. Use a linear or acyclic branch/merge graph.
- Clear prompts for every step.
- `inputs` when the step consumes materials.
- `outputs` for materials produced by the step.

Every produced material must correspond to a slot in `workflow.yaml`.
Every non-external slot must be produced by exactly one step.
Every step must be reachable from `__start__` and able to reach `__end__`.

`scenario/scenario.md` must document the actual generated steps and materials.
It must contain one substantive section for every step id in `scenario/state.yml`.
Each step section must explain the step purpose, required inputs, produced
outputs, and runtime behavior. Never use placeholders such as `暂无描述`,
`TODO`, `TBD`, `no description`, or empty step sections.

### Exact UI Schema

Use this exact `workflow.yaml.ui.tabs` shape:

```yaml
ui:
  tabs:
    - id: results
      label: Results
      layout: vertical
      slots:
        - id: final_report
```

Do not use `key`, `contents`, `content`, `slot`, `material`, or other aliases
inside UI tabs. Every exposed slot must appear exactly once under
`ui.tabs[].slots[].id`.

### Exact Transition Schema

Use this exact `scenario/state.yml.transitions` shape:

```yaml
transitions:
  __start__:
    - to: collect_inputs
  collect_inputs:
    - to: synthesize_result
      when: "enough evidence was collected"
  synthesize_result:
    - to: __end__
```

Use `when` for natural-language branch hints. Do not use `condition` for route
hints. Do not create cycles or self-loops.

### Phase Behavior

For `workflow.analyze_skill`:

- Decide whether the source Skill can become a Workflow.
- Return the requested verdict, candidates, coverage, tool mapping, and script
  report.
- Prefer `generatable` when a safe executable design can be inferred.

For `workflow.design_brief`:

- Produce a design brief that is specific enough for later phases.
- Include slots, steps, transitions, UI placement, and script strategy.

For `workflow.generate_skeleton`:

- Return complete `workflow.yaml`.
- Include non-empty `id`, `name`, `slots`, and `steps`.
- Do not return `state_yaml` in this phase unless explicitly requested.

For `workflow.generate_state_machine`:

- Return complete `scenario/state.yml`.
- Return `workflow_yaml` only when it must be adjusted to match state.
- Ensure all step ids and material ids match `workflow.yaml`.

For `workflow.generate_scenario_scripts`:

- Return `scenario_md`.
- Include a complete section for every state step. Each section must be useful
  to a human editor and must not contain placeholder text.
- Return `scripts` only when safe helper scripts are necessary.
- Reuse caller-provided source scripts when they are safe and relevant.

For `workflow.repair`:

- Repair only the requested target unless diagnostics show a coupled file must
  also change.
- Prefer precise `edits` for small local changes:
  `{"file":"scenario/state.yml","old":"exact old text","new":"replacement"}`.
- Also include final full content for every repaired core file.
- Leave unrelated files empty or omitted.

For `workflow.polish_info`:

- Polish only the requested metadata fields.
- Keep text concise and suitable for Workflow catalog display.
