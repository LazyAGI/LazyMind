# Baseline verification — project tests and migration catalogue

Verified 2026-10-20 against exact base commit `81683cfbea2f0a1cb0c696327abb77e7e412df37`.

## Isolation and method

`git archive 81683cfb backend/core` was extracted into `/tmp/lazymind-core-baseline-81683cfb.QAZnv1`. This contains the complete baseline Core module and its imported Core dependencies, without shared working-tree business changes. The tracked archive is 1,956 files / 45,457,295 bytes; no Git worktree, runtime configuration, user data, or service deployment was created or changed.

The same exact test selection was run independently in that module and the current authorized worktree:

```sh
go test ./chat ./conversationgroup -run '^(TestProjectTypesHaveIndependentLifecycle|TestSameNameProjectsUseDirectoryIdentity|TestProjectNamesAreIndependentOfGroups|TestDeletedProjectRecreationPreservesHistoryAndRestoreIsAtomic|TestConcurrentExplicitProjectCreation)$' -count=1 -v
```

## Observed comparison

| Test | Base result | Current result | Same failing location/message |
| --- | --- | --- | --- |
| TestProjectTypesHaveIndependentLifecycle | FAIL | FAIL | project_type_isolation_test.go:26 — Invalid request |
| TestSameNameProjectsUseDirectoryIdentity | FAIL | FAIL | project_type_isolation_test.go:151 — Invalid request |
| TestProjectNamesAreIndependentOfGroups | FAIL | FAIL | project_test.go:218 — Invalid request |
| TestDeletedProjectRecreationPreservesHistoryAndRestoreIsAtomic | FAIL | FAIL | project_test.go:289 — Invalid request |
| TestConcurrentExplicitProjectCreation | FAIL | FAIL | project_test.go:386 — Invalid request |

All five fail while registering their test workspace. These five failures are confirmed present at the base commit on this machine; this is not an inference from unchanged filenames. No baseline business fix was attempted. This comparison does not assert why the environment rejects those workspace paths or establish results for other failures in the full-suite log.

Evidence retained outside the repo:
- `/tmp/knowledge-project-tests-base-81683cfb.log` (exact base run)
- `/tmp/knowledge-project-tests-current.log` (current run)
- `/tmp/kb-core-full.log` (parent's earlier full run)

## Migration catalogue test correction

`backend/core/migrate/mode_test.go` expected 110 v0_3 dev migrations. An initial focused run failed with actual count 112. The assertion now expects 112 and explicitly checks the two newly introduced versions `20261020034257` and `20261020130000`, preserving the existing exhaustive catalogue check rather than weakening it.

`go test ./migrate -count=1` passed (21.001s). Log: `/tmp/knowledge-migration-catalog-tests.log`.

Only the migration test and this report were changed for this verification subtask. Temporary reconstructed source was removed after testing with non-forced rm -r targeting that exact directory; evidence logs remain under /tmp.
