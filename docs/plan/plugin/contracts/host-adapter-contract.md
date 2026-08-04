# Workflow Host Adapter Contract v1

## 1. 分层

Host 接入固定分为：

```text
Workflow Tool Protocol
→ Host Profile / Tool Registry
→ Executor Supervisor
→ Host Executor Adapter
→ Host SubAgent Runtime
```

- Workflow Tool Protocol 定义工具 schema 和业务语义。
- Host Profile 决定注册的 Agent-facing 工具、等待策略和 capability mapping。
- Executor Supervisor 确定性管理 Attempt 生命周期。
- Host Executor Adapter 将 Attempt Context 转换为 Host RunSpec，并程序化调用 Host SubAgent Runtime。
- Workflow Runtime 不创建具体 Host SubAgent，不读取 Host 模型配置。

## 2. Agent-facing 与 Executor-only 工具

Agent-facing Transition Tools：

```text
get_ready_steps
advance_step
advance_step_and_hand_off
```

Executor-only Tools：

```text
claim_attempt
get_attempt_context
report_attempt_progress
save_artifact
complete_attempt
fail_attempt
cancel_attempt
```

Executor-only Tools 只注册给受认证的 Executor Supervisor，不注册给主 Agent 或 SubAgent。模型不负责 heartbeat、progress、Artifact commit 或终态收敛。

## 3. 两种 advance 等待语义

`advance_step` 与 `advance_step_and_hand_off` 必须复用同一个 Runtime transition handler 和 Executor Supervisor。

### `advance_step`

```text
transition commit
→ durable Attempt
→ Supervisor ownership
→ SubAgent execution
→ Artifact validation/commit
→ terminal commit
→ return terminal result
```

### `advance_step_and_hand_off`

```text
transition commit
→ durable Attempt
→ Supervisor/durable worker ownership
→ Workflow Event Stream available
→ return handoff acknowledgement
```

handoff acknowledgement 返回后，Supervisor 继续执行 SubAgent、提交 Artifact 和写入终态。可靠接管前不得 handoff。

## 4. Executor Supervisor

Supervisor 必须实现：

1. 调用 Runtime transition 创建 queued Attempt。
2. claim Attempt 并持有 lease/fencing generation。
3. 读取固定 Attempt Context。
4. 启动独立 heartbeat timer。
5. 调用 Host Executor Adapter。
6. 从执行器 callback 转发 progress 和流式 Artifact。
7. 持久化结构化 `ExecutionResult.outputs`。
8. 校验 required outputs、cardinality 和 partial selector。
9. 在正常、错误、取消和 panic 路径提交唯一合法终态。
10. 在进程硬崩溃时由 lease reaper 将 Attempt 转为 interrupted/recoverable。

Supervisor 生命周期不得通过 prompt 要求模型补充实现。

## 5. Host Executor Adapter

统一接口：

```text
BuildRunSpec(AttemptContext, ResolvedCapabilities) → HostRunSpec
RunSubAgent(HostRunSpec, ExecutionCallbacks) → ExecutionResult
Cancel(executor_ref)
```

`ExecutionCallbacks` 至少包含：

```text
on_progress
on_artifact
on_log（可选，且不得包含私有 reasoning）
```

`ExecutionResult` 至少包含：

```json
{
  "status": "succeeded",
  "summary": "...",
  "outputs": [],
  "error": null,
  "executor_ref": "..."
}
```

Adapter 必须通过 Host 的程序化执行接口创建 SubAgent；不得要求主 Agent自行调用 `create_subagent` 后再记住回报 Runtime。

## 6. Host Profile

### Codex v1

```yaml
host: codex

workflow_tools:
  enabled:
    - list_workflows
    - get_workflow
    - prepare_workflow
    - start_workflow
    - get_workflow_state
    - get_ready_steps
    - advance_step
    - list_artifacts
    - read_artifact
    - patch_artifact
    - stop_workflow
    - resume_workflow

  disabled:
    - advance_step_and_hand_off

execution:
  executor: codex
  advance_mode: synchronous
```

Codex v1 不选择 `advance_step_and_hand_off`。
Codex v1 注册 `patch_artifact` 供模型自主修订 Artifact，但不在 Status View 中提供用户手工编辑；Codex 不接入 LazyMind Chat，只使用只读 Workflow Status View 和 Artifact 下载。

### LazyMind v1

```yaml
host: lazymind

workflow_tools:
  enabled:
    - list_workflows
    - get_workflow
    - prepare_workflow
    - start_workflow
    - get_workflow_state
    - get_ready_steps
    - advance_step
    - advance_step_and_hand_off
    - list_artifacts
    - read_artifact
    - patch_artifact
    - stop_workflow
    - resume_workflow

execution:
  executor: lazymind
  mode_policy:
    manual: synchronous
    approval: handoff
    auto: handoff
    dynamic: handoff
    driver_retry: handoff
```

Host Profile 可以缩减注册给模型的工具集合，不得改变工具协议语义。

## 7. Capability Mapping

Workflow step 只声明中立 capability：

```yaml
steps:
  - id: research
    capabilities:
      - web_search
      - read_input_resource
      - emit_artifact
```

Host Profile 将 capability 映射为具体实现：

```yaml
executor_capabilities:
  web_search:
    tool: host.web_search
  read_input_resource:
    tool: workflow.read_input_resource
  emit_artifact:
    mode: supervisor_callback
```

规则：

- `prepare_workflow` 校验目标 Host 是否满足所有 required capabilities。
- Runtime 在 Attempt Context 中传递中立 capability，不传递 Host 私有工具配置。
- Host Adapter 只向 SubAgent 注册本 Attempt 允许的具体工具。
- capability 缺失时不得启动 Attempt，返回稳定错误码。
- Artifact 输出优先通过 Supervisor callback 或结构化 `ExecutionResult.outputs` 提交。

## 8. Codex 与 LazyMind Executor

`CodexExecutor`：

- 使用 Codex 程序化 SubAgent 执行接口。
- 同步 `advance_step` 等待 Attempt 终态。
- progress 由 Codex execution callback 转发。
- 不注册 handoff 工具。
- 主 Agent 可以调用 `patch_artifact` 进行模型自主修订。
- 不创建或同步 LazyMind Conversation。
- 结果展示仅使用只读状态图、步骤信息和 Artifact 下载。

`LazyMindExecutor`：

- 将 Attempt Context 转换为 `SubAgentContext`/`AgentRunPlan`。
- 使用 LazyMind SubAgent runner。
- 同时支持同步与 handoff 等待策略。
- Driver、approval、synthetic wakeup 属于 LazyMind Host Policy。

## 9. Codex Workflow Status View

Codex v1 的 Workflow 展示面是只读 Status View：

```text
/workflows/{workflow_session_id}/status
```

必须展示：

- Workflow 名称、revision、总体状态和运行时间；
- 完整状态图、当前可达路径和每个 Step 状态；
- Attempt 次数、运行状态、耗时、失败摘要和 Executor Host；
- Artifact 名称、slot、revision、文件类型、大小和下载链接；
- queued/running/progress/waiting/succeeded/failed/interrupted 的实时更新。

数据来源仅为 `get_workflow_state`、Workflow Event Stream、`list_artifacts` 和 Artifact download endpoint。Status View 不包含 Chat、消息输入框、附件上传、用户手工编辑 Artifact、审批、handoff、retry/rewind 或其他 Runtime mutation 控件。用户需要重新执行或要求模型修订 Artifact 时回到 Codex task，由 Codex Agent 调用 Workflow Tools；其中模型自主修订使用 `patch_artifact`。

## 10. 能力门禁

Host 只有同时满足以下条件才能启用执行：

- 可程序化创建、取消并观察 SubAgent；
- 可从非模型 callback 获取终态；
- Supervisor 可独立维持 heartbeat；
- 可将结构化输出提交到 Artifact writer；
- 可在工具取消、异常和进程崩溃后收敛 Attempt 状态。

不满足门禁的 Host 只能使用只读 Workflow Tools，不得用 prompt 模拟可靠 Executor。

## 11. 验收测试

- 同一 transition fixture 在同步和 handoff 模式产生相同 Attempt/Artifact 领域结果。
- 模型不调用任何 lifecycle tool 时，Supervisor 仍产生 heartbeat 和唯一合法终态。
- handoff 在 durable ownership 之前失败，不结束 turn。
- Codex tool selection trace 不出现 `advance_step_and_hand_off`。
- LazyMind approval/Driver handoff 后可由事件恢复并继续。
- capability allowlist 不向 SubAgent 泄漏未授权 Host 工具。
- required output 缺失时 Attempt 失败，不错误完成。
- Codex Status View 不调用 LazyMind Chat API、不暴露任何修改控件，并能从 snapshot + event stream 恢复状态图；Codex Agent Profile 注册的 `patch_artifact` 不向 Status View 暴露。
- Artifact 下载使用受权 URL/capability，不暴露底层存储路径。
