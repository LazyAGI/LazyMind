# Community comment drafts — not posted

Use the submitted LazyMind PR URL alongside these drafts. Public [Quick Start](README.md),
[PR validation](PR_VALIDATION.md) and [RC evidence](evidence/rc/README.md) document the
supported scope. Posting either comment requires a separate user action.

## Superpowers #2282

We built a reusable Superpowers Dispatch Reliability Workflow integration with
LazyMind. Its external executor owns Codex launch: the pinned v7 task-brief script
must finish, the brief must be nonempty, and local in-progress state must have an
observable receipt before dispatch. Six Runtime steps retain the execution Artifacts.

The latest real Runtime run completed normally; an explicit missing-brief injection
failed validation with zero child starts and no Dispatch attempt. Earlier RC evidence
also covers same-session recovery through authenticated UI Retry. Both native normal
samples passed, so we do not claim to have reproduced a natural upstream failure or
measured a reliability improvement. This is one-task integration, not full SDD; its
local state does not synchronize Codex's Todo UI, and YAML alone cannot intercept
other agents. The linked example includes a CSV demo, custom-task setup and redacted
traces. Feedback from developers seeing similar dispatch-ordering failures is welcome.

## Superpowers #2245

We built a runnable LazyMind Workflow example that checks actual worktree HEAD against
a declared full SHA, then requires an exact coverage checklist mapped to protected
acceptance tests before reporting success. The RC ran caller-defined tasks, including
a Unicode file path, through real Codex processes. An injected wrong HEAD blocked
launch; deleting a coverage row after a real child blocked complete success.

A separate disposable parallel experiment recorded distinct worktree/index paths and
an unchanged parent repository. The reusable entry point handles one task per run;
it does not guarantee arbitrary-process isolation or attest the backend model. Test
quality and the requirement-to-test mapping remain caller responsibilities. The linked
CSV Quick Start, custom-task instructions and raw evidence are available for trying
this bounded integration. No full upstream fix or speed/success-rate gain is claimed.
Feedback on comparable base/coverage failures would help assess its useful scope.
