# #2245 — actual Git base and finite task coverage

[Upstream discussion](https://github.com/obra/superpowers/issues/2245) ·
[Advanced Quick Start](../ADVANCED.md) · [Comparison](../comparison.md)

This is a supplementary, bounded case. The small native plugin demo concentrates
on #2282; it does **not** certify the Codex host's Git state or coverage. Use the
existing six-step executor for the following checks.

| Check | Implemented boundary | Real historical evidence |
| --- | --- | --- |
| Git base | Read actual full HEAD in a dedicated linked worktree; compare with declared SHA immediately before the owned child starts | [Wrong-base injection](../evidence/public/wrong-base.json): BASE_MISMATCH, zero launches |
| Finite coverage | Exact declared IDs, no missing/duplicate/skipped entries; execute trusted checks; protect Plan/tests and restrict changed files | [Missing coverage](../evidence/public/missing-coverage.json): child ran, completion failed |
| Custom tasks/reuse | Same workflow with Task 7 and different coverage IDs, including Unicode paths | [Reuse/custom summary](../evidence/public/reuse-custom.json) |
| Recovery | Same-session recovery only for supported pre-dispatch failure | [Recovery](../evidence/public/recovery.json) |

[Task schema](../ADVANCED.md#your-own-repository-and-plan), [task binding](../task.py)
and [launch gates](../gates.py) are reusable across the two issue cases in the
Advanced executor. Tests/coverage mappings are trusted caller inputs: an ID mapped
to a passing command does not prove the command tests a useful requirement.

Not covered: backend model identity attestation, arbitrary-process containment,
shared Git Index safety against noncooperating processes, full multi-task SDD
planning, or reviewer loops. Cooperating worktree/index identity and lock checks
are not OS isolation. Experiments use disposable repositories; no dangerous
parallel Git writes are performed in a real shared project.

The native normal sample passed. The base/coverage failures above were explicit
fault injections, not naturally reproduced upstream failures. No success-rate,
speed or token-efficiency improvement is claimed. Current regression and E2E
status is separate in [PR validation](../PR_VALIDATION.md) and
[review response](../REVIEW_RESPONSE.md).
