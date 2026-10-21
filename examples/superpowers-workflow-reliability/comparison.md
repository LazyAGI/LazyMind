# Native Skill and LazyMind Workflow

The native sample uses unmodified Superpowers v7.0.0 at
`bb92a77741419a4ab5f06e711a283343f1ada0c3`. It passed its slug task and independent
checks. The reusable Workflow examples use CSV and custom tasks. These executions
have different scope (native includes reviewers); they are **not a fair benchmark**.
No reliability percentage, speedup or token-efficiency comparison is supported.

| Issue | Check | Native Skill | LazyMind Workflow | Actual evidence | Limits |
| --- | --- | --- | --- | --- | --- |
| #2282 | Brief before dispatch | Brief command precedes implementer spawn in normal sample | Nonempty frozen brief checked before owned Popen | [Native](evidence/public/native.json), [normal](evidence/public/normal.json), [missing](evidence/public/brief-missing.json) | Missing brief is deliberate injection, not natural bug reproduction |
| #2282 | Observable task state | Model-followed convention; no attested Host Todo history published | Persisted in_progress event checked; completed only after acceptance | [Normal events](evidence/public/normal.json) | Executor state; no Codex Todo UI synchronization |
| #2282 | Recovery | Not evaluated here | Failed Attempt retained; UI Retry then same-session resume | [Recovery](evidence/public/recovery.json) | Only before dispatch; uncertain launches are not replayed |
| #2245 | Actual base | Normal native task completed; no negative comparison | Actual full HEAD checked against declared SHA before child | [Wrong base](evidence/public/wrong-base.json) | Expected SHA must be trusted; injected actual empty commit |
| #2245 | Enumerable coverage | Three requirements and tests passed | Exact dynamic checklist plus protected checks and allowed-file scope | [Missing coverage](evidence/public/missing-coverage.json), [custom tasks](evidence/public/reuse-custom.json) | Missing row injected after real child; tests must meaningfully encode requirements |
| #2245 | Model identity | Requested model only | Requested model recorded; actual backend N/A | [Native](evidence/public/native.json), [normal](evidence/public/normal.json) | No identity hard gate |
| #2245 | Parallel isolation | Not claimed | Linked worktree/index validation and cooperating locks | [Guard tests](test_gates.py), [parallel regression](test_parallel.py) | These tests are not live parallel E2E; arbitrary-process containment remains unsupported |

The public directory contains selected historical records, not complete traces.
Its [manifest and guide](evidence/public/README.md) explain provenance and offline
checking. [PR validation](PR_VALIDATION.md) distinguishes current reruns, historical
real E2E and unit/API doubles. Full SDD conversion is outside this integration.
