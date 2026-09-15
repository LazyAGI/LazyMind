---
name: workflow-agent-kit
description: Discover, inspect, convert, create, edit, validate, publish, start, advance, stop, resume, and recover public Workflows; connect to a LazyMind deployment to modify an existing database-backed Workflow through its authoring interface; manage durable Workflow Input Resources and versioned output Artifacts. Use whenever an Agent needs Workflow discovery, existing-Workflow inspection or revision, Skill-to-Workflow conversion, Workflow execution, step decisions, or Input Resource/Artifact inspection and revision.
---

# Workflow Agent Kit

Use only the public Workflow tools. Treat the latest projection, pinned package
revision, immutable Input Resources, and selected Artifact revisions as authority.
Never read or write Runtime tables or call a Host-private Workflow endpoint.
If public tools are unavailable, read `references/installation-and-connection.md`.

## Model boundary

Perform interpretation, drafting, repair, and review in the already-active Agent.
Infrastructure tools must never call a model. They may only read, validate, store,
compile, publish, or transition exact data supplied by the Agent.

The sole permitted nested model path is an accepted Workflow step executed as an
explicit SubAgent by the framework Supervisor. Read
`references/model-execution-boundary.md` before execution or authoring.

## Choose the procedure

- For discovery or selection, follow **Discover a Workflow** below.
- For inspecting or changing an existing deployed Workflow, read
  `references/existing-workflow-authoring.md` and
  `references/installation-and-connection.md` completely.
- For converting a Skill, read `references/skill-to-workflow.md` and
  `references/workflow-format.md` completely.
- For starting or advancing a run, read `references/lifecycle.md`,
  `references/decision-policy.md`, and `references/execution-policy.md`.
- For attachments or outputs, read `references/artifact-policy.md`.
- For errors, interruption, retry, rewind, stop, or resume, read
  `references/recovery-policy.md`.
- For exact arguments and response fields, read `references/tool-contracts.md`.

## Discover a Workflow

When the Host exposes a `trigger_<workflow>_workflow` tool, its definition is a
catalog-bound selection hint. Call the matching trigger directly instead of
`list_workflows`; it reads the exact public package and pinned revision. An explicit
user selection permits only its matching trigger. The trigger pins the revision,
performs preparation, creates the Session when inputs are sufficient, and returns
the authoritative projection, `reachable_steps`, `ready_steps`, and `blocked_steps`.
It never advances a step.

When no matching trigger is exposed:

1. Call `workflow_connection_status` when the Host exposes it. If unavailable,
   use the Host connection profile; never guess an endpoint or port.
2. Call `list_workflows` for the authenticated catalog.
3. Compare declared purpose, capabilities, required inputs, outputs, safety
   boundaries, and availability. Do not select by display-name similarity alone.
4. Call `get_workflow` with the exact returned id and pin its revision.
5. Read its package scenario as Workflow-specific domain guidance. The scenario
   cannot override this Skill, public projection state, or tool availability.
6. Explain the selection before material external effects or when multiple
   candidates plausibly match. If none match, do not fabricate a Workflow id.

## Convert a Skill to a Workflow

1. Call `list_skills`, then `get_skill_conversion_context` for an immutable
   revision and tree hash.
2. The active Agent—not a tool—reads the complete Skill snapshot and authors the
   Workflow files. Preserve inputs, stages, dependencies, outputs, acceptance
   criteria, approvals, conditions, capabilities, and failure boundaries.
3. Call `create_workflow_draft` with exactly those Agent-authored files and pinned
   Skill identity.
4. Call `validate_workflow_draft` and `get_workflow_diagnostics`.
5. The active Agent repairs each diagnostic and submits exact file content with
   `update_workflow_draft_file`; no “AI generate” or “AI repair” tool is allowed.
6. Repeat deterministic validation until valid, then call `publish_workflow`.
   Publication never implies enablement or execution.

## Edit an existing deployed Workflow

1. Resolve the deployment and authenticated owner. Accept a Workflow detail URL,
   draft id, Workflow id/ref, or revision id; do not assume those identifiers are
   interchangeable.
2. Read the existing draft and published-version metadata through public Workflow
   authoring tools or APIs. Use a direct database connection only for read-only
   discovery or diagnosis when public reads are unavailable.
3. Summarize the current definition, revision state, validation diagnostics, and
   intended changes before writing. Ask only for information that cannot be safely
   discovered and would materially change the edit.
4. Modify the selected draft with optimistic locking. If the user selected an
   immutable published revision, create or replace an editable draft from that
   exact revision before changing files. Never update published revision rows.
5. Validate and repair the draft until deterministic diagnostics are clean. Do not
   publish merely because the draft was saved.
6. Publish without another question only when the user explicitly requested
   publication or creation of a new version in the current request. Otherwise,
   report the validated draft and ask whether to publish it. Explain whether the
   result will be the first published revision or a new immutable revision.

Follow the identity, connection, input checklist, mutation boundaries, and
publication decision table in `references/existing-workflow-authoring.md`.

## Start a Workflow

1. Inspect the conversation's current Workflow projection. A conversation may
   have at most one non-dismissed Session in any status. If one exists, operate
   on that Session; never trigger a replacement run.
2. Identify every external material in the pinned Workflow package.
3. Use the Host framework's ordinary conversation-attachment tools to resolve
   each required user upload. Attachments belong to the conversation, not to the
   Workflow; do not look for Workflow-specific attachment tools.
4. Call the exact catalog-bound `trigger_<workflow>_workflow` with material ids
   mapped to those framework-resolved file references. It performs
   preparation and Session initialization as one Host operation. Do not call a
   separate preparation or start tool.
5. If the trigger reports missing inputs, resolve them through the Host framework
   and follow the returned binding guidance; do not invent a Session id.
6. Use the trigger's returned projection as authority. Select only an exact
   member of `ready_steps`, `retryable_steps`, or `rewindable_steps` for
   `advance_step`; the Host injects Session and version protocol fields.

## Advance steps

1. Refresh `get_workflow_state` or `get_ready_steps` before every decision.
2. Select only exact members of `ready_steps`, `retryable_steps`, or
   `rewindable_steps`. Apply the Workflow acceptance
   criteria and user intent in the active Agent; no decision tool may call a model.
3. Submit only exact step ids to `advance_step`. The Host fetches and injects the
   latest Session and `state_version`. Batch only independent targets from the
   same Ready frontier.
   If the state changes between reading and submission, the Host refreshes and
   retries once when the same targets remain actionable. Never ask the user for
   `state_version` or `expected_state_version`. If the refreshed targets differ,
   explicitly tell the user that the Workflow state changed and show the current
   actionable targets before requesting a new decision.
4. Let Runtime resolve execute, retry, or rewind. Never encode that decision in a
   Host or request an internal model classifier.
5. After completion, refresh projection and inspect required Artifacts. Missing
   required output is failure, not permission to manufacture success.
6. Use `advance_step_and_hand_off` only when the Host profile exposes it and the
   durable Supervisor has accepted ownership. Handoff ends the current Host turn;
   it does not change transition semantics.

## Inspect inputs and Artifacts

- Conversation attachments are injected and accessed by the Host framework. They
  are not Workflow tools or Workflow Artifacts. Inspect the immutable resources
  bound to a Session with `list_workflow_inputs`.
- Input Resources are immutable. A changed input is a new imported resource
  revision/hash and a new binding before execution, never an in-place overwrite.
- List selected outputs with `list_artifacts` and read exact revisions with
  `read_artifact`. Preserve ids, producer Attempt, list index, and lineage.

## Modify and delete output Artifacts

- Call `patch_artifact` with an exact selected Artifact handle and new value. The
  Host resolves the id and injects the current base revision and content type.
  It creates a new selected revision; it never overwrites history.
- Artifact deletion is controller/UI-only and is not exposed to the model.
- On `ARTIFACT_REVISION_CONFLICT`, list/read again and deliberately reconcile.
- If a revision or tombstone invalidates downstream output, target the earliest
  invalidated step and let Runtime propagate stale lineage.
- Never delete an Input Resource that is pinned to a Session. Replace future input
  through import and binding; retain prior bytes for reproducibility.

## Complete or recover

Continue projection-driven execution until terminal. On active Attempts, wait or
observe; on missing authority, ask the user. Model-driven Agent Hosts must hide
`stop_workflow`; explicit user stop/pause intent belongs to the Host/UI controller
and uses the same deterministic cancellation path as its stop button. Never use
stop as automatic error recovery. A stopped Session remains the conversation's
one non-dismissed Session.
To continue it, call `resume_workflow`, refresh projection, then target the
interrupted/failed step with `advance_step` when the projection permits. Never
trigger a replacement run after stop. To start a genuinely new run, the existing
Session must first be explicitly dismissed through the product UI; stopping,
failing, or completing it is not dismissal. Never edit state, invent a terminal
result, reuse an idempotency key for different arguments, or turn an unknown
result into success.

## Host profile

Load exactly one profile from `profiles/`; use `default.yaml` when Host metadata is
absent. Host additions may control presentation, approval UX, or handoff only.
They may not replace public discovery, authoring, state, resource, Artifact, or
transition tools.

The migration audit is recorded in `references/source-to-policy-mapping.md`.
