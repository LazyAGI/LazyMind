# Artifact and authoring policy v1

## Artifact review

Treat Artifact revisions as immutable execution facts. Read the latest revision
through Workflow tools, verify every required output against the step acceptance
criteria, and preserve its Attempt, Input Resource, and predecessor lineage. A
missing required Artifact is a structured execution failure. A user-requested
change creates a new revision; it never overwrites history. If that change makes
downstream output stale, target the earliest invalidated step and let Runtime
resolve rewind and stale propagation.

## Deterministic authoring

1. Pin the source Skill package and revision before analysis.
2. Decide whether its procedure is representable as a deterministic Workflow.
3. Generate the draft in the Host; Authoring Tools do not call a model.
4. Submit files with `create_workflow_draft` or `update_workflow_draft_file`.
5. Run deterministic diagnostics and repair every error against the same draft
   revision. Do not silently weaken graph, tool, safety, or Artifact constraints.
6. Publish only a validated draft using an idempotent command and expected draft
   revision. A conflict requires reread and deliberate reconciliation.

Published Workflow revisions and source Skill revisions remain linked so a later
regeneration is auditable and cannot silently replace an active Session contract.
