---
name: workflow
description: Orchestrate a LazyMind Workflow with native Codex subagents for generation and validation, publish their artifacts, and yield for human review.
---

Use the plugin's LazyMind MCP tools to discover and start the selected workflow.

Before workflow.start, ask only for missing required inputs. With request_user_input_async, accepted means displayed: keep the turn active and use an interruptible wait until the user answers. If the tool is unavailable or fails, ask in plain text.

Use an explicit unique idempotency_key of 16–256 characters. Preserve it for unchanged-payload retries; use a new key when startup answers or inputs change. Pass known answers as clarification_values. With status=needs_input no run exists: ask the returned questions using their exact IDs, preserve request_context/revision_id and supplied files, and resubmit the answers. Resolve source attachments to absolute paths on the Connector machine and pass them through workflow.start inputs under declared material IDs. The host connector resolves the native conversation identity. Never guess an ID or use the Workflow session_id as driver_session_id; if identity is unavailable, follow the returned host guidance and retry the same start.
The host displays the Workflow panel automatically at the end of the bound chat. Use that panel for review and control; do not open a browser panel or add a panel link to your reply.
Read workflow.state before advancing. Claim an existing execution_id; begin a ready step only when Core permits it.

For a step_contract with external_execution.version="1", use native Codex subagents for both generation and validation. The root chat owns orchestration and holds the execution_handle. Do not start another top-level chat, invoke a second Codex process, or use LazyMind model.chat to replace the native workers. If native subagents are unavailable, explain the missing host capability and stop advancing.

Give each worker the complete step_contract and an isolated output directory, including launch_request, user_request, external_execution.prompt, skill_files, constraints, acceptance criteria, typed input resources and output requirements. Use task_specification for business fields, enums and output shapes; external prompt/constraints govern execution rather than its native model, tool wrapper or automatic publication instructions. Use the supplied skill_files from this revision; do not substitute a current or similarly named installed skill. Never pass workers the execution_handle, credentials, or authority to mutate Workflow state. Workers may use native host tools and return a structured request for a declared LazyMind tool to the root. The root calls workflow.tool.list and workflow.tool.call with its handle, the exact advertised name/schema and a stable command_id. Preserve that command_id and arguments on uncertain retries.

Prefer native host image/video capabilities when available. If a worker lacks a required capability, the root may use an available declared LazyMind tool. Media input references use resource:<id> from workflow.input.import, not arbitrary server paths. If the capability is also unavailable in LazyMind, report what the user must configure there and yield; retry this run after configuration. Do not invent assets or silently change the requested output format.

Workers return an artifact manifest containing slot, content_type, local_path or value, stable seq, and a summary of their validation. Files must exist on the Connector machine before publication; another machine's path is not usable. Have a native validation worker check the requested deliverable, acceptance criteria and generated previews before reporting success. The root forwards completed artifacts as soon as they are available using workflow.artifact.publish, waits for acknowledgements, and calls workflow.step.complete with the unchanged handle and executor_ref identifying the native worker. Required output slots and types remain authoritative. A worker's final text alone is not a saved artifact.

Keep worker IDs so Stop can interrupt only this Workflow's workers. After a stop, recovery, or grant replacement, discard stale worker results; never submit using an old handle. A failed worker must produce a failed outcome with a useful summary, not a successful empty result. Continue only from the latest Core state. Legacy revisions without external_execution keep their returned executor_host semantics; do not claim a LazyMind-owned attempt.
When Core reports awaiting_user, tell the user to review in the panel and END this turn. Do not approve, begin another step, or poll while waiting.
On awaiting_executor, stopped, completed, or binding_required, also end this turn. For completed, deliver the artifact links.
When a queued continuation arrives, read the latest Core state first. The notification is not execution authorization.
Stop is handled by Core and the host connector. Do not replace user Panel actions with your own approvals.
