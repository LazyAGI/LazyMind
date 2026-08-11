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

### `create_workflow_draft(name, files, skill_id?)`

`files` maps allowed relative package paths to exact Agent-authored text. The tool
checks the Host-pinned revision/tree hash, derives `source_type` from whether a
Skill is selected, and stores that text unchanged. Required initial paths
are documented in `workflow-format.md`.

### `update_workflow_draft_file(path, content)`

Stores one exact Agent-authored file in the selected authoring context. Host reads
and injects the latest draft id/version. It never generates a patch.

### `validate_workflow_draft()`

Runs the deterministic Go graph compiler. It returns validity, graph/hash, and
path-addressed diagnostics; it does not repair content.

### `get_workflow_diagnostics()`

Runs strict deterministic checks for pinned snapshot, package completeness, graph
validity, framework-tool availability, and script audit. It does not ask a model
to judge quality.

### `publish_workflow()`

Re-runs strict diagnostics and publishes an immutable revision only when valid.
The response contains Workflow ref and revision metadata. The main Agent must not
call it until diagnostics are clean. The tool does not generate or revise files.

## Capability boundary

Tool-list absence is a capability result, not permission to call internal or
product-specific endpoints. Controller/SDK APIs retain full protocol fields, but
model tools are context-bound and must not expose those fields.
