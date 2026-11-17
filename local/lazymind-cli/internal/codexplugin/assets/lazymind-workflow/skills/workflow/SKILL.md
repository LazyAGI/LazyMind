---
name: workflow
description: Run a selected LazyMind workflow in Codex using the shared LazyMind MCP execution protocol and native subagents when available.
---

Use the connected LazyMind MCP server's instructions, tool schemas and current
state guidance. Discover the selected Workflow and execute its pinned step
contract. Read its complete prompt, Skills, inputs, outputs and acceptance
criteria before working. Business requirements come from that contract.

The root chat holds the execution_handle and submits state changes. Prefer native
Codex subagents for step execution when available; otherwise execute directly.
Give each worker the complete contract and resolved inputs. Keep credentials and the handle with
the root. For a step declaring paper-agent-v1, use workflow.worker.grant and an
isolated worker-only MCP connection. The worker directly discovers/calls its tools;
never proxy each business operation or forward the root handle. If this host cannot
attach/inherit an isolated connection, disclose the exact missing capability and
stop that execution rather than falling back to model-response forwarding. Existing
non-paper revisions retain their documented controller tool proxy behavior.

Use Codex capabilities first for business operations, then discover an authorized
LazyMind fallback through workflow.tool.list when needed. Follow returned schemas
and error guidance. The executor verifies actual results against the contract
and reports concise evidence, identifying same-agent verification when applicable.

Prefer file handoff for large contracts and artifacts using contract_directory,
arguments_file, result_directory, output.value_file and output.local_path as
described by the corresponding tools. Respect the host's worker limits and the
contract's output ordering. Await worker completion notifications where supported.

Publish results, await acknowledgements, then complete the step. Read
workflow.state on continuation or recovery. Ask returned input questions, wait
for actual user answers, and leave review decisions to the user. Deliver saved
artifacts when the state confirms completion.

For paper-agent-v1 revisions, including completed runs, use the authoritative
session resume/recovery path and the revision step for manuscript edits or the
data-analysis step for redraws, carrying the latest bound material pool. Do not
use generic completed-edit/action/model-job routes for paper revisions. Read
workflow.state for the exact admitted recovery action and preserve user review gates.

For other revisions, user-requested changes to completed artifacts use workflow.edit.context and
workflow.edit.commit. For an artifact_edit notification, use workflow.action.get
and workflow.action.respond and follow the returned preview/confirmation state.

If the embedded panel cannot render or the user reports it missing, read workflow.state and present its web_url as a clickable link for browser review and export. Use only the returned URL.
