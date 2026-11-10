---
name: workflow
description: Run a selected LazyMind workflow in Codex using the shared LazyMind MCP execution protocol and native subagents when available.
---

Use the connected LazyMind MCP server's shared workflow instructions, tool schemas
and returned contracts. They define startup inputs, artifact publication, questions,
review, stop and recovery for every external host; this Skill only maps Codex
capabilities to that protocol. Do not recreate business steps or output rules here.

Execute quietly: normal progress belongs in saved artifacts and the Workflow panel,
not conversational updates, repeated plans or stage summaries. Send brief text only
for user-requested status, required input/review/configuration, a blocking failure
or final delivery; keep host-required notices minimal. Pass this rule to workers.
Return artifacts, manifests and concise validation evidence. The controller follows
the returned state, proxies tools and saves results; avoid duplicating worker
analysis, rederiving confirmed requirements or searching source during normal work.
Wait for completion notifications; use bounded status polling only when necessary,
never repeated directory checks. Preserve actual checks and human checkpoints.

The root chat owns the execution_handle and all workflow state writes. For an
external-agent execution, use native Codex subagents by default for generation and
validation. Give each worker one complete canonical business contract: the pinned
step prompt, shared execution skill_files and constraints, acceptance criteria,
inputs and required outputs, plus an isolated output directory. The adapter's
execution_ref points to this same execution object; do not repeat its business
instructions. An objective_ref of #/prompt reuses the step prompt; any objective
text is an additional runtime refinement. Keep the execution_handle and credentials out of worker prompts.
Workers return an artifact manifest and validation evidence. The root proxies
advertised tools, publishes real artifacts as they become available, and completes
the attempt. executor_ref is optional and must identify a real task, conversation
or trace; it does not establish independent review.

If native subagents are unavailable, execute in the current external agent and
perform a separate verification pass on the actual artifacts before completion.
Report same-agent validation honestly; never invent a worker or claim independent
validation. Do not launch a second Codex process or create another top-level chat
as a worker substitute. Native revisions keep their returned executor_host semantics.

Use request_user_input_async when available for the questions returned at startup
or in control.input_request. Accepted means displayed, not answered: wait for actual
user input before saving answers with workflow.input.answer. Plain conversation is
also supported. Never invent answers or approve a review on the user's behalf.

Keep worker IDs so Stop cancels only this workflow's workers. Discard results after
revocation or grant replacement. A continuation notification requires a fresh
workflow.state read; it is not an execution grant. The connector supplies real
conversation identity and optional panel presentation; never invent a driver ID.
Use the same artifact/control protocol even if embedded panel delivery is unavailable.

The returned LazyMind Skills are authoritative for the business work. Every executor
must read all supplied Skill content before generating or validating; do not replace
it with Codex presentation, writing or design Skills. Codex helpers may only supply
tool operations under the LazyMind instructions. Discover declared LazyMind tools
first; use applicable configured tools and supplement missing operations with Codex
tools. Missing Skill files are a blocking contract issue, not permission to improvise.
Return used Skill paths and tool substitutions with validation evidence.

## Artifact edits after generation

An `artifact_edit` notification is a separate preview request, including on a completed run. Use `workflow.action.get` with its session and job IDs. Execute the exact returned `generation_request.system_prompt` and `user_prompt`, using a native subagent when available; these are the shared LazyMind planner's business instructions. Submit the external answer through `workflow.action.respond` with the unchanged version and request hash. Process another request only when returned. On `ready` or `cancelled`, end the turn; the user confirms the preview in LazyMind. Never rewind the workflow, regenerate the deck, or commit the preview on the user's behalf.

Keep host context compact: fetch the current step contract once and give every executor its complete Skills. Retain the contract privately or in working memory; do not echo it or repeatedly fetch unchanged contracts or inputs. Return concise artifact references and validation evidence. Do not print entire HTML/base64 artifacts, compiled graphs, or package implementations into the conversation. For file publication, `local_path` belongs directly on `output`, never under `output.value`.

For a new user instruction in this chat to edit, regenerate selected pages, insert,
delete or reorder pages of a completed artifact, call `workflow.edit.context` on
that run. Use its original LazyMind Skills, edit policy, current artifact
references and the full user instruction, delegating to a native worker when
available. The external executor inspects and validates the current artifact; preserve unspecified pages. The root submits
one `workflow.edit.commit` bundle containing the updated downloadable file and all
nonempty linked lists, using the unchanged context version. List positions are
visual page numbers; `list_index` is a stable identity, not a page number. On a
conflict, reread context and reconcile, never blindly repeat the old change. This
explicit user edit is distinct from a pending AI dialog preview: never use the
bundle endpoint to bypass its confirmation. Do not start a replacement workflow.
