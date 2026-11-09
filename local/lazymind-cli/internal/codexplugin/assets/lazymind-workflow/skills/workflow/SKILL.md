---
name: workflow
description: Run a selected LazyMind workflow in Codex using native subagents and the shared LazyMind MCP execution protocol.
---

Use the connected LazyMind MCP server's shared workflow instructions, tool schemas
and returned contracts. They define startup inputs, artifact publication, questions,
review, stop and recovery for every external host; this Skill only maps Codex
capabilities to that protocol. Do not recreate business steps or output rules here.

The root chat owns the execution_handle and all workflow state writes. For an
external-agent execution, launch native Codex subagents for generation and
validation. Give them the complete pinned step contract with its shared execution
skill_files, constraints, inputs and required outputs, plus an isolated output
directory. Keep the execution_handle and credentials out of worker prompts.
Workers return an artifact manifest and validation evidence. The root proxies
advertised tools, publishes real artifacts as they become available, and completes
with executor_ref identifying the worker. No second Codex process or top-level chat
substitutes for a native worker. If subagents are unavailable, report that capability
and stop advancing. Native revisions keep their returned executor_host semantics.

Use request_user_input_async when available for the questions returned at startup
or in control.input_request. Accepted means displayed, not answered: wait for actual
user input before saving answers with workflow.input.answer. Plain conversation is
also supported. Never invent answers or approve a review on the user's behalf.

Keep worker IDs so Stop cancels only this workflow's workers. Discard results after
revocation or grant replacement. A continuation notification requires a fresh
workflow.state read; it is not an execution grant. The connector supplies real
conversation identity and optional panel presentation; never invent a driver ID.
Use the same artifact/control protocol even if embedded panel delivery is unavailable.

The returned LazyMind Skills are authoritative for the business work. Every worker
must read all supplied Skill content before generating or validating; do not replace
it with Codex presentation, writing or design Skills. Codex helpers may only supply
tool operations under the LazyMind instructions. Discover declared LazyMind tools
first; use applicable configured tools and supplement missing operations with Codex
tools. Missing Skill files are a blocking contract issue, not permission to improvise.
Return used Skill paths and tool substitutions with validation evidence.
