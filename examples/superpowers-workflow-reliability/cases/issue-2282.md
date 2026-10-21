# #2282 — require a Brief before implementation

[Upstream discussion](https://github.com/obra/superpowers/issues/2282) ·
[Try in Codex](../README.md#try-in-codex) · [Demo](../DEMO.md)

A Skill describes ordering but cannot itself prevent an agent from selecting its
next tool early. The pinned v7.0.0 native sample generated its Brief and passed;
we did **not** observe a natural missing-Brief failure. [Selected native evidence](../evidence/public/native.json).

## Native LazyMind boundary

[prepare → validate_brief → implement](../../../workflows/superpowers-brief-showcase/scenario/state.yml)
is a real Core graph. The validate_brief step declares a native script tool and
`runtime.post_step_checks`; Core routes that step to its LazyMind worker, without
issuing an external execution handle to Codex. A no-argument tool copies the exact input revision from the public Attempt Context;
the pinned [check](../../../workflows/superpowers-brief-showcase/scripts/brief.py) rejects null,
blank, invalid or unexpected demo Brief content before validation can commit.
Core therefore cannot admit the implementation step after that failure.

`FAULT_BRIEF_MISSING` deliberately asks Codex to publish a null Brief candidate (verify that it actually did).
`FAULT_BRIEF_EMPTY` publishes an empty string. These are artifact-level injections,
not deletion of a Superpowers file or a replay of the upstream incident. A native
validation SubAgent has already run; zero **implementation attempts** must not be
confused with zero model calls or zero child processes.

An unchanged null Brief was retried and failed again. Editing the source Brief
created a human revision and invalidated the validator; the tested Panel then
disabled Retry. Edited-input recovery is **BLOCKED**, not a completed native E2E.
Do not repair by editing the Gate output. Human edits and approvals are trusted user actions. Implementation requires two Artifacts and human review.
[Current acceptance status](../REVIEW_RESPONSE.md) says which parts were observed;
[reproduction guide](../DEMO.md) includes optional recording instructions; video is NOT_RECORDED.

## Stronger filesystem checks remain available

The [Advanced path](../ADVANCED.md#missing-brief-and-recovery) uses the upstream
brief script and checks an actual file, recorded in_progress state and Git base
before its owned Codex launch. [Missing Brief](../evidence/public/brief-missing.json)
records zero child launches; [normal](../evidence/public/normal.json) records a real
child and six successful Runtime steps; [recovery](../evidence/public/recovery.json)
retains a failed attempt followed by same-session authorized UI Retry.

Core provides graph/lease/output/recovery constraints; these two business check
implementations cover different boundaries. The plugin has no pre-tool hook, does
not synchronize Codex Todo UI, and does not police other Codex work.
