# Legacy Workflow behavior inventory

This inventory classifies the pre-refactor implementation. It is the deletion
ledger for later PRs; compatibility adapters may translate these boundaries but
must not add new business rules.

| Existing area | Classification | Current authority | Migration target |
|---|---|---|---|
| `backend/core/workflow/runtime_projection.go` | Runtime invariant | Core | `workflow/graphengine` |
| `backend/core/workflow/transition_handlers.go` | Runtime invariant and tool adapter | Core | Workflow Tool facade |
| `backend/core/workflow/store.go` | persistence compatibility boundary | Core | Workflow repository mapped to physical `plugin_*` schema |
| `backend/core/workflow/eventloop.go` | LazyMind-coupled dispatch | Core | queued Attempt and generic Outbox |
| `algorithm/lazymind/chat/plugin/plugin_manager.py` | mixed public decision and Host policy | algorithm/chat | shared Skill plus LazyMind Profile |
| `algorithm/lazymind/chat/plugin/driver_agent.py` | LazyMind Host policy | algorithm/chat | LazyMind Host Adapter |
| `algorithm/lazymind/chat/engine/subagent/runner.py` | model execution | algorithm/chat | LazyMindExecutor |
| `algorithm/lazymind/chat/engine/subagent/db.py` | persistence bypass | algorithm/chat | Workflow Client |
| `algorithm/lazymind/chat/api/subagent_routes.py` | compatibility adapter | algorithm/chat | Executor claim/report protocol |
| `frontend/src/modules/chat/store/pluginPanel.ts` | product projection | frontend | shared Workflow projection reducer |

## Frozen observable behavior

- Serial and parallel readiness is derived from the authoritative projection.
- Choice routes expose only the selected downstream branch.
- Advancing a failed/interrupted target resolves to retry; advancing a
  succeeded target resolves to rewind and stales downstream lineage.
- Partial retry preserves the selector in Attempt context.
- Stop interrupts active execution without deleting saved partial artifacts;
  resume derives new readiness from persisted state.
- `advance_step` waits for a terminal Attempt while
  `advance_step_and_hand_off` acknowledges only after durable ownership.
- Artifact revisions are immutable, ordered, and linked to their producer
  Attempt.
