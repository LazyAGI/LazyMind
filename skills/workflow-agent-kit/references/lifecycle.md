# Workflow lifecycle v1

1. Discover an enabled, authorized Workflow and pin its revision.
2. Call `prepare_workflow`; bind every missing durable Input Resource.
3. Call `start_workflow` only with a ready, unexpired preparation.
4. Refresh the projection, select only `ready_steps`, and advance through the
   tool allowed by the active Host profile.
5. Review required Artifacts before reporting terminal success.
6. Stop, resume, retry, or revise only through Workflow tools.

Preparation is not a Session. Conversation memory is not projection state. A
state-version conflict always requires a fresh projection and a new decision.
