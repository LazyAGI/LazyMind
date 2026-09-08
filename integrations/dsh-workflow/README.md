# LazyMind DSH Workflow bundle

This local development bundle keeps LazyMind-specific workflow presentation outside DSH. It observes a successful `mcp__lazymind__workflow_start*` call, validates its structured run URL, binds that run to the calling DSH Session through the local Assistant Bridge, and appends a replayable presentation event. Its browser half renders the existing LazyMind `/workflow-runs/:sessionId` page in an iframe, so its controls remain LazyMind controls.

For a manual preview, build this package against the same DSH release as the target profile, then add it with `dsh plugin --profile web add <package>`. The bundle patch assumes the existing `mcp-lazymind` row remains enabled and inserts only the integration row.
