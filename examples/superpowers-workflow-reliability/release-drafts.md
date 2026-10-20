# Technical drafts — not posted

These drafts describe the audited experimental scope. No PR or Issue comment has
been submitted. Attach the eventual reviewed example/commit link when publishing;
there is currently no public branch commit containing these uncommitted changes.
See the [release audit](../../RELEASE_AUDIT.md) and [comparison](comparison.md).

## LazyMind PR

**Title:** Add a bounded Superpowers dispatch reliability Workflow example

Superpowers dispatch prerequisites can be omitted by a prompt-following controller.
This example adds a six-step standard Workflow and an external executor that owns
Codex process launch. It requires a nonempty task brief, a persisted in-progress
event and the intended Git HEAD before launch, then validates finite coverage and
unchanged tests before recording success. It uses existing public Runtime APIs;
production Runtime code is unchanged.

Validation includes an independently repeated real Codex/Runtime E2E, a missing-
brief injection with zero launches and no downstream attempt, real API rejection
of early dispatch/missing required outputs, 28 regression tests, and retained
native/recovery/concurrent-run evidence. Both normal native samples passed; this
is not evidence of an upstream failure rate or cost improvement.

The gate applies only to this executor. Codex Todo UI synchronization, backend
model attestation and arbitrary-process containment are not provided. Repeat runs
reuse the installed Workflow ref: blank-draft publication of the same ID currently
fails on the tested Runtime. README documents this limitation and the tested host.

## Superpowers #2282

We built an experimental LazyMind Workflow around a pinned, unmodified Superpowers
v7.0.0 task-brief script. The accompanying executor owns the actual Codex launch:
missing/empty briefs and missing/stale local Todo evidence prevent dispatch. An
independent real Runtime rerun with injected brief deletion failed validation with
zero launches and no Dispatch attempt; the normal rerun completed.

Both normal native SDD samples passed. The audited controller's tool receipts show
brief generation completing before its first spawn, so we did not naturally
reproduce missing-brief dispatch. No explicit in-progress transition was observed
in the recorded native ledger. The Workflow's Todo is an executor event/Artifact,
not synchronization with Codex's Todo UI. YAML alone is not a global dispatch hook.
The example includes traces and bounded same-session recovery evidence.

## Superpowers #2245

Our bounded Workflow example compares the actual worktree HEAD with a captured
full SHA before Codex launch, and checks an exact finite coverage set plus fixed
and independent behavior tests. An injected real HEAD change blocked launch;
removing a coverage row after a real child finished prevented complete success.
Two real Workflow/Codex runs in disposable linked worktrees overlapped, used
separate indexes and left the parent snapshot unchanged.

This covers only the small fixture and cooperating execution path. It does not
attest the backend model, reproduce the original large-scope task, prove arbitrary-
process containment, or validate a shared-index concurrent commit strategy. Both
normal native samples passed, and differing reviewer/controller work makes these
runs unsuitable for comparative cost or reliability percentages.
