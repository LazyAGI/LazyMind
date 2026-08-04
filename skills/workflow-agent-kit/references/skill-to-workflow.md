# Skill to Workflow v1

1. Call `get_skill_conversion_context` to pin and read the complete Skill package,
   its revision, referenced files, and available Workflow tool catalog.
2. Decide whether the procedure has explicit inputs, executable steps, observable
   outputs, and acceptance criteria. Decline conversion when essential behavior
   cannot be represented safely or deterministically.
3. Design the graph, conditions, durable Input Resources, tools, required
   Artifacts, and acceptance criteria. Do not copy Host-private paths, credentials,
   model configuration, or hidden state into the Workflow.
4. Call `create_workflow_draft`, then update individual files with
   `update_workflow_draft_file` using the expected draft revision.
5. Call `validate_workflow_draft` and `get_workflow_diagnostics`. Repair every
   error against the same pinned Skill revision; do not weaken safety or output
   requirements to silence diagnostics.
6. Call `publish_workflow` only after deterministic validation succeeds. Preserve
   the source Skill revision link and use an idempotent command.

The Host model authors content. Core supplies the snapshot, contracts,
diagnostics, revision control, and publication gate; authoring tools never invoke
a model implicitly.
