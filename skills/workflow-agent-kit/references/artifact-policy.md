# Artifact policy v1

Required outputs are immutable, revisioned Artifacts linked to their Attempt,
Input Resources, and predecessor revisions. Verify each required output against
the step acceptance criteria before reporting success. Missing required output is
a structured execution failure, never permission to manufacture a result.

Input attachments are immutable Input Resources. Store bytes with
`import_input_resource`, read with `read_input_resource`, and bind the exact id,
revision, and content hash. Changed input is a new resource; never mutate a bound
resource or replace it with a Host path or temporary URL.

Use `patch_artifact` to store an intentional output revision authored by the
active Agent. Use `delete_artifact` to create a selected tombstone revision with
`deleted: true`. A product UI human edit uses the same public revision mechanism
through its Host adapter. No path overwrites or physically erases history.

Before patch or delete, list/read the selected revision and pass its
`base_revision`. On conflict, reread and reconcile. If a revision or tombstone
invalidates downstream results, target the earliest invalidated step and let
Runtime propagate staleness.
