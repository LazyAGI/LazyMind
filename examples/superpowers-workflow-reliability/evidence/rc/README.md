# RC evidence — 2026-10-20

[Developer commands](../../README.md) · [中文入口](../../README.CN.md) ·
[Acceptance report](../../../../RELEASE_CANDIDATE.md)

This directory contains **real Runtime/Codex execution**, deliberate fault injection
and separately labeled regression logs. It is not a benchmark. The source commit
changed during the observed Retry permission fix; [provenance.json](provenance.json)
records which code ran each case. No historical native trace is presented as a new
RC run. Native evidence remains one directory above.

| Input / classification | Files | Observed outcome |
| --- | --- | --- |
| New CSV task / normal E2E | [csv-a](csv-a/result.json), [csv-b](csv-b/result.json) | Initial install and compatible repeat use, two real commits |
| Caller Plan Task 7 / normal E2E | [custom-labels](custom-labels/result.json), [Unicode variant](custom-unicode/result.json) | Different allowed files and coverage vocabulary; real code and tests |
| Brief deletion / fault injection + real Runtime | [pre-recovery](missing-before-recovery.json), [current code](missing-current/result.json) | Zero process starts, failed validation, no Dispatch attempt |
| Same-session recovery / real UI + Runtime + Codex | [result](missing-brief/result.json), [trace](missing-brief/trace.jsonl), [UI receipts](ui-retry-receipts.json) | Old failed attempt retained; new attempt succeeds |
| Actual new empty commit / fault injection | [wrong-base](wrong-base/result.json) | BASE_MISMATCH; zero launches |
| Row deletion after a real child / fault injection | [missing-coverage](missing-coverage/result.json) | Child passed, completion gate rejected, no result Artifact |
| Unit/API doubles | [53-test log](units-current.log) | Regression only, not E2E |

[commands.json](commands.json) records actual argv, starts/ends and return codes.
The first `resume-missing-brief` returned 1 because external API Retry was denied
403; the later same command completed after the code fix and UI Retry. The trace
retains both. Expected negative exits are 1. Timings and usage are observations,
not billing or efficiency estimates. Backend model identity is N/A.

[verification-live.json](verification-live.json) independently re-read all eight
session projections, attempts and artifacts through authenticated GET requests,
compared protected Git blobs, checked unchanged parent repositories and allowed
changes, reran tests and exercised additional CSV/label behaviors. It is a derived
verification record; original API/child receipts remain in each trace/result.
Every real child has a Popen PID receipt, successful exit, turn.completed and raw
trace hash. Negative zero-launch claims apply to this executor, not the whole OS.

The lifecycle directory retains one first-publish/compile trace, repeated install
and preflight reads, and the incompatible legacy package rejection. First publish
has diagnostics.valid=true; repeated installs use GET/PATCH and no draft creation.
All sessions share revision b6a4f724-0804-479a-986c-6c5043cb096d.

## Reproduce

Run the README Quick Start for CSV A, then its repeat-use commands for CSV B.
Use its explicit `recovery` demo commands for deletion/restoration/UI Retry/resume.
For Base and Coverage negatives, use a **fresh disposable** demo for each:

```bash
.venv/bin/python demo.py "$TRIAL/wrong-base"
git -C "$TRIAL/wrong-base/worktree" commit --allow-empty -m 'Injected wrong HEAD'
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run \
  --task "$TRIAL/wrong-base/task.json" --repo "$TRIAL/wrong-base/worktree" --state "$TRIAL/wrong-base/run"
# Expected exit 1, BASE_MISMATCH, no launch.
.venv/bin/python demo.py "$TRIAL/missing-coverage"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run --fault missing-coverage \
  --task "$TRIAL/missing-coverage/task.json" --repo "$TRIAL/missing-coverage/worktree" --state "$TRIAL/missing-coverage/run"
# Expected exit 1 after a real child; incomplete coverage cannot succeed.
```

Caller-task seeds are in [inputs/custom-labels](inputs/custom-labels/docs/plan.md)
and [inputs/custom-unicode](inputs/custom-unicode/docs/plan.md). Copy one seed
(including .gitignore) into a new empty repository, commit all inputs, create a
linked worktree at its full SHA, and copy the corresponding recorded `task.json`
outside the worktree. Replace **expected_base** with your new commit SHA; do not
reuse this machine's historical SHA. Run with the new repo/task/state arguments.
No fixed task number, fixture commit or three fixed coverage IDs are in the RC executor.

To check the published snapshots without credentials or model calls:

```bash
python3 verify_rc_evidence.py
```

That is an **offline integrity/consistency check**, not a new E2E. It validates all
three manifests and source hashes plus recorded terminal, gate and recovery facts.
For live testing, use run/status; fresh public Runtime/Codex access is required.

## Redaction and preservation

The [manifest](manifest.json) records original and exported hashes. Known host roots,
test owner identity and Runtime execution handles are removed. Catalog responses
are explicitly projected to reliability packages, with an omitted-item count;
unrelated workflows are not published. UI receipts contain only selected calls and
relevant status lines, with source-line hashes. Config/token/session credential
files, private stderr and unrelated conversations are excluded. The fixture traces
contain no user business code. Hashes detect accidental changes; they do not provide
third-party execution attestation. Original 89-file and prior 38-file audit manifests
are preserved. Duplicate readback JSON is omitted because independent verification
and the exact GET response in each trace retain the relevant evidence.
