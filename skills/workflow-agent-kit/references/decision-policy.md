# Decision policy v1

Apply the first matching rule. Runtime state wins over conversation recollection.

| Priority | Condition | Decision |
|---|---|---|
| 1 | Preparation has missing inputs | Bind durable resources or request input; do not start. |
| 2 | Any non-dismissed Session already exists | Reuse it; never trigger a replacement run. |
| 3 | Session is stopped and continuation is explicit | `resume_workflow`, refresh projection, then advance the interrupted step when allowed. |
| 4 | No Ready step | Observe active Attempts/events; do not manufacture a transition. |
| 5 | User changes a succeeded result | Target the earliest invalidated step; Runtime resolves rewind/staleness. |
| 6 | Failed/interrupted step is targeted | Advance that step alone; Runtime resolves retry/resume. |
| 7 | Multiple independent applicable Ready steps | Submit one atomic batch only if the profile permits parallel execution. |
| 8 | Ready step needs no approval | Use the profile's waiting tool and continue from the returned projection. |
| 9 | Explicit continuous scope/boundary | Wait through prerequisites; hand off only at the requested/final boundary if permitted. |
| 10 | Ordinary Ready frontier | Use handoff if permitted; otherwise use the waiting tool. |

`stop_workflow` is not a failure-recovery primitive. Use it only for explicit
pause/stop intent. Failure recovery targets the failed step in the same Session.
Starting a new run requires explicit dismissal of the prior Session first.

`advance_step` and `advance_step_and_hand_off` request the same Runtime transition.
Only waiting/ownership semantics differ. Codex never selects handoff. LazyMind may
select it only after durable ownership acceptance.

Terminal success requires every contract-required Artifact. Version conflict,
permission denial, invalid target, and missing Artifact are structured outcomes;
none may be converted into success or repaired by directly editing projection.
