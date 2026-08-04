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

### Input Resource tools

- `import_input_resource(name, mime_type, content_base64)` stores immutable bytes.
- `read_input_resource(resource_id)` returns metadata and base64 content.
- `list_workflow_inputs(session_id)` returns exact Session bindings.
- `bind_workflow_input(session_id, material_id, resource, command_id?)` binds the
  returned resource id, revision, and hash; it never accepts a local path or URL.

## Preparation and start

### `prepare_workflow(workflow_id, input_bindings?, command_id?)`

- `workflow_id: string`.
- `input_bindings: object` — durable resource references keyed by material id.
- `command_id: string` — optional UUID; generate once if omitted.

Returns a preparation record containing its id and plan/status. It does not create
a Session. Resolve missing inputs before starting.

### `start_workflow(preparation_id, session_id, command_id?)`

- `preparation_id: string` — exact id from prepare.
- `session_id: string` — stable Host-generated id for this run.
- `command_id: string` — defaults to the preparation id.

Consumes the preparation idempotently and returns the initial Session result.

## Projection and execution

### `get_workflow_state(session_id)`

Returns the authoritative projection. Always retain `state_version`, terminal or
active status, Ready frontier, active/failed Attempts, and required outputs.

### `get_ready_steps(session_id)`

Returns `session_id`, `state_version`, `ready_steps`, and the source projection.
An empty frontier means wait/observe or terminal; it never permits guessing.

### `advance_step(session_id, expected_state_version, steps, command_id?)`

- `expected_state_version: integer` — from the latest projection.
- `steps: array` with at least `step_id`; optional `task_id`, `objective`,
  `user_input`, `runtime_instruction`, and `partial_indices`.
- `command_id: string` — stable UUID for this transition.

Submit multiple steps only when they are independent members of the same Ready
frontier. Runtime determines `resolved_operation`; never send retry/rewind as an
operation. On state conflict, refresh and decide again rather than replaying.

## Errors

MCP tool failures return `isError: true` with structured `code`, `message`,
`retryable`, `status_code`, and `details`. Important handling:

- `STATE_VERSION_CONFLICT`: refresh projection and reconsider targets.
- `TRANSITION_RESULT_UNKNOWN`: reconcile state/command outcome; do not blindly retry.
- `IDEMPOTENCY_CONFLICT`: generate a new id only for a genuinely new command.
- `PERMISSION_DENIED`: stop and obtain the correct identity/authority.
- `LAZYMIND_NOT_FOUND`: follow `installation-and-connection.md`.

## Artifact revisions

### `list_artifacts(session_id)` and `read_artifact(artifact_id)`

List returns selected output revisions. Read accepts an exact revision id and
returns content, `revision`, `selected`, `validity`, `deleted`, producer Attempt,
slot, list index, and lineage metadata.

### `patch_artifact(artifact_id, base_revision, value, content_type?, caption?, command_id?)`

Creates a new selected immutable revision from exact Agent-authored content.
`base_revision` must still be selected. It performs no generation or review.

### `delete_artifact(artifact_id, base_revision, command_id?)`

Creates a new selected tombstone revision with `deleted: true` and emits
`artifact.delete`. Historical revisions remain readable. Repeated or stale delete
requests return a revision conflict rather than erasing additional data.

## Deterministic Skill-to-Workflow authoring

### `get_skill_conversion_context(skill_id, revision_id?)`

Returns an immutable Skill snapshot with revision id, tree hash, files/references,
and available Workflow tools. It performs storage reads only and never summarizes,
classifies, or generates with a model.

### `create_workflow_draft(name, skill_id, revision_id, tree_hash, files)`

`files` maps allowed relative package paths to exact Agent-authored text. The tool
checks the pinned snapshot and stores that text unchanged. Required initial paths
are documented in `workflow-format.md`.

### `update_workflow_draft_file(draft_id, path, content, expected_version)`

Stores one exact Agent-authored file using optimistic version checking. Use the
returned draft version for the next edit. It never generates a patch.

### `validate_workflow_draft(draft_id)`

Runs the deterministic Go graph compiler. It returns validity, graph/hash, and
path-addressed diagnostics; it does not repair content.

### `get_workflow_diagnostics(draft_id)`

Runs strict deterministic checks for pinned snapshot, package completeness, graph
validity, framework-tool availability, and script audit. It does not ask a model
to judge quality.

### `publish_workflow(draft_id)`

Re-runs strict diagnostics and publishes an immutable revision only when valid.
The response contains Workflow ref and revision metadata. The main Agent must not
call it until diagnostics are clean. The tool does not generate or revise files.

## Capability boundary

Tool-list absence is a capability result, not permission to call internal or
product-specific endpoints. `advance_step_and_hand_off` is a Host extension and
may be absent; all other lifecycle and Artifact operations above are public.
