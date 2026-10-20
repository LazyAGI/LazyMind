# Issue 2245: base, coverage, model and isolation

[Upstream issue](https://github.com/obra/superpowers/issues/2245) distinguishes
actual worktree base, launched model identity, enumerable scope, and shared-index
hazards. The pinned worktree skill detects existing isolation and tests the
baseline, but does not enforce comparison with the intended full base SHA.

## Observed and implemented

The approved fixture starts at
`332bf19bb249ccdee2c9b0cb96c1a85113b4b0dd`. All native and normal Workflow runs started there.
Neither normal native sample reproduced the wrong-base failure; both produced
all three coverage rows.

The executor uses `git rev-parse HEAD` on the actual worktree before dispatch,
compares it with the captured full SHA, and records absolute Git directory/index
identity. The final fault case creates a real empty commit in the disposable
worktree while retaining the original expected SHA: BASE_MISMATCH, zero child
launches. [Actual wrong-head evidence](../evidence/wrong-base-actual/run/result.json).
An earlier synthetic case changed the expected SHA instead; it is preserved
separately under evidence/wrong-base and is not the actual wrong-head test.

Coverage requires exactly spaces, punctuation, empty, with unique DONE entries
and evidence text. SKIPPED can describe partial work but cannot satisfy complete
success. Three immutable tests plus nine independent acceptance checks verify
behavior. A report's existence is insufficient. Removing one row after a real
child completed produced COVERAGE_MISMATCH and prevented final success.
[Coverage failure](../evidence/missing-coverage/run/result.json);
[independent native checks](../evidence/native-independent-checks.json).

## Real bounded parallel execution

[parallel.py](../parallel.py) creates one new disposable repository and two linked
worktrees, both at the fixed base. Before starting either Workflow it checks the
actual worktree, Git directory and Index paths for collisions. Each worker runs
the same six-step package, with its own state directory and Runtime session.

The two real Codex children overlapped **57.548 seconds**. Both completed all six
steps, committed their own slug.py implementation, and passed the fixed and
independent checks. The parent main HEAD, file hash and Git Index hash remained
unchanged. Exact identities, times, usage, result states and Artifacts are in the
[parallel result](../evidence/parallel-workflows/result.json) and per-worker traces.
No business repository or shared Index was used for the experiment.

## Limits

- Model: all coding runs request gpt-6-astra. The API does not attest the backend child
  model; actual identity is N/A. No model-identity hard gate is claimed.
- Isolation: tests create real linked worktrees and verify distinct worktree,
  git_dir and index paths; duplicate paths fail. Shared common Git metadata remains.
  The additional real experiment used two concurrent workers with distinct indexes.
  No dangerous shared-index write race or hostile-process test was run.
- The lock coordinates cooperating executor instances. It does not contain arbitrary
  shell access or prevent another process from changing files after inspection.
- Coverage is finite fixture acceptance, not proof of general program correctness.
- Native SDD's reviewer count differs from the minimal Workflow; cost comparisons
  would confound orchestration and verification effort.

中文结论：真实 HEAD 不匹配与 Coverage 缺项均已验证。补充实验让两个真实 Agent
在独立 worktree/Index 中并行写入并提交，两会话均完成，父仓库未改变。该有界实验
不等于任意进程隔离或服务端模型身份认证。两个 Issue 继续复用同一六步骤 Workflow。
