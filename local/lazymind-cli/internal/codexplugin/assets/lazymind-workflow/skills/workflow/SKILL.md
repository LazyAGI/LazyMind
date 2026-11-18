---
name: workflow
description: Run a selected LazyMind workflow in Codex using the shared LazyMind MCP execution protocol and native subagents when available.
---

Use the connected LazyMind MCP server's instructions, tool schemas and current
state guidance. Discover the selected Workflow and execute its pinned step
contract. Read its complete prompt, Skills, inputs, outputs and acceptance
criteria before working. Business requirements come from that contract.

The root chat holds the execution_handle and submits state changes. When native
subagents are available, delegate each step to a fresh subagent by default. The
root coordinates workers, proxies tools and publishes their results; the worker
performs the business work. Execute directly only when subagents are unavailable
or the user explicitly requests it. Give each worker the complete contract and
resolved inputs in a fresh context without inherited controller history. Keep controller credentials and
the execution_handle out of worker instructions and inherited context. Workers can
request declared tool calls through the root, which returns actual tool results;
the executing Agent still owns research, generation, checking and repair.

An isolated worker-only MCP connection from workflow.worker.grant is optional,
including for paper-agent-v1. Use it only when the host can attach it to the worker.
Otherwise keep the subagent and proxy declared tool calls through the root. Do not
stop solely because an isolated worker connection is unavailable.

Use Codex capabilities first for business operations, then discover an authorized
LazyMind fallback through workflow.tool.list when needed. Follow returned schemas
and error guidance. The executor verifies actual results against the contract
and reports concise evidence, identifying same-agent verification when applicable.

Prefer file handoff for large contracts and artifacts using contract_directory,
arguments_file, result_directory, output.value_file and output.local_path as
described by the corresponding tools. Respect the host's worker limits and the
contract's output ordering. Await worker completion notifications where supported.

For paper-agent-v1 text outputs, use workflow.artifact.preview while generating,
including literature_review_document, write_outline_document and
write_document_document (also revisions). Send cumulative public Markdown
snapshots with increasing seq, followed by state=ended; send state=aborted when
discarding a draft. This is a read-only preview, never publication or approval.
Have workers write the current snapshot atomically to a shared UTF-8 file and notify the
root after each meaningful section (or a few paragraphs), before the step ends.
The root forwards updates immediately with text_file while servicing tool requests;
do not wait only for the final worker result. Never stream private reasoning,
credentials or complete an output and replay it to simulate generation.

For INVALID_ARGUMENT tool errors, return the actual diagnostic to the worker.
Let it correct its arguments/code and call again with a new command_id within
the current execution (at most two corrections). Do not mark a step failed for
the first correctable input error; report persistent failures without retrying
a terminal step or bypassing user review.

For paper steps, read paper_prepare_inputs.submission_contract before acting.
Register actual host-retrieved/read sources with the declared paper_import_sources;
do not repeat LazyMind searches just to obtain a receipt. After preparation returns
status=validated, stop candidate repair. Accepted quality_warnings do not require
resubmission. With result_directory, use the returned publication_outputs objects
unchanged as workflow.artifact.publish output; do not rebuild signed files or
receipts. Publication failures require fixing transport only, not regenerating
accepted content. Keep the total to at most two corrections of rejected inputs.

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
