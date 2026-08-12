# LazyMind source → shared policy ledger

This ledger records where the migrated behavior came from. The shared policy is
now authoritative by default; the former Host policy is a bounded rollback path.

| Original source | Existing rule | Shared policy clause | Shadow input/evidence |
|---|---|---|---|
| `chat/workflow/workflow_manager.py::_COLD_START_WORKFLOW_PROMPT` | Trigger only for direct intent; explicit named Workflow must trigger | Discover/prepare before start | Cold-start prompt tests remain authoritative |
| `build_cold_start_tools` preflight | `need_information` blocks start; `ready` produces one valid first step | Missing-input and preparation gates | Existing preflight tests |
| `_build_cold_execution_policy` | Auto hands off; dynamic waits only for explicit multi-step/no-approval cases | Rules 7–9 | Golden launch cases |
| `_build_step_status_section` | Go Ready projection is the execution frontier; conditions disambiguate choices | Rules 3 and 6 | `ready_steps`, active edges |
| `_build_mode_guidance` Rule 0 | Persist explicit user constraints before advancement | Host intent capture before shared decision | Intent-writer tests; policy receives normalized intent tokens only |
| `_build_mode_guidance` Rule 1 | Changed succeeded output targets earliest invalid step | Rule 4 | `changed_succeeded_step` |
| `_build_mode_guidance` Rule 2 | Atomic batch of independent Ready steps; retry is singular | Rules 5–6 | Ready/attempted sets |
| `_build_mode_guidance` Rules 3–4 | Approval and explicit uninterrupted boundary choose wait vs handoff | Rules 7–9 | approval map + normalized intent tokens |
| `build_advance_step_tool` | Wait for submitted task result and continue current turn | Waiting-tool semantics | Existing manager tests |
| `build_advance_step_and_hand_off_tool` | Submit atomically then stop after durable acceptance | Handoff semantics | Existing manager/stream-guard tests |
| Public Workflow transition facade | Runtime resolves transition and accepts/rejects command | Runtime authority/idempotency | Transition submission tests |
| LazyMind handoff callback | Synthetic next turn after asynchronous completion | LazyMind Host profile (`synthetic_turn`) | Host handoff tests; not a Runtime policy rule |
| `engine/subagent/runner.py::_build_subagent_plan` | Attempt objective, inputs, artifacts and output contract are isolated | Execute/review contract | SubAgent prompt-plan tests |
| `engine/subagent/runner.py` terminal handling | Artifacts and terminal outcome are reported after execution | Required-Artifact terminal rule | SubAgent artifact tests |

## Rollback and observation gate

`LAZYMIND_WORKFLOW_POLICY_V1` is default-on. Setting it to false explicitly selects
the bounded former Host policy and increments the rollback counter. Optional
`workflow.shadow-trace.v1` entries are observational only and record `authority`
from the path that actually made the decision; they never invoke tools or mutate
Runtime state.
