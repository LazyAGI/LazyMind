# #2282: Brief and task-state prerequisites

[Upstream issue](https://github.com/obra/superpowers/issues/2282) describes dispatch
without required preparation. At pinned Superpowers v7.0.0, `task-brief` rejects
empty extracted text when invoked; it cannot force the controller to invoke it.
This example adds an executable gate around its own child process.

Read the [Quick Start](../README.md#quick-start-a-csv-task) first. The
[Workflow](../../../workflows/superpowers-reliability/workflow.yaml) follows:
Prepare → Validate dispatch → Dispatch → Collect → Validate completion → Record.
Core enforces admission and required output slots; the [executor](../project.py)
and [Guard](../gates.py) enforce actual brief/state checks before Popen. YAML prose
alone is not a hard gate.

| Scenario | Recorded result | Evidence |
| --- | --- | --- |
| Native normal Skill | Brief command before implementer spawn; final tests pass | [Native excerpt](../evidence/public/native.json) |
| Workflow normal | Nonempty brief and in_progress event before child; six steps/artifacts succeed | [Normal](../evidence/public/normal.json) |
| Delete generated brief | BRIEF_MISSING, zero launches, dispatch unexecuted | [Brief missing](../evidence/public/brief-missing.json) |
| Restore + authorized UI Retry | Same Session; failed attempt 1 retained, attempt 2 succeeds | [Recovery](../evidence/public/recovery.json) |

The negative case deliberately deletes the brief after real upstream generation.
It proves blocking at the executor boundary; it is not a natural reproduction of
an upstream bug. Empty brief, missing/stale Todo and failed dispatch are also
covered by [regression tests](../test_gates.py); those tests use local fixtures and
callbacks, not new provider failures or E2E.

Reproduce the negative and recovery using the [explicit commands](../README.md#missing-brief-and-recovery).
The missing-brief run exits 1 with launch_count 0. Restore the same brief, click
**Retry** in the authenticated Workflow page, then use `--resume`. The CLI respects
UI recovery authority; it does not bypass the external API's 403 restriction.

Todo evidence is the executor's persisted task state, not Codex UI synchronization.
Backend model identity is N/A. No comparative success-rate or performance claim is
made. [Evidence provenance](../evidence/public/README.md).
