# Issue 2282: brief and task-state prerequisites

## Reusable RC

The [CLI](../README.md) now binds the brief to a caller's committed Plan/task and
immutable declaration. [Fresh missing-brief injection](../evidence/rc/missing-current/result.json)
blocked before Popen. A separate failed session was restored, retried through the
logged-in Workflow UI and completed through CLI `--resume`; its failed attempt is
retained in [Core history](../evidence/rc/missing-brief/result.json).
An attempted external API Retry returned 403 and was fixed by respecting UI authority;
[raw trace](../evidence/rc/missing-brief/trace.jsonl) retains that failure.
The later sections preserve the original fixed-fixture/native observations.

## Historical evidence

[Upstream issue](https://github.com/obra/superpowers/issues/2282) reports omitted
brief generation and Todo transitions. The retrieved September 27 comment also
notes the implementer template's missing placeholder explanation/fail-fast.
The pinned v7.0.0 script checks that extracted text is nonempty **when invoked**;
it cannot force the controller to invoke it. Its current implementation writes
directly to the output and then checks size. PR 2014 discusses stronger script-side
protection; that is separate from dispatch enforcement. PR 2175 was unavailable.

## Native samples

Two real Codex CLI runs used unmodified SDD. Both completed the task/reviews and
passed the three original tests plus nine independent checks. Neither reproduced
a missing-brief failure. Both ledgers contain pending and completed state, without
an explicit in_progress Todo transition. This is an observed omission in the
recorded workflow, not proof that every hidden Host event was absent.

The initial [CLI trace](../evidence/native-trace.jsonl) omits spawn calls and retains
collaboration waits. Its [report](../evidence/native-worktree/.eval/native-report.md)
is treated as a report. A second run persisted its own controller session and
extracted only actual tool calls/results:
[tool trace](../evidence/native-audited/native/tool-trace.jsonl),
[launch receipt](../evidence/native-audited/native/launch.json),
[actual code diff](../evidence/native-audited/native/final.patch),
[independent checks](../evidence/native-audited/native/independent-checks.json).

| Audited event | Actual UTC time on 2026-10-19 | Observation |
| --- | --- | --- |
| task-brief command/result | 16:34:20.081 → 16:34:20.229 | Exit 0; brief written |
| implement_slug spawn/receipt | 16:34:42.789 → 16:34:42.916 | Real /root/implement_slug task |
| review_slug spawn/receipt | 16:36:02.297 → 16:36:02.386 | Real task reviewer |
| final_review_slug spawn/receipt | 16:36:38.359 → 16:36:38.463 | Real final reviewer |

These are timestamped tool receipts, not reconstructed from the agent's narrative.
The backend encrypts spawn message bodies; the export replaces opaque ciphertext
with a hash and does not decrypt it. It excludes reasoning, messages and unrelated
sessions. Child-internal RED test execution remains implementer-reported; final
GREEN behavior was independently verified. Backend model identity remains N/A.

## Workflow and fault injection

Prepare runs the actual upstream script and persists an in_progress Todo event.
Validate reads brief bytes, Todo/event equality and actual Git HEAD. Dispatch
rechecks the same data inside a cooperating worktree lock before launching Codex.
Completion requires a real terminal CLI event, successful exit, exact coverage,
unchanged fixed tests and independent acceptance checks.

| Case | Actual result | Child launch attempts | Evidence |
| --- | --- | ---: | --- |
| Normal | Core session completed; six selected Artifacts | 1 real process | [final run](../evidence/workflow-final/result.json) |
| Missing brief (injected deletion) | BRIEF_MISSING; downstream unexecuted | 0 | [result](../evidence/missing-brief/run/result.json) |
| Blank brief (injected) | BRIEF_EMPTY; downstream unexecuted | 0 | [result](../evidence/empty-brief/run/result.json) |
| Missing Todo (injected deletion) | TODO_MISSING; downstream unexecuted | 0 | [result](../evidence/missing-todo/run/result.json) |
| Stale Todo (injected completed state) | TODO_NOT_IN_PROGRESS; downstream unexecuted | 0 | [result](../evidence/stale-todo/run/result.json) |
| Failed dispatch (injected callback exit 23) | DISPATCH_FAILED; no success Artifact | 1 callback, 0 model processes | [result](../evidence/dispatch-failure/run/result.json) |
| Retry after restoring brief | Same session completed after UI Retry | 1 real process after recovery | [history](../evidence/retry-missing-brief/run/result.json) |

Automatic begin after failure returned HTTP 409 recovery_required. The official
Workflow page's Retry created validation attempt 2; the executor claimed it and
continued. Attempt 1 remains failed/stale. This is a real recovery observation,
not a simulated state transition. Cross-process recovery after uncertain child
launch remains unsupported.

The hard gate belongs to this executor's launch boundary. Todo is an observable
executor event plus Artifact, not synchronization with Codex's todo UI.

中文结论：两个正常原生样本均未复现 Brief 缺失；补跑保留了真实 spawn 调用与回执，
确认 Brief 先于派发完成。未观察到显式 in_progress Todo 转换，实际子模型身份仍未知。
Workflow 的缺失/空 Brief 故障在真实 Runtime 中阻断，下游启动为 0。
派发失败不产生最终成功产物；修复后通过正式页面“重试”完成同一会话。
