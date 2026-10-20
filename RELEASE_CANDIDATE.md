# Superpowers Dispatch Reliability — release candidate

Development baseline: `17b5e716cffe754f95eac28f2626971a0fc095f4`.
Latest fetched main: `3486910b9f761ae84f3f98b823bcb2e14d2500d4`.
The audited work was saved in a local commit and rebased without conflicts; no historical evidence was removed.

## Gap list (implementation in progress)

- P0: fixture-only task binding/coverage; add one declared, tracked Plan task in an isolated linked worktree, immutable acceptance inputs and explicit file scope.
- P0: repeat blank publication fails; query enabled and disabled owned workflows, compare package contract, reuse exact compatible revision; never swallow publish errors.
- P0: installation assumes local state/credentials; provide private configuration and explicit local login or supplied bearer file, dependency/CLI/runtime preflight.
- P1: recovery depends on undocumented UI steps; support explicit validation retry through the public control API; retain attempts and prohibit uncertain dispatch replay.
- P1: developer output discoverability and repeatable commands; provide run/status/restore-brief commands and a different CSV task walkthrough.
- P2: backend-model attestation, arbitrary-process isolation, automatic full SDD conversion/review, Windows execution, distributed/crash-safe child supervision remain outside this RC.

Final acceptance and candidate SHA will be recorded after real execution and review.
