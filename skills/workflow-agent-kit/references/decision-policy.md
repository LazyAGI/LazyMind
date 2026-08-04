# Decision policy v1

Apply the first matching rule. Runtime state wins over conversation recollection.

| Priority | Condition | Decision |
|---|---|---|
| 1 | Preparation has missing inputs | Bind durable resources or request input; do not start. |
| 2 | Session is stopped and resume is explicit | `resume_workflow`; otherwise only observe. |
| 3 | No Ready step | Observe active Attempts/events; do not manufacture a transition. |
| 4 | User changes a succeeded result | Target the earliest invalidated step; Runtime resolves rewind/staleness. |
| 5 | Failed/interrupted step is targeted | Advance that step alone; Runtime resolves retry/resume. |
| 6 | Multiple independent applicable Ready steps | Submit one atomic batch only if the profile permits parallel execution. |
| 7 | Ready step needs no approval | Use the profile's waiting tool and continue from the returned projection. |
| 8 | Explicit continuous scope/boundary | Wait through prerequisites; hand off only at the requested/final boundary if permitted. |
| 9 | Ordinary Ready frontier | Use handoff if permitted; otherwise use the waiting tool. |

`advance_step` and `advance_step_and_hand_off` request the same Runtime transition.
Only waiting/ownership semantics differ. Codex never selects handoff. LazyMind may
select it only after durable ownership acceptance.

Terminal success requires every contract-required Artifact. Version conflict,
permission denial, invalid target, and missing Artifact are structured outcomes;
none may be converted into success or repaired by directly editing projection.
