# Superpowers Workflow reliability report / 交付报告

> Historical experimental snapshot. For the reusable RC and new acceptance results, see [RELEASE_CANDIDATE.md](RELEASE_CANDIDATE.md).

原始交付包含可运行代码、27 个回归测试、真实 Runtime/Codex 执行、失败注入证据和中英文
案例文档。#2282 的受控派发链路已完成真实验证；补跑原生实验记录了实际 spawn 回执。
#2245 的 Base/Coverage 和双 worktree 真实并行已验证；实际模型身份及任意进程隔离仍无认证保证。

本报告保留原始交付记录；独立复核、最小修正及最新 28 项测试结果见
[RELEASE_AUDIT.md](RELEASE_AUDIT.md)。早期 revision-1 正常/恢复只记录 3 个固定测试，
9 项独立验收适用于后续 revision-2 正常/并行运行。

## Source baseline

| Item | Pinned value |
| --- | --- |
| LazyMind origin/main at synchronization | 17b5e716cffe754f95eac28f2626971a0fc095f4 |
| Development branch | feat/superpowers-workflow-eval |
| Superpowers | v7.0.0, bb92a77741419a4ab5f06e711a283343f1ada0c3 |
| LazyLLM submodule (unchanged) | f225093714fdfc679ef9e77a64c722c9a19b1379 |
| Isolated task fixture | 332bf19bb249ccdee2c9b0cb96c1a85113b4b0dd |
| Coding Host | Codex CLI 0.155.0-alpha.9 |
| Requested coding model / reasoning | gpt-6-astra / medium |
| Actual backend child model | N/A — not independently attested |

The initial directory had no source. The repository was cloned, main fetched and
fast-forwarded, and a managed isolated worktree created from origin/main. No
reset-hard, force-clean, push, PR, merge or upstream comment was performed.
The original checkout remains separate from this development worktree.

## Model and local environment setup

The current default conversation model is **deepseek-flash**. A new UI conversation
used that default and returned FLASH_READY; its public history records the model
route and measured usage. The earlier deepseek-v4-flash test is retained separately.
SiliconFlow BAAI/bge-m3 is now selected and available. The user completed provider
verification; public group/readiness APIs independently confirm both providers as
verified and both selected roles ready. No secret was read or exported in this
continuation, and no new direct embedding request is claimed.

The earlier HTTP 401 is preserved as historical evidence in model-setup.json;
it no longer describes the current saved configuration. See
[readiness](examples/superpowers-workflow-reliability/evidence/model-readiness.json)
and [Flash conversation](examples/superpowers-workflow-reliability/evidence/flash-chat-verification.json).
Coding comparisons continue to use the pinned gpt-6-astra Host/model, so application
model changes do not silently alter the original controlled comparison.

The local instance is ready with all 15 services running and frontend/Core/auth/
Chat health checks passing. An editor process occupied the original Python service
ports; only this isolated instance was moved to free ports. Other applications
were not terminated. Local-only PyArrow 20.0.0 resolved the NumPy 1.26.4 import
conflict; no unrelated dependency changes were committed.

Bootstrap checks from the earlier setup phase: runtime-manager 220 PASS / 3 SKIP
and graph engine 84 PASS. These are environment/unit checks, not Agent E2E.
Ignored detailed setup logs remain under local/build/setup-evidence.

## Changed files

- DESIGN.md and this REPORT.md.
- workflows/superpowers-reliability/workflow.yaml and scenario/{state.yml,scenario.md}.
- examples/superpowers-workflow-reliability/{gates.py,runtime.py,run.py}: guarded
  external execution through public authoring, preparation, execution, artifact
  and completion APIs.
- fixtures.py, fixture/, native.py, probe_runtime.py, test_gates.py,
  test_trace_export.py, parallel.py, test_parallel.py, export_tool_trace.py,
  export_evidence.py, verification.json: reproducible inputs, native runner, real API probes,
  regression tests and evidence export.
- README.md, README.CN.md, cases/issue-2282.md, cases/issue-2245.md,
  comparison.md, model-setup.json and UPSTREAM-LICENSE within the example.
- evidence/: 89 allowlisted trace/result/report files plus their manifest.
- examples/README.md and examples/README.CN.md indexes.

No Runtime, integration adapter, business logic, or submodule implementation changed.
The package was manually authored after reading the pinned Skill and published
through the Agent Kit's deterministic API as a blank-source draft. It is not
presented as automatic Skill conversion or a server-linked Skill snapshot.

## Actual results

| Validation | Outcome | Evidence |
| --- | --- | --- |
| Native SDD normal fixture | PASS: implementation, 3 fixed tests and 9 independent checks | [native trace](examples/superpowers-workflow-reliability/evidence/native-trace.jsonl), [checks](examples/superpowers-workflow-reliability/evidence/native-independent-checks.json) |
| Native missing-brief/wrong-base failure reproduction | NOT REPRODUCED in this normal sample | Same trace; no historical-version result substituted |
| Native audited follow-up | PASS: 3 actual spawn calls/receipts, brief precedes dispatch, 3 + 9 checks pass | [tool events](examples/superpowers-workflow-reliability/evidence/native-audited/native/tool-trace.jsonl), [checks](examples/superpowers-workflow-reliability/evidence/native-audited/native/independent-checks.json) |
| Actual child model / encrypted prompt content | BLOCKED: no backend attestation; opaque messages omitted | Requested model and spawn receipts do not prove backend identity |
| Final Workflow real E2E | PASS: six steps, six Artifacts, real Codex child, terminal completed | [result](examples/superpowers-workflow-reliability/evidence/workflow-final/result.json) |
| Missing brief | PASS as negative test: task FAIL BRIEF_MISSING, launches 0 | [result](examples/superpowers-workflow-reliability/evidence/missing-brief/run/result.json) |
| Empty brief | PASS as negative test: task FAIL BRIEF_EMPTY, launches 0 | [result](examples/superpowers-workflow-reliability/evidence/empty-brief/run/result.json) |
| Actual wrong HEAD | PASS as negative test: task FAIL BASE_MISMATCH, launches 0 | [result](examples/superpowers-workflow-reliability/evidence/wrong-base-actual/run/result.json) |
| Coverage row removed after real child | PASS as negative test: task FAIL COVERAGE_MISMATCH, no final success | [result](examples/superpowers-workflow-reliability/evidence/missing-coverage/run/result.json) |
| Injected dispatch callback exit 23 | PASS as negative test: task FAIL DISPATCH_FAILED | [result](examples/superpowers-workflow-reliability/evidence/dispatch-failure/run/result.json) |
| Same-session recovery | PASS after official UI Retry; failed attempt retained | [result](examples/superpowers-workflow-reliability/evidence/retry-missing-brief/run/result.json) |
| Runtime early dispatch / missing required output | PASS: both actual API requests rejected | [probes](examples/superpowers-workflow-reliability/evidence/runtime-probes/result.json) |
| Missing / stale Todo | PASS as negative tests: both blocked, launches 0 | [missing](examples/superpowers-workflow-reliability/evidence/missing-todo/run/result.json), [stale](examples/superpowers-workflow-reliability/evidence/stale-todo/run/result.json) |
| Regression tests | PASS: 27, including parallel false-positive prevention | [latest log](examples/superpowers-workflow-reliability/evidence/unit-tests-continued.log); original 22-test log retained |
| Final package diagnostics | PASS, no warnings | [diagnostics](examples/superpowers-workflow-reliability/evidence/final-publication/diagnostics.json) |
| Two simultaneous Workflow/Codex executions | PASS: 57.548 s overlap, separate worktrees/branches/indexes/sessions, parent unchanged | [parallel evidence](examples/superpowers-workflow-reliability/evidence/parallel-workflows/result.json) |
| General process containment / desktop queue wakeup | NOT PROVEN / NOT RUN | Bounded external-executor scope; no blanket isolation or desktop queue claim |

Final published revision is 1410bea3-ed40-4af0-909f-72dc084695dc (revision 2).
The final normal execution pinned that revision. The earlier revision-1 normal
run and failed automatic-retry attempt are preserved rather than discarded.
The normal workflow child ran from 2026-10-19T16:22:56.869857Z to
16:23:47.079776Z (50.210 s). Full Workflow stage and HTTP timings are in JSONL.
The audited native controller ran from 16:34:03.532156Z to 16:37:31.935382Z
(208.402 s); its extracted tool events carry actual timestamps. The initial
native stream has no per-tool clock. Complete child-only token totals remain N/A.

The latest Runtime requires an explicit UI Retry after failure: automatic begin
returned 409 recovery_required. The final executor claims the UI-created attempt,
without impersonating a browser decision or editing state tables.

## Hard gates and conventions

Hard checks in this external executor: actual brief bytes/nonemptiness and hash
consistency; observed in_progress event; actual HEAD against intended SHA; clean
tracked baseline; cooperating launch lock; successful child exit plus terminal
event; exact unique coverage IDs; unchanged tests and independent behavior checks.
The parallel supervisor also rejects duplicate actual worktree/index/Git-directory
paths before launch. Its post-run checks require two distinct completed sessions,
actual time overlap and unchanged parent worktree/index.
Core additionally enforces graph admission, execution handles, required output
publication and terminal state.

Prompt conventions: child editing scope, no extra subagents, desired model
inheritance. The CLI sandbox and a cooperating lock do not prove arbitrary-process
containment. Actual model identity is unavailable. Distinct worktree/index paths
were checked in two concurrent real executions; arbitrary-process containment is
not proven. Both workers committed only in their disposable evaluation branches.

Both issues share one six-step package and launch boundary. #2282's observable Todo
is executor state/Artifact, not synchronization with Codex's Todo UI. Merely loading
the YAML in another Host does not install the executor's hard checks.

## Review and release recommendation

The scope is a reproducible experimental example. Both native runs passed; neither
records an explicit in_progress Todo transition. The initial JSON stream omits spawn
calls; the follow-up preserves actual calls/receipts from its own persisted session.
The exporter excludes messages, reasoning and unrelated sessions; encrypted tool
message bodies are replaced by hashes, without attempting decryption.
Fault injection is separately labeled. Native uses two reviewers while the Workflow
uses deterministic validation, so token/time totals do not justify improvement
percentages or comparative cost claims.

Suitable for community review **as an experimental example with these limits**,
not as proof of a universal upstream fix. Remaining evidence boundaries: backend
model attestation, full child-internal telemetry, broader repeated samples, desktop
queue integration and general process containment. The model-configuration blocker
is resolved; the bounded parallel experiment is now complete.

No publication to GitHub has been performed. User review remains the next external
delivery decision. See the [Chinese case](examples/superpowers-workflow-reliability/README.CN.md)
and [comparison](examples/superpowers-workflow-reliability/comparison.md).
