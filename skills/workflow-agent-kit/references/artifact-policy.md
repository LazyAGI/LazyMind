# Artifact policy v1

Required outputs are immutable, revisioned Artifacts linked to their Attempt,
Input Resources, and predecessor revisions. Verify each required output against
the step acceptance criteria before reporting success. Missing required output is
a structured execution failure, never permission to manufacture a result.

Use `patch_artifact` to store an intentional revision authored by the active Agent. A product UI human edit
uses its Host adapter. Neither path overwrites history. If an edit invalidates
downstream results, target the earliest invalidated step and let Runtime propagate
staleness.
