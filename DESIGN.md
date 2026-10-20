# Superpowers dispatch reliability evaluation

> Historical experimental snapshot. For the reusable RC and new acceptance results, see [RELEASE_CANDIDATE.md](RELEASE_CANDIDATE.md).

Baseline: LazyMind `17b5e716cffe754f95eac28f2626971a0fc095f4`;
Superpowers v7.0.0 `bb92a77741419a4ab5f06e711a283343f1ada0c3`.
Development branch: `feat/superpowers-workflow-eval`.

Issue [2282](https://github.com/obra/superpowers/issues/2282) describes a caller
omitting brief generation and Todo updates. The current script rejects absent
task text, but a caller can skip it. The implementer template still lacks a
specific missing-brief fail-fast and SDD contains no in_progress instruction.
PR [2014](https://github.com/obra/superpowers/pull/2014) concerns script-side
overwrite/empty-output protection; it does not intercept dispatch. PR 2175 could
not be retrieved. Current checked-out code, not PR status, determines this design.

Issue [2245](https://github.com/obra/superpowers/issues/2245) concerns actual
worktree base, actual child model, enumerable coverage, and parallel isolation.
Check initial HEAD against a captured full SHA before any implementation. Check
every fixed requirement against both a checklist and independent tests.
Requested model is not an attestation of the backend model.

LazyMind already supplies immutable revisions, required outputs, ordered graph
admission, execution handles, failed attempts and retry. Its Codex queue adapter
cannot intercept arbitrary Codex tool calls. Prompts and JSON artifacts cannot
alone be called hard gates.

Scope: a standard six-step Workflow and a small deterministic external executor
in the example. It uses public authoring, preparation, execution, artifact and
completion APIs. Only the accepted dispatch step launches a real Codex subprocess.
The executor owns that launch, rechecks brief bytes/hash, observable Todo state and
Git HEAD immediately before it, and records a failure instead of success on errors.
No runtime or submodule changes. Running the YAML through an arbitrary executor
does not install these checks.

Prepare → validate preconditions → dispatch → collect → validate completion →
record. Required JSON artifacts preserve observations, not just conclusions.
A per-worktree file lock prevents cooperating duplicate dispatches. Distinct
working paths and Git index paths can be checked; general filesystem containment,
model identity and hostile-process isolation remain outside this example.

Validation: identical tiny Python slug task, initial commit, requested model and
Codex CLI version. Run unmodified native SDD normally; run Workflow with a real
Codex child; retain real traces separately from deterministic fault injection.
Inject missing/blank brief, stale Todo, wrong base, dispatch failure and missing
coverage; prove launch count remains zero for pre-dispatch failures. Verify a
normal task, fixed tests, Runtime artifacts/state and failed-step retry.
Continuation: run two actual Workflow children concurrently in fresh linked worktrees;
require distinct worktree/index paths before launch, observed overlap, distinct
completed sessions and an unchanged parent repository. Verify missing/stale Todo
against actual Runtime sessions as well as unit tests.
No percentages from small samples; unknown metrics are N/A. Experiments use only
disposable repositories. No push, PR, upstream comment, or shared-index write race.
