# LazyMind Host adapter

LazyMind may add approval UI, stop-tool behavior, SSE presentation, synthetic
turns, and durable handoff. These capabilities affect orchestration and
presentation only. They do not change Runtime readiness, Attempt operations,
Artifact lineage, permissions, or state-version checks.

Use `advance_step_and_hand_off` only after durable Supervisor acceptance.

`import_workflow_attachment(path)` is a Host path adapter: it reads a
user-selected LazyMind attachment and imports its bytes through the public
`import_input_resource` API. Never persist the path or a signed URL.
