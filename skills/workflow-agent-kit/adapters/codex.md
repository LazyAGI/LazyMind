# Codex Host adapter

Codex uses synchronous `advance_step`, platform approvals, native SubAgents, and
ordinary task progress. It does not select handoff or depend on LazyMind Driver,
synthetic turns, SSE, or editable Workflow Panel behavior. The deterministic
Codex Executor Supervisor owns Attempt claim, heartbeat, Artifact persistence,
and terminal reporting.
