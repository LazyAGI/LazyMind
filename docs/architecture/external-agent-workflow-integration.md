# 外部 Agent Workflow 交互架构

## 1. 目标

让同一套 LazyMind Workflow 能在多个外部 Agent 中运行，并复用 LazyMind 的 WorkflowPanel。

新增一个外部 Agent 时，应主要实现宿主适配器，不应复制 Workflow 状态机、执行协议或 Panel。

本设计的核心原则：

- Core 管理权威状态。
- Controller 推进流程。
- Executor 执行具体步骤。
- 复用 WorkflowPanel；DSH 用 iframe，Codex 用内置浏览器。
- 支持 Runtime Adapter 的宿主通过 HostAction 唤醒外部 Controller；Codex 的 queue Adapter 可显式启用。

## 2. 本次边界

本次重构外部 Agent 集成逻辑及其 CLI 接入基础设施，不重新设计 Workflow 状态机：

```text
Workflow Core                       保持现有状态机和执行协议
    ↑
External Agent Integration         提取可复用的运行协调逻辑
    ↑
Agent Adapter                      保留宿主专用代码
```

这里的三层是代码职责，不是三跳通信链路。共享集成层是编译进宿主插件的代码库，不新增服务或 MCP 代理：

| 通信路径 | 用途 | 重构影响 |
| --- | --- | --- |
| Agent → MCP → LazyMind | 调用 workflow 工具，读取状态、开始和完成执行 | 保留原协议和通信路径 |
| 宿主插件 → 本地 Bridge → Core | 绑定、获取 HostAction、提交投递回执 | 提取共享逻辑，通过 Adapter 调用宿主 API |
| iframe → Core | 用户审核、Continue、Stop 等操作 | 保留原路径 |
| Codex → 内置浏览器 | 启动后自动打开返回的 Workflow URL | 复用 HTTP 页面，不新增 UI 服务 |

Core 创建 HostAction，共享集成层获取通知，再由宿主 Adapter 唤醒 Agent。DSH 通过 Runtime SDK 接入；Codex 通过桌面自带 CLI 的 queue 投递唤醒；桌面端到端时序仍需验收。

本次不修改：

- Workflow 图与状态机语义；
- `controller_host` / `executor_host` 路由语义；
- `workflow.control.v1`；
- Workflow 审核、版本、幂等和执行隔离；
- `execution_handle` 的授权语义。

本期以“一个宿主会话主要关注一个 Workflow”为使用前提。支持多个宿主分别接入，每个 Workflow Session 仍只有一个绑定的 Controller；不增加多个 Controller 共同推进、跨宿主转移 Controller 或复杂的会话内多任务调度。

保留现有的运行归属检查：停止旧 Workflow 时，不应误取消后来开始的用户任务。这是兼容性保护，不要求为此新建调度系统。

CLI 的 pairing、Bridge 和安装配置也在重构范围内，需要去除 DSH 专用假设。若第二宿主无法满足现有投递协议，应明确降级或另行讨论协议扩展，不能声称仅实现 Adapter 就能获得完整能力。

## 3. 角色

### 3.1 Workflow Core

唯一权威状态源，负责：

- 保存 Workflow Session、Step、Artifact 和 Review；
- 校验 Continue、Retry、Rewind、Stop 等用户指令；
- 决定 Controller 和每一步的 Executor；
- 创建和管理 HostAction；
- 发放、校验和作废 `execution_handle`。

### 3.2 Controller

负责推进 Workflow：

- 调用 `workflow.state` 读取权威状态；
- 依据 Core 返回的状态与 `admission.can_begin`，为可开始的步骤调用 `workflow.step.begin`；
- 对已有 `execution_id` 的通知，调用 `workflow.step.claim` 认领或观察已有执行，不直接重新 begin；
- 在等待用户或等待内部执行时结束当前 turn；
- 被 HostAction 唤醒后继续推进。

Controller 可以是 LazyMind 内部编排逻辑，也可以是 DSH 等外部 Agent。

### 3.3 Executor

负责执行一个具体 StepContract：

- 读取输入；
- 调用工具或模型完成任务；
- 发布 Artifact；
- 提交执行结果。

同一个外部 Agent 可以同时承担 Controller 和 Executor，但两个职责在协议上保持分离。

### 3.4 iframe / WorkflowPanel

负责用户交互：

- 展示 Workflow 状态和产物；
- 编辑、排序、回滚产物；
- 提交 Confirm、Continue、Retry、Rewind、Stop、Resume 等用户意图。

iframe 直接与 Workflow Core 通信，不把按钮转换成自然语言交给 Agent。

## 4. 工程分层

三层分别回答三个问题：

| 层 | 回答的问题 | 例子 |
| --- | --- | --- |
| Workflow Core | Workflow 现在允许做什么？ | 还有待审核产物，不允许开始下一步 |
| External Agent Integration（共享集成层） | 外部 Agent 现在应该如何配合？ | 当前是该 Workflow 的自动推进，应结束本轮运行 |
| Agent Adapter | 在这个宿主里具体怎么做？ | 在 DSH 中调用 `concludeTurn()` |

本文中的“共享集成层”对应代码包 `workflow-agent-core`。它不是第二套 Workflow Core，也不重新推导图、审核规则或执行授权。

### 4.1 共享集成层

宿主无关的共享实现，不依赖 DSH 或其他宿主 SDK：

| 模块 | 职责 |
| --- | --- |
| 协议解析 | 解析标准化后的 MCP 结果，校验 `session_id`、`interaction_url`、control 与可信来源 |
| 绑定与上下文 | 绑定 Workflow 和宿主会话，记录当前运行归属，重启后恢复上下文并向 Core 校验 |
| HostAction dispatcher | 拉取、认领、投递和回执；通过 Adapter 核对不确定投递 |
| 运行协调 | 消费 Core 的 control/admission，决定结束 turn、拒绝自动继续或允许已授权执行收尾 |
| Panel store（可选） | 保存当前展示哪个 Workflow 等 UI 状态，不参与绑定、审核或执行授权 |

运行协调需要保留现有行为：

| 情况 | 共享层的处理 | Adapter 提供的能力 |
| --- | --- | --- |
| 提交产物后等待审核 | 停止该 Workflow 的自动推进 | 结束当前 turn |
| LazyMind 正在执行内部步骤 | 外部 Agent 让出执行，不自行模仿该步骤 | 结束当前 turn |
| `draining`，仍有已授权执行 | 不开始新步骤，允许有效执行按 Core 授权收尾 | 工具执行前钩子和调用上下文 |
| 用户在同一会话发起其他工作 | 区分手动输入与 Workflow 自动推进，不把等待状态变成整个会话禁用 | 输入来源、运行状态与归属信息 |
| 插件或进程重启 | 从历史恢复关联，再向 Core 校验；不能仅凭打开 Panel 或读取历史 Workflow 建立控制权 | 持久化事件读取 |
| 子 Agent 已提交结果 | 保留向父 Agent 返回结果的收尾路径 | 父子关系和宿主结果返回能力 |
| 状态同步失败 | 暂停无法确认授权的自动推进；已提交成功的工具结果不能改报为执行失败 | 保留原始工具结果，执行暂停决定 |

例如：

```text
Core 返回 awaiting_user
→ 共享层确认当前 turn 属于该 Workflow 的自动推进
→ Adapter 结束 turn
→ 用户在 Panel 审核并继续
→ Core 创建 HostAction
→ 共享层通过 Adapter 唤醒 Controller
```

工具执行前的本地检查用于协调宿主行为，Core 仍对实际请求做最终授权校验。

### 4.2 Agent Adapter

Adapter 提供宿主能力，并将宿主事件转换为共享层能够理解的事件。它可以传递 Workflow、execution 和 action 标识，但不自行判断审核是否通过或下一步是否可执行。

从第一阶段就分为 Runtime 和 UI 两个入口：

| 接口方向 | 必须表达的内容 |
| --- | --- |
| Runtime → 共享层：运行开始、工具执行前、工具结果后 | 宿主会话、当前运行标识、输入来源、标准化工具操作与结果 |
| 共享层 → Runtime：发送继续通知 | 目标宿主会话、Workflow ID、action ID、消息；action ID 用作请求关联标识 |
| 共享层 → Runtime：核对投递 | 按 action ID 查询持久化接收证据，区分已接收、未找到、无法判断 |
| 共享层 → Runtime：结束 turn / 取消 | 明确目标运行；取消前核对它仍属于目标 Workflow |
| Runtime → 共享层：恢复上下文 | 历史事件、运行状态及必要的父子 Agent 关系 |
| 共享层 → UI：展示 Panel | 宿主会话、Workflow ID、已校验的 URL |

Runtime Adapter 按宿主实例管理，可处理多个会话，所以运行操作显式携带目标会话；不使用含义不明的全局“当前会话”。接口定义见 `integrations/workflow-agent-core/src/adapter.ts`，由 Fake Adapter 契约测试验证；不能把发送接口的 `Promise<void>` 当作完整投递契约。

发送正常返回表示宿主已接收输入，不能仅表示网络请求已发出。发生异常后，Adapter 提供核对能力，由共享层决定如何向 Core 回执。“未找到”不等于证明未投递，不能据此自动重发 unknown 通知。

DSH 专属实现包括工具名 hash 处理、`tool/result` 和 `tool/ptc-dispatch` 事件解析、生命周期钩子、`sessionController.prompt/cancel`、持久化事件查询、`concludeTurn()` 及 slots 注入。

### 4.3 WorkflowPanel 与运行环境

WorkflowPanel 继续由 LazyMind Web 托管，直接向 Core 提交用户操作，不经过 Agent 解释或重新实现业务逻辑。

- DSH UI Adapter 使用 `/workflow-runs/{session_id}/embed` 挂载 iframe，保留 `hostOrigin`、来源窗口与 session 校验，以及展开/收起交互。
- Codex 使用 `/workflow-runs/{session_id}/embed`，由内置浏览器直接加载共享 WorkflowPanel；MCP 返回的 `interaction_url` 已包含 `/embed`。页面、API 和状态刷新沿用现有实现。

共享 Panel store 只管理展示状态，不参与绑定、审核或执行授权。浏览器 UI 不导入 Runtime 凭据或 Node 专用依赖。

### 4.4 Codex 最小适配

`workflow.start` 返回 `session_id` 和 `interaction_url` 后，Agent 自动调用宿主 `open_in_codex`，参数为 `target.type=browser`、`target.url=interaction_url`、`placement=bottom`。每个新 Session 打开一次，不随状态查询重复打开，不要求用户手动点击链接。URL 使用工具返回值，不硬编码端口；本地 HTTP 页面已通过展示和刷新验证。

自动打开由 MCP 服务指令与工具描述引导 Agent 调用宿主工具，不是 MCP 服务直接操纵窗口。工具缺失或失败时明确报告并提供链接，不声称已打开。正在运行的 MCP 连接需要重新加载才能使用更新后的指令。

Codex 不再注册 MCP App HTML 资源或专用面板工具，不需要额外 HTTPS 代理、端口或本地 CA。通用 `LAZYMIND_WEB_URL` 覆盖能力仍保留供有需要的部署使用。

当前 Codex 接入采用 queue：MCP 在 `workflow.start` 内绑定原会话并返回最新状态，dispatcher 向该会话排队投递 continue。元数据缺失时允许 Agent 传入从宿主取得的真实 thread UUID；这是协议明确记录的身份信任降级。启动不再需要扫描会话或额外绑定唤醒。

Codex 暂不支持原生 turn 中断。Stop 仍在 Core 生效，cancel 通知明确记录为不支持；人工审核通过 MCP 指令、状态返回和唤醒 prompt 要求 Agent 结束当前 turn。无 pre-tool / pre-step 钩子，不能宣称与 DSH 完全等价。

安装通过既有 Codex Connect 入口完成：注册个人市场插件、自动配对并迁移旧 MCP 入口。插件 MCP 进程管理 queue dispatcher 生命周期，退出时清理 worker；构建产物内嵌到 LazyMind CLI。源码入口为 `make codex-workflow-install`。配置和验收步骤见 [Codex queue 接入](../../integrations/codex-workflow/README.md)。不需要 App Server Socket，不安装额外 CLI，不启动另一份 Codex 后端。


## 5. HostAction

HostAction 是 Core 发给外部 Controller 的持久化唤醒通知，不是 Step 执行任务，也不是 Workflow 的权威状态。

示例：

```json
{
  "id": "action-1",
  "kind": "continue",
  "session_id": "workflow-1",
  "native_session_id": "agent-session-1",
  "binding_generation": 1,
  "execution_id": "execution-1",
  "status": "pending"
}
```

现有状态流转（重构保持不变）：

```text
pending → dispatching
pending → superseded
dispatching → accepted / failed / unknown
unknown → accepted（核对接收证据后）
```

需要区分三件事：

| 阶段 | 含义 | 依据 |
| --- | --- | --- |
| claim | 哪个 dispatcher 负责本次发送 | instance ID、dispatch token 与租约 |
| accepted | 宿主已接收通知，可能仍在排队 | 宿主接收结果；异常恢复时核对持久化事件 |
| Workflow 推进或完成 | Agent 实际执行后产生业务结果 | Core 的 execution、产物与状态 |

`accepted` 不表示 Agent 已开始或完成执行。HostAction 不另建“Agent 执行完成确认”，执行结果由 Workflow 协议管理。

如果消息已进入宿主队列，但返回响应时断线，投递结果是 `unknown`。dispatcher 应核对相同 action ID 的持久化输入；不能盲目重发。当前协议中 dispatch 租约过期也进入 unknown，unknown 只能凭接收证据转 accepted，不能自动恢复为 pending。DSH 可回报正数 `native_event_seq`；Codex 当前没有可供 Core 接收的原生事件序号，结果不明时保持 `unknown`，不得编造数字序号。

`failed` 表示确定的投递失败，不能用它表示“宿主可能已经收到”的异常。`consumed_at` 表示通知已被业务推进消费，与 accepted 不同；`binding_generation` 用于拒绝旧绑定通知。发送前以及排队输入真正运行时，都需重新检查通知是否仍有效。保留单 pairing/profile dispatcher 的运行锁与 Core 的 claim 保护。

HostAction 表达：

```text
“这个 Workflow 有新的控制或执行事件，请唤醒对应 Controller，并重新读取 Core 状态。”
```

Controller 被唤醒后必须调用 `workflow.state`，不能把 HostAction 当成完整状态快照。

## 6. 核心交互链路

### 6.1 启动与展示

```text
Controller → workflow.start → Core
Core → session_id + interaction_url
共享集成层 → 解析结果并验证来源
共享集成层 → 为创建该 Workflow 的 driver 建立绑定
Core → 返回最新 control
UI Adapter → 展示 Panel
DSH 页面 → iframe → LazyMind WorkflowPanel
Codex → open_in_codex → 内置浏览器 → LazyMind WorkflowPanel
```

### 6.2 用户继续流程

```text
用户 → iframe Continue
iframe → Core WorkflowControlCommand
Core → 校验并持久化用户意图
Core → HostAction(continue)
共享集成层 → 获取并认领 HostAction
Runtime Adapter → 将关联 action ID 的通知送入目标会话
Controller → workflow.state → workflow.step.begin
```

### 6.3 外部执行

```text
Controller → workflow.step.begin
Core → executor_host=external-agent
Core → StepContract + execution_handle
外部 Executor → 执行并发布 Artifact
外部 Executor → workflow.step.complete
Core → 更新权威状态
```

### 6.4 LazyMind 内部执行

```text
Controller → workflow.step.begin
Core → executor_host=lazymind
LazyMind Executor → 执行并提交结果
Core → 更新权威状态
Core → HostAction(continue, execution_id)
共享集成层 → Runtime Adapter 唤醒目标会话
Controller → workflow.state → workflow.step.claim(execution_id)
Controller → 根据 Core 返回结果观察内部执行，并在允许时继续后续流程
```

### 6.5 停止

```text
用户 → iframe Stop
iframe → Core
Core → 立即停止 Session 并作废执行权
Core → HostAction(cancel)
共享集成层 → 核对当前运行归属
Runtime Adapter → 取消仍属于该 Workflow 的运行
```

Stop 不等待 Agent 确认后才生效。

### 6.6 Retry / Rewind 创建的已有执行

```text
用户 → Panel Retry / Rewind
Core → 校验并创建恢复 execution
Core → HostAction(continue, execution_id)
共享集成层 → Runtime Adapter 唤醒 Controller
Controller → workflow.state → workflow.step.claim(execution_id)
Core → 返回该执行的路由与授权结果
外部执行获得有效 handle → 执行并提交
内部执行 → 仅观察并让出执行
```

带 execution_id 的通知关联已有执行，不能统一替换成 `state → step.begin`。HostAction 本身不授予执行权，以 claim 返回的结果为准。

## 7. 用户操作归属

| 操作 | 处理位置 | 是否唤醒 Controller |
| --- | --- | --- |
| 展开、切换 Tab、预览 | iframe 本地 | 否 |
| 编辑、保存、排序产物 | iframe → Core | 否 |
| Confirm | iframe → Core | 视后续状态而定 |
| Confirm and Continue | iframe → Core | 是 |
| Continue | iframe → Core | 是 |
| Retry / Rewind | iframe → Core | 需要继续编排时唤醒 |
| Stop | iframe → Core 立即生效 | Codex 当前不支持中断 turn；Core 停止先行生效，DSH 保留取消能力 |
| Resume | iframe → Core | 需要继续编排时唤醒 |

## 8. 建议代码结构

```text
integrations/
├── workflow-agent-core/
│   ├── protocol.ts       # MCP 结果和 HostAction 类型
│   ├── dispatcher.ts     # HostAction 拉取、认领、回执
│   ├── coordinator.ts    # 绑定恢复、等待、工具准入与执行收尾协调
│   ├── runtime.ts        # Runtime 公共入口
│   ├── transport.ts      # Bridge 通信与单会话通知范围，不加载宿主凭据
│   ├── panel-store.ts    # iframe 展示状态
│   └── adapter.ts        # Adapter 接口
│
├── dsh-workflow/
│   ├── host-adapter.ts   # DSH 生命周期、prompt/cancel、历史查询
│   ├── events.ts         # DSH 工具名和事件标准化
│   └── ui-adapter.tsx    # DSH slots 和 iframe
│
└── codex-workflow/
    ├── src/main.ts       # 配对校验和共享 dispatcher 装配
    ├── src/adapter.ts    # queue 唤醒与人工审核 prompt
    └── src/runtime-lock.ts # 同一配对的 dispatcher 单实例锁
```

## 9. DSH 迁移原则

从现有 DSH 集成提取共享逻辑，保持行为不变：

| 现有代码 | 目标位置 |
| --- | --- |
| `protocol.ts` 的通用数据结构、结果与 URL 校验 | 共享层 protocol |
| `protocol.ts` 的 DSH 工具名、事件与 metadata 解析 | DSH events Adapter |
| `bridge.ts` 的 HTTP 通信 | 共享 transport；本地凭据加载留在 Runtime 接入入口 |
| `window-store.ts` | 共享集成层 |
| HostAction polling/claim/settle 与恢复决策 | 共享 dispatcher |
| `host.ts` 的等待、工具准入、运行归属及收尾决策 | 共享 coordinator |
| DSH 生命周期钩子、历史查询、父子关系、goal API | DSH Runtime Adapter；由共享协调结果驱动 |
| `runtime-lock.ts` | Runtime 接入基础设施，保留每 pairing/profile 的互斥 |
| `ctx.slots` 和 iframe 注入 | DSH UI Adapter |
| `sessionController.prompt/cancel` | DSH Runtime Adapter |

CLI 接入基础设施也需要同步调整：

- `assistantbridge/workflow_host.go` 当前固定绑定 provider 为 `deepseek-harness`，应改为从可信 pairing 记录取得 provider。
- `workflowhost/pairing.go` 当前仅接受 `dsh-...` 身份。新增宿主的身份需要区分账户、provider 和 profile，并兼容现有 DSH 配对，不能因重构使原绑定失效。
- 更新各宿主安装配置、共享包构建与 DSH bundle 打包交付，验证安装后的产物，而不只验证源代码。

## 10. 不变量

- Core 始终是权威状态源。
- iframe 按钮先提交 Core，不直接控制 Agent。
- HostAction 只用于唤醒或取消 Controller。
- Controller 醒来后必须重新读取 `workflow.state`。
- Executor 只能使用有效的 `execution_handle` 提交外部执行结果。
- 内部执行不向外部 Agent 发放 `execution_handle`。
- Agent Adapter 不包含 Workflow 业务判断；共享协调逻辑只消费 Core 决定，不复制状态机。
- accepted 只表示宿主接收，不表示执行完成；不确定投递不得自动重发。
- 打开 Panel 或读取其他 Workflow 不会转移控制权。
- 取消前核对运行归属，保留对用户其他工作的保护。
- 重构后现有 DSH 行为和测试必须保持一致。

## 11. 实施顺序

1. 明确统一事件、目标会话、运行归属和投递回执契约；用第二宿主的真实能力验证可行性。
2. 在迁移前补 Fake Adapter 契约测试，固定现有协调行为。
3. 从 DSH 分别提取通用协议、绑定、dispatcher、运行协调与可选 Panel store。
4. 将 DSH 专用事件和生命周期操作留在 Adapter，同步泛化 CLI pairing/Bridge，保持现有配对兼容。
5. 运行现有 DSH 与 Core 相关测试，验证构建、打包、安装后的行为。
6. 接入第二个 Agent，执行相同契约测试及端到端验证；不支持的能力明确说明降级范围。
7. Codex 复用既有 MCP 工具，并在 start 成功后通过宿主工具自动打开 HTTP Workflow 页面；工具不可用时明确降级。

最低验收场景：

- start 后绑定成功；绑定失败时停止自动推进；读取历史 Workflow 不抢占绑定。
- Continue 正常投递、重复轮询不重复发送、宿主接收后断线或进程重启可核对回执。
- 过期绑定、已消费通知和排队后失效的通知不会重新推进 Workflow。
- 审核等待、内部执行等待、draining 收尾、子 Agent 返回结果保持现有行为。
- Retry/Rewind 正确认领已有 execution，不额外 begin 新执行。
- Stop 立即在 Core 生效；延迟取消不误伤后来的用户任务。
- 多个宿主和多个会话的通知、配对身份与 Panel 状态相互隔离。
- UI 构建不引入 Runtime 凭据或 Node 专用依赖，现有 DSH 安装与重启恢复正常。

## 12. 当前落地范围

- 共享层已按上述目录提取；DSH 的 `host.ts` 负责注册生命周期与工具钩子，`host-adapter.ts` 负责宿主 API，`events.ts` 负责事件标准化。
- 原 DSH `protocol.ts`、`bridge.ts` 和 `client/window-store.ts` 保留兼容导出，避免一次性修改现有引用。
- 共享包当前作为源码库参与 DSH 构建，最终内联进插件产物，不要求用户额外安装共享包。
- CLI 提供 `EnsureForProvider`；旧 `Ensure` 仍创建兼容的 DSH 配对。Bridge 使用可信配对中的 provider，不接受请求体指定身份。
- 新增 SDK 无关的 Fake Adapter 契约测试；这证明共享逻辑不依赖 DSH，但不能替代第二个真实宿主的端到端验收。
- Codex 继续使用既有 STDIO MCP 安装入口，新增展示行为仅为启动后自动调用宿主浏览器工具；复用原有 WorkflowPanel 和 Core API。
- Codex 的 `integrations/codex-workflow` 使用桌面自带 CLI 的 queue，复用共享 dispatcher、HostBridge 和单实例锁；MCP 启动时绑定。配置与测试入口见该目录 README。
- Codex 集成要求显式提供桌面自带 CLI 路径和匹配的 profile 配对；不连接 App Server。Stop 只保证在 Core 生效，取消回执明确说明不支持中断。
- Codex 尚无 DSH 的 pre-tool / pre-step 钩子和持久事件序号；不确定投递保持 `unknown`，不自动重发。审批不自动批准，不能宣称两个宿主已完全等价。
