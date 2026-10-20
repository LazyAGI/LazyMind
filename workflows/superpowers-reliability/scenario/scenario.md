# Superpowers dispatch reliability

Run this package only with the external executor in
`examples/superpowers-workflow-reliability/run.py`.
It executes one approved fixture task in a disposable Git worktree, checks dispatch
prerequisites, records a real child process and verifies enumerable coverage.
The six JSON outputs contain measured receipts.

The YAML defines graph dependencies and required artifacts. The accompanying
executor owns the pre-dispatch and completion checks. Loading this package into
another host does not intercept that host's tools. Model identity is recorded only
when observable; a requested model name is not proof of the backend model.

Steps: `prepare` generates the brief and observable Todo; `validate_dispatch`
checks actual prerequisites; `dispatch` rechecks and starts the child; `collect`
retains execution evidence; `validate_completion` checks coverage and tests;
`record` marks the task complete.
