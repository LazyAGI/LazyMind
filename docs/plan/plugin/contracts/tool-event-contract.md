# Workflow Tool and Event Contract v1

本文固定第一版公共工具与 Workflow Event Stream 的协议准则。具体 JSON Schema 可以在实现 PR 中从本文生成或补充，但不得改变这里的语义；语义变化必须提升 contract version。

## 1. 公共工具分组

### Discovery

```text
list_workflows
get_workflow
```

### Session lifecycle

```text
prepare_workflow
start_workflow
get_workflow_state
stop_workflow
resume_workflow
```

### Transition

```text
get_ready_steps
advance_step
advance_step_and_hand_off
```

不得公开 `retry_step` 或 `rewind_step`；其内部语义见 Runtime Contract。

`advance_step_and_hand_off` 属于共享 Workflow Tool Protocol，但是否选择由 Host Profile 决定。它必须复用 `advance_step` 的 Runtime command handler、状态校验和 Executor Supervisor，不得形成第二套 transition。LazyMind Profile 在审批、Driver 和异步推进场景选择它；Codex Profile 明确只选择 `advance_step`。Adapter 可以按 Profile 缩减实际注册给模型的工具集合，但不能改变协议语义。

### Attempt execution（Executor-only）

```text
claim_attempt
get_attempt_context
report_attempt_progress
save_artifact
complete_attempt
fail_attempt
cancel_attempt
```

这些操作属于同一公共协议，但只供经过认证的 Executor Supervisor 使用，不注册为模型可自主选择的工具。模型遗漏、拒绝或重复生成任何 lifecycle 指令都不能影响 Supervisor 的可靠状态收敛。

### Artifact

```text
list_artifacts
read_artifact
patch_artifact
```

`save_artifact` 是 Executor-only 输出提交操作；`patch_artifact` 是 Agent/模型自主修订既有 Artifact 的公共工具。用户在 LazyMind Panel 中手工编辑不调用 `patch_artifact`，而由产品侧 human revision 接口记录 `change_source=human`。三种写入都产生不可变 revision，并使用统一的 lineage/stale 规则。

### Workflow authoring

```text
get_skill_conversion_context
create_workflow_draft
update_workflow_draft_file
validate_workflow_draft
get_workflow_diagnostics
publish_workflow
```

## 2. 通用 Tool Envelope

所有公共工具响应必须包含：

```json
{
  "contract_version": "workflow.v1",
  "request_id": "req_123",
  "ok": true,
  "data": {}
}
```

失败响应必须使用结构化 `error`。会改变状态的工具还必须接收 `command_id`；已存在 Session 上的写操作必须接收 `expected_state_version`。工具不得返回 Host 私有模型配置或本地路径。

## 3. 启动协议

### `prepare_workflow`

职责：解析并固定 Workflow revision，校验访问权限、Host capability 和必要输入，生成短期 preparation。它不得创建 Session、Attempt 或启动执行。

最低响应字段：

```json
{
  "preparation_id": "prep_123",
  "status": "ready",
  "workflow_ref": "writer-workflow",
  "workflow_revision": "rev_3",
  "missing_inputs": [],
  "warnings": [],
  "expires_at": "..."
}
```

Preparation 必须绑定 actor、revision、规范化 input snapshot 和 capability evaluation；过期或上下文变化后不得启动。

### `start_workflow`

职责：从有效 preparation 幂等创建 Session、初始 projection 和启动 Outbox event。

最低响应字段：

```json
{
  "workflow_session_id": "ws_123",
  "status": "active",
  "state_version": 1,
  "ready_steps": ["prepare"],
  "event_stream_url": "/api/workflows/ws_123/stream",
  "status_url": "/workflows/ws_123/status"
}
```

## 4. Advance 工具契约

请求必须包含目标 step、`command_id` 和 `expected_state_version`；可包含用户对本次执行的 instruction 和 partial selector。

Runtime transition handler 的内部成功结果必须包含：

```json
{
  "accepted": true,
  "resolved_operation": "execute",
  "target_step_id": "outline",
  "attempt_id": "attempt_123",
  "state_version": 25,
  "projection": {}
}
```

该内部结果供 Executor Supervisor 使用，不是同步 `advance_step` 的最终 Agent-facing 响应。Runtime 不得信任客户端传入的内部 operation。兼容接口若仍接受 `retry`/`rewind`，必须转成同一 command handler，并由 Runtime 重新解析和校验。

### 同步执行语义

`advance_step` 是同步 composite tool：先通过 Runtime 短事务创建/claim Attempt，再由确定性 Supervisor 运行 SubAgent、维持 heartbeat、从 callback 转发 progress、保存结构化输出并在 `finally` 写终态。Agent-facing 响应在终态提交后返回，必须包含：

```json
{
  "accepted": true,
  "resolved_operation": "execute",
  "attempt_id": "attempt_123",
  "execution_mode": "synchronous",
  "attempt_status": "succeeded",
  "state_version": 27,
  "projection": {}
}
```

模型不得直接负责 heartbeat、progress 或 terminal command。Supervisor 无法验证 required outputs 时必须 fail Attempt，不能因为模型遗漏工具调用而错误 complete。

### Handoff 语义

`advance_step_and_hand_off` 接收与 `advance_step` 相同的业务目标，但 `execution_mode` 固定为 `handoff`。它只有在以下事实均成立后才能返回 handoff acknowledgement：

1. transition 已持久化并产生 Outbox；
2. Attempt 已创建；
3. Executor Supervisor 已可靠接管，或 durable worker 保证可在进程重启后接管；
4. Workflow Event Stream 已能发布 queued/running 状态。

它不等待 Attempt 终态；终态由 Supervisor 写入，支持 handoff 的 Host 通过事件/Driver/wakeup 继续。接管失败必须返回结构化错误，不得静默结束 turn。Codex v1 Profile 的强制策略是不得选择此工具。

## 5. Event Stream

Workflow Panel 和 Host Adapter 使用独立于 Chat SSE 的 Workflow Stream：

```text
GET /api/workflows/{session_id}/stream
```

正常页面只保持一条长连接，不通过轮询或“每个事件后重新查询”驱动 UI。

### 首包

连接成功后首先发送：

```text
event: workflow.snapshot
id: 108
data: { projection, state_version, cursor }
```

这样可以避免先 GET 再建 SSE 之间的丢事件窗口。也允许提供独立 `get_workflow_state` 作为 Agent 工具、恢复手段和不支持 SSE 的客户端接口。

### 增量事件

持久状态事件必须携带足以直接更新前端 projection 的 payload，不得只发“已更新，请重查”的空通知：

```text
workflow.patch
step.patch
attempt.patch
artifact.upsert
artifact.stale
workflow.waiting
workflow.completed
```

高频非持久进度使用：

```text
attempt.progress
```

progress 可以按 100–250ms 合并，不增加 `state_version`。transition、终态和 Artifact revision 事件必须立即发送且可恢复。

### Cursor 与恢复

- 每个可恢复事件拥有 session 内单调 cursor，并写入 SSE `id`。
- 客户端重连携带 `Last-Event-ID`。
- 服务端能补齐时按 cursor 重放；无法补齐时发送新的 `workflow.snapshot`。
- 客户端发现 `state_version` 跳跃或未知 breaking event 时必须 resync。
- heartbeat 只维持连接，不改变领域状态。

## 6. Chat 与 Workflow 事件边界

Chat token、对话消息、reasoning 展示和 synthetic turn 属于 Chat Event Stream；Workflow 状态、Attempt、Artifact 和 projection 属于 Workflow Event Stream。

LazyMind 组合页面可以通过 gateway 将两种事件复用到一条物理 SSE，但事件来源和 schema 必须保持独立。独立 Workflow 页面和只读 Workflow Status View 只订阅 Workflow Stream。Codex Executor 只写 Runtime，不创建、同步或伪造 LazyMind Chat 事件。

## 7. 版本和兼容

- Tool schema、Event schema 和共享 Skill references 使用同一 major contract version。
- 新增可选字段允许 minor version 演进；删除字段、改变状态语义或错误码必须提升 major version。
- Runtime 对不支持的 major version 返回 `UNSUPPORTED_CONTRACT_VERSION`。
- Compatibility adapter 必须记录调用量和调用方，且不得定义公共协议中不存在的新行为。

## 8. 验收要求

- 每个工具有 request/response/error schema fixture。
- Go handler、Python client 和 MCP adapter 运行同一组 contract fixtures。
- Event reducer 可以从 snapshot 加事件序列重建与 Runtime 相同的 projection。
- 断线重放、cursor 过期 snapshot、progress 合并和未知版本均有测试。
- 公共 payload 不出现 `plugin_*`、模型配置、本地绝对路径和 Host 私有密钥。
