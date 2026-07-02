# 阶段 4：驱动模式增强与并发能力

> 在已落地的 Plugin `auto`/`dynamic`/`never` 驱动模式基础上，扩展插件驱动模式的精细化控制，补全 DriverAgent 能力，增加步骤级执行控制（停止 / 继续 / 重试 / 范围重跑），支持无依赖步骤并行执行，并引入 **TaskCenter**（对话级异步任务与定时触发）。
>
> 前置依赖：SubAgent 基础设施（`01-subagents`）、Plugin 机制（`02-plugin`）、数据历史与富媒体（`03-data_history`）**必须已落地**。
>
> 配套文档（渐进式披露，按需展开）：
> - 示例 / 伪代码：[`code.md`](./code.md)
> - 目标清单：[`target.md`](./target.md)

## 阅读顺序

1. 先读「一、核心概念与设计原则」，掌握模式语义、配置层级与状态机原则（§1.5）。
2. 「二」用户配置体系；「三」驱动模式控制；「四」步骤级执行控制（停止 / 继续 / 重试）。
3. 「五」并行执行；「六」意图与约束；「七」范围重跑。
4. 「八」Ask 机制；「九」TaskCenter；「十」定时任务；「十一」DriverAgent 能力补全。
5. 「十二、对外接口」「十三、状态查询工具」是能力补充；「十四、步骤中断恢复」；「十五、实施顺序」是收尾。

---

## 一、核心概念与设计原则

### 1.1 驱动模式层级 ✅

**不再使用**环境变量 `LAZYMIND_PLUGIN_MODE`。改为三个**正交**的用户配置字段，每个用户独立；支持**全局默认**与**对话级临时覆盖**，以对话配置为准。

| 字段 | 类型 | 默认 | 含义 |
| --- | --- | --- | --- |
| `enable_plugin` | bool | `true` | 是否启用插件机制；`false` 即纯问答（取代旧 `plugin_mode=never`） |
| `plugin_mode` | enum | `dynamic` | 仅 `enable_plugin=true` 时生效；`auto` 启用 DriverAgent 自动裁决推进，`dynamic` 禁用 DriverAgent、由 ChatAgent 自行决策 |
| `enable_subagent` | bool | `true` | 是否允许 ChatAgent 自主创建非 Plugin 的发散型 SubAgent；不影响插件步骤的 SubAgent |

`plugin_mode` 两档行为（仅在 `enable_plugin=true` 下）：

| `plugin_mode` | DriverAgent 是否参与 | ChatAgent 退出时机 |
| --- | --- | --- |
| `auto` | 是 | 调用 `advance_step_and_hand_off` 后由 `plugin_stop_tools` 退出 |
| `dynamic`（默认） | 否 | 由 ChatAgent 自行决定（可连续多步，或 ask 后退出） |

SubAgent 开关（`enable_subagent`）与插件步骤无关，控制 ChatAgent 是否允许自主发散创建非 Plugin 的 SubAgent，独立 bool 字段，默认 `true`：

- `enable_subagent=true`：ChatAgent 可自主创建 SubAgent 推进发散目标，由 ChatAgent 直接驱动，不经过 DriverAgent。
- `enable_subagent=false`：禁用大模型自主 SubAgent，不影响插件步骤的 SubAgent 执行。

### 1.2 配置层级与持久化✅

```
用户全局默认（设置页）
  enable_plugin / plugin_mode / enable_subagent
       ↓ 进入对话时加载为初始值
对话级配置（对话界面可改，写入 conversations 列）
  enable_plugin / plugin_mode / enable_subagent
       ↓ 每轮 chat 请求 body 透传（对话配置优先）
Go / Python 运行时
```

| 层级 | 存储 | 说明 |
| --- | --- | --- |
| 用户全局默认 | `user_chat_settings`（新增表） | `user_id` + `enable_plugin` + `plugin_mode` + `enable_subagent` |
| 对话级覆盖 | `conversations.enable_plugin` / `conversations.plugin_mode` / `conversations.enable_subagent`（新增列，可空） | 空则回落全局默认；对话 UI 修改即写库 |

前端：设置页维护全局默认；进入对话时从全局默认初始化对话配置（若对话尚无覆盖）；对话顶栏 / PluginPanel 提供模式切换。

### 1.3 前端交互按钮语义✅

`dynamic` 模式每步完成后默认暂停等用户，`auto` 模式由 DriverAgent 裁决。两者共用相同的按钮语义：

| 场景 | 前端展示 | 后端语义 |
| --- | --- | --- |
| 步骤完成，等待用户 | 「继续」「重试」 | 前端构造标准 `POST /conversations:chat` 消息，由 ChatAgent 解析意图推进（§4.2） |
| SubStep 执行中 | 「停止」 | 确定性中断（§4.1） |
| ChatAgent 流式输出中 | 「停止」 | 确定性中断（§4.1） |

要点：

- **「继续」「重试」**：前端构造标准消息（如「继续执行下一步」「重试步骤 X，原因是…」），进入对话 history，**仅 ChatAgent** 处理；SubAgent 不可 ask，也不可代用户做继续 / 重试决策。
- **「停止」**：调用确定性后端接口（§4.1），**不**经过 ChatAgent 推理。

各模式下「停止」可作用对象与 UI 时机：

| 模式 | 可停止对象 | UI 时机 |
| --- | --- | --- |
| `auto` | ChatAgent 流式输出中、SubStep 执行中 | 全程显示「停止」 |
| `dynamic` | SubStep 执行中 | SubStep running 时显示「停止」；步骤完成后显示「继续」「重试」 |

### 1.4 设计原则：Go 依然是唯一 Orchestrator✅

并行执行、停止中断、DriverAgent 裁决、定时触发，均由 Go 侧 `eventloop.go` / TaskCenter 控制。Python 的 `_trigger_plugin_step` / `advance_step` 只负责发射信号；多路并行由 Go 并发启动多个 `go subagent.Run(...)`。

### 1.5 Session 状态机简化原则

Session 只维护三种语义状态：`active`（有任务在跑）、`waiting`（等用户决策）、`completed`（明确结束）。

**不再使用** `failed` / `interrupted` 作为 Session 终态——失败和中断是 SubAgent task 的属性，不是 Session 的属性。步骤失败后 Session 统一进入 `waiting`，由用户决策下一步。

`completed` 的唯一判据：`plugin_session_steps` 中最新一条记录的 `step_id == "__end__"`（持久化写入，可追溯）。`__end__` 到达时先写步骤记录，再更新 Session 状态。

**auto 模式兜底**：`triggerNextChatTurn` 完成后，若 Session 仍是 `active`（ChatAgent 未调用 `advance_step`），自动降级为 `waiting`（`reason: chat_agent_no_advance`），避免永久卡住。

**completed 回退**：`completed` 状态下用户可选择回退到某个已成功步骤，ChatAgent 调用 `advance_step` 重新启动，Session 回到 `active`；`__end__` 步骤记录保留，但不再是最新步骤，`IsEndStepLatest` 返回 false。

---

## 二、用户配置体系

### 2.1 数据表✅

```sql
CREATE TABLE user_chat_settings (
  user_id          VARCHAR(255) PRIMARY KEY,
  enable_plugin    BOOLEAN      NOT NULL DEFAULT TRUE,
  plugin_mode      VARCHAR(16)  NOT NULL DEFAULT 'dynamic',  -- dynamic | auto
  enable_subagent  BOOLEAN      NOT NULL DEFAULT TRUE,
  updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);

ALTER TABLE conversations
  ADD COLUMN enable_plugin   BOOLEAN,       -- NULL → 回落 user_chat_settings
  ADD COLUMN plugin_mode     VARCHAR(16),   -- NULL → 回落 user_chat_settings
  ADD COLUMN enable_subagent BOOLEAN;
```

### 2.2 加载顺序（Go `ChatConversations`）✅

1. 读 `conversations.enable_plugin` / `plugin_mode` / `enable_subagent`；非空则用对话值。
2. 否则读 `user_chat_settings`；无记录则 `enable_plugin=true`、`plugin_mode=dynamic`、`enable_subagent=true`。
3. 写入 `buildChatRequestBody`，透传 Python `agentic_config`。
4. **首次发消息**（`ensureConversation` 新建会话时）若请求 body 携带 `initial_plugin_settings`，以其为准写入 `conversations` 列；**已有会话**时完全忽略请求 body 中的配置字段，`loadUserAgentConfig` 始终从 DB 读取。

> 代码示例见 [`code.md` · C1](./code.md#c1)。

### 2.3 前端✅

前端配置流转路径：

- **新建对话**：弹窗首次打开时调用 `GET /api/core/user/chat-settings` 拉取用户全局默认；用户在弹窗修改后暂存到 `pendingPluginSettingsRef`（不调接口）；**第一次发消息**时将暂存值作为 `initial_plugin_settings` 放入请求 body，由 Go `ensureConversation` 写入 `conversations` 表。
- **已有对话**：配置来自 `GET /conversations/{id}:detail` 返回的 `enable_plugin` 等字段（conversation detail 回填），**不**再调用 `getChatSettings`；用户在弹窗修改后直接调用 `PATCH /conversations/{id}/plugin-settings` 更新 DB，后续轮次生效。
- 前端**不需要**独立的 `GET /conversations/{id}/plugin-settings` 接口；后续消息 body 无需携带配置字段（Go 始终从 DB 读）。
- 移除对 `LAZYMIND_PLUGIN_MODE` 的依赖；`eventloop.go` 的 `defaultMode()` **废弃**，改从 `plugin_context.plugin_mode`（由 Go 在创建 / 推进步骤时注入）读取。

---

## 三、驱动模式控制

### 3.1 模式判断（Go `OnSubAgentDone`）✅

步骤 SubAgent 完成时，先看 `enable_plugin`，再按 `plugin_mode` 决策：

- `enable_plugin=false` → 忽略插件事件（纯问答，根本不会进入插件流水线）
- `enable_plugin=true` 且 `plugin_mode=auto` → `go advanceAutoMode(...)` → `callDriverAgent` → 按 verdict 决策；`triggerNextChatTurn` 完成后若 Session 仍是 `active`，`checkAndFallbackIfStuck` 降级为 `waiting`
- `enable_plugin=true` 且 `plugin_mode=dynamic` → 发 `step_waiting`（`reason: dynamic_pause`），等用户下轮消息或按钮模拟消息

SubAgent 以任何终态（`succeeded` / `failed` / `interrupted`）结束时，均按同一逻辑判断 Session 下一状态（§1.5）：有并行 sibling 未完成则保持 `active`；全部完成后看 `IsEndStepLatest` 决定 `completed` 或 `waiting`。**步骤 `failed` 不会使 Session 进入 `failed` 终态**。

> 代码示例见 [`code.md` · C2](./code.md#c2)。

### 3.2 DriverAgent 输出格式与失败降级✅

**DriverAgent 输出自然语言评估（非 verdict codes）**：DriverAgent 输出 1-2 句纯自然语言，描述步骤结果是否合格及原因，**不输出** PASS/RETRY/FAIL/DONE 等结构化指令码。该评估作为 synthetic user turn 注入下一轮 ChatAgent，由 ChatAgent 自主决策（调用 `advance_step_and_hand_off` 推进下一步、传 `retry_hint` 重试当前步、或传 `__end__` 完成插件）。

降级策略：
1. HTTP 超时或 500 → 降级：推送 `driver_fallback` + `step_waiting`，不自动推进（即 fallback 到 `dynamic` 让用户介入，而非静默 PASS）。
2. `callDriverAgent` 重试 1 次（退避 5s），仍失败才降级。

### 3.3 `advance_step` / `advance_step_and_hand_off` 与 ChatAgent 退出✅

ChatAgent 有**两个**步骤推进工具，语义不同：

| 工具 | 是否在 `plugin_stop_tools` | 行为 |
| --- | --- | --- |
| `advance_step` | **否** | 同步推进：工具内部阻塞等待 SubAgent 完成，返回结果摘要；ReAct 继续推理 |
| `advance_step_and_hand_off` | **是** | 推进步骤并结束当前对话轮次：立即返回 `"step queued"`，框架随即强制退出 ReAct、SSE 关闭；SubAgent 后台运行，完成后由 DriverAgent 裁决或等待用户决策（Conversation Events 通知前端） |

**按模式动态注册**（`resolve_plugin_injection` 中根据 `plugin_mode` 决定）：

| `plugin_mode` | 注册的工具 | 设计意图 |
| --- | --- | --- |
| `auto` | 仅 `advance_step_and_hand_off` | ChatAgent 无法调用同步工具，每步必退出交给 DriverAgent 裁决 |
| `dynamic` | `advance_step_and_hand_off`（默认）+ `advance_step` | 默认每步完成后交用户决策；仅用户明确要求连续推进多步时才用 `advance_step` |

使用规则（注入 ChatAgent system prompt，仅 `dynamic` 模式可见两个工具时生效）：

- 默认使用 `advance_step_and_hand_off`：每步完成后让用户查看结果并决策下一步。
- 仅当用户明确要求连续执行多个步骤（如「重跑 1-3」「全部跑完」）时，对前 N-1 步使用 `advance_step` 同步等结果，最后一步使用 `advance_step_and_hand_off` 退出。

⚠️ TODO：仅在工具返回成功（无 Error: 前缀 / success: true）时才触发 stop-tool 退出，失败时把 Error 作为 tool result 继续 ReAct 循环。需要的话我可以帮你改这一块。

`advance_step`（同步）等待实现：工具调用后通过 FileSystemQueue 轮询 step done 事件（Go 在 SubAgent 完成时 enqueue 结果摘要）；超时后返回 partial 摘要，ReAct 可选择继续或 ask。

---

## 四、步骤级执行控制

### 4.1 停止（确定性后端）

**接口**：`POST /api/core/conversations/{id}:stop`（扩展现有 `stopChatGeneration`，新增 plugin 语义分支）。

Go 处理：

1. 向 Python chat 进程发送**取消信号**（见下）。
2. 将当前 `running` 的 `plugin_session_steps.status` 置为 `interrupted`（步骤级属性）。
3. `subagent.MarkTaskInterrupted` 中止对应 `sub_agent_tasks`。
4. `plugin_sessions.status=waiting`（Session 不进入 `interrupted` 终态，保持可恢复）。
5. 推送 `step_waiting` + `user_stopped: true`。

**算法侧中断（新增）**：当前无「后端 → 算法」反向中断通道，方案如下。

```
Go :stop
  → Redis / DB 写入 cancel_flag(conversation_id)
  → 可选：经 Conversation Events SSE 推 cancel 事件给仍打开的 FE
Python chat stream 循环
  → 每轮 ReAct 前 / FileSystemQueue dequeue 间隙轮询 cancel_flag
  → 或：Go 向 Python 侧 FileSystemQueue(sid=conversation_id) enqueue {"tag":"control","action":"cancel"}，新建channel，不要影响流式输出; 多个SubAgent都要能停掉，互不影响
  → ReAct / SubAgent runner 收到后中止，返回 partial
  → 终止后按需清除标志位，避免后续干扰
```

SubAgent 同步执行中：runner 同样轮询同一 `cancel_flag` 或 queue control 消息。

> 代码示例见 [`code.md` · C4](./code.md#c4)。

### 4.2 继续 / 重试（模拟用户消息）✅

前端**不**调用 Go 推进 API，而是构造标准 chat 消息：

```json
POST /conversations:chat
{
  "query": "继续",
  "action": "CHAT_ACTION_NEXT",
  "input": [{"input_type": "text", "text": "继续执行下一步"}]
}
```

或重试：

```json
{ "input": [{"input_type": "text", "text": "重试步骤 generate_images，因为图片风格不符合要求"}] }
```

ChatAgent 在 `resolve_plugin_injection` 看到 `session.status=waiting` 且有 `interrupted` 或刚完成步骤时，结合用户文本调用 `advance_step` / 带 `retry_hint` 的 `advance_step`。消息进入 history，可追溯。需防止用户重复点击导致重复触发（前端按钮置灰 + 后端幂等校验）。

> 代码示例见 [`code.md` · C5](./code.md#c5)。

---

## 五、并行执行

### 5.1 并行决策者：ChatAgent

并行与否由 **ChatAgent** 根据 `state.yml` 的依赖描述自行判断，不由 Go 自动推断。ChatAgent 在一次 ReAct 步骤中**同时输出多个 `advance_step` / `advance_step_and_hand_off` 工具调用**（parallel tool calls），触发并行执行。

并行与同步/异步无关：

| 场景 | ChatAgent 调用方式 | SubAgent 是否并行 |
| --- | --- | --- |
| 并行 + 同步 | 同时输出多个 `advance_step` | 是；两个 SubAgent 同时跑，工具调用都阻塞等结果，结果全部返回后 ReAct 继续 |
| 并行 + 异步 | 同时输出多个 `advance_step_and_hand_off` | 是；两个 SubAgent 同时跑，ReAct 立即退出，后续由 DriverAgent 裁决 |
| 串行 + 同步 | 顺序调用多个 `advance_step` | 否；上一步完成后才调下一步 |

### 5.2 Go 并行编排

Go 侧 `HandlePluginStepCreated` 每次只处理一个 step，不感知「是否并行」；并发效果来自 ChatAgent 同时触发多个 `task_created`，每个都走独立的 `go subagent.Run`。

Go 需要新增并行跟踪：`plugin_sessions.parallel_step_ids`（JSONB）记录当前批次在跑的 step 集合。`OnSubAgentDone` 收到单步完成时：

- 集合仍有未完成步骤 → 发 `step_parallel_done`，等待剩余。
- 集合清空 → 触发 DriverAgent 整体裁决（`auto`）或 `step_waiting`（`dynamic`）。

> 代码示例见 [`code.md` · C10](./code.md#c10)。

### 5.3 同步并行的等待语义

`advance_step`（同步）通过 FileSystemQueue 轮询等待 SubAgent 完成。ChatAgent 并行调用多个 `advance_step` 时，框架并发执行所有工具调用，每个工具各自阻塞等结果；所有结果都返回后 ReAct 才继续推理。Go / Python 不需要为「同步并行」做特殊处理——并发由 parallel tool calls 机制保证，每个工具实例独立轮询自己的 SubAgent 状态。

### 5.4 Stop-tool 在 parallel tool calls 中的退出时机

**现有机制**：`functionCall.py` 的 `_post_action` 中，先执行 `tool_calls_results = self._tools_manager(tool_calls)`（所有工具并发运行直到全部完成），**之后**再检查 `called_names & self._stop_tools`：

```python
tool_calls_results = self._tools_manager(tool_calls)   # 全部执行完
if self._stop_tools:
    called_names = {tc['function']['name'] for tc in tool_calls}
    if called_names & self._stop_tools:                # 有 stop-tool
        return '\n'.join(str(r) for r in tool_calls_results)  # 立即退出
```

因此：**所有 parallel tool calls 全部执行完毕后，若其中任意一个是 stop-tool，本轮 ReAct 立即退出**。这对 `advance_step_and_hand_off` 并行是安全的——多个工具同时输出 `task_created`，全部执行完后 ReAct 退出；各 SubAgent 已经在 Go 侧并发运行。

### 5.5 混用 sync / async 的处理

混用**没有实质问题**。`tool_calls_results = self._tools_manager(tool_calls)` 并发执行所有工具：`advance_step` 在内部阻塞等 step_A SubAgent 完成，`advance_step_and_hand_off` 立即返回；两个工具都完成后，框架才检查 stop-tools 并退出 ReAct。因此 step_A 的同步等待结果不会被截断，Go 侧两个 SubAgent 均已正常启动。

混用的实际效果等同于「全异步」：ReAct 退出时 step_A 已完成（同步等完了）、step_B 在后台运行（异步），ChatAgent 拿到 step_A 的结果摘要但因为 stop-tool 退出而不再消费。若需要 ChatAgent 消费 step_A 的结果继续推理，应统一用 `advance_step`（全同步）；若不需要，混用和全异步行为一致，不用禁止。

### 5.6 ChatAgent 提示词要求

需在 system prompt 中说明并行判断规则：

- 读取当前 `state.yml` 步骤的 `inputs` 声明，找出前驱均已完成、互不依赖的步骤。
- 若存在多个就绪步骤，在同一个 ReAct 步骤中**同时**调用对应的 `advance_step` / `advance_step_and_hand_off`。
- 若需要 ChatAgent 消费并行步骤的结果继续推理，用 `advance_step`（全同步）；若不需要，用 `advance_step_and_hand_off`（退出当前轮次，交由 DriverAgent 或用户决策）。

### 5.7 前端

- `step_parallel_done` 事件：单个并行步骤完成。
- PluginPanel / StateGraph 能正确展示多个步骤同时 `running`。
- DriverAgent 在并行步骤全部完成后才做整体裁决。

---

## 六、意图与约束（框架级，插件无感知）

全局约束与步骤约束是**平台机制**，插件 `plugin.yaml` / `scenario.md` **不需要**声明字段，也**不需要**插件作者实现工具。

### 6.1 存储

约束是**语义声明**，与步骤执行次数（attempt）无关。`plugin_session_steps` 每次执行 INSERT 一条新记录（attempt 递增），因此步骤级约束**不**存在 `plugin_session_steps` 里，而是单独按 `(session_id, step_id)` 唯一存储：

| 字段 | 表 | Key | 含义 |
| --- | --- | --- | --- |
| `intent_context` | `plugin_sessions` JSONB | `session_id` | 全局约束，如「全程清淡风格」 |
| `intent_context` | `plugin_step_intents`（新表） | `(session_id, step_id)` UNIQUE | 步骤级约束，如「第 2 步仅竖版」；无论该步骤执行多少次 attempt，约束只有一条，`update_intent` 直接 UPSERT |

```sql
CREATE TABLE plugin_step_intents (
  id           VARCHAR(36)  PRIMARY KEY,
  session_id   VARCHAR(36)  NOT NULL REFERENCES plugin_sessions(id),
  step_id      VARCHAR(64)  NOT NULL,
  intent_context JSONB      NOT NULL DEFAULT '{}',
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
  UNIQUE (session_id, step_id)
);
```

### 6.2 ChatAgent 工具与提示词

**注入 ChatAgent**（`resolve_plugin_injection`）：

- system prompt 追加框架内置提示段（非插件目录文件）：说明如何识别 / 维护约束、**按 §8.4 阶段边界**决定何时 ask。
- 每轮将 DB 中已有的全局 `intent_context`（`plugin_sessions`）与步骤级 `intent_context`（`plugin_step_intents`）序列化注入 user turn 或 system 附录，供 ChatAgent 在推理和调用 `advance_step` 时参考。

**注入 SubAgent**（SubAgent 启动时）：

- `resolve_plugin_injection` 在构建 SubAgent system prompt 时，同样注入当前 `plugin_sessions.intent_context`（全局约束）和对应 `plugin_step_intents` 记录（步骤级约束）。
- SubAgent 在整个执行过程中（每次工具调用、生成内容时）都能持续参考约束，而不只依赖 objective 里一次性的 `runtime_instruction`。
- `runtime_instruction` 仍然保留，用于传递**本次执行的临时指令**（如 retry_hint、partial_indices 指示），与约束语义不同。

**工具**（注册到 ChatAgent，SubAgent 不注入）：

| 工具 | 作用 |
| --- | --- |
| `update_intent` | UPSERT 全局或步骤级 `intent_context`，Go REST 持久化 |

> 状态查询工具（`list_plugin_steps` 等）见 §十三。

意图变更时，ChatAgent 重新修订全局意图及**后续未执行**步骤的执行参数；已完成的步骤不重新执行。约束通过 SubAgent system prompt 注入，SubAgent 不需要 `update_intent` 工具，只需读取、遵守约束即可。

> 代码示例见 [`code.md` · C6](./code.md#c6)。

### 6.3 前端展示

PluginPanel 步骤卡片旁展示当前步骤 `intent_context` 摘要；全局约束展示在 session 头部。

---

## 七、范围重跑与自然语言触发 ⚠️ 已开发完成，待PPT场景加好后联调

### 7.1 问题：从 `stop_tools` 到 LLM 决策

原方案依赖 `advance_step` + Go 批量重置 `plugin_session_steps.status=pending`。**不推荐**依赖 Go 侧批量 reset DB 状态做编排——与「ChatAgent 驱动、history 可追溯」原则冲突，且 attempt / artifact 版本难以一致。

改为：**由 ChatAgent 在一轮或多轮内顺序调用 `advance_step`**，Go 仅处理单次 `task_created`（已有 `rewind` / `retry_hint` 可选提示），**不**实现「批次内跳过 step_waiting」的隐藏状态机，也**不**新增 Go 侧 `run_range` 批次字段。

### 7.2 推荐方案（二选一，默认方案 A）

用户说「重新跑步骤 1 到 3」时，ChatAgent 解析步骤 ID 列表，按序推进。

**方案 A（默认）— 前两步同步、第三步异步后退出**

1. 调用 `advance_step(step1, rewind=True, retry_hint=...)` → 阻塞等待 SubAgent 完成，收到结果摘要。
2. 同理 `advance_step(step2, ...)`，同步等结果。
3. 调用 `advance_step_and_hand_off(step3, ...)` → 立即返回，ReAct 强制退出，SSE 关闭。
4. step3 SubAgent 后台运行；完成后 Conversation Events 通知前端（`batch_done`）。

**方案 B — 三步全同步后退出**

1. step1、step2、step3 全部用 `advance_step` 同步等结果。
2. ChatAgent 对三步结果做一轮汇总回复，ReAct 自然结束，SSE 关闭。

| 方案 | 适用 |
| --- | --- |
| A | 第三步耗时长，用户不必保持 SSE；或用户明确说「后台跑」 |
| B | 需要 ChatAgent 对三步结果做**一轮汇总**再交给用户 |

`auto` 模式下：每步统一用 `advance_step_and_hand_off`，每步均由 DriverAgent 裁决，**不支持**单轮内连跑多步；用户范围重跑需切 `dynamic` 或逐步确认。

> 代码示例见 [`code.md` · C7](./code.md#c7)。

### 7.3 `advance_step` / `advance_step_and_hand_off` 工具增强

- docstring 动态嵌入可 `rewind` 步骤列表（已有）。
- 可选参数 `retry_hint`、`rewind: bool`（已有）；**不**新增 Go 侧 `run_range` 批次字段。
- ChatAgent system 提示（仅 `dynamic` 模式可见 `advance_step` 时）：识别「重跑 N~M 步」时，前 N-1 步用 `advance_step` 同步等结果，最后一步用 `advance_step_and_hand_off` 退出交由用户决策。`auto` 模式下 ChatAgent 只有 `advance_step_and_hand_off`，不支持单轮连跑多步。
- 注意：dynamic模式下，当ChatAgent判断可能为最后一步时，提交同步任务，并且在执行完毕后主动推进到 `__end__` ，避免用户点击继续，发现已经结束。对应的，我们要有机制去发现state.yml中哪些步骤可以直连__end__，给予提示

---

## 八、Ask 机制（仅 ChatAgent）

### 8.1 形态

| 类型 | `ask_user` 参数 | 前端呈现 |
| --- | --- | --- |
| 选择题 | `choices: string[]`，`allow_multiple?: bool` | 单选 / 多选卡片 + 确认 |
| 填空题 | `choices` 为空或省略 | 文本输入框 |

### 8.2 工具定义

ChatAgent 专用工具 `ask_user`（**不**注册给 SubAgent）：

```python
def ask_user(question: str, choices: list[str] | None = None,
             allow_multiple: bool = False, step_id: str | None = None) -> str:
    """Suspend and ask the user. Returns user's answer on next turn."""
```

调用后：

1. ReAct **立即退出**（加入 `plugin_stop_tools` 或等价逻辑）。
2. Go 收到 `task_created` 子类型 `agent_ask`（或独立 SSE 帧 `ask_pending`），payload 含 `question`、`choices`、`ask_id`。
3. **结束当前 Chat SSE**。

> 代码示例见 [`code.md` · C8](./code.md#c8)。

### 8.3 用户回答路径

**采用「结束 SSE → 用户回答 → 新 Chat 轮次」**（与继续 / 重试一致），**不**用 FileSystemQueue 在同一条 SSE 内阻塞等待（避免长连接与网关超时）。

用户回答时：

```json
POST /conversations:chat
{
  "input": [{"input_type": "text", "text": "用户可见回答"}],
  "ask_response": { "ask_id": "...", "selected": ["选项A"] }
}
```

Go 将 `ask_response` 透传 Python；`resolve_plugin_injection` 将回答注入当前 turn；ChatAgent 继续推理。

**对话历史的影响与前端呈现**：

- `ask` 和 `ask_response` 均写入 `conversation_messages`，分属两条记录：
  - ChatAgent 发出 ask 的那轮：消息类型为 `assistant`，`metadata.ask_pending` 存 `{ ask_id, question, choices }`；前端渲染为带选项卡片（或文本输入框）的 assistant 消息泡。
  - 用户回答的那轮：消息类型为 `user`，`content` 为用户可见文本（如「竖版」），`metadata.ask_response` 存 `{ ask_id, selected }`；前端渲染为普通用户消息泡，可见文本即用户填写 / 选择的内容。
- 历史记录中 ask 与回答成对出现，可完整追溯；重新加载对话时前端直接渲染历史 ask 卡片（已有 `selected` 则展示已选状态，不可再次提交）。
- `ask_response` 字段**不**影响普通对话消息的渲染路径——前端仅在检测到 `metadata.ask_pending` 时才切换为卡片形态；无该字段的消息按普通文本处理。

### 8.4 默认能力与阶段边界

**`ask_user` 始终注册给 ChatAgent**（无论 `plugin_mode` 为 `dynamic` 还是 `auto`，只要 `enable_plugin=true` 进入插件流程，乃至纯问答场景均可用），SubAgent 永不注册。差异在于**何时应主动调用**，而非工具是否存在。

| 阶段 | 条件 | `dynamic` | `auto` |
| --- | --- | --- | --- |
| **插件前** | 尚无 `active plugin_session`（未 `trigger_*` / 未首步 `advance_step`） | 缺失关键意图时主动 ask | **同样允许 ask**；收集齐意图后再触发插件 |
| **插件执行中** | 已有 `plugin_session`，步骤由 SubAgent +（auto 下）DriverAgent 推进 | 允许 ask；每步完成后默认 `step_waiting` | **不主动 ask**；基于已有 `intent_context` 合理推断；每步 `advance_step_and_hand_off` 后即退出，后续由 DriverAgent 裁决 |
| **例外** | 任意阶段，用户显式说「先问我」「帮我确认一下」 | ask | ask |

要点：

- **auto ≠ 禁用 ask**。auto 限制的是**已进入插件流水线之后** ChatAgent 在步骤推进过程中的主动问询，避免与 DriverAgent 自动裁决冲突。
- **插件前**（选插件、定 cron、澄清全局约束等）两种模式行为一致，均可 ask。
- 框架内置的意图管理提示词按上表写清阶段判断，**不要**写成「auto 模式禁止 ask_user」。

---

## 九、TaskCenter：对话级异步任务✅

### 9.1 不复用 `asyncjob`

现有 `backend/core/asyncjob` 面向**资源型**后台任务（导入、索引等），Job 粒度与 SubAgent 步骤绑定过紧。**新建 `backend/core/taskcenter/`**，独立表与 API；可**复用**其 worker 循环、锁、重试退避等**模式**，不共用 `async_jobs` 表。

### 9.2 任务定义：管理 Dialog，不管理 SubAgent

| 概念 | 说明 |
| --- | --- |
| **Task** | 一次「可追踪的主任务」，绑定 `conversation_id`（+ 可选 `plugin_session_id`） |
| **SubAgent / Plugin Step** | Task 的**子过程**，不入 Task 队列；仅在 Task 详情中关联查询 |

无论 SubAgent 同步或异步执行，**async 任务清单里只有主 Task**。用户离开页面后，Task 仍运行；完成通过 Conversation Events / TaskCenter 通知。

### 9.3 什么对话算 Task

| 来源 | 是否创建 Task | 说明 |
| --- | --- | --- |
| 用户启用插件并触发 `trigger_*` / 首步 `advance_step` | **是**（`type=plugin_run`） | 默认创建；可在用户设置关闭「自动加入任务中心」 |
| 用户明确说「以后台任务方式执行」或点击「后台运行」 | **是**（`type=background_chat`） | 即使无插件 |
| 定时任务某次触发 | **是**（`type=scheduled`，`schedule_id` 指向定义） | 每次 cron 到期 **INSERT 新行**；与 `plugin_run` 等同表展示 |
| 普通纯问答 | 否 | 除非用户手动「加入任务中心」 |

Task 创建时机：Go 在首次 `task_created`（plugin_step 或 subagent）或用户显式 API 时 `INSERT task_center_tasks`。

> 代码示例见 [`code.md` · C11](./code.md#c11)。

### 9.4 任务分类与定时双层模型

**`task_center_tasks` 只记录「每次执行」**，与普通 Task 混排在同一列表 / API（`GET /api/core/tasks`）。**不**在 Task 表存 cron 或「重复规则」。

| `task_type` | `schedule_id` | 说明 |
| --- | --- | --- |
| `plugin_run` | 空 | 用户触发插件的一次运行 |
| `background_chat` | 空 | 用户显式后台执行的一次对话 |
| `scheduled` | **非空**，FK → `user_schedules.id` | 定时器**某次触发**产生的一次执行；与普通 Task 同表、同状态机 |

**定时规则本身**存 `user_schedules`（§10），与执行实例分离：

```
user_schedules（定时任务定义：cron、prompt、enabled、next_run_at）
       │ 每次 next_run_at 到期
       ▼
task_center_tasks（单次执行实例，task_type=scheduled，schedule_id=定义.id）
       │ 与普通 plugin_run / background_chat 并列展示
       ▼
标准 chat/plugin 管道（conversation_id 可复用绑定会话或新建）
```

| 存什么 | 表 | 是否进 Task 列表 |
| --- | --- | --- |
| 定时规则（cron、时区、模板、启停） | `user_schedules` | 否；走 `GET /api/core/schedules` |
| 每次触发的一次运行 | `task_center_tasks` | **是**；与一次性 Task 混排 |
| SubAgent / Plugin Step | `sub_agent_tasks` / `plugin_session_steps` | 否；在 Task 详情中关联查询 |

状态（仅指 `task_center_tasks` 执行实例）：`pending` → `running` → `waiting`（等人）→ `succeeded` / `failed` / `canceled`。

### 9.5 与看板的关系（阶段 5 预研）

[`5-dashboard/target.md`](../5-dashboard/target.md) 的看板、StateGraph **消费 TaskCenter + plugin_sessions 数据**，不在本阶段实现 UI，但 Task 表设计预留：

- `progress_json`：各步骤状态快照（供看板卡片）
- `predicted_completion_at`：可选，后续由 DriverAgent / 历史耗时估算

### 9.6 数据表（草案）

```sql
CREATE TABLE task_center_tasks (
  id                VARCHAR(36)  PRIMARY KEY,
  user_id           VARCHAR(255) NOT NULL,
  conversation_id   VARCHAR(36)  NOT NULL,
  plugin_session_id VARCHAR(36),
  task_type         VARCHAR(32)  NOT NULL,  -- plugin_run | background_chat | scheduled
  title             TEXT,
  status            VARCHAR(16)  NOT NULL DEFAULT 'pending',
  schedule_id       VARCHAR(36) REFERENCES user_schedules(id),  -- 仅 task_type=scheduled 时非空
  progress_json     JSONB,
  created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
  finished_at       TIMESTAMPTZ
);
CREATE INDEX idx_tct_user_status ON task_center_tasks(user_id, status);
```

Worker：Task 进入 `running` 后，本质仍是标准 chat/plugin 管道；TaskCenter 仅**跟踪**状态，不替代 `subagent.Run`。

---

## 十、定时任务（用户行为，与 plugin 无关） 

### 10.1 原则

- **不**在 `plugin.yaml` 配置 cron。
- 用户在对话中说「每天晚上 9 点收集 GitHub trends 并写报告」→ ChatAgent 调用 `create_schedule` 工具 → Go 写入 **`user_schedules`（规则）**，**不**在 Task 表创建 `recurring` 类型行。
- 每次调度器触发时：**先** `INSERT task_center_tasks`（`task_type=scheduled`，`schedule_id=规则.id`），**再** `triggerNextChatTurn`；该执行实例与普通 Task 一起出现在任务中心（§9.4）。

### 10.2 表结构（新建，不复用 `async_jobs`）

```sql
CREATE TABLE user_schedules (
  id                VARCHAR(36)  PRIMARY KEY,
  user_id           VARCHAR(255) NOT NULL,
  conversation_id   VARCHAR(36),              -- 绑定会话，可空
  cron_expr         VARCHAR(64)  NOT NULL,
  timezone          VARCHAR(64)  NOT NULL DEFAULT 'Asia/Shanghai',
  prompt_template   TEXT         NOT NULL,  -- 触发时发给 Chat 的 query
  enabled           BOOLEAN      NOT NULL DEFAULT TRUE,
  last_run_at       TIMESTAMPTZ,
  next_run_at       TIMESTAMPTZ  NOT NULL,
  created_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
```

### 10.3 调度器

新建 `backend/core/taskcenter/scheduler.go`，模式参照 `resourceupdate/scheduler.go`：乐观锁抢占 `user_schedules.next_run_at`，到期后：

1. `INSERT task_center_tasks`（`task_type=scheduled`，`schedule_id=sch.ID`，状态 `running`）
2. `triggerNextChatTurn(user_id, conversation_id, prompt_template)`
3. 更新 `user_schedules.last_run_at` / `next_run_at`

### 10.4 ChatAgent 工具

| 工具 | 说明 |
| --- | --- |
| `create_schedule` | 解析自然语言时间 → cron + prompt_template |
| `list_schedules` / `cancel_schedule` | 管理用户定时任务 |

**注入原则**：这三个工具与 `enable_plugin` 无关，在 `chat_service.py` 中**无条件注入**（独立于 `resolve_plugin_injection`），即使 `enable_plugin=false` 也可用。

---

## 十一、DriverAgent 能力补全 ✅

### 11.1 配置注入

`advanceAutoMode` 从 DB `llm_config` 加载用户配置，写入 `callDriverAgent` body 的 `llm_config`，确保 DriverAgent 能正常调用模型与工具（与 ChatAgent / SubAgent 对齐）。

### 11.2 附件与 Artifact 读取

`callDriverAgent` body 新增：

- `history_files_per_turn`：用户上传附件（已有）
- `plugin_artifacts_summary`：当前步骤及相关 slot 的 artifact 摘要（Go 从 `sub_agent_artifacts` + `plugin_slot_order` 组装）

Python `evaluate_step` 注入工具集：

- `find_artifact` / `read_artifact`（读插件产出，与 ChatAgent 侧一致）

DriverAgent 据此生成自然语言评估，由 ChatAgent 判断是否推进下一步。

### 11.3 `verdict=RETRY` 全局次数上限

**非** `plugin.yaml` 配置。全局默认 `max_retries=3`（`backend/core/plugin/config.go` 或环境变量 `LAZYMIND_DRIVER_MAX_RETRIES`，**不**暴露给插件作者）。

- `attempt < max_retries`：正常 RETRY。
- `attempt >= max_retries`：强制 PASS，发 `max_retries_exceeded` warning。
- 仅 `auto` 模式生效；`dynamic` 模式由用户逐次驱动，不限制。

---

## 十二、对外接口

```
# 用户配置
GET   /api/core/user/chat-settings
PATCH /api/core/user/chat-settings
  body: { "enable_plugin", "plugin_mode", "enable_subagent" }

PATCH /api/core/conversations/{id}/plugin-settings
  body: { "enable_plugin", "plugin_mode", "enable_subagent" }   # 对话级覆盖

# 停止（扩展）
POST /api/core/conversations/{id}:stop
  → 中断 ChatAgent + SubStep，step → interrupted，session → waiting

# Chat
POST /api/core/conversations:chat
  body: enable_plugin, plugin_mode, enable_subagent（可选，对话级）
  body: ask_response: { ask_id, selected[] }（回答 ask）

# TaskCenter（每次执行实例，含 scheduled 触发）
GET  /api/core/tasks
GET  /api/core/tasks/{id}
POST /api/core/tasks/{id}:cancel

# 定时规则（定义层，不进 Task 列表）
GET    /api/core/schedules
POST   /api/core/schedules
DELETE /api/core/schedules/{id}

# SSE 事件（新增或扩展）
step_waiting         → { reason, step_id, user_stopped? }  # reason: dynamic_pause | user_stopped | driver_fallback
ask_pending          → { ask_id, question, choices?, allow_multiple? }
driver_fallback      → { step_id, reason }
batch_done           → { steps[], summaries[] }
step_parallel_done   → { step_id }
max_retries_exceeded → { step_id, attempt }
task_status_changed  → TaskCenter 状态变更
```

---

## 十三、状态查询工具

用户可通过对话询问「现在跑到哪一步了」「第 2 步的结果是什么」「有哪些步骤失败了」等问题；ChatAgent 通过只读查询工具获取当前会话插件执行状态后作答，**不触发**新的步骤执行。

| 工具 | 作用 |
| --- | --- |
| `list_plugin_steps` | 列出各步骤状态（只读） |
| `get_step_result` | 某步骤的 artifact 摘要（只读） |
| `get_failed_steps` | 失败步骤及错误信息（只读） |

仅在有 `active_session_id` 时注入 ChatAgent；SubAgent 不注入。

> 代码示例见 [`code.md` · C12](./code.md#c12)。

---

## 十四、步骤中断恢复（Checkpoint-Resume）

### 14.1 问题

步骤 SubAgent 执行到一半被用户停止或因异常终止时，`save_artifact` 已实时持久化了**部分产出**到 DB（`sub_agent_artifacts` + `plugin_slot_revisions`）。用户说「继续」后，当前行为是创建全新 task，SubAgent 虽能通过 `_build_artifact_context_section` 看到 session 级 artifact 汇总，但：

1. SubAgent **不知道**当前是在续做一个被中断的步骤。
2. 缺少"只做剩余部分、跳过已有产出"的明确指引。
3. 已完成的 output 可能被重复生成。

### 14.2 设计方案

**不新增工具参数，不增加 Go 侧逻辑**。利用 SubAgent 已有的 artifact 可见性 + ChatAgent 的 `retry_hint` 传递语义：

SubAgent 启动时已经具备：
- objective prompt 中声明了该步骤应产出的所有 output_key
- `_build_artifact_context_section` 注入了 session 中所有已有 artifact（包括上次中断保存的）

唯一缺的是一句话告诉它"你是在续做，对比已有产出只做缺失部分"。这通过 **ChatAgent 的 `retry_hint`** 解决：

- 用户说「继续」→ ChatAgent 调用 `advance_step_and_hand_off(step_id, retry_hint='Previous attempt was interrupted. Check existing artifacts for this step and only produce missing outputs. Do not regenerate already-saved artifacts.')`
- 用户说「重试」→ ChatAgent 调用 `advance_step_and_hand_off(step_id, rewind=True)`（全量重做，现有行为）

SubAgent 收到带 checkpoint 语义的 `retry_hint` 后，自行对比 objective 中的 output_key 与 artifact 清单中已有的 key，跳过已完成的部分。

### 14.3 为什么不需要额外参数或 Go 侧逻辑

| 方案 | 复杂度 | 问题 |
| --- | --- | --- |
| 新增 `resume: bool` + Go 查 artifact + 组装 checkpoint_context | 高：Go 需要理解 state.yml outputs、查 DB artifact、组装 completed/missing 列表 | Go 做了本该由 LLM 做的事 |
| ChatAgent 通过 `retry_hint` 传达语义，SubAgent 自行判断 | 低：零新参数、零 Go 改动 | SubAgent 已有全部信息（output 声明 + artifact 清单），只差一句提示 |

SubAgent 本身就是 LLM，让它自己对比"我该做什么"和"已有什么"比让 Go 写死逻辑更灵活——特别是对 list slot 部分保存的场景（"5 张图只存了 2 张"），LLM 比确定性代码更能理解"续做剩余 3 张"的语义。

### 14.4 ChatAgent 决策逻辑

ChatAgent 通过用户消息语义 + 步骤状态决策（由 system prompt 指引）：

| 用户操作 | ChatAgent 行为 |
| --- | --- |
| 说"继续" / 点击「继续」 | `advance_step_and_hand_off(step_id, retry_hint='Previous attempt was interrupted. Check existing artifacts and only produce missing outputs.')` |
| 说"重试" / 点击「重试」 | `advance_step_and_hand_off(step_id, rewind=True)` |

`auto` 模式下：DriverAgent RETRY 走 `rewind=True`（全量重试）；DriverAgent 不走 resume——判定结果不合格时应完整重做。

### 14.5 前端配合

- 步骤状态为 `interrupted` → 前端展示「继续」+「重试」按钮。
- 「继续」发消息：`继续执行步骤 {step_id}`。
- 「重试」发消息：`重试步骤 {step_id}`。

### 14.6 边界情况

| 情况 | SubAgent 行为 |
| --- | --- |
| 步骤所有 output_key 均已有 artifact | SubAgent 对比后发现无缺失，直接返回"已完成" |
| list slot 只保存了部分（如 2/5 张图） | SubAgent 从 artifact 清单看到已有 2 张，续做剩余 3 张 |
| 中断后用户修改了意图/约束 | ChatAgent 判断约束变更是否影响已有产出——若影响则传 `rewind=True` 全量重做 |

> 代码示例见 [`code.md` · C13](./code.md#c13)。

---

## 十五、实施顺序

1. **配置与数据层**
   - `user_chat_settings`；`conversations` 增列；废弃 `LAZYMIND_PLUGIN_MODE`。
   - `plugin_sessions.intent_context` JSONB；`plugin_step_intents` 新表（`(session_id, step_id)` UNIQUE）；`plugin_sessions.parallel_step_ids` JSONB。
   - `task_center_tasks`、`user_schedules` 表。

2. **Go**
   - 配置加载与透传；`eventloop.go` 按 `enable_plugin` + `plugin_mode` 分叉。
   - Session 状态机简化：`OnSubAgentDone` 的 `failed` 分支改为 `waiting`；移除 `SessionStatusFailed` 写入路径；`__end__` 分支先写 `plugin_session_steps` 再更新 Session 状态（`IsEndStepLatest` 判据）。
   - `advanceAutoMode` 新增 `checkAndFallbackIfStuck` 兜底（ChatAgent 未推进时降级为 `waiting`）。
   - `:stop` 扩展 + cancel 信号；`advanceAutoMode` 降级 + 全局 max_retries。
   - 并行编排（`get_parallel_steps` → 多路 `go subagent.Run`）。
   - TaskCenter repository + scheduler；**不**注册 `plugin_step_run` 到 asyncjob。

3. **Python**
   - `advance_step`（同步，FileSystemQueue 轮询等待）+ `advance_step_and_hand_off`（结束当前轮次，加入 `plugin_stop_tools`）。
   - 按 `plugin_mode` 动态注册工具（§3.3）。
   - `driver_agent.py`：llm_config、附件 + **plugin artifact** 读取。
   - `plugin_manager.py`：`update_intent`、`ask_user`、查询工具；框架内置意图管理提示词。
   - 工具注入规则拆分：`resolve_plugin_injection` 只负责插件相关工具和提示词；`_build_chat_agent_task_context` 移到 `chat_service.py`，条件改为 `enable_plugin or enable_subagent`（非仅 `enable_plugin`）；`create_schedule` / `list_schedules` / `cancel_schedule` 在 `chat_service.py` 无条件注入，不经 `resolve_plugin_injection`。
   - `_FRAMEWORK_TOOLS` 列表补全 `patch_artifact` 和 `discard_draft`（与 `runner.py` 保持一致）。
   - ChatAgent system prompt 注入中断恢复指引：识别「继续」时构造 checkpoint 语义的 `retry_hint`（§14.2）。
   - 范围重跑提示词（§7.2）。

4. **前端**
   - 设置页 + 对话级 `enable_plugin` / `plugin_mode` / `enable_subagent` UI。
   - 「停止」「继续」「重试」按钮语义（§1.3、§四）；`ask_pending` UI。
   - 并行状态展示（StateGraph 多节点 running）。
   - TaskCenter 列表（简版，看板 UI 留阶段 5）。
