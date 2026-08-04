# LazyMind Host adapter

LazyMind may use approval UI, DriverAgent, SubAgent execution, stop-tool, SSE,
synthetic turns, and durable handoff. These capabilities affect orchestration and
presentation only. They do not change Runtime readiness, Attempt operations,
Artifact lineage, permissions, or state-version checks.

Use `advance_step_and_hand_off` only after durable Supervisor acceptance; trusted
Driver synthetic turns may bypass user-input filtering, while ordinary Workflow
user messages must still be filtered.
