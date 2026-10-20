# Technical drafts — not posted

Use the eventual reviewed candidate link when publishing. No branch was pushed and
no PR or Issue comment was submitted. [RC acceptance](../../RELEASE_CANDIDATE.md).

## LazyMind PR

**Title:** Add a reusable Superpowers dispatch reliability Workflow example

This example binds one committed Plan task to an isolated Git worktree and owns
Codex process launch through six accepted Workflow steps. It checks brief/state/base
before launch and declared coverage, protected acceptance tests and file scope before
success. configure/install/run/status/restore-brief provide a documented developer
flow. Compatible installations reuse the existing Workflow revision; incompatible
packages fail without overwriting drafts. Production Runtime is unchanged.

Validation: 53 regression tests; clean client checkout/venv; eight real Runtime
sessions with six actual Codex children, including caller-authored Task 7 and Unicode
paths; first/repeated use, zero-launch brief/base gates, post-child coverage rejection
and same-session recovery through authenticated UI Retry. A pre-fix API Retry 403 is
retained as failure evidence. Artifact state, Git files and behavior were independently
read back. Both historical native SDD samples passed. No failure-rate or efficiency
gain is inferred. Backend identity, Codex Todo UI sync, arbitrary-process containment,
full SDD and uncertain-dispatch replay are outside this bounded integration.

## Superpowers #2282

We built a bounded LazyMind integration using the pinned, unmodified v7.0.0 task-brief
script. The external executor owns actual Codex launch and blocks missing/blank
briefs or missing/stale local task-state evidence. Fresh real Runtime testing with
injected brief deletion produced BRIEF_MISSING, zero process receipts and no Dispatch
attempt. Restoring the brief, using authenticated UI Retry and resuming the executor
completed the same session while retaining the failed attempt.

Both normal native SDD samples passed; actual audited tool receipts show brief
creation finishing before spawn. We did not naturally reproduce the missing-brief
bug. No in_progress event was observed in the recorded native ledger. The Workflow
state is executor evidence, not synchronization with Codex's Todo UI. YAML alone does
not intercept other agents. The RC accepts a caller-declared single task and includes
reproducible commands, raw redacted evidence and explicit control boundaries.

## Superpowers #2245

The integration compares actual worktree HEAD with a caller-declared full SHA and
binds one Plan task to exact allowed files, immutable acceptance inputs and a complete
coverage-to-check mapping. Caller-authored CSV/label tasks (including a Unicode path)
completed through real Codex/Runtime execution. A real injected HEAD change blocked
launch; removing a checklist row after a real child prevented complete success.
The earlier disposable parallel experiment verified distinct worktree/index paths
and an unchanged parent snapshot; the RC entry itself runs one task at a time.

Actual backend model identity remains N/A. This does not prove arbitrary-process
containment, shared-index safety, completeness of arbitrary language requirements,
or parity with SDD's controller/reviewer work. Both native normal samples passed;
there is no comparative reliability, cost or speed percentage. The useful result is
a repeatable, inspectable hard gate on the executor's own dispatch/completion path.
