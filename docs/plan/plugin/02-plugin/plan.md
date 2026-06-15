# Plugin 机制方案

> 基于已落地的 SubAgent 基础设施，为 ChatAgent 设计 Plugin 执行机制：每个插件由多个 Step 组成，每个 Step 是一个异步 SubAgent；ChatAgent 负责意图识别与步骤触发；前端复用 Task Center 面板展示各步骤执行过程。
>
> 配套文档：
> - 示例 / 伪代码：[`code.md`](./code.md)
> - 端到端交互路径（文生图场景）：[`trace.md`](./trace.md)
>
> 前置依赖：SubAgent 基础设施（`docs/plan/plugin/01-subagents/plan.md`）**必须已落地**。

## 阅读顺序

1. 先读「一、核心概念与设计原则」建立心智模型。
2. 「二~五」是各层设计契约（数据 / 文件结构 / 工具 / 执行引擎），实现前必读。
3. 需要看代码长什么样时，跳到 [`code.md`](./code.md)；想看全链路怎么跑通，读 [`trace.md`](./trace.md)。
4. 「六、对外接口」「七、实施顺序」是收尾与排期。

---

## 一、核心概念与设计原则

### 1.1 Plugin = 多 Step 有序 SubAgent 序列

插件（Plugin）是一个**有状态的多步骤工作流**，每个 Step 是一个独立的异步 SubAgent：

- **插件**：声明式描述（`plugin.yaml` + `scenario.md` + `state.yml`），定义步骤集合、状态机转移、依赖关系。
- **Step**：等同于一个 SubAgent，有自己的 `sub_agent_tasks` 记录、`sub_agent_steps` 执行轨迹、`sub_agent_artifacts` 成果，走完整 SubAgent 生命周期。
- **插件会话**（Plugin Session）：一次用户触发产生一个会话，持有当前进行中的步骤状态与全部已产出 artifact。

核心简化：**Plugin 机制不引入新的 Python 执行引擎**，完全复用 SubAgent 的 `/api/subagent/run` 端点、`SubAgentDB`、事件流协议。Plugin 层只是在 ChatAgent 工具层 + Go 编排层增加了「步骤序列化」和「跨步依赖注入」逻辑。

### 1.2 auto 含义重新定义

原 SubAgent plan 中 `auto/manual` 指 ChatAgent 是否同步等待 SubAgent 完成。在我们的新Plugin 方案中：

- **所有ChatAgent自主调用的非Plugin的SubAgent均同步执行**（等同于原 auto 模式）。
- **所有Plugin Step（SubAgent）均异步执行**（等同于原 manual 模式）。
- **`auto` 的新含义**：Go 是否在 Step 完成后**模拟用户输入**自动推进到下一步，还是等待真实用户输入。
  - `auto`：Step 完成后 Go 自动借助 **DriverAgent** ，以合成消息触发 ChatAgent，继续决策下一步。
  - `manual`（human）：Step 完成后 Go 向前端发 `step_waiting`，关闭当轮 SSE，等待用户手动继续。

ChatAgent 的 `create_subagent` 调用中 **`auto` 参数不再出现**——Plugin Step 统一走异步，mode 由 Go 侧提供的全局配置来决定。

### 1.3 主 SSE vs Task SSE（直接复用 SubAgent 协议）

| 通道 | 内容 | 消费方 |
| --- | --- | --- |
| **主 SSE**（`POST /conversations:chat`） | ChatAgent 文本流 + `task_created` 通知 | 前端主消息框 |
| **Task SSE**（`GET /tasks/{id}:stream`） | `task_start / progress / artifact / done / error` | 前端 Task Center |

每个 Step 对应一个 `sub_agent_tasks` 记录，前端在 Task Center 中分组（按 Plugin Session）展示各步骤进度与产物。完全复用已有 Task SSE 协议，**无需新增事件类型**。

### 1.4 设计原则：ChatAgent 不调用 StepAgent

ChatAgent **不直接调用 Step 执行端点**（`/api/subagent/run`）。LLM 调用的是具有业务语义的 Plugin 工具（`trigger_<plugin_id>` / `advance_step`），工具内部经由 `_trigger_plugin_step()` 最终调用 `create_subagent(agent_type='plugin_step', ...)` 向 Go 发出 `task_created` 信号。Go 识别到这是一个 Plugin Step 后，按 SubAgent 协议调 `/api/subagent/run`，SubAgent 框架执行具体逻辑。

这一封装层（见 [§4.1](#41-chatagent-plugin-工具)）使 LLM 只需填写业务参数（`user_input`、`step_id`），无需感知底层的 `agent_type`、`session_id` 等字段，同时保持 Go 作为唯一 Orchestrator 的架构约束不变。

ChatAgent 立即返回（不阻塞轮询），Go 自行管理异步执行与推进。

### 1.5 设计原则：Go 是唯一 Orchestrator

```
前端 ←──SSE──→ Go（Plugin EventLoop） ←── ChatAgent（意图识别 / 步骤决策）
                                       ←── StepAgent = SubAgent（步骤执行）
```

- Go 持有 Plugin Session 状态，驱动步骤序列。
- Python 侧全部为无状态单次调用：ChatAgent、SubAgent 端点均不跨请求保存状态。
- 依赖注入：Go 在调 SubAgent 时，将前序步骤的 artifact 路径注入到 `input_artifact_keys`（已有字段），SubAgent 框架用 `get_artifact` 工具读取。

### 1.6 大模型不感知 Plugin Session ID / Step ID 数字 ID

工具参数与返回值只用步骤名称（`step_id` 如 `"optimize_prompt"`）和插件名称（`plugin_id`）。Go 负责将名称解析为数据库 ID；LLM 永远不接触 UUID。

### 1.7 设计原则：每个会话最多只有一个活跃 Plugin

一个 conversation 在同一时间只能运行一个插件，通过两层约束保证：

- **ChatAgent 层（软约束）**：ChatAgent 发现当前会话已有活跃 session；`chat_service.py` 在有活跃 session 时只注入 `advance_step` 工具，不再注入其他插件的 `trigger_<plugin_id>`，从工具集层面消除二次触发的可能。
- **Go 层（硬约束）**：收到 `task_created`（`agent_type='plugin_step'`, `is_cold_start=true`）时，查询该 conversation 是否已存在 `status='active'` 的 `plugin_sessions` 记录；若存在则拒绝创建新 session，向主 SSE 返回 error 事件。

---

## 二、数据层（新增 3 张表，复用 3 张 SubAgent 表）

SubAgent 的三张表（`sub_agent_tasks` / `sub_agent_steps` / `sub_agent_artifacts`）**不修改**，Plugin Step 的执行数据完整存入其中（`agent_type = 'plugin_step'`）。

新增三张 Plugin 专属表：

### 2.1 `plugin_sessions`

```sql
CREATE TABLE plugin_sessions (
    id                  VARCHAR(36)  PRIMARY KEY,
    conversation_id     VARCHAR(36)  NOT NULL,
    plugin_id           VARCHAR(64)  NOT NULL,
    trigger_history_id  VARCHAR(36),              -- 触发本会话的消息 ID
    status              VARCHAR(16)  NOT NULL DEFAULT 'active',
    -- 'active' | 'completed' | 'failed' | 'waiting'
    current_step_id     VARCHAR(64),              -- 当前/最后执行的 step
    create_user_id      VARCHAR(255),
    created_at          TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ps_conv ON plugin_sessions(conversation_id, created_at DESC);
```

### 2.2 `plugin_session_steps`

每个 Step 执行实例映射到一个 `sub_agent_tasks` 记录：

```sql
CREATE TABLE plugin_session_steps (
    id                  VARCHAR(36)  PRIMARY KEY,    -- step 执行实例 ID（= sub_agent_tasks.id）
    session_id          VARCHAR(36)  NOT NULL REFERENCES plugin_sessions(id),
    step_id             VARCHAR(64)  NOT NULL,        -- state.yml 中的 step 名称
    attempt             INT          NOT NULL DEFAULT 1,   -- 同一 step 的第几次执行
    task_id             VARCHAR(36)  NOT NULL,        -- = sub_agent_tasks.id
    status              VARCHAR(16)  NOT NULL DEFAULT 'pending',
    -- 直接镜像 sub_agent_tasks.status
    created_at          TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_pss_session ON plugin_session_steps(session_id, step_id, attempt);
CREATE INDEX idx_pss_task    ON plugin_session_steps(task_id);
```

> `plugin_session_steps.status` 是 `sub_agent_tasks.status` 的镜像，由 Go 在处理 SubAgent 事件时同步更新，方便 Plugin 层查询而无需 JOIN。

**`sub_agent_tasks` 在 Plugin 场景的字段语义**：

| 字段 | Plugin 场景取值 |
| --- | --- |
| `agent_type` | `'plugin_step'` |
| `title` | `'{plugin_id}:{step_id}'`（如 `'image-plugin:optimize_prompt'`） |
| `params` | `{"plugin_id": "...", "step_id": "...", "session_id": "...", "user_input": "...", "step_exec_id": "..."}` |
| `input_artifact_keys` | 前序 Step 的 artifact key 列表（Go 注入） |
| `output_artifact_keys` | 本 Step 声明的 outputs（来自 `state.yml`） |

### 2.3 `plugin_slot_revisions`

记录每个 Slot 的写入历史，是 Plugin Panel 的数据基础：

```sql
CREATE TABLE plugin_slot_revisions (
    id              VARCHAR(36)  PRIMARY KEY,
    session_id      VARCHAR(36)  NOT NULL REFERENCES plugin_sessions(id),
    slot_id         VARCHAR(64)  NOT NULL,    -- 对应 plugin.yaml ui.tabs[].slots[].id
    revision        INT          NOT NULL,    -- 从 1 开始，同 (session_id, slot_id) 内自增
    list_index      INT,                      -- cardinality=list 时的列表下标（0-based）；single 时为 NULL
    selected        BOOLEAN      NOT NULL DEFAULT TRUE,   -- 始终跟向最新，暂不支持手动切换
    artifact_key    VARCHAR(255) NOT NULL,    -- 对应 sub_agent_artifacts.artifact_key
    step_id         VARCHAR(64)  NOT NULL,    -- 写入此版本的 step 名称
    attempt         INT          NOT NULL,    -- 该 step 的第几次执行
    created_at      TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_psr_slot_rev  ON plugin_slot_revisions(session_id, slot_id, revision);
CREATE INDEX        idx_psr_artifact  ON plugin_slot_revisions(artifact_key);
CREATE INDEX        idx_psr_session   ON plugin_slot_revisions(session_id, slot_id);
```

`selected` 字段由 Go 在插入新 revision 时维护：新记录插入时置为 `TRUE`，同时将同 `(session_id, slot_id)` 的旧记录（`cardinality=single`）置为 `FALSE`；`list` 模式各项独立，`selected` 始终为 `TRUE`。

---

## 三、插件文件结构

每个插件的所有文件统一放在 `plugin/plugins/<plugin-id>/` 下：

```
plugin/plugins/<plugin-id>/
  plugin.yaml              # 注册元数据（必须）
  tools.py                 # 插件自定义工具函数（可选）
  scenario/
    scenario.md            # ChatAgent 意图识别指南（必须）
    state.yml              # 状态机 + step 执行 spec（必须；未来可降级）
    driver.md              # auto 模式推进策略（auto 模式必须存在）
  frontend/                # 插件自定义前端代码（可选；存在时覆盖自动渲染；build 时拷贝入镜像）
    index.tsx
```

### 3.1 plugin.yaml

```yaml
id: image-plugin
name: AI 图片生成
description: 帮助用户生成高质量图片，先优化提示词再生成图片

steps:
  - id: optimize_prompt
    label: 优化提示词
  - id: generate_image
    label: 生成图片

tool_scripts:
  - path: tools.py
    functions: [dalle_generate]

ui:
  tabs:
    - id: result
      label: 生成结果
      slots:
        - id: preview_images
          type: image
          cardinality: list      # 追加模式，每次生成为列表中的一项
          label: 生成图片

    - id: prompt
      label: 提示词
      slots:
        - id: optimized_prompt
          type: text
          cardinality: single    # 覆盖模式，selected 始终指向最新 revision
          label: 优化后的提示词
```

`cardinality` 语义：

| 值 | 展示行为 | Revision 行为 |
| --- | --- | --- |
| `single` | Slot 内始终展示一条内容 | 多次写入创建新 revision，`selected` 自动跟向最新 |
| `list` | Slot 内展示条目列表，追加而非覆盖 | 每个列表项独立记录一条 revision |

### 3.2 scenario/state.yml

状态机包含两个保留状态：
- `__start__`：虚拟入口，无对应 step，仅用于声明初始跳转目标，`plugin_loader` 解析后不会创建 SubAgent。
- `__end__`：虚拟终态，transitions 目标为 `__end__` 时表示流程结束；DriverAgent 裁决 `DONE` 即等价于转入此状态，Go 停止 auto loop 并发 `plugin_completed`。

```yaml
initial: __start__

transitions:
  __start__:
    - to: optimize_prompt
      condition: "冷启动，直接进入提示词优化"
  optimize_prompt:
    - to: generate_image
      condition: "提示词优化完成，可进入生图"
    - to: optimize_prompt
      condition: "用户对提示词不满意，重新优化"
  generate_image:
    - to: optimize_prompt
      condition: "用户不满意图片，重新优化提示词"
    - to: generate_image
      condition: "保持描述不变重新生图"
    - to: __end__
      condition: "用户满意，流程完成"

steps:
  optimize_prompt:
    prompt: |
      用户想生成一张图片。用户描述：{{user_input}}
      将描述优化为高质量英文图片生成 prompt，完成后调用 save_artifact('optimized_prompt', text)。
    tools: []
    outputs:
      - artifact_id: optimized_prompt
        content_type: text
        slot_id: optimized_prompt   # 写入 Tab:prompt / Slot:optimized_prompt（single）

  generate_image:
    prompt: |
      使用优化后的 prompt 生成图片：{{optimized_prompt}}
      调用 dalle_generate(prompt) 生成图片，完成后调用 save_artifact('image_url', url)。
    tools: [dalle_generate]
    inputs:
      - artifact_id: optimized_prompt
        required: true
    outputs:
      - artifact_id: image_url
        content_type: image
        slot_id: preview_images     # 追加到 Tab:result / Slot:preview_images（list）
```

### 3.3 scenario/scenario.md

向 ChatAgent 描述插件能力与意图识别规则。格式见 [`code.md C1`](./code.md#c1)。

### 3.4 scenario/driver.md

auto 模式下 Go 完成一个 Step 后调 DriverAgent，以其输出作为合成用户消息触发 ChatAgent。`driver.md` 是 DriverAgent 的 system prompt，规定评判策略（PASS / RETRY / DONE / FAIL）。

---

## 四、工具集（ChatAgent 工具扩展）

Plugin 机制在 ChatAgent 工具层做最小扩展，**不引入新的工具调用协议**。

### 4.1 ChatAgent Plugin 工具

| 工具 | 场景 | 输入 | 输出 |
| --- | --- | --- | --- |
| `trigger_<plugin_id>` | 冷启动（无活跃 session） | `user_input: str` | 触发成功提示；内部写 `task_created` 信号 |
| `advance_step` | 有活跃 session | `step_id: str`, `user_input: str` | 触发成功提示；内部写 `task_created` 信号 |

两个工具**均是 ChatAgent 的 stop tool**：工具调用成功后 ReAct 立即停止，不进入 summarize。

`trigger_<plugin_id>` 和 `advance_step` 内部均调用 `_trigger_plugin_step()`，执行两层校验：

1. **格式校验**：step_id 在状态机可达步骤中；user_input 非空。
2. **依赖状态校验**：从 DB 查依赖 artifact 的产出状态，按 required/optional 语义判断。

校验通过后，写入 `task_created` 信号（`agent_type='plugin_step'`），Go 识别并创建 Plugin Session Step。

### 4.2 `task_created` 信号（Plugin Step 专用字段）

Plugin Step 的 `task_created` 沿用 SubAgent 协议（见 SubAgent plan 3.1），`agent_type='plugin_step'`，`params` 中额外携带：

```json
{
  "task_id": "...",
  "agent_type": "plugin_step",
  "params": {
    "plugin_id": "image-plugin",
    "step_id": "optimize_prompt",
    "session_id": "ps-placeholder-uuid",
    "user_input": "...",
    "is_cold_start": true
  },
  "input_artifact_keys": [],
  "output_artifact_keys": ["optimized_prompt"]
}
```

`session_id` 在冷启动时为占位符（Go 会分配真实 ID 并替换）；热路径时为已有 session ID。

### 4.3 SubAgent 工具（Step 执行层，不变）

Step 执行时完全复用 SubAgent 工具：`save_artifact` / `get_artifact` / `list_artifacts`，加上 `plugin.yaml` 声明的插件专属工具（`tools.py` 中的函数）。

---

## 五、执行引擎

### 5.1 Python 层：无变化

- `/api/subagent/run` 端点：**直接复用**，不修改。
- SubAgent 框架（`runner.py`）：**直接复用**，不修改。
- Plugin Step 的提示词由 Go 将 `state.yml step.prompt` + 注入的 artifacts 拼好后通过 `objective` 字段下发，框架照常执行。

新增 Python 组件仅在 ChatAgent 工具层：

- `plugin/plugins/` 目录结构 + `plugin.yaml` 解析（`plugin_loader.py`）。
- `trigger_<plugin_id>` 和 `advance_step` 工具注册（`plugin_manager.py`）。
- 依赖状态校验（`_trigger_plugin_step`）。

### 5.2 Go 层：Plugin EventLoop

Go 处理 `task_created`（`agent_type='plugin_step'`）时走 Plugin 专属路径：

**冷启动路径**（`is_cold_start=true`）：

1. 查询该 conversation 是否已存在 `status='active'` 的 `plugin_sessions` 记录；若存在则拒绝，向主 SSE 返回 error。
2. 分配 plugin_sessions 记录（实际 ID 替换占位符），写 `plugin_session_steps`。
3. 按 `state.yml` 构造 Step 的完整 objective（注入 user_input + 前序 artifacts）。
4. 调 `createSubAgentTask`（通用函数，写 `sub_agent_tasks`）。
5. 立即 `go runSubAgent(task, resume=false)`（完全复用 SubAgent goroutine）。
6. 向前端发 `task_created` 通知（含 task_id 和 plugin_session_id）。

**Artifact 写入 Slot**（`routeToTaskSSE` 的 `artifact` 分支新增逻辑）：

当 SubAgent 产出 artifact 事件时，Go 检查该 artifact 的 `artifact_key` 是否在 `state.yml` 的 step outputs 中声明了 `slot_id`：

```
artifact 事件到达
  → 查 state.yml outputs[artifact_key].slot_id
  → 有 slot_id：
      → 查 plugin.yaml ui 找到 slot 定义（cardinality）
      → 计算新 revision = MAX(revision WHERE session_id=? AND slot_id=?) + 1
      → cardinality=single：将旧记录 selected=FALSE，再插入新记录 selected=TRUE
      → cardinality=list：计算 list_index = COUNT(WHERE session_id=? AND slot_id=?)，直接插入 selected=TRUE
      → 写 plugin_slot_revisions 记录
  → 原 artifact 事件照常推送到 Task SSE（不变）
```

Task SSE 的 `artifact` 事件结构不变，前端 Plugin Panel 通过订阅各 Step 对应的 Task SSE 获取 `artifact` 事件后，用 `artifact_key` 去匹配 `slot_id`，实时刷新对应 Slot 内容。

**Step 完成后推进**（`routeToTaskSSE` 的 `done` 分支新增逻辑）：

```
SubAgent done
  → 更新 sub_agent_tasks.status = 'succeeded'
  → 更新 plugin_session_steps.status = 'succeeded'
  → auto：调 DriverAgent → 以 judgment 合成用户消息 → 调 ChatAgent（新一轮）
  → manual：发 step_waiting 给前端 → 当轮 SSE 关闭
```

**auto 推进的 ChatAgent 调用**与用户主动发消息的调用逻辑完全一致，Go 只是将 DriverAgent 评判作为 `role=user` 消息注入对话历史。

### 5.3 Go Plugin Session 状态机

Go 维护 Plugin Session 的 step 状态，用于依赖校验兜底：

```
plugin_session_steps 查询：
  SELECT status FROM plugin_session_steps
  WHERE session_id=? AND step_id=? ORDER BY attempt DESC LIMIT 1
```

ChatAgent 的 `_trigger_plugin_step` 已做主路径依赖校验，Go 侧的检查作为防御性断言。

### 5.4 DriverAgent

`driver.md` 不存在时，禁止该插件使用 `auto` 模式。 即使用户配置auto，也忽略，不报错。

DriverAgent 输出裁决词：

| 裁决 | 含义 | Go 行为 |
| --- | --- | --- |
| `PASS` | 步骤通过，继续推进 | 合成「Step X completed, proceed.」→ ChatAgent |
| `RETRY` | 步骤需重试 | 合成「Step X result unsatisfactory, retry.」→ ChatAgent |
| `DONE` | 流程完成，无需继续 | 结束 auto loop，发 `plugin_completed` 给前端 |
| `FAIL` | 无法恢复的失败 | 更新 session status='failed'，发 error 给前端 |

### 5.5 human 模式（advance=true 的恢复路径）

前端 `advance=true` 时，Go 根据最后一个 step 状态决定行为：

| 上次 step 状态 | Go 行为 |
| --- | --- |
| `running`（心跳未超时） | 等待当前执行完成，不重复触发 |
| `interrupted` | 直接 `go runSubAgent(task, resume=true)`（跳过 ChatAgent） |
| `succeeded` | 合成「Step X completed. User confirmed. Please proceed.」→ ChatAgent |

---

## 六、对外接口

### 6.1 Plugin Session API

```
GET  /api/core/conversations/{conv_id}/plugin-sessions
     → plugin_sessions WHERE conversation_id=? ORDER BY created_at DESC
     → 含各 session 的 current_step_id 和 status

GET  /api/core/plugin-sessions/{session_id}
     → session 详情 + plugin_session_steps 列表（含 task_id 用于关联 artifacts）

GET  /api/core/plugin-sessions/{session_id}/slots
     → 返回 plugin_slot_revisions，按 slot_id 分组，每组只返回 selected=TRUE 的记录
     → 含 artifact_key / content_type / step_id / attempt，供前端 Panel 初始化渲染
```

`/slots` 接口用于前端 Panel 初始化（页面加载或刷新时拉取全量当前 Slot 内容）；运行时增量更新通过 Task SSE 的 `artifact` 事件完成。

### 6.2 Task SSE（直接复用 SubAgent Task SSE）

```
GET /api/core/tasks/{task_id}:stream
```

完全复用，前端 Task Center 按 plugin_session_id 分组展示各步骤任务卡片。

### 6.3 Plugin 信息接口

```
GET /api/core/plugins
     → 已加载插件列表（id, name, description, steps[{id, label}]）

GET /api/core/plugins/{plugin_id}
     → 插件详情（含 scenario.md 摘要、steps、当前 conversation 中的活跃 session）
```

---

## 七、前端 Plugin Panel

### 7.1 Panel 挂载位置

Plugin Panel 是对话视图中的一个独立区块，**始终挂载在当前会话 bot 最后一条消息之后**。一个会话只有一个活跃 Panel，对应唯一一个 `plugin_session`。

```
对话视图
  ├── [user] 帮我生成一张赛博朋克城市的图片
  ├── [bot]  好的，我来帮你优化提示词并生成图片…（ChatAgent 回复）
  └── [PluginPanel]  ← 始终吸附在 bot 最后一条消息后
        ├── Tab: 生成结果
        └── Tab: 提示词
```

### 7.2 数据流

```
页面加载 / 会话切换
  → GET /plugin-sessions/{session_id}/slots   （初始化全量 Slot 内容）

运行时（Step 执行中）
  → 前端已订阅各 Step 的 Task SSE（Task Center 现有逻辑）
  → 监听 artifact 事件：根据 artifact_key 匹配 slot_id（从 plugin.yaml ui 声明中查找）
  → 命中 slot_id 时，追加或覆盖对应 Slot 的展示内容
  → selected 始终指向最新写入，无需额外处理
```

前端无需订阅新的事件通道，**完全复用 Task SSE 的 `artifact` 事件**。

### 7.3 Panel 渲染模式

Plugin Panel 支持两种渲染模式，框架在加载插件时按优先级选择：

**模式一：插件自定义渲染（优先）**

插件提供 `frontend/index.tsx`，直接编排框架提供的基础组件（`SlotImage` / `SlotText` / `SlotFile` 等），自主决定布局、Tab 组织方式和交互细节。`plugin.yaml` 的 `ui` 声明在此模式下仍需存在，用于 Go 层写入 Slot Revision，但前端布局完全由 `index.tsx` 控制。

**模式二：框架自动渲染（降级）**

插件不提供 `frontend/index.tsx` 时，框架读取 `plugin.yaml` 的 `ui` 声明，按标准模板自动生成 Panel：TabBar + 每个 Tab 内按 Slot 顺序平铺内容区，无需插件编写任何前端代码。

```
frontend/index.tsx 存在？
  ├── 是 → 插件自定义渲染（完全控制布局）
  └── 否 → 框架按 plugin.yaml ui 声明自动渲染（标准 Tab + Slot 模板）
```

框架提供的基础组件（自定义渲染可直接使用）：

| 组件 | 用途 |
| --- | --- |
| `<SlotImage slotId="..." />` | 渲染 image 类型 Slot，自动处理 list / single |
| `<SlotText slotId="..." />` | 渲染 text 类型 Slot |
| `<SlotFile slotId="..." />` | 渲染 file 类型 Slot |
| `<useSlot(slotId)>` | Hook，获取 Slot 当前 selected revision 数据，供自定义渲染 |

### 7.4 `plugin.yaml` ui 声明的前端消费

`ui` 声明在两种模式下各有用途：

- **自动渲染**：前端通过 `GET /api/core/plugins/{plugin_id}` 获取 `ui` 声明，直接按 tabs / slots / cardinality 生成 Panel 骨架。
- **自定义渲染**：`ui` 声明由 Go 层消费（写 `plugin_slot_revisions` 时判断 cardinality），前端 `index.tsx` 通过 `useSlot(slotId)` Hook 订阅数据，不直接解析 `ui` yaml。

两种模式下，Slot 初始内容均来自 `GET /plugin-sessions/{id}/slots`，运行时增量更新均通过 Task SSE 的 `artifact` 事件完成。

---

## 八、实施顺序

Plugin 机制以 SubAgent 基础设施为前提，不重复其实施步骤。

1. **插件文件结构 + Loader**：
   - `plugin/plugins/` 目录约定。
   - `plugin_loader.py`：解析 `plugin.yaml` / `state.yml` / `scenario.md` / `driver.md`，startup 校验（`driver.md` 缺失时 auto step → error）。
   - `state.yml` StateMachine（`is_reachable` / `get_reachable_steps`）。

2. **ChatAgent Plugin 工具层**：
   - `plugin_manager.py`：`build_cold_start_tools()`（per-plugin `trigger_<id>`）和 `build_advance_tool(session)`。
   - `_trigger_plugin_step()`：两层校验 + 写 `task_created`（`agent_type='plugin_step'`）。
   - `chat_service.py` 集成：按 conversation 是否有活跃 session 注入工具，设置 stop_tools。

3. **数据层**：
   - 新增 `plugin_sessions` + `plugin_session_steps` + `plugin_slot_revisions` 表 + migration。
   - Go Plugin Session CRUD（`CreateSession` / `UpdateSessionStep` / `WriteSlotRevision`）。

4. **Go Plugin EventLoop**：
   - `task_created`（`agent_type='plugin_step'`）走 Plugin 路径：冷启动时检查 active session 唯一性约束，分配 session + step 记录，构造 objective，调 `createSubAgentTask`，`go runSubAgent`。
   - `routeToTaskSSE` 的 `artifact` 分支：检查 `slot_id` 绑定，写 `plugin_slot_revisions`，维护 `selected` 字段。
   - `routeToTaskSSE` 的 `done` 分支新增 Plugin 推进逻辑（auto → DriverAgent → ChatAgent；manual → step_waiting）。
   - `advance=true` 恢复路径（三种 step 状态的 Go 分支处理）。

5. **DriverAgent 集成**：
   - `driver_agent.py`：`evaluate_step()` 调 LLM 产出 PASS/RETRY/DONE/FAIL 裁决。
   - Go 侧裁决解析与推进分支。

6. **Plugin Session API**：`/plugin-sessions`、`/plugin-sessions/{id}`、`/plugin-sessions/{id}/slots` 三个端点；`/plugins` 接口在 `ui` 字段中返回完整 Tab/Slot 声明。

7. **前端 Plugin Panel**：
   - 框架提供基础 Slot 组件（`SlotImage` / `SlotText` / `SlotFile`）和 `useSlot` Hook。
   - `PluginPanel` 容器：优先加载插件的 `frontend/index.tsx`；不存在时按 `plugin.yaml` ui 声明自动渲染标准 Tab/Slot 模板。
   - 初始化时调 `/plugin-sessions/{id}/slots` 填充已有内容。
   - 复用 Task SSE 订阅，监听 `artifact` 事件，按 `artifact_key → slot_id` 映射实时更新 Slot。
   - `step_waiting` 事件触发「等待用户确认」UI 状态。
   - `plugin_completed` 事件触发插件完成通知。

8. **首个插件（image-plugin）端到端验证**：
   - `plugin/plugins/image-plugin/` 完整文件（含 `ui` 声明和 `slot_id` 绑定）。
   - curl 端到端测试（冷启动 → optimize_prompt → generate_image → DONE）。
   - 验证 Panel Tab 切换、Slot 实时刷新、list cardinality 追加行为。
