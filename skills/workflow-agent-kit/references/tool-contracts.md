# Workflow MCP tool contracts v1

Lifecycle transitions use idempotent `command_id`; preserve it when reconciling an
unknown result and never reuse it for different arguments. Draft file updates use
`expected_version` optimistic locking. Do not retry publish after an unknown result
until the draft or published revision has been reread.

All tools below are deterministic and model-free except that `advance_step` may,
after an accepted transition, cause the framework Supervisor to execute the target
step with a SubAgent. See `model-execution-boundary.md`.

## Connection and discovery

### `workflow_connection_status()`

No arguments. Discovers Core, performs a live Workflow read, and returns
`connected`, `base_url`, `source`, `contract_version`, and the discovery response.

### `list_workflows()`

No arguments. Returns only enabled Workflows visible to the authenticated user.
Choose by declared capability, required inputs, outputs, and risk.

### `get_workflow(workflow_id)`

- `workflow_id: string` — exact id returned by discovery.

Returns definition and revision metadata. Do not construct ids from display names.

### Input Resources

`list_workflow_inputs()` returns exact immutable bindings for the Host-bound
Session. User-uploaded attachments belong to the Host conversation and are
resolved through its ordinary attachment tools, not through Workflow tools. When
the trigger receives a resolved file reference, the Host imports it and injects
resource id, revision, and hash.

## Session initialization

### `trigger_<workflow>_workflow(input_bindings?)`

The Host binds this tool to one authorized workflow id and pinned revision. It
performs preparation and Session initialization and returns `session_id`,
`state_version`, the authoritative `projection`, `reachable_steps`, `ready_steps`,
and `blocked_steps`. It never advances a step. If required inputs are missing, it
returns a structured waiting result and must not be described as started. List
allowlisted attachments and call this same trigger with material-to-attachment-id
bindings. Host injects the original user query as request context. Attachment
discovery remains a Host framework responsibility.

Separate preparation/start tools are controller/runtime APIs and are not exposed
to the model.

## Projection and execution

### `get_workflow_state()`

Returns the authoritative projection. Always retain `state_version`, terminal or
active status, Ready frontier, active/failed Attempts, and required outputs.

### `get_ready_steps()`

Returns `session_id`, `state_version`, `ready_steps`, `retryable_steps`,
`rewindable_steps`, and the source projection. These are disjoint exact target
classes: forward execution, failed/interrupted recovery, and succeeded-step rewind.
Empty target classes never permit guessing.

### `advance_step(step_ids)`

- `step_ids: array[string]` — exact ids from the latest target classes.

Host injects Session, state version, and command identity. Runtime derives the
immutable step contract from the pinned graph. Model tools cannot override task
ids, objectives, user input, Runtime instructions, or partial retry selectors.

Submit multiple steps only when they are independent members of the same Ready
frontier. Submit retryable or rewindable targets one at a time. Runtime determines
`resolved_operation`; never send retry/rewind as an operation. The Host handles a
state conflict by refreshing and retrying once only when the same targets remain
actionable. The model and user never supply a version. If refreshed targets differ,
surface the returned `user_notice` explicitly and request a new decision.

### Stop and resume

Stop interrupts active Attempts and preserves the Session, projection, inputs,
Artifacts, and history. It does not dismiss/delete the Session and must only be
called by a deterministic Host/UI controller for explicit user pause/stop
intent—not by a model and not because a step failed. Model-driven Agent Hosts
must omit `stop_workflow` from their projected tool set. Controller/SDK calls carry
Session and command fields. When the model is operating a Host-bound stopped
Session, it may call `resume_workflow()` with no arguments; the Host supplies those
fields. Resume acts on that same stopped Session. After resume, refresh projection and use
`advance_step` on the interrupted step when permitted. Never call
the trigger as the next action after stop.

## Errors

MCP tool failures return `isError: true` with structured `code`, `message`,
`retryable`, `status_code`, and `details`. Important handling:

- `STATE_VERSION_CONFLICT`: refresh projection and reconsider targets.
- `TRANSITION_RESULT_UNKNOWN`: reconcile state/command outcome; do not blindly retry.
- `IDEMPOTENCY_CONFLICT`: generate a new id only for a genuinely new command.
- `PERMISSION_DENIED`: stop and obtain the correct identity/authority.
- `LAZYMIND_NOT_FOUND`: follow `installation-and-connection.md`.

## Artifact revisions

### `list_artifacts()` and `read_artifact(artifact_ref)`

List returns selected output revisions. Read accepts a slot handle such as
`report` or `images[0]` and
returns content, `revision`, `selected`, `validity`, `deleted`, producer Attempt,
slot, list index, and lineage metadata.

### `patch_artifact(artifact_ref, value, caption?)`

Creates a new selected immutable revision from exact Agent-authored content. Host
resolves artifact id, selected base revision, content type, and command. Destructive
delete remains a UI/controller capability and is not a model tool.

## Deterministic Skill-to-Workflow authoring

### `get_skill_conversion_context(skill_id)`

Returns an immutable Skill snapshot with revision id, tree hash, files/references,
and available Workflow tools. It performs storage reads only and never summarizes,
classifies, or generates with a model.

### `preflight_skill_workflow_conversion(skill_id)`

Runs deterministic checks on the pinned Skill package before authoring. Returns
`status`, `summary`, snapshot identity, file counts, and structured checks with
codes, severities, paths, messages, and suggestions. A `blocked` status means the
Agent must not draft or publish until the Skill is fixed. A `warning` status means
drafting may continue, but the warning must be carried into validation.

### `create_workflow_draft(name, files, skill_id?)`

`files` maps allowed relative package paths to exact Agent-authored text. The tool
checks the Host-pinned revision/tree hash, derives `source_type` from whether a
Skill is selected, and stores that text unchanged. Required initial paths
are documented in `workflow-format.md`.

### `update_workflow_draft_file(path, content)`

Stores one exact Agent-authored file in the selected authoring context. Host reads
and injects the latest draft id/version. It never generates a patch.

### `validate_workflow_draft()`

Applies deterministic finalization in memory, then runs the Go graph compiler on
that finalized view. It returns validity, graph/hash, and path-addressed
diagnostics; it does not repair content with a model, and it does not write to the
draft or change its version.

### `get_workflow_diagnostics()`

Applies the same in-memory finalization, then runs strict checks for pinned
snapshot, package completeness, graph validity, capability/tool declarations,
framework-tool availability, UI tab alignment, execution boundaries, and script
audit. It does not ask a model to judge quality, and it does not write to the draft.

### `publish_workflow()`

Re-runs strict diagnostics and publishes an immutable revision only when valid.
The response contains Workflow ref and revision metadata. The main Agent must not
call it until diagnostics are clean. The tool does not generate or revise files.

Validation, diagnostics, and publish share the same deterministic finalization
stage. It may inject required capabilities/tools, credential clarification fields,
execution-boundary prompts, and UI tab alignment from the pinned Skill snapshot.
Only `publish_workflow` persists the result: it rewrites the package files through
a YAML normalizer (comments and key order are not preserved) and advances the draft
version, so reread the draft before the next `update_workflow_draft_file`. The two
read tools leave the draft untouched, which is why their diagnostics still predict
what publish will enforce. This creates semantic/runtime parity with LazyMind's
deterministic post-processing, not byte-for-byte parity with LazyMind UI's internal
AI generation or repair path.

## Hosted external Skill-to-Workflow task

### `start_skill_workflow_task(agent_type, skill, task_description, ...)`

Starts the complete LazyMind-managed flow for an external Agent. The allowed
`agent_type` values are `codex`, `trae-work`, `workbuddy`, `cursor`,
`raccoon-work`, and `deepseek-harness`. The hosted flow does not accept or expose
LazyMind internal Skill ids. Instead, pass `skill.name` and exactly one source:
`skill.url` for a SkillHub/GitHub/direct zip URL, `skill.zip_path` for a ZIP
readable by the MCP process, or `skill.zip_base64` for an inline ZIP.
`zip_path` is an MCP/SDK convenience: the adapter reads the file and sends
`zip_base64` to Core. Core never reads a caller-supplied local path.
`skill.zip_sha256` is optional for ZIP sources; when provided, LazyMind
verifies it against the decoded zip content, and otherwise calculates the hash
itself for reuse. LazyMind reuses an exact user-owned source mapping; same name
alone does not prove the Skill is the same. Conflicting sources return
`SKILL_NAME_CONFLICT`. ZIPs are limited to 20 MiB; the whole HTTP request is
limited to 32 MiB, including base64 overhead and input files.

The request may include external conversation/thread identifiers, durable input
bindings, inline `input_files`, config, and an idempotency key. Inline files are
intended for small external Agent inputs and require `material_id`, `name`,
`mime_type`, and base64 `content_base64`; an optional `content_hash` must match
`sha256:<hex>` when provided. The response returns a `task_id`, canonical status,
`display_status`, `skill` source status, stage, linked draft or Workflow
identifiers when known, a LazyMind URL, and structured failure or user-action
guidance. `config` supports only `reuse_workflow` (boolean, default true).
Unknown fields are rejected with a reason. The task is durably queued before
the submission returns; background workers install, generate, publish and execute.
An identical idempotency key and request returns the existing task. Changed
arguments with that key return `IDEMPOTENCY_CONFLICT`.

### `get_skill_workflow_task(task_id)`

Polls the task. Status values are `queued`, `converting`, `running`,
`waiting_user_action`, `succeeded`, and `failed`. `waiting_user_action` means the
external Agent must stop the hosted flow and direct the user into LazyMind using
the returned `lazymind_url` and suggestion. `display_status` maps those statuses
to user-facing states: `waiting`, `running`, `waiting_user_action`, `succeeded`,
and `failed`. Both reads are side-effect-free. Responses include `stage_label`,
execution `progress`, `done`, `result_ready`, `next_action` and
`poll_after_seconds`. Follow `poll`, `read_result`, `open_lazymind`, or
`review_error`; a zero polling interval means stop polling.

### `get_skill_workflow_result(task_id)`

Returns the task state and, when `result_ready=true`, final summary, effective
selected artifacts, and LazyMind URL. A pending/blocked task is a normal status
response, not a transport error. SDK/MCP expands relative LazyMind links using
`LAZYMIND_PUBLIC_URL` when configured, otherwise the Core origin.

## Capability boundary

Tool-list absence is a capability result, not permission to call internal or
product-specific endpoints. Controller/SDK APIs retain full protocol fields, but
model tools are context-bound and must not expose those fields.
