---
name: workflow-agent-kit
description: Discover, prepare, execute, review, recover, and author versioned Workflows through the neutral Workflow v1 tools.
version: workflow.v1
---

# Workflow Agent Kit

## Required reading

1. Load exactly one file from `profiles/`; absent Host metadata means `default`.
2. Read `references/decision-policy.md` before making a lifecycle decision.
3. Read `references/artifact-and-authoring.md` before reviewing output or authoring.
4. Treat the latest Runtime projection and its `state_version` as authoritative.

## Discover and prepare

Discover a Workflow, run `prepare_workflow`, bind durable Input Resources, resolve
all missing inputs, and only then call `start_workflow`. A preparation is not a
running Session. Never infer a start from discovery or preparation success.

## Execute

Select targets only from `ready_steps`. A Ready list is a frontier, not display
order. Independent applicable steps may be submitted atomically when the profile
permits parallel execution; alternatives must be narrowed from Runtime conditions
and user intent. Never submit a blocked or downstream step speculatively.

Use `advance_step` when the Host must wait for the Attempt result. Use
`advance_step_and_hand_off` only when the active profile permits handoff and a
durable Supervisor has accepted ownership. Handoff is a turn boundary, not a
different transition. Never choose retry versus rewind: name a Ready or previously
attempted target and let Runtime return `resolved_operation`. On a version conflict,
refresh state and decide again; do not blindly replay with a new command id.

## Review and recover

Review required outputs against acceptance criteria before reporting completion.
Preserve immutable Artifact revisions and lineage. On failure or interruption,
target only that attempted step. When the user changes a succeeded result, target
the earliest invalidated step and allow Runtime to resolve rewind and stale its
downstream lineage. Never batch a retry with fresh frontier work. Stop, resume,
cancel, and retry through Workflow tools; never edit projection state.

If a required Artifact is absent when an Executor reports success, report a
structured failure. Do not manufacture output or mark the Attempt complete.

## Authority during migration

The shared policy may run in shadow mode. A shadow trace is observational and must
record both decisions, comparison dimensions, policy/profile versions, and
`authority: legacy`. It must never invoke a tool or mutate Runtime state.

For authoring, follow `references/artifact-and-authoring.md`: analyze a pinned
Skill revision, generate a draft outside Runtime, submit it to deterministic
diagnostics, repair reported diagnostics, and publish only a validated revision.
Authoring tools never invoke a model.

The source audit for migrated LazyMind rules is in
`references/source-to-policy-mapping.md`.
