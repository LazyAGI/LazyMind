# Execution policy v1

Treat `ready_steps` as a frontier, not display order. Batch only independent,
simultaneously applicable steps when the Host profile permits parallel execution.
Do not batch alternatives, blocked work, retries, or speculative downstream work.

`advance_step` waits for the Supervisor to persist a terminal Attempt result.
`advance_step_and_hand_off` may be used only when the profile permits it and a
durable Supervisor has accepted ownership. Both request one Runtime transition;
the Runtime returns `resolved_operation` as execute, retry, or rewind.

The model never manages claim, fencing, heartbeat, progress, or terminal writes.
Those are deterministic Executor Supervisor responsibilities.

After Runtime accepts `advance_step`, the Supervisor may create a SubAgent through
the Host framework to execute the fixed step. That SubAgent is the only permitted
nested model call. Runtime route selection, operation resolution, lifecycle, and
Artifact commits remain deterministic before, during, and after SubAgent execution.
