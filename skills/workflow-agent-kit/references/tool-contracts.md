# Workflow MCP tool contracts v1

Every mutating call is idempotent. Preserve its `command_id` when reconciling an
unknown result. Never reuse that id for different arguments.

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

## Capability boundary

The MCP server advertises only implemented public facade tools. At this revision,
standard stop/resume and Artifact list/read/patch tools are not advertised because
their Core public facades are not yet complete. Tool-list absence is a capability
result, not permission to call internal endpoints.
