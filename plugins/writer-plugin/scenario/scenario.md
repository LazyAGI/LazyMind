# AI Writer Plugin

## Scenario

Help users compose structured long-form writing, including articles, reports, technical documents, creative stories, fiction, short stories, and novel-style drafts. The workflow runs in six steps:

1. **build_context** — parse the writing intent, target audience, core sub-topics, style, and factual consensus
2. **generate_outline** — produce a structured outline based on the context
3. **plan_sections** — generate per-chapter writing instructions from the outline
4. **generate_draft** — serially draft the full document per the section instructions
5. **review_document** — review the draft across multiple dimensions, scoring it and surfacing revision suggestions
6. **finalize_report** — apply the review feedback and produce the final report

Every step supports a full rerun: when the user is unhappy with a step's result, that step can be retriggered.

## Intent Recognition

### Cold start (no active session)

- When the user asks for structured writing such as "write a report", "draft an article", "write a survey", "write an introduction to X", "write a short story", "write a novel chapter", or "写一篇小说" → invoke `trigger_writer_plugin(user_input=<user's exact original request>)`.

  `user_input` must preserve the user's request verbatim. Do not rewrite, expand, translate, summarize, or add inferred details before calling the trigger tool.
  If the user does not provide all details, keep the original request intact and trigger the plugin first; the workflow can infer reasonable defaults or collect context during `build_context`.

### With an active session

| User intent | Recommended step | Tool call |
|---|---|---|
| Re-parse the writing intent | build_context | `advance_step(step_id='build_context', user_input=<note>)` |
| Unhappy with the outline, regenerate it | generate_outline | `advance_step(step_id='generate_outline', user_input=<note>)` |
| Re-plan the section instructions | plan_sections | `advance_step(step_id='plan_sections', user_input=<note>)` |
| Redraft the document | generate_draft | `advance_step(step_id='generate_draft', user_input=<note>)` |
| Re-review | review_document | `advance_step(step_id='review_document', user_input=<note>)` |
| Produce the final report again | finalize_report | `advance_step(step_id='finalize_report', user_input=<note>)` |
| Satisfied with the final result | (no action — DriverAgent marks DONE automatically) | — |

When the user or DriverAgent indicates the problem originates from a prior step, use `advance_step` with that prior step's `step_id` to rewind and redo it. The available prior steps are listed dynamically by `advance_step`'s Rewind list and need not be enumerated here.

## Notes

- For cold start, go through `trigger_writer_plugin` and pass the user's exact original request as `user_input`.
- Do not use `ask_user` before triggering this plugin merely to collect optional writing preferences such as length, style, audience, plot elements, or structure. Optional details belong inside the plugin workflow.
- After a tool returns, briefly tell the user which step is currently running, for example:
  - Cold start: "Parsing your writing request, please wait…"
  - Regenerating the outline: "Regenerating the outline…"
- Concrete writing content (section drafts, final report, etc.) is produced by the tools the subagent collaborates with; the main Agent does not need to re-state the body.

## Artifact Handoff

- Each step's output is stored as a file path — the artifact carrier is a file path, not the file content itself.
- Orchestration flow: `get_artifact(key=A)` returns a path → pass that path as an argument to the downstream tool function (the tool reads the file itself) → the tool returns a new path → call `save_artifact(content_type='file', value=<new path>, key=B)` to persist → the next step calls `get_artifact(key=B)` to get the path back → continue.
- The main Agent is responsible for **orchestrating tool calls and passing paths**, not for generating any content (to avoid bloating the context).
