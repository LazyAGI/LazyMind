# 发布前独立审计 / Release audit

> Historical experimental snapshot. For the reusable RC and new acceptance results, see [RELEASE_CANDIDATE.md](RELEASE_CANDIDATE.md).

**结论：GO — 可作为有明确边界的实验性示例提交 LazyMind 独立 PR，并在两个
Superpowers Issue 中交流。** 本结论适用于本例执行器控制的派发链路，不代表完整修复
上游 Issue、生产级隔离或效率优势。重复发布缺陷仍存在，已限制并说明首次安装入口。
没有 push、创建 PR、合并或对外留言。

审计日期：2026-10-20。原始 REPORT 仅作为待核对的声明清单，未作为验证依据。
本轮实际读取原始记录、重新导出本实验的原生工具回执、回读真实 Runtime，并在独立
干净 checkout 中重新执行正常 E2E、BRIEF_MISSING 负例和两个 Runtime API 负例。

## 版本与可复现范围

| 项目 | 核验值 |
| --- | --- |
| 当前分支 | feat/superpowers-workflow-eval |
| LazyMind HEAD / 本地 origin/main | 17b5e716cffe754f95eac28f2626971a0fc095f4 |
| Superpowers | v7.0.0 / bb92a77741419a4ab5f06e711a283343f1ada0c3 |
| 固定 fixture base | 332bf19bb249ccdee2c9b0cb96c1a85113b4b0dd |
| 审计候选本地临时提交 | 143641fec9dc826fd611e0e88f6075bcbd0ae896 |
| 候选 tree | 37992cb5980a44d0bd2212945134efb97d2b0ef7 |
| Runtime 已发布 revision | 1410bea3-ed40-4af0-909f-72dc084695dc（revision 2） |
| 实际环境 | macOS、Python 3.9.6、Git 2.50.1、Codex CLI 0.155.0-alpha.9 |
| 编码模型 | 请求 gpt-6-astra / medium；实际后端身份 N/A |

当前开发分支仍包含未提交改动；不能仅分享该分支的 HEAD 就认为示例已交付。为了不替
用户提交已有工作，审计将 baseline 克隆到新的临时目录，应用候选 diff，并仅在该临时
克隆内提交。候选执行前后均为干净状态。正式分支没有新增提交。

[候选来源](examples/superpowers-workflow-reliability/evidence/release-audit/candidate.json)、
[受测源码逐文件哈希](examples/superpowers-workflow-reliability/evidence/release-audit/tested-sources.json)
证明当前 Python 执行器、测试与 Workflow 文件与受测快照一致。后续改动只涉及审计证据和
文档。服务使用已有的本地 Runtime；本轮没有在空白机器上重新安装全部 LazyMind 依赖。

## 逐项检查

PASS 指检查达到列明的目标；负例的任务结果仍为 FAIL。BLOCKED 表示没有该项可信证据。

| 检查 | 结果 | 独立证据与解释 |
| --- | --- | --- |
| 历史证据完整性 | PASS | [89 份逐文件校验](examples/superpowers-workflow-reliability/evidence/release-audit/original-manifest-check.json)：导出哈希和本地原始哈希全部匹配；原证据未改写 |
| Native 真实派发及顺序 | PASS | [原始工具记录](examples/superpowers-workflow-reliability/evidence/native-audited/native/tool-trace.jsonl) 与指定会话重新导出的 34 条事件逐字节一致；[复核](examples/superpowers-workflow-reliability/evidence/release-audit/native-provenance-check.json)确认 Brief 回执早于 3 次真实 spawn 中的首次派发 |
| Native 实现结果 | PASS | 两份保存的代码补丁分别应用到全新固定 fixture，重新通过 3 个固定测试及 9 项验收：[重放记录](examples/superpowers-workflow-reliability/evidence/release-audit/native-patch-replay.json)。这是确定性补丁复核，不是新 Native Agent E2E |
| 既有 Workflow 状态与 Artifact | PASS | 13 个历史会话加 2 个新会话，通过公开 API 回读后与原始 projection/slots 一致：[逐会话核对](examples/superpowers-workflow-reliability/evidence/release-audit/runtime-claims-check.json) |
| 正常真实 E2E 重跑 | PASS | [新结果](examples/superpowers-workflow-reliability/evidence/release-audit/normal/result.json)、[子进程原始 CLI 流](examples/superpowers-workflow-reliability/evidence/release-audit/normal/child-trace.jsonl)、[独立回读](examples/superpowers-workflow-reliability/evidence/release-audit/normal/readback-control.json)：1 次启动、六步成功、六个选中 Artifact、completed=true、3+9 项测试通过 |
| BRIEF_MISSING 重跑 | PASS（预期任务 FAIL） | [新 trace](examples/superpowers-workflow-reliability/evidence/release-audit/brief-missing/trace.jsonl)、[结果](examples/superpowers-workflow-reliability/evidence/release-audit/brief-missing/result.json)、[独立回读](examples/superpowers-workflow-reliability/evidence/release-audit/brief-missing/readback-control.json)：Validate 失败，Dispatch/Collect/Completion/Record 均为 none，只有 preparation Artifact，启动 0 |
| Core 强制准入和 required output | PASS | [本轮真实 API 探测](examples/superpowers-workflow-reliability/evidence/release-audit/runtime-probes/trace.jsonl)：提前 Dispatch 被 409 STEP_NOT_REACHABLE 拒绝；无 preparation 产物的成功提交被 422 REQUIRED_OUTPUT_MISSING 拒绝；模型调用 0 |
| 状态图和 Artifact Contract | PASS | [已发布内容](examples/superpowers-workflow-reliability/evidence/release-audit/workflow-read.json) 与源码语义一致；[核对结果](examples/superpowers-workflow-reliability/evidence/release-audit/artifact-contract-check.json)：六节点、七边、六个单值 JSON 槽及依赖一致。服务器补充 ui，故 workflow.yaml 字节哈希不同；场景和 state 字节相同 |
| 缺失/空 Brief、实际错误 HEAD、Todo、Coverage、派发失败 | PASS（预期任务 FAIL） | 逐会话核对中的失败码、下游 none 和最终产物缺失符合相应负例；dispatch-failure 是 exit=23 的注入回调，非真实供应商故障 |
| 同会话恢复 | PASS（历史证据复核） | [原始 UI 调用摘录](examples/superpowers-workflow-reliability/evidence/release-audit/recovery-ui-excerpts.json)记录 Retry 点击及 failed→queued；[原始结果](examples/superpowers-workflow-reliability/evidence/retry-missing-brief/run/result.json)保留失败 Attempt 1 和成功 Attempt 2。本轮没有重新执行 UI 恢复 |
| 双 worktree 并行 | PASS（有界历史实验） | [复算](examples/superpowers-workflow-reliability/evidence/release-audit/parallel-claims-check.json)确认重叠 57.548 秒、不同路径/Index、父仓库快照一致；两会话状态和子进程 usage 与原始流相符。没有新跑危险共享 Index 竞争 |
| 性能/可靠性结论 | PASS（声明受限） | [指标复核](examples/superpowers-workflow-reliability/evidence/release-audit/metrics-check.json)及 CLI 回执一致；Native 含控制器和 Reviewer，Workflow 仅一个 Implementer，不能比较成本/速度百分比。两次正常 Native 均未复现缺 Brief/错误 Base |
| Coverage evidence 类型 | FAIL → 已修复 | 修复前 null 被转为字符串后通过：[复现](examples/superpowers-workflow-reliability/evidence/release-audit/coverage-before-fix.json)。现在必须为非空字符串，新增回归覆盖 null/list/dict/number/bool/空白 |
| 回归测试 | PASS | 干净候选运行 [28 项测试](examples/superpowers-workflow-reliability/evidence/release-audit/unit-tests.log)。含真实文件/Git、注入回调和合成观测，不当作 Agent E2E |
| 已安装同名 Workflow 再次空白发布 | FAIL（已说明限制） | [真实 publish 500](examples/superpowers-workflow-reliability/evidence/release-audit/publication/trace.jsonl)：plugin_drafts.created_by/plugin_id 唯一键冲突；没有启动子进程。未扩展 Runtime/发布器；重复执行须复用经核验的已有 ref |
| 干净 checkout 运行示例 | PASS（已有服务与凭据） | 临时干净候选运行测试和新 E2E；复用的 revision 与本包一致。首次发布有原始成功记录，本轮重复发布失败单独保留。README 已区分首次安装与复用 |
| 空白机器安装/其他平台与 CLI | BLOCKED（未验证） | 没有另一台空白机器或其他受测 CLI；现有服务 ready/15 running：[健康记录](examples/superpowers-workflow-reliability/evidence/release-audit/runtime-status.log) |
| 模型身份、任意进程隔离、桌面 queue 唤醒 | BLOCKED（无认证/未测） | 请求模型名、子 Agent 报告、路径不同均不足以证明这些能力 |
| 文档、许可证、敏感信息和 diff | PASS | [最终静态检查](examples/superpowers-workflow-reliability/evidence/release-audit/static-checks.json)、[许可证比对](examples/superpowers-workflow-reliability/evidence/release-audit/license-check.json)。MIT 通知与上游 LICENSE 完全相同；根仓库 Apache-2.0；未包含密钥、执行凭证或私人用户名路径 |
| 独立 PR / 最小侵入 | PASS | 仅示例、Workflow、设计/报告与 examples 索引；无生产 Runtime、服务配置、业务逻辑或子模块源码变更。文件数主要来自原始证据 |

## 新运行的时间与启动证据

时间均为 UTC，2026-10-20；每步开始/完成和 HTTP 开始/完成时间在对应 JSONL。

| 运行 | 实际开始 → 完成 | 实际结果 |
| --- | --- | --- |
| 正常 Workflow，session 82299a57b40d4ee6837ef4c1bd116c89 | 03:08:08.855319 → 03:09:05.794840 | completed，六步骤成功 |
| 正常 Codex 子进程 | 03:08:09.211490 → 03:09:05.478860 | exit 0 + turn.completed，56.268 秒 |
| 缺 Brief，session ac1391a53d2049b69d4d23ae26e4a9bc | 03:09:05.931357 → 03:09:06.101791 | BRIEF_MISSING，failed |

正常子进程报告 input=158179、cached input=133504、output=1209；这些是 CLI usage，
不是账单。负例在 Prepare 完成后明确注入删除 Brief，是真实 Runtime 上的故障注入，
不是自然复现原生缺陷，也不是 Mock E2E。它没有 dispatch_started、child trace 或
child final 文件，Core 的 Dispatch 没有 Attempt。结合受测 `Guard.dispatch` 的唯一启动
入口，支持“本执行器没有启动该子进程”；不将其扩大为全机器进程监控结论。

## 硬约束的实际边界

- [gates.py](examples/superpowers-workflow-reliability/gates.py)：`dispatch` 在本 worktree
  的协作锁内再次核验非空 Brief、内容哈希、Todo 事件和实际 HEAD。验证没通过不会调用
  launch。完成检查要求准确且唯一的 Coverage ID、DONE、非空文本证据、测试文件哈希、
  修改范围及行为测试。文本证据本身不认证测试执行，行为由执行器另行运行的测试验证。
- [runtime.py](examples/superpowers-workflow-reliability/runtime.py)：只能在 Core 接受的
  hosted attempt 内执行；核对 step/executor host，按 StepContract 发布必需产物。
  Core 的 [hosted 完成路径](backend/core/workflow/hosted/service.go)调用
  [ValidateRequiredOutputs](backend/core/workflow/executor/artifact_sink.go)，要求当前
  Attempt 生成有效产物。它验证槽存在，不验证 Brief/Coverage 的业务语义。
- [状态图](workflows/superpowers-reliability/scenario/state.yml)按产物依赖排序；
  acceptance_criteria 是描述，不是 Core 执行的断言。绕过此 Python 执行器、改用其他
  Host，并不会自动获得这些语义硬检查；可写的 Artifact 不等于可信的执行证明。
- expected SHA、输入计划和本机观察者是信任前提。协作锁不能阻止任意进程改文件；
  路径和 Index 唯一性不是 OS 安全隔离。网络中断、超时后的孙进程清理和不确定派发的
  自动恢复未被证明；当前只支持准备成功后校验失败的显式 UI Retry。

## 两个 Issue 的覆盖

| 能力 | 本例覆盖 | Issue 层面的剩余范围 |
| --- | --- | --- |
| #2282 Brief 必须生成且非空 | 受控入口完整验证：正常、缺失、空、变更后失效 | 不是 Codex 全局工具钩子；原生缺失故障未自然复现 |
| #2282 Todo 与派发失败 | 本地事件/Artifact 和失败状态完整验证 | Codex Todo UI 同步未覆盖；真实供应商故障未注入 |
| #2245 实际 Base Commit | 固定 full SHA 与真实 HEAD 比较，错误 HEAD 阻断 | 意图中的 SHA 必须由可信来源提供 |
| #2245 可枚举 Coverage | 三项固定任务 + 3+9 行为检查，缺项阻止完整成功 | 不覆盖任意大任务或 17 个实现家族的原上游场景 |
| #2245 实际模型 | 仅记录请求值 | 后端身份认证尚未覆盖 |
| #2245 并行工作区/Index | 两个真实并行执行、独立路径与 Index、父仓库不变 | 未证明共享 Index 写入策略、任意进程隔离或长期上下文可靠性 |

两 Issue 复用一个六步骤包；整体均为有边界的部分解决方案。只有固定实验中的可观察
前置条件和枚举验收可以称为完整验证。

## 本轮最小修正与交付说明

1. `gates.py`：Coverage evidence 由字符串强转改为非空字符串检查；`test_gates.py`
   增加一个参数化回归测试。没有新增执行能力或 Runtime 改动。
2. 中英文 README：区分首次发布/复用，补充受测环境与服务启动链接；纠正把 revision-1
   历史运行也概括为 3+9 测试的措辞。REPORT 保留原历史测试数并链接本轮审计。
3. 将 examples 索引中的本例归入“可运行实验”；新增本报告、[技术说明草稿](examples/superpowers-workflow-reliability/release-drafts.md)
   及独立审计证据。原始 89 文件 manifest 保持不变；
   [本轮 manifest](examples/superpowers-workflow-reliability/evidence/release-audit/manifest.json)
   分别列出原始/脱敏哈希。

README、comparison、REPORT 的核心结论在上述范围内得到支持。恢复 UI 证据现在直接可查；
没有用模型报告代替工具回执。原始 bootstrap 的 220 PASS/3 SKIP 和 graph 84 PASS 已按
本地历史日志复核；也保留首次环境测试 6 FAIL 的计数，不能当作本轮 E2E：
[历史环境日志核对](examples/superpowers-workflow-reliability/evidence/release-audit/bootstrap-log-check.json)。

建议人工审阅这些限制后提交一个实验示例 PR。对社区只能描述“受控执行器可阻断已注入
故障”，不得声称“原生必然失败”“已完整解决两 Issue”或给出性能/可靠性提升百分比。
首次安装有历史成功证据，但同名更新器问题未修复；完整新机安装和模型认证没有通过记录。
