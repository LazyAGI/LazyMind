# AI Writer Plugin

## Scenario

Help users compose structured long-form writing, including articles, reports, technical documents, creative stories, fiction, short stories, and novel-style drafts. The workflow includes explicit material-based revision decisions:

1. **build_context** — parse the writing intent, target audience, core sub-topics, style, and factual consensus
2. **generate_outline** — produce a structured outline based on the context
3. **plan_sections** — generate per-chapter writing instructions from the outline
4. **generate_draft** — serially draft the full document per the section instructions
5. **decide_draft_action** — emit a revision decision material only when revision is explicitly requested
6. **generate_patch** — generate a patch set from the user's modification request and validate it
7. **decide_patch_action** — emit an acceptance material only when applying the patch is explicitly approved
8. **apply_patch** — create a new revised draft and revised writing context without overwriting prior materials
9. **review_document** — review either the revised draft or the original draft through an OR input expression
10. **finalize_report** — produce the final report from the selected effective draft

Every step supports a full rerun: when the user is unhappy with a step's result, that step can be retriggered.

## Intent Recognition

### Cold start (no active session)

- Invoke `trigger_writer_plugin(user_input=<user's exact original request>)` when the user explicitly asks to use the AI Writer plugin.
- Otherwise, invoke it only when all of the following are true:
  1. The user requests a complete, independently deliverable piece of writing.
  2. Producing it reliably depends materially on existing knowledge, source materials, supplied documents, factual background, professional constraints, or continuity with prior content.
  3. The task genuinely benefits from multiple workflow stages, such as context building, outlining, section planning, drafting, and whole-document review.
  4. No more specific writing skill or plugin matches the task.

`user_input` must preserve the user's request verbatim. Do not rewrite, expand, translate, summarize, or add inferred details before calling the trigger tool. A request is not complex merely because it mentions an article, report, document, story, or word count. If it can be completed reliably in one direct response, do not invoke the plugin.

### Trigger examples

Invoke the plugin for requests such as:

- "Use the AI Writer plugin to complete this article."
- "Based on the industry materials I provided, write a complete analysis report for senior management. Unify the data definitions, design the chapter structure, and check the final document for consistency."
- "Synthesize the project background, interview notes, and previous proposals into a complete project retrospective report."
- "Continue the novel with a complete chapter based on the story bible and previous chapters, preserving character, plot, and world-building continuity."
- "Use these technical references to produce a complete in-depth article, including structural planning, section-by-section drafting, and final review."

### Do not trigger

Handle requests like these directly without invoking the plugin:

- "Write a short product introduction."
- "Write a leave-request email."
- "Polish this paragraph."
- "Summarize the main points of this material."
- "Translate this passage into English."
- "Give me an article outline."
- "Suggest five titles."
- "Create a Word document for me."
- "Explain reinforcement learning."
- "Let's discuss how this article could be improved."
- "Write an 800-word introduction to artificial intelligence."

Do not trigger solely because the user says "write," requests a named document type, or specifies a long word count. Simple document-file creation is not a complex writing task.

### Prefer a more specific capability

The AI Writer plugin is a general fallback. If another skill or plugin more precisely matches the writing domain, use it instead:

- "Write a bid proposal." → use a proposal-writing skill or plugin.
- "Write an academic paper based on these experiment results." → use an academic-writing skill or plugin.
- "Create a product requirements document." → use a product-document skill or plugin.
- "Write my résumé." → use a résumé-writing skill or plugin, if available.

### Boundary examples

- "Write an artificial intelligence industry report." → Do not invoke by default; the request does not establish a material dependency on sources or a need for a multi-stage workflow.
- "Using these ten market research documents, write an artificial intelligence industry report for senior management. Design the structure, reconcile the data, draft it section by section, and review it for consistency." → Invoke, unless a more specific industry-report skill or plugin is available.
- "Create a meeting-minutes document." → Do not invoke; creating a document is not itself a complex writing task.
- "Synthesize six meeting transcripts, project records, and decision history into a formal project retrospective report." → Invoke if no more specific project-retrospective writing capability is available.

### With an active session

| User intent | Recommended step | Tool call |
|---|---|---|
| Re-parse the writing intent | build_context | `advance_step(step_id='build_context', user_input=<note>)` |
| Unhappy with the outline, regenerate it | generate_outline | `advance_step(step_id='generate_outline', user_input=<note>)` |
| Re-plan the section instructions | plan_sections | `advance_step(step_id='plan_sections', user_input=<note>)` |
| Redraft the document | generate_draft | `advance_step(step_id='generate_draft', user_input=<note>)` |
| Revise the draft | decide_draft_action | `advance_step(step_id='decide_draft_action', user_input=<modification request>)` |
| Abandon the revision after seeing the patch | review_document | `advance_step(step_id='review_document', user_input=<note>)` |
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
- `generate_patch` / `apply_patch` require a draft to already exist. The revised result is stored as `revised_draft_document` and `writing_context_after_revision`; the original materials remain immutable. To revise again, rewind to the relevant decision or patch step.

## Artifact Handoff

- Each step's output is stored as a file path — the artifact carrier is a file path, not the file content itself.
- Orchestration flow: `get_artifact(key=A)` returns a path → pass that path as an argument to the downstream tool function (the tool reads the file itself) → the tool returns a new path → call `save_artifact(content_type='file', value=<new path>, key=B)` to persist → the next step calls `get_artifact(key=B)` to get the path back → continue.
- The main Agent is responsible for **orchestrating tool calls and passing paths**, not for generating any content (to avoid bloating the context).
