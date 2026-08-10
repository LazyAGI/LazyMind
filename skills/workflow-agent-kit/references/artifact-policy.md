# Artifact policy v1

Required outputs are immutable, revisioned Artifacts linked to their Attempt,
Input Resources, and predecessor revisions. Verify each required output against
the step acceptance criteria before reporting success. Missing required output is
a structured execution failure, never permission to manufacture a result.

Conversation attachments are owned and resolved by the Host framework, outside
Workflow. When one is supplied as Workflow input, the Host imports it as an
immutable Input Resource and supplies the exact resource id, revision, and content
hash. Changed input is a new resource; never mutate a bound resource.

Use `patch_artifact` with an exact selected handle to store an intentional output
revision authored by the active Agent. The Host resolves its id and current base
revision. Deletion/tombstoning is controller/UI-only and is not exposed to the
model. A product UI human edit uses the public revision mechanism through its Host
adapter. No path overwrites or physically erases history.

Before patch, list/read the selected revision; the Host supplies `base_revision`.
On conflict, reread and reconcile. If a revision or tombstone
invalidates downstream results, target the earliest invalidated step and let
Runtime propagate staleness.
