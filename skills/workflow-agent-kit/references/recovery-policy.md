# Recovery policy v1

- Failed or interrupted Attempt: target that step alone; Runtime resolves retry.
- Changed succeeded result: target the earliest invalidated step; Runtime resolves
  rewind and downstream staleness.
- Stopped Session: resume only after explicit user or Host authorization.
- Version conflict or event gap: refresh the projection before deciding again.
- Unknown command outcome: reconcile by command id; never blindly replay.
- Missing Artifact: fail structurally and preserve the Attempt evidence.

Never edit projection state, choose an internal retry/rewind operation, or combine
recovery with fresh frontier work.
