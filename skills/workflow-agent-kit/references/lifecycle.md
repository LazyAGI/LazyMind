# Workflow lifecycle v1

1. Inspect the conversation first. If any non-dismissed Session exists, reuse its
   exact id and projection; do not prepare another Session.
2. Only when no non-dismissed Session exists, discover an enabled, authorized
   Workflow and pin its revision.
3. Call the exact catalog-bound `trigger_<workflow>_workflow`. The Host pins the
   revision, prepares the run, creates the Session when inputs are sufficient,
   and returns its initial projection. No separate preparation/start tool is
   exposed to the model.
4. If the trigger reports missing inputs, use the Host framework's existing
   conversation-attachment mechanism, then call the same trigger with the
   resolved `input_bindings`. No Session exists yet. Do not invent a Session or
   claim it started.
5. Refresh the projection and select only exact `ready_steps`, `retryable_steps`,
   or `rewindable_steps`; Runtime resolves the operation.
6. Review required Artifacts before reporting terminal success.
7. Stop only on explicit user intent through the Host/UI controller; do not
   expose stop as a model tool. Resume the same Session, refresh, and then advance
   the interrupted step; never stop-and-prepare as a recovery strategy.

Conversation memory is not projection state. A
state-version conflict always requires a fresh projection and a new decision.
Stopped, failed, and completed Sessions remain non-dismissed and therefore block
a new run until the user explicitly dismisses them.

The trigger's returned `session_id` is authoritative: do not trigger again,
synthesize another id, or invoke a separate preparation/start tool.
