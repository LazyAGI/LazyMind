# Workflow Runtime Contract v1

本文定义 Workflow Runtime 第一版必须遵守的领域不变量。文中的“必须”“不得”是代码评审和验收条件，不允许 Host Profile、共享 Skill、兼容层或 Agent prompt 覆盖。

## 1. 决策优先级

发生冲突时按以下优先级处理：

1. Runtime invariant 与权限规则。
2. 版本化 Tool/Event Contract。
3. Host Profile。
4. 共享 Workflow Skill policy。
5. Agent 当前 turn 的判断。

Runtime 拒绝的 command 不得由 Host 通过直接写表、旧接口或本地状态绕过。

## 2. 权威状态与写入边界

1. Workflow Session、Step、Attempt、Input Binding 和 Artifact revision 的持久化状态是唯一权威事实。
2. Projection 是权威事实的确定性投影；Agent 与前端缓存不是第二套状态机。
3. 所有领域状态变化必须经 Runtime command 完成，并在同一事务内校验权限、`command_id`、`expected_state_version` 和前置状态。
4. 成功的领域事务必须在同一事务内写入持久 Workflow Event；需要外部执行或投递时同时写入 Outbox。不得先返回成功再异步补写关键事件或派发事实。
5. Executor 只能通过 Attempt 与 Artifact 协议回报结果，不得直接修改 Session 或 Step 状态。
6. Host 私有模型配置、API key、LazyLLM sid、本地绝对路径和平台私有消息 ID 不得进入公共领域模型；允许以中立 `host_ref` 存储关联引用。

## 3. Command 与并发

所有会改变状态的公共工具必须接收：

```json
{
  "command_id": "cmd_123",
  "expected_state_version": 24
}
```

- `command_id` 在同一 actor 和操作域内唯一。
- 相同 `command_id`、相同规范化请求重复提交时，返回第一次的持久化响应。
- 相同 `command_id`、不同请求必须返回 `COMMAND_ID_REUSED`。
- `expected_state_version` 不匹配时不得产生部分写入，返回 `STATE_VERSION_CONFLICT` 和最新 projection。
- 一次事务成功后 `state_version` 必须严格递增；纯 progress event 不增加领域 `state_version`。

## 4. Session 状态

第一版 Session 状态限定为：

```text
prepared（仅 preparation，不是 Session 状态）
active
waiting
stopping
stopped
completed
failed
```

规则：

- `start_workflow` 只能从有效 preparation 创建一个新 Session。
- `completed` 和 `failed` 是终态事实，但 Runtime 可以通过合法 rewind/retry 重新打开 Session，并记录审计事件。
- `stop_workflow` 首先阻止新 Attempt 派发，再取消或等待运行中 Attempt 到达终态。
- `resume_workflow` 只能恢复 `stopped` 或满足恢复条件的 `waiting` Session。
- Session 状态不得由 Chat turn 是否结束、handoff 或 synthetic turn 决定。

## 5. `advance_step` 解析规则

`advance_step` 是唯一面向 Agent 的步骤执行工具。Runtime 根据目标步骤的最新有效 Attempt 解析内部动作：

| 当前事实 | 是否接受 | `resolved_operation` | 必须执行的行为 |
|---|---:|---|---|
| 无有效 Attempt，且步骤 Ready | 是 | `execute` | 创建新的 queued Attempt |
| 有效 Attempt 为 `failed` | 是 | `retry` | 旧 Attempt stale，创建下一 attempt |
| 有效 Attempt 为 `interrupted` | 是 | `retry` | 旧 Attempt stale，创建下一 attempt |
| 有效 Attempt 为 `succeeded` | 是 | `rewind` | 本步骤及受 lineage 影响的下游 Attempt/Artifact stale，再创建新 Attempt |
| 有效 Attempt 为 `queued`、`claimed` 或 `running` | 否 | — | 返回 `STEP_ALREADY_ACTIVE` |
| 步骤 blocked | 否 | — | 返回 `STEP_NOT_READY` 和缺失依赖 |
| 步骤 unreachable | 否 | — | 返回 `STEP_NOT_REACHABLE` |
| Session stopping/stopped | 否 | — | 返回 `SESSION_NOT_ACTIVE` |

额外规则：

- Agent 不选择 `retry` 或 `rewind`，但工具响应和审计事件必须返回 `resolved_operation`。
- `retry` 只能作用于 failed/interrupted Attempt。
- `rewind` 只能作用于 succeeded Attempt。
- stale 传播必须基于持久化 input binding/lineage，不得仅按图的后继节点粗暴失效。
- partial retry 必须显式携带稳定的 partial selector，并只替换对应 cardinality item。

## 6. Attempt 状态与领取协议

Attempt 状态限定为：

```text
queued → claimed → running → succeeded
                           ↘ failed
                           ↘ interrupted
                           ↘ cancelled
```

领取采用 at-least-once delivery 加排他 lease：

- `claim_attempt` 必须返回不可复用的 `lease_token` 和 `lease_expires_at`。
- heartbeat 成功才能延长 lease；默认间隔与超时写入版本化服务配置，不由 Agent 自行决定。
- 所有 progress、Artifact save 和终态写入必须携带当前 `lease_token`。
- lease 过期后新的 Executor 可重新 claim，并获得更高 fencing generation。
- 旧 generation 的任何写入必须返回 `ATTEMPT_LEASE_LOST`。
- Attempt 终态采用 first valid terminal write wins；重复相同终态幂等，不同终态返回 `ATTEMPT_ALREADY_TERMINAL`。
- `cancel_attempt` 与 `complete_attempt` 竞争时，以持有有效 lease 且最先提交成功的合法终态为准，并写审计事件。

Attempt 生命周期调用属于 Executor Supervisor，不属于模型决策。Supervisor 必须以系统 timer 发送 heartbeat，以执行器 callback/事件桥报告 progress，并用 `defer/finally` 保证正常返回、错误、取消和 panic 均尝试写入一个合法终态。进程硬崩溃时由独立 lease reaper 使 Attempt 进入 interrupted/recoverable；不得依赖模型在下一轮对话中修复 running 状态。

公共 Tool Protocol 提供两种等待策略，但不得改变上述事实：`advance_step` 同步等待终态；`advance_step_and_hand_off` 在 durable dispatch/Supervisor ownership 成功后返回 acknowledgement。两者必须复用同一 Runtime transition，不得形成两套状态机。LazyMind Profile 可选择 handoff，Codex v1 Profile 只选择同步执行。

## 7. Artifact、lineage 与 stale

1. 每个 Workflow 输出 Artifact 必须关联 `workflow_session_id`、slot、revision、producer step 和 producer Attempt。
2. Artifact revision 不可原地覆盖；Agent/模型调用 `patch_artifact` 产生新 revision，并记录 `change_source=agent`。
3. Attempt 完成前可以保存 partial Artifact，但必须标记 `partial`，不得自动成为 selected revision。
4. `complete_attempt` 必须验证 required outputs 已存在且满足 cardinality，再原子选择输出并推进状态。
5. retry/rewind 不删除历史 Artifact，只将受影响 revision 标记 stale/unselected。
6. stale 传播以 Attempt Input Binding 记录的实际 revision witness 为依据。
7. 用户手工修改 Artifact 通过产品侧 human revision 接口产生新 revision，记录 `change_source=human`，不得伪装成 `patch_artifact` 调用，并按依赖关系传播 stale。

## 8. 停止与恢复

- `stop_workflow`、`cancel_attempt` 和 Host 的 stop-turn 是三个独立动作。
- stop-turn 不得隐式宣称 Attempt 或 Session 已停止。
- Runtime 收到 `stop_workflow` 后不得创建或派发新的 Attempt。
- 已保存的 partial Artifact 必须保留。
- 恢复时必须重新计算 projection，不得复用 Host 内存中的 Ready 列表。

## 9. 错误响应最低要求

公共 Runtime 错误至少包含：

```json
{
  "code": "STATE_VERSION_CONFLICT",
  "message": "human-readable summary",
  "retryable": true,
  "details": {},
  "state_version": 25,
  "projection": {}
}
```

错误码必须稳定、可测试；不得要求 Agent 从自然语言判断错误类型。

## 10. 验收要求

Runtime contract tests 必须覆盖：

- command 幂等与 command ID 冲突；
- state version 冲突无部分写入；
- execute/retry/rewind 决策表；
- stale lineage 和 partial retry；
- claim lease、过期重领与 fencing；
- complete/cancel 终态竞争；
- required outputs 原子校验；
- stop/resume 与服务重启恢复；
- Outbox 与领域事务的一致性。
