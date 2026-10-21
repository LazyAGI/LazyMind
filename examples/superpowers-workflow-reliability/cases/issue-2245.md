# #2245: Actual base, coverage and observability

[Upstream issue](https://github.com/obra/superpowers/issues/2245) spans base selection,
model identity, task coverage and parallel isolation. This single-task integration
covers actual Git base and declared finite coverage; the broader issue remains
**partially covered**.

[Task validation](../task.py) binds the full expected SHA, committed Plan, protected
acceptance files and dynamic coverage IDs. [Guard](../gates.py) checks actual HEAD
and linked worktree/index identity before launch. Completion reruns declared tests,
checks permitted changes and rejects incomplete/duplicate/skipped coverage.
Caller-provided tests are trusted inputs: mapping an ID to a command alone does
not prove arbitrary prose requirements.

| Scenario | Recorded result | Evidence |
| --- | --- | --- |
| Actual wrong HEAD | BASE_MISMATCH in Prepare; zero children, downstream unexecuted | [Wrong base](../evidence/public/wrong-base.json) |
| Missing coverage row after real child | Child exits 0; Validate completion fails, no result Artifact | [Missing coverage](../evidence/public/missing-coverage.json) |
| Caller Task 7 / Unicode filename | Changed only declared file; custom coverage, protected tests pass | [Custom summaries](../evidence/public/reuse-custom.json) |

After the [Quick Start](../README.md#quick-start-a-csv-task), run each negative in a
**fresh disposable** demo:

```bash
.venv/bin/python demo.py "$TRIAL/wrong-base"
git -C "$TRIAL/wrong-base/worktree" commit --allow-empty -m 'Injected wrong HEAD'
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run \
  --task "$TRIAL/wrong-base/task.json" --repo "$TRIAL/wrong-base/worktree" --state "$TRIAL/wrong-base/run"
# Expected exit 1: BASE_MISMATCH, launch_count 0.
.venv/bin/python demo.py "$TRIAL/missing-coverage"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run --fault missing-coverage \
  --task "$TRIAL/missing-coverage/task.json" --repo "$TRIAL/missing-coverage/worktree" --state "$TRIAL/missing-coverage/run"
# Expected exit 1 after a real Codex child: COVERAGE_MISMATCH.
```

Both are explicit fault injection, not spontaneous Skill failures. The wrong-base
record includes actual and expected full SHA values from independent Git readback.
The coverage record includes the remaining rows and rejected completion state.

Backend model identity cannot be reliably attested here (N/A). Cooperating locks,
worktree/index checks and [parallel regression tests](../test_parallel.py) do not
prove isolation against arbitrary processes or replace OS containment. The CLI runs
one declared task; full parallel SDD scheduling and reviewer loops are unsupported.
[Public evidence](../evidence/public/README.md) does not claim a new parallel E2E.
