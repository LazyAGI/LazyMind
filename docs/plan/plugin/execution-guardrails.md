# Workflow Agent Kit Execution Guardrails

本文把主计划中的 15 个 PR 限定为可执行单元，不增加 PR 数量。一个 PR 可以包含多个按顺序提交的 commit，但只有满足本文件的入口与退出条件才能合并。

## 1. 全局执行准则

### 必须

1. 每个 PR 开始前确认依赖 PR 已满足退出条件。
2. 新代码只使用 Workflow 命名；旧 `plugin_*` 仅允许出现在 persistence compatibility boundary。
3. 所有公共行为遵守 `contracts/` 下的 v1 契约。
4. 每个迁移 PR 必须包含 feature flag、兼容行为、观测指标和回滚路径。
5. 行为迁移采用 `introduce → shadow → compare → canary → default-on → remove`；可以在同一 PR 内完成相邻阶段，但不得跳过 compare/canary 证据。
6. 新旧路径并存时只能有一个权威写入者；禁止无去重协议的 dual-write。
7. 每个 compatibility adapter 必须记录调用方和调用量，并设置删除 gate。
8. 每个 PR 合并前运行 `make lint`、相关单元测试、contract tests 和列出的 golden scenarios。
9. 数据库变化必须遵守 [`contracts/database-migration-contract.md`](contracts/database-migration-contract.md)：先 expand schema，再部署兼容代码；应用回滚不得依赖 destructive down migration。

### 禁止

- 为赶进度在 Host 内复制 Runtime 状态机。
- 在 compatibility adapter 中新增业务规则。
- 在一次 PR 中同时改变公共语义和删除全部回滚路径。
- 用自然语言日志替代结构化错误码或状态。
- 通过轮询替代 Workflow Event Stream 作为正常 Panel 刷新机制。
- 把 Codex/LazyMind 的完整私有对话或 reasoning 写入 Runtime。

## 2. 第一阶段非目标

- 不重命名既有数据库物理表、列和历史 migration。
- 不接入或同步 Codex 与 LazyMind Chat，不为 Codex 创建 LazyMind Conversation。
- 不为 Codex 提供可编辑 Workflow Panel 或用户手工编辑 Artifact 的入口；Codex Agent 可使用公共 `patch_artifact` 自主修订 Artifact。
- 不让 Runtime 调用模型或选择模型。
- 不为任意第三方 Host 建设通用 marketplace。
- 不保证不同 Host 生成字节级相同的 Artifact，只保证契约与状态语义相同。
- 不在重构期间改变现有 LazyMind 用户行为，除非主计划明确列出并受 feature flag 控制。

## 3. 统一 PR 模板

每个 PR 描述必须包含：

```text
Objective
Dependencies
Contract clauses implemented
In scope / Out of scope
Compatibility path
Feature flag and default
Metrics/logs
Required tests and golden scenarios
Rollback
Old path deletion gate
```

## 4. 十五个 PR 的入口与退出条件

### PR 1：契约与行为基线

入口：当前生产行为可运行。

必须交付：版本化 Tool/Event/Attempt Context schema fixture、`advance_step` 与 `advance_step_and_hand_off` 的共同 transition/不同等待语义、错误码表、串行/并行/choice/retry/rewind/partial retry/stop/resume golden fixtures，以及旧行为清单。

退出：Go、Python 可读取同一 fixture；fixture 能描述当前 projection、Attempt、Artifact revision 和事件序列。不得改生产链路。

### PR 2：Workflow 命名与 persistence 映射

入口：PR 1 fixtures 固定。

必须交付：Go/Python/前端公共领域类型和配置命名迁移；旧数据库 column mapping；Host 中立 Session 引用的 expand migration；schema capability 检测；旧 API 薄适配；公共 payload 残留扫描。

退出：非白名单代码和公共 schema 不含旧 Plugin 领域名；旧数据库 fixture 可完整读写；旧 binary 在 expand 后 schema 上通过核心读写测试；新 reader 可读取未 backfill 旧行；feature flag 可切回旧路由别名。不得改变状态机语义。

### PR 3：共享 Skill 与 Host Profile

入口：公共领域名稳定。

必须交付：Skill policy pack、default/lazymind/codex profile、两个 advance 工具的选择准则、旧 prompt 规则映射表和 shadow decision trace。LazyMind Profile 可选择 handoff，Codex Profile 只选择同步工具。

退出：golden decision cases 中 shadow 结果达到事先记录的等价要求；生产仍以旧决策路径为权威。

### PR 4：Core Workflow Tool facade

入口：Tool Contract schema 固定。

必须交付：公共 handler/facade、`workflow_preparations`、持久 `workflow_events`、Workflow Stream backend、权限与版本校验、结构化错误、command 幂等；内部继续调用既有 Runtime 实现。

退出：Go handler 通过全部 Tool Contract fixtures；preparation 可幂等消费；snapshot + event replay 可重建 projection；新旧入口对 golden fixtures 产生相同领域结果。

### PR 5：algorithm/chat Workflow Client

入口：Facade 可用。

必须交付：统一 Python Client、类型化请求响应、错误映射、超时/重试策略、LazyMind Host File Adapter、`workflow_input_bindings` 和 Input Resource binding；替换分散 HTTP payload 和直接 Workflow DB 查询。

退出：静态扫描确认 workflow adapter 外无直接 Runtime 表查询；LazyMind Attachment 可固定为 Input Resource，来源 Host 临时 URL/路径不进入 Attempt Context；LazyMind 行为 golden tests 通过；可用 flag 切回旧 client。

### PR 6：queued Attempt 与 claim/report

入口：Runtime Contract 的 Attempt 规则已有测试 fixture。

必须交付：`plugin_session_steps` 的兼容 lease/heartbeat/fencing 扩展、queued Attempt、通用 `workflow_outbox`、progress/complete/fail/cancel 和 FakeExecutor。旧 `plugin_run_outbox` 继续服务旧 worker，新旧 claim 域必须隔离。

退出：FakeExecutor 在无 algorithm/chat 时跑通串行、并行、崩溃重领和终态竞争；旧 binary 不读取新 Outbox；关闭新 feature 后旧生产派发可继续，且不需要回滚 schema。

### PR 7：LazyMindExecutor

入口：Attempt 协议稳定。

必须交付：确定性 Executor Supervisor、claim loop、系统 heartbeat、执行器 progress callback、Attempt Context 到现有 AgentRunPlan 的 adapter、结构化 Artifact/终态提交、同步 `advance_step` 与可靠 `advance_step_and_hand_off`、canary flag。

退出：即使模型不调用任何 lifecycle/progress 工具，Supervisor 仍能产生 running、heartbeat 和唯一合法终态；handoff 只有在 durable ownership 成功后发生；canary Workflow 与旧执行器的 golden projection、Artifact lineage 和用户可见事件等价；失败可安全退回旧执行路径。

### PR 8：移除 Core 固定 SubAgent endpoint 依赖

入口：LazyMindExecutor canary 稳定并有观测证据。

必须交付：schema/service capability 均满足后默认启用 queued Attempt，关闭 Core 直接 `/api/subagent/run` 调用；旧 endpoint 和 `plugin_run_outbox` 仅作兼容入口。

退出：关闭 algorithm/chat 时 Attempt 保持 queued/recoverable；恢复服务后可继续；回滚只切派发 flag，不迁移数据。

### PR 9：收敛 algorithm/chat

入口：Executor 已成为默认执行路径，Skill shadow 已验证。

必须交付：公共决策迁入 Skill，Python 仅保留 Host 策略、模型/Agent、交互和事件翻译；启用前后 decision trace 对比。

退出：公共规则只有一份权威来源；关闭 Driver/handoff 不改变 Runtime projection；可按 policy flag 回退。

### PR 10：Skill to Workflow Authoring 解耦

入口：Workflow Tool facade 和 Skill policy 可用。

必须交付：固定 Skill snapshot、draft 文件 API、deterministic diagnostics、发布门禁和无模型 fixture generator。

退出：LazyMind 模型与静态 fixture 走相同校验/发布路径；Authoring Tool 内无隐式模型调用。

### PR 11：LazyMind 全量回归与旧链路清理

入口：PR 1–10 的 default-on 观测窗口完成。

必须交付：全量 golden/contract/UI 回归、Workflow Stream 前端 reducer、对话内与独立 Panel 的同源 projection、调用量报告、旧路径删除清单和第一阶段验收报告。

退出：主计划第一阶段全部十二项验收满足；Panel 正常刷新不轮询、不逐事件重新查询；只删除调用量归零且有回滚替代的旧入口。

### PR 12：Codex 只读 Adapter

入口：第一阶段验收完成。

必须交付：Codex 可调用 discovery/state/artifact read tools，身份映射、Host 中立 Session 引用读取、权限测试、上下文大小限制，以及不含 Chat 和编辑能力的只读 Workflow Status View；状态图必须展示步骤/Attempt 状态并提供 Artifact 下载。

退出：Codex 能发现 Workflow、解释结构、读取 Session/Artifact；Status View 不查询或创建 LazyMind Conversation，不提供 Artifact 编辑控件；此阶段 Agent Adapter 尚不注册写工具，不得创建或修改 Runtime 状态。

### PR 13：Codex 串行 Executor

入口：只读 Adapter 稳定，Attempt/Host Adapter Contract v1 固定，Codex 已满足程序化创建、观察和取消 SubAgent 的 Host 能力门禁。

必须交付：Codex 身份与既有 origin/controller/executor refs 的绑定、prepare/start、只选择同步 `advance_step` 的阶段性 Codex Profile、Codex Executor Supervisor、程序化 SubAgent claim/execute/cancel、系统 heartbeat/progress callback、Input Resource 读取、结构化 Artifact save、终态处理和 Status View 实时状态更新；本 PR 暂不启用 `patch_artifact`，所有新写入受 schema capability 与 canary flag 控制。

退出：关闭 LazyMind 算法服务仍可完成串行 golden Workflow；测试 Agent 即使遗漏所有进度/终态调用，Supervisor 仍可靠完成或失败 Attempt；Codex 工具选择 trace 中不出现 `advance_step_and_hand_off`；Codex Session 使用兼容 `conversation_id=''` 且不被旧对话查询误关联；关闭 Codex feature 后旧 LazyMind 路径继续运行且不回滚 schema；Runtime 不出现 Codex 模型私有配置。

### PR 14：Codex 并行、恢复与模型 Artifact 修订

入口：串行链路稳定并有失败恢复指标。

必须交付：并行 Ready steps、review、partial retry、stop/resume、Codex Profile 启用公共 `patch_artifact`、模型自主修订产生不可变 Artifact revision 并更新 lineage/stale，以及 Status View 并行状态展示和断线恢复；Status View 仍不得提供用户手工编辑控件。

退出：除明确 Host UI 差异外，Codex 通过完整 Runtime golden suite；`patch_artifact` 的授权、revision、lineage、stale 与并发冲突测试，以及终态竞争和权限测试通过；用户手工编辑不会调用 `patch_artifact`。

### PR 15：Codex Skill to Workflow

入口：Authoring Contract 和 Codex 写入权限稳定。

必须交付：固定 Skill package 读取、Codex 生成/修复 draft、diagnostics 循环和 publish。

退出：同一 Skill 可由 LazyMind、Codex 和静态 fixture 生成并发布合法 Workflow；三者走相同 deterministic gate。

## 5. 计划变更规则

- 改变 Runtime invariant、公共工具语义或 Event schema 必须先修改对应 contract，并记录迁移与兼容影响。
- 实现中发现未决问题时，先归类为 Runtime、Tool/Event、Resource 或 Host Policy；不得直接在某个 Host 中形成事实标准。
- 如果某 PR 无法同时满足退出条件，可以延后未完成能力，但不得增加隐式第二实现；主计划状态必须明确标记未完成 gate。
