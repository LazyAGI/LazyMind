# Core test failures — independent attribution

Checked on 2026-10-20 against exact baseline `81683cfbea2f0a1cb0c696327abb77e7e412df37`. This report separates reproduced baseline failures, changes fixed during implementation, and intermittent failures whose cause is not established. It does not claim a completely green full-suite run.

## Sidecar startup timeout: not reproduced in isolated comparison

The full-suite evidence `/tmp/kb-core-final-full.log` reports:

- `cmd/feishu-cli-sidecar.TestSidecarProductionStartupWiresHelperAndPreservesCLI/valid`
- `startup_credential_test.go:143`, POST `/v1/execute`: `context deadline exceeded (Client.Timeout exceeded while awaiting headers)`.
- That test uses a three-second HTTP client timeout. The fixture subprocess listens only on an ephemeral loopback port; no deployed service is involved.

An independent complete `backend/core` archive from the exact baseline was extracted into `/tmp/knowledge-sidecar-base-81683cfb.n42Sv4`. The identical command was run in the archive module and the current authorized worktree:

```sh
GOTOOLCHAIN=go1.25.11 go test ./cmd/feishu-cli-sidecar \
  -run '^TestSidecarProductionStartupWiresHelperAndPreservesCLI$/^valid$' \
  -count=1 -v
```

| Source | Result | Valid subtest duration | Package duration |
| --- | --- | --- | --- |
| Exact baseline archive | PASS | 0.43 s | 1.108 s |
| Current worktree | PASS | 0.43 s | 1.028 s |

Evidence: `/tmp/knowledge-sidecar-base-81683cfb.log`, `/tmp/knowledge-sidecar-current.log`.

`git diff` from that baseline is empty for `backend/core/cmd/feishu-cli-sidecar`, `providerconnection`, `cloudsession`, `go.mod`, and `go.sum`. This supports lack of a direct implementation change in the exercised path; it does **not** prove the timeout's cause. Classification: intermittent full-suite timeout, with both isolated baseline and current checks passing. A resource-contention explanation is plausible but unproven. No production fix or timeout relaxation was made.

## Confirmed existing project/workspace failures

The earlier exact-archive comparison independently reproduces the following five failures at baseline and current code. All fail while registering their test workspace, with `Invalid request`:

| Package / test | Failing source location |
| --- | --- |
| chat / TestProjectTypesHaveIndependentLifecycle | project_type_isolation_test.go:26 |
| chat / TestSameNameProjectsUseDirectoryIdentity | project_type_isolation_test.go:151 |
| conversationgroup / TestProjectNamesAreIndependentOfGroups | project_test.go:218 |
| conversationgroup / TestDeletedProjectRecreationPreservesHistoryAndRestoreIsAtomic | project_test.go:289 |
| conversationgroup / TestConcurrentExplicitProjectCreation | project_test.go:386 |

See `baseline-project-verification.md` and `/tmp/knowledge-project-tests-base-81683cfb.log`, `/tmp/knowledge-project-tests-current.log`. The specific reason those workspace paths are rejected remains unestablished. These are confirmed baseline failures on this machine, not inferred merely from unchanged filenames.

## Implementation-related failures repaired during this task

- **Error catalogue:** earlier `TestDirectAPIErrorMessagesAreCatalogued` and `TestCoreErrorConstructorsAreCatalogued` identified newly introduced messages missing aliases. Runtime/original/resource aliases were added. The later missing `knowledge decision history binding changed` alias was also added. `GOTOOLCHAIN=go1.25.11 go test ./common ./knowledgeruntime ./chat` subsequently passed `common` (1.421 s) and `knowledgeruntime` (0.528 s); the Chat package retained the separately confirmed baseline project failures.
- **Migration catalogue:** `TestRepositoryStructuredMigrationCatalogLoads` still expected 110 development migrations while this feature adds two. The exhaustive expectation now includes both new migration versions and a total of 112. The focused migration test passed; `/tmp/kb-core-final-full.log` also records `migrate` passing (45.255 s). See `baseline-project-verification.md` for the correction evidence.
- **MCP progress:** `/tmp/kb-core-final-full.log` captured `TestManagementRegistryAndDispatchGuard` failing with `progress notification missing`. The transport now enables SSE when knowledge management is enabled, preserving actual start/end progress notifications and their token. `/tmp/kb-contract-final.log` records `capability/mcp` PASS (0.965 s), including that transport test; this implementation regression is resolved.

Latest contract verification in `/tmp/kb-contract-final.log` also records `common` PASS (3.304 s), `migrate` PASS (18.332 s), and `knowledgeruntime` PASS (1.902 s). These supersede the repaired errors in the earlier full-suite log; they do not erase the known full-suite failures above.

## Toolchain-only build failure

The initial Go 1.26.1 macOS arm64 run crashed in the Go linker (`cmd/link/internal/arm64.gensymlate`, nil pointer), before running the main Core tests. External linking reproduced that compiler-toolchain crash. With `GOTOOLCHAIN=go1.25.11`, the main Core package builds and its tests run; `/tmp/kb-core-final-full.log` records the main package passing (33.145 s). Do not present this linker crash as an application test regression.

## Scope and cleanup

Only this report was written for the sidecar verification. No source changes, deployed-service operations, runtime data/configuration changes, or commits were performed. The temporary tracked-code baseline archive is removed after verification; the two evidence logs remain under `/tmp`.
