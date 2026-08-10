# LazyMind Host adapter

LazyMind may add approval UI, stop-tool behavior, SSE presentation, synthetic
turns, and durable handoff. These capabilities affect orchestration and
presentation only. They do not change Runtime readiness, Attempt operations,
Artifact lineage, permissions, or state-version checks.

Use `advance_step_and_hand_off` only after durable Supervisor acceptance.

Conversation attachments remain a LazyMind framework capability. ChatAgent sees
the current turn's attachments before its query and uses `find_user_attachment`
or `read_user_attachment`; every SubAgent receives a snapshot of its parent
conversation's attachment context and the same tools. Workflow adds no attachment
listing or lookup tool. When a resolved file is used as Workflow input, the
Host-only adapter imports it as an immutable Input Resource.
