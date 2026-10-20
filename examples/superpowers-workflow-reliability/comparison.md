# Comparison / 对照

## RC developer trial (2026-10-20)

The RC accepts a caller-declared task. This table adds new live Runtime/Codex runs;
it does not retrospectively change the old native samples or claim SDD parity.

| Issue | Check target | Native Skill | LazyMind RC | Actual evidence | Limit |
| --- | --- | --- | --- | --- | --- |
| 2282 | Brief before launch | Both normal historical samples passed | Real deletion blocked; 0 processes | [missing-current](evidence/rc/missing-current/result.json) | Explicit fault injection, not natural upstream failure |
| 2282 | State and recovery | No in_progress observation in historical ledger | Persisted event/Artifact; UI Retry then CLI resumes same session | [retry](evidence/rc/missing-brief/result.json) | Local executor state; no Codex Todo UI sync; no uncertain-dispatch replay |
| 2245 | Full SHA | Correct base in normal samples | Real new HEAD blocked before launch | [wrong-base](evidence/rc/wrong-base/result.json) | Caller supplies intended SHA |
| 2245 | Caller-defined coverage | Historical three-item fixture only | CSV four IDs and label task three IDs; protected tests, permitted files and extra behavior checks; missing row blocks success | [independent verification](evidence/rc/verification-live.json), [missing row](evidence/rc/missing-coverage/result.json) | Tests and scope are trusted inputs, not universal semantic proof |
| Both | First / repeated use | Not evaluated as an installer | One new publication, compatible revision reused by eight sessions | [lifecycle](evidence/rc/README.md) | Existing Runtime/Codex prerequisites; legacy Core duplicate blank-publish defect remains |
| 2245 | Model / arbitrary-process isolation | N/A | N/A | Requested model and worktree/index receipts only | No new attestation or containment claim |

Six actual children ran in this RC trial; five sessions completed and three ended
in expected gates. The original pre-fix Retry API call returned 403; the trace retains
that failure, followed by the supported UI-authorized recovery. [Source versions,
acceptance scope and readiness](../../RELEASE_CANDIDATE.md). Usage is available per
child in raw receipts; no cross-method speed, reliability or cost percentage is valid.

## Historical fixed-fixture comparison

The historical set includes two small normal native samples, two normal Workflow runs (v1 then v2),
labeled fault injection, one same-session UI recovery, and two concurrent Workflow
executions in disposable linked worktrees. It is not a failure-rate
or cost benchmark. All coding runs use the same fixture base, task and requested
gpt-6-astra model on Codex CLI 0.155.0-alpha.9. Backend model identity is unavailable.

| Issue | Check | Native Skill | LazyMind Workflow | Actual evidence | Limits |
| --- | --- | --- | --- | --- | --- |
| 2282 | Nonempty brief before dispatch | Audited script completion precedes actual spawn receipt; failure not reproduced | Missing/blank file blocks launch | [native tools](evidence/native-audited/native/tool-trace.jsonl), [missing](evidence/missing-brief/run/result.json), [blank](evidence/empty-brief/run/result.json) | Initial CLI summary omits spawn; audited raw tool calls retained with encrypted messages omitted |
| 2282 | Observable task state | Ledger pending → completed; no in_progress observation | Persisted in_progress before launch; completed after verification | [final trace](evidence/workflow-final/trace.jsonl), [missing Todo](evidence/missing-todo/run/result.json), [stale Todo](evidence/stale-todo/run/result.json) | Executor Todo, not Codex UI synchronization |
| 2282 | Failed dispatch cannot succeed | Normal run only | Injected exit 23 fails step; no result Artifact | [failure](evidence/dispatch-failure/run/result.json) | Failure callback is injection, not an actual provider outage |
| 2282 | Recovery | Not tested | Same session, failed attempt 1 then successful attempt 2 | [recovery](evidence/retry-missing-brief/run/result.json) | Official UI Retry required; uncertain launches are not replayed |
| 2245 | Actual base SHA | Correct base observed | Real wrong HEAD detected; launch count 0 | [wrong HEAD](evidence/wrong-base-actual/run/result.json) | Expected SHA must come from a trusted task specification |
| 2245 | Enumerable coverage | Three rows and tests pass | Missing row blocks completion; fixed tests and nine extra checks | [coverage](evidence/missing-coverage/run/result.json), [final](evidence/workflow-final/result.json) | Finite fixture, not universal semantic proof |
| 2245 | Actual model | N/A | N/A | Requested model in execution receipt | No identity hard gate |
| 2245 | Parallel workspace/index | Not tested | Two real concurrent runs; unique worktrees, branches, indexes, sessions; parent unchanged | [parallel E2E](evidence/parallel-workflows/result.json) | Cooperating processes in a disposable repository; no arbitrary-process containment proof |
| Both | Runtime admission/output | Not applicable | Early dispatch and missing required output rejected | [API probes](evidence/runtime-probes/result.json) | Real API tests, zero model calls |

## Metrics

CLI-reported usage is retained, including cache counts. It is not billing data.

| Run | Input tokens | Cached input tokens | Output tokens | Measured elapsed / scope |
| --- | ---: | ---: | ---: | --- |
| Native controller turn | 806945 | 769024 | 5014 | N/A; launch clock not instrumented in initial run |
| Audited native controller turn | 709293 | 660992 | 3786 | 208.402 s, whole controller run including waits |
| Final Workflow child | 92354 | 83712 | 1395 | 50.210 s |
| Parallel Workflow child 1 | 134088 | 109568 | 1343 | 57.551 s |
| Parallel Workflow child 2 | 157110 | 132352 | 1357 | 67.934 s |

Native includes two reviewer dispatches and a controller model; the Workflow uses
deterministic control/validation and one implementer. The figures have different
coverage and cannot support an efficiency percentage. Price, backend-model
attestation and hidden child-only token totals: N/A. Workflow phase timestamps
and HTTP start/end times are in its JSONL trace. The audited native tool trace
provides per-call timestamps; the initial native stream does not. Native usage
comes from the controller turn receipt; complete child-only usage is unavailable.

The two additional children overlapped for 57.548 s, derived from actual start/end
receipts. Both completed all six steps. The parent main HEAD, files and Git Index
hash remained unchanged. This measures concurrency, not a speedup against native.

中文结论：可以交付一个可复跑的受控派发案例，证明具体故障被确定性检查阻断。
不能据此声称原生 Skill 必然失败、已经全面解决两个 Issue，或给出速度/成本提升
百分比。#2245 的模型身份和一般并行隔离保留为未完成验证。
