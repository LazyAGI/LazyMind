# Workflow tool contracts v1

Agent-facing tools are grouped by responsibility:

- Discovery: `list_workflows`, `get_workflow`.
- Lifecycle: `prepare_workflow`, `start_workflow`, `get_workflow_state`,
  `stop_workflow`, `resume_workflow`.
- Transition: `get_ready_steps`, `advance_step`,
  `advance_step_and_hand_off`.
- Artifact: `list_artifacts`, `read_artifact`, `patch_artifact`.
- Authoring: `get_skill_conversion_context`, `create_workflow_draft`,
  `update_workflow_draft_file`, `validate_workflow_draft`,
  `get_workflow_diagnostics`, `publish_workflow`.

Claim, Attempt context, heartbeat/progress, Artifact persistence, and Attempt
terminal tools are Executor-only. Route facts, database rows, state mutation, and
Host-private model configuration are never model-facing contracts.
