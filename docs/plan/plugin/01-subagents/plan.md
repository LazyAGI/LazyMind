# SubAgent 基础设施方案

> 为 ChatAgent 设计完整的 SubAgent 执行基础设施：数据层、工具集、事件流、auto/manual 两种驱动模式及前端实时展示。
>
> 配套文档（渐进式披露，按需展开）：
> - 示例 / 伪代码：[`code.md`](./code.md)
> - 端到端交互路径（文生图场景）：[`trace.md`](./trace.md)

## 阅读顺序

1. 先读「一、核心概念与设计原则」建立心智模型。
2. 「二~五」是各层设计契约（数据 / 事件 / 工具 / 执行引擎），实现前必读。
3. 需要看代码长什么样时，跳到 [`code.md`](./code.md)；想看全链路怎么跑通，读 [`trace.md`](./trace.md)。
4. 「六、对外接口」「七、中断与恢复」「八、实施顺序」是收尾与排期。

---

## 一、核心概念与设计原则

### 1.1 SubAgent

由 ChatAgent 在 ReAct 推理过程中通过 `create_subagent` 工具派生的自主执行单元。

- 每轮 ReAct 决策产生一个或一组**并行** SubAgent，执行完后进入下一轮决策。
- 目标明确（`agent_type` + `objective` + `params`），独立执行，可调用工具。
- 通过 `save_artifact` 显式输出成果。
- 生命周期：`pending → running → succeeded | failed | interrupted | canceled`。

### 1.2 全局 auto / manual 模式（Chat 级别开关）

| 模式 | `create_subagent` 行为 | 主 SSE | 后续决策 |
| --- | --- | --- | --- |
| **auto（同步）** | 阻塞，等待 SubAgent 完成后返回摘要字符串 | 全程开放，直到 ChatAgent 输出最终文本 | ChatAgent 收到结果后继续 ReAct，无需用户介入 |
| **manual（异步）** | 立即返回，SubAgent 后台执行 | 发完 `task_created` 后关闭 | 用户主动发消息时 ChatAgent 继续决策 |

差异完全由 `create_subagent` 工具实现层控制，LLM 不感知模式，也不需要为不同模式写不同提示词。

### 1.3 主 SSE vs Task SSE

两个完全独立的事件通道，auto / manual 均适用：

| 通道 | 内容 | 消费方 |
| --- | --- | --- |
| **主 SSE**（`POST /conversations:chat`） | ChatAgent 文本流 + `task_created` 通知 | 前端主消息框 |
| **Task SSE**（`GET /tasks/{id}:stream`） | `task_start / progress / artifact / done / error` | 前端右侧 Task Center 面板 |

前端收到主 SSE 中的 `task_created`（含 task_id）后，立即订阅对应 Task SSE，在右侧面板实时渲染进度与 artifact。

### 1.4 设计原则：大模型不感知任何 ID

**所有工具的参数和返回值中，LLM 不接触任何 UUID / 数字 ID。** 任务和成果通过以下方式引用：

- **名称**：任务标题（如 `"素材收集"`）、artifact key（如 `"style_refs"`）。
- **位置序号**：`"第2个SubAgent"`、`"第3步的结果"`。
- **类型**：`"image_generation 任务"`。

工具实现层持有当前 session 上下文（conversation_id、task 序列），负责将引用解析为数据库 ID，LLM 永远不感知解析过程。

### 1.5 设计原则：算法层不反向依赖后端

算法层（Python）不直接持有 core 业务库连接，也不硬编码 core 表结构。任务 / 成果状态读写二选一：

- **HTTP 优先**：通过 `core_api_client` 调 core 暴露的内部端点（如 auto 轮询任务状态）。
- **连接信息随请求下发**：当确需算法层直接读写 sub_agent 相关表时（SubAgent 框架持久化 `sub_agent_steps`、读写 artifact），**由 Go 在调用 `/api/subagent/run` 时把数据库连接信息（DSN）放进请求体下发**。依赖方向仍是 Go → Python。

> DSN 经 `lazymind.common.postgres.normalize_postgres_connection_url` 规整后使用；连接随请求创建、用完释放，不做全局单例缓存，不落 LLM 上下文、不写日志明文。

### 1.6 mode 全链路透传（前置改动）

`auto` / `manual` 是 Chat 级别开关，当前链路尚无此字段，需新增透传（缺省 `auto`）：

```
FE(body.mode)
  → Go ChatConversations 读取 raw["mode"]
  → buildChatRequestBody 写入 body["mode"]
  → /api/chat/stream 新增 mode 入参
  → handle_chat(mode=...)
  → agentic_config['mode'] = mode   ← create_subagent 读取处
```

---

## 二、数据层（3 张新表）

无需修改现有 `chat_histories`（多轮对话模型，语义不匹配）。

### 2.1 `sub_agent_tasks`

```sql
CREATE TABLE sub_agent_tasks (
    id                   VARCHAR(36)  PRIMARY KEY,
    conversation_id      VARCHAR(36)  NOT NULL,
    trigger_history_id   VARCHAR(36),              -- 触发该任务的消息 ID（同批任务共享）
    seq_in_conversation  INT          NOT NULL,    -- 本对话中的创建序号（1-based，供"第N个"解析）
    agent_type           VARCHAR(64)  NOT NULL,
    title                VARCHAR(255) NOT NULL,
    mode                 VARCHAR(8)   NOT NULL,     -- 'auto' | 'manual'
    status               VARCHAR(16)  NOT NULL DEFAULT 'pending',
    progress_pct         INT          NOT NULL DEFAULT 0,
    current_phase        TEXT,
    estimated_sec        INT,
    last_heartbeat       TIMESTAMP    NOT NULL DEFAULT NOW(),
    workspace_path       VARCHAR(512) NOT NULL,
    input_artifact_keys  JSONB        NOT NULL DEFAULT '[]',  -- key 名列表，不含 task_id
    output_artifact_keys JSONB        NOT NULL DEFAULT '[]',
    create_user_id       VARCHAR(255),
    created_at           TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sat_trigger ON sub_agent_tasks(trigger_history_id);
CREATE INDEX idx_sat_status  ON sub_agent_tasks(status, last_heartbeat);
CREATE UNIQUE INDEX uq_sat_conv_seq ON sub_agent_tasks(conversation_id, seq_in_conversation);
```

**`seq_in_conversation` 并发赋值约束**：同一轮 ReAct 可能并行 spawn 多个 SubAgent，多个 `task_created` 几乎同时到达 Go，必须在事务内串行分配，二选一：

- 事务内 `SELECT COALESCE(MAX(seq_in_conversation),0)+1 FROM sub_agent_tasks WHERE conversation_id=? FOR UPDATE`；或
- 使用 per-conversation 数据库序列 / 应用层按 conversation_id 加锁。

`uq_sat_conv_seq` 作为兜底，防止并发竞态写入重复序号。

### 2.2 `sub_agent_steps`

SubAgent 执行过程的 ReAct 步骤序列，用于断点恢复时重建 LLM 上下文。每行对应一步（assistant 推理 + tool_calls 一行，tool_results 一行）。

```sql
CREATE TABLE sub_agent_steps (
    id          VARCHAR(36)  PRIMARY KEY,
    task_id     VARCHAR(36)  NOT NULL REFERENCES sub_agent_tasks(id),
    seq         INT          NOT NULL,       -- 0-based，ReAct 步骤序号
    role        VARCHAR(16)  NOT NULL,       -- 'assistant' | 'tool'
    content     JSONB        NOT NULL,
    -- role='assistant': {"text": "...", "tool_calls": [{"id": "call_xxx", "name": "...", "args": {...}}]}
    -- role='tool':      {"tool_results": [{"tool_call_id": "call_xxx", "name": "...", "result": "..."}]}
    created_at  TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sas_task ON sub_agent_steps(task_id, seq);
```

**tool_call_id 配对约束（断点恢复正确性依赖）**：重建 LLM messages 时，OpenAI 风格要求每个 `role=tool` 消息的 `tool_call_id` 能匹配前序 `role=assistant` 的 `tool_calls[].id`，否则模型 API 报错。因此 assistant 行须存 tool_call 的 `id`，tool 行须存对应 `tool_call_id`；resume 时按 `seq` 回放并校验配对，发现孤儿 tool_result 行则丢弃该步并从最近一个完整 assistant 边界重放。

### 2.3 `sub_agent_artifacts`

```sql
CREATE TABLE sub_agent_artifacts (
    id            VARCHAR(36)  PRIMARY KEY,
    task_id       VARCHAR(36)  NOT NULL REFERENCES sub_agent_tasks(id),
    artifact_key  VARCHAR(64)  NOT NULL,
    content_type  VARCHAR(32)  NOT NULL,
    -- content_type 取值：
    --   text      → value: {"text": "..."}
    --   json      → value: {"data": {...}}
    --   image     → value: {"url": "https://...", "path": "relative/to/workspace"}
    --   file      → value: {"filename": "x.pdf", "path": "...", "size": 1024}
    --   file_list → value: {"paths": ["a.jpg", "b.jpg"]}  （均为 workspace 相对路径）
    value         JSONB        NOT NULL,
    seq           INT          NOT NULL DEFAULT 1,  -- 同一 key 多次追加时递增（如逐张生图）
    created_at    TIMESTAMP    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_saa_task_key ON sub_agent_artifacts(task_id, artifact_key, seq);
```

**Key 固定、数量不定（重要约定）**：`output_artifact_keys` 创建时即固定声明（如 `["images", "style_keywords"]`），**LLM 不会在执行中临时新增 key**。单 key 的产出数量事先不确定，通过两种方式表达：

1. **多行追加**：同一 `artifact_key` 多次 `save_artifact`，每次插一行、`seq` 递增（适合逐条产出、希望前端流式追加，如逐张生图）。
2. **list 型 value**：`content_type=file_list` 时 `value.paths` 是变长列表（适合一次性产出一批文件）。

二者可组合。done 前完整性校验只检查"每个声明的 key 是否至少产出过 1 行"，**不检查具体条数**（见 4.x 框架职责）。

**路径约定**：所有 `path` 字段存相对于 `workspace_path` 的相对路径。Go 访问文件时拼接完整路径；前端通过文件服务接口读取。

---

## 三、事件流与事件协议

### 3.1 Python→Go 事件协议扩展（承重墙，必须先做）

**现状**：ChatAgent 的原始事件（`text` / `tool_calls` / `tool_results`）在 **Python 侧**就被 `AgentEventFrameTranslator.feed()` 翻译成固定结构帧 `{code, msg, data:{think, text, sources, status}, cost}`，再经 SSE 发给 Go。Go 的 `UpstreamStreamChunk` 只反序列化 `data.{text, think, sources, status}`，**没有 `tag` 通道**；translator 对未知 tag（如 `task_created`）直接返回空帧丢弃。

因此"主 SSE 携带 `tag`、Go `switch ev.Tag` 拦截"在现有协议中不成立，必须新增结构化通道：

- **Python（`event_translator.py`）**：`feed()` 新增分支，`tag == 'task_created'` / `'heartbeat'` 时产出携带 `extra={'task_created': {...}}` 或心跳占位的帧（复用 `_stream_frame(extra=...)`），而非丢弃。
- **Python（`chat_service.py` / `response_payload`）**：确保 `data` 能携带 `task_created` 子对象（透传 translator 的 extra）。
- **Go**：`UpstreamStreamChunk` 新增 `TaskCreated *TaskCreatedEvent json:"task_created,omitempty"`；在**现有 upstream 消费循环**（`streamSingleAnswer` 的 `for d := range ch`）识别 `d.TaskCreated != nil`，而非另起 `processMainSSEEvent`。

`task_created` 事件结构见 [code.md C2](./code.md#c2)；Go 拦截逻辑见 [code.md C4](./code.md#c4)。

### 3.2 Redis 流与辅助 key

| Redis Key | 类型 | 存储内容 | 消费方 | 说明 |
| --- | --- | --- | --- | --- |
| `rag/subagent/stream:{task_id}` | LIST | Task SSE 事件（task_start / progress / artifact / done / error） | 前端 Task Center | 过期 2 小时；支持 offset 回放 + tail |
| `rag/subagent/status:{task_id}` | HASH | 任务最新状态快照（status / progress / current_phase） | `:status` 查询、扫描 | 派生缓存，丢失可从 DB 重建 |

复用现有 `watchChatChunks` / `getChatChunksFrom` 的 LIST + offset tail 模式实现回放，不另造轮子。Task SSE 事件格式见 [code.md C3](./code.md#c3)。

### 3.3 Redis 非权威：DB 为准，Redis 为加速

**关键原则**：Redis 仅作流式加速缓存，**不是权威数据源**。所有 Task SSE 事件由 Go 转发的同时已同步落库（`sub_agent_tasks` 状态 + `sub_agent_artifacts` 成果）。Redis 流过期或重启导致数据丢失时，不影响最终一致性。

**Task SSE 重连协议（DB 补历史 → 再订阅 Redis）**——前端订阅 `:stream` 或断线重连时，服务端：

1. **先查 DB 补历史**：读 `sub_agent_tasks`（状态/进度）+ `sub_agent_artifacts`（成果，ORDER BY artifact_key, seq），合成 `task_start` + 历史 `progress` + 历史 `artifact` 先行下发，保证完整快照。
2. **若已终态**（succeeded/failed/interrupted）：补完后发 `done`/`error` + `[DONE]`，不订阅 Redis。
3. **若仍 running**：补完快照后从 Redis 流当前尾部 tail 增量；Redis key 不存在（过期/重启）则退化为对 DB 轮询直到终态。

即便 Redis 完全不可用，Task Center 仍能从 DB 恢复全部已落库成果，仅丢失尚未落库的瞬时增量（下次 DB 快照补齐）。

SubAgent 的**完整执行步骤**持久化到 `sub_agent_steps`，无需额外 Redis buffer；恢复时直接查表重建 LLM 上下文。

---

## 四、工具集

所有工具的参数和返回值均不含任何 ID；实现层通过 session 上下文（ChatAgent）或注入的 task_id（SubAgent）自动解析。

### 4.1 ChatAgent 工具

| 工具 | 输入 | 输出 |
| --- | --- | --- |
| `todo_writer` | `todos: list[{title, description}]` | `"Plan saved (N steps). Proceeding with step 1."` |
| `create_subagent` | `agent_type`, `title`, `objective`, `params`, `input_artifact_keys`, `output_artifact_keys`（可选 `tools`、`resume`） | auto：阻塞至终态返回摘要串；manual：立即返回后台执行提示。详见 4.3 与 [code.md C1](./code.md#c1) |
| `list_subagents` | `status: str = None`（过滤 pending/running/succeeded/failed/interrupted） | 自然语言列表，如 `"1. 任务指令优化（task_optimization, succeeded）\n2. 素材收集（…, running, 60%）"` |
| `get_subagent_status` | `task_ref: str`（标题 / "第N个" / 类型名） | 状态摘要，如 `"素材收集（running）：已完成 60%，正在分析风格特征，预计还需 30 秒。"` |
| `list_artifacts` | `task_ref: str` | key 摘要，如 `"素材收集任务共有 2 个成果：style_refs（file_list）、style_keywords（json）。"` |
| `get_subagent_artifacts` | `task_ref: str`, `keys: list[str] = None`（不传返回全部） | 各 artifact 结构化描述：文件返回路径，文本返回内容摘要 |

### 4.2 SubAgent 工具

| 工具 | 输入 | 输出 / 说明 |
| --- | --- | --- |
| `todo_writer` | 同 ChatAgent | 同 ChatAgent |
| `save_artifact` | `key`, `value`（文件类型传本地绝对路径，框架自动复制到 workspace 并转相对路径）, `content_type`（text/json/image/file/file_list） | `"Artifact '{key}' saved."`；框架在 emit `done` 前校验每个声明 key 至少 save 过 1 行 |
| `get_artifact` | `key`, `task_ref: str = None`（不传则当前对话按 key 取最新一条；可传标题 / "第N个" / 类型名精确定位） | artifact 内容串（文本 / 文件路径 / JSON 描述） |
| `list_artifacts` | `task_ref: str = None`（不传列出当前对话所有任务） | 摘要串，如 `"可用成果：\n- 第1步（任务指令优化）: optimized_task（text）\n- 第2步（素材收集）: style_refs（file_list）、style_keywords（json）"` |
| `get_tool_result` | `call_index: int`（0-based） | 该步工具调用原始返回值串。**框架需新增**按调用序号索引工具结果的能力（当前 `FunctionCall` 仅在 `locals['_lazyllm_agent']['workspace']['history']` 存历史，无此接口），可基于 workspace history 或 `sub_agent_steps` 实现 |

### 4.3 工具注册规则与 `create_subagent` 行为

**工具描述驱动行为**：所有工具通过工具描述指导 LLM 决策，复用现有 ReAct 上下文组织方式，无需特殊系统提示词。`create_subagent` 描述中应说明适合创建 SubAgent 的场景（任务复杂、耗时长、需独立工具调用链）。

**task_id 生成与事件实时性**：`task_id` 由工具内部 `uuid.uuid4()` 生成；通过 `_write_agent_data('task_created', ...)` 写入 `FileSystemQueue`，`StreamCallHelper` 的 drain 循环（`_adrain`，future 未完成时持续 flush）持续取数并经 translator 发往 Go。子线程经 `globals._init_sid(sid)` 继承主请求 sid，与 drain 共用同一队列桶，因此**即使工具阻塞轮询**，`task_created` 仍能实时到达 Go。

**队列桶隔离**：SubAgent 不能复用 ChatAgent 的 `FileSystemQueue`（否则其 LLM 输出会混入主 SSE）。`FileSystemQueue` 按 `globals._sid` 分桶，故 SubAgent **必须由 Go 独立调用 `/api/subagent/run`，且该请求使用独立 sid（约定 `sid = task_id`）**，事件走 Task SSE。

**auto / manual 分支**（完整实现见 [code.md C1](./code.md#c1)）：

| 模式 | 工具行为 | 对话后续 |
| --- | --- | --- |
| **auto** | 轮询 core 内部状态端点等待终态，return 摘要，LLM 继续 ReAct | 当前轮次继续，直到 LLM 无新 SubAgent 且输出最终文本 |
| **manual** | 立即 return（不等执行），Go 在主 SSE 关闭后后台调 `/api/subagent/run` | 当前 SSE 关闭，等待用户下一次输入构成新轮次 |

**auto 阻塞与连接超时（必须处理）**：Go 调上游 `/api/chat/stream` 的 HTTP client 默认 `Timeout = 10min`（`chat.go`），而 auto 阻塞期间主 SSE 静默。长任务超过 10 分钟会被掐断。处理：

1. **调大上游 chat client 超时**（或 0 表不限、依赖 ctx 取消），与 SubAgent 最长预期时长匹配。
2. **心跳保活**：轮询期间周期性（如每 15s）`_write_agent_data('heartbeat')`；translator 把 `heartbeat` 翻译成 SSE 注释行 / 空 data 帧（不进入文本内容）。心跳帧的 translator 分支需与 3.1 的 `task_created` 分支一并实现，否则同样被丢弃。

**条件工具注册**：查询类工具（`list_subagents` / `get_subagent_status` / `list_artifacts` / `get_subagent_artifacts`）在 auto / manual **都注册**，但**仅当对话内已存在 SubAgent 时生效**（无任务时不出现在工具列表）。

> auto 模式 `create_subagent` 返回话术会引导 LLM 调 `get_subagent_artifacts`，故 auto 也必须注册，否则引导到不存在的工具。

Go 每次组装 ChatAgent 请求时通过 `SELECT COUNT(*) FROM sub_agent_tasks WHERE conversation_id=? > 0` 判断，把 `has_subagents` 标志随 `/api/chat/stream` 请求体下发，Python 据此决定是否注册查询类工具。

---

## 五、执行引擎

### 5.1 Python `/api/subagent/run`（SSE）

**由 Go 调用**（auto / manual 均如此）。Go 收到 `task_created` 建库后立即调此端点。请求用独立 sid（`sid = task_id`）获得独立队列桶；请求体携带 `db_dsn`，供 SubAgent 框架持久化步骤 / 读写 artifact。

```
POST /api/subagent/run
{
    "task_id":             "uuid",       ← create_subagent 生成；同时作为本请求 sid
    "agent_type":          "image_generation",
    "objective":           "...",
    "params":              {"count": 4},
    "input_artifact_keys": ["optimized_prompt"],
    "output_artifact_keys": ["images"],  ← key 固定声明；每个 key 产出条数不限
    "workspace_path":      "/data/subagent/user-xyz/task-uuid/",
    "db_dsn":              "postgresql://...",  ← Go 下发，normalize 后使用，请求结束释放
    "resume":              false         ← true 时从 sub_agent_steps 加载恢复
}
→ SSE 流（task_start / progress / artifact / done / error）
```

### 5.2 Python SubAgent 框架层职责

1. **执行上下文注入**：`task_id` / `conversation_id` / `workspace_path` / `db_dsn` 注入工具实现层（不暴露给 LLM）。
2. **执行步骤持久化**：ReAct 每步完成后同步写 `sub_agent_steps`（assistant 存带 `id` 的 tool_calls，tool 存带 `tool_call_id` 的 tool_results），确保中断时已有步骤不丢失。
3. **`done` 前完整性校验**：检查 `output_artifact_keys` 每个 key 是否**至少产出过 1 行** artifact（不校验条数）；有 key 完全缺失则 emit `error`（含缺失 key 列表），不发 `done`。
4. **断点恢复**：`resume=true` 时查 `sub_agent_steps ORDER BY seq`，按 `tool_call_id` 校验配对后重建 LLM messages，从断点继续，已完成步骤不重复执行。

### 5.3 Go 事件路由职责

Go 处理两条 SSE：主 ChatAgent SSE 与各 SubAgent 的 `/api/subagent/run` SSE。核心逻辑（拦截 `task_created`、启动并消费 SubAgent SSE、先落 DB 再写 Redis）见 [code.md C4](./code.md#c4)。

**关键点**：

- 主 SSE 不再有 `task_event`，也不依赖 `ev.Tag`；`task_created` 由 translator 翻译成 `data.task_created` 后 Go 在 upstream 消费循环识别。
- SubAgent 事件经独立 SSE → Go goroutine 消费 → **先落 DB（权威）再写 Redis（实时 tail）** → Task SSE 转发。
- `create_subagent` 通过轮询 core 内部状态端点感知终态，不直连 DB。
- 执行步骤由 Python 框架直接写 `sub_agent_steps`，Go 不处理。

> 端到端串联见 [`trace.md`](./trace.md)。

---

## 六、对外接口（Task Center API）

```
GET  /api/core/conversations/{conv_id}/tasks
     → sub_agent_tasks WHERE conversation_id=? ORDER BY seq_in_conversation
     → 按 trigger_history_id 分组，含各任务最新状态

GET  /api/core/tasks/{task_id}
     → 任务详情 + 已完成 sub_agent_steps 步数

GET  /api/core/tasks/{task_id}/artifacts
     → sub_agent_artifacts WHERE task_id=? ORDER BY artifact_key, seq（支持分页）

GET  /api/core/tasks/{task_id}:stream   （重连协议：DB 补历史 → Redis tail，见 3.3）
     → 1. 先查 DB 合成 task_start + 历史 progress + 历史 artifact 先行下发（完整快照）
     → 2. 已终态：补完后发 done/error + [DONE]，不订阅 Redis
     → 3. 仍 running：从 Redis 流尾部 tail；key 不存在则退化为 DB 轮询直到终态

GET  /internal/subagent/tasks/{task_id}   （内部端点，供 Python auto 轮询）
     → 优先读 Redis rag/subagent/status:{task_id}，miss 回落查 sub_agent_tasks
     → {status, progress, current_phase, summary}
```

---

## 七、中断与恢复

**Go 启动时扫描**：`running` 且 `last_heartbeat` 超过 5 分钟 → 标为 `interrupted`（见 [code.md C5](./code.md#c5)）。

**用户触发恢复**（auto / manual 均支持）：用户发消息 → ChatAgent 调 `list_subagents("interrupted")` 决策恢复 → 调 `create_subagent(resume=True)` → 工具调 `/api/subagent/run`（`resume=true`）→ Python 框架查 `sub_agent_steps`、按 `tool_call_id` 校验配对后重建上下文 → 从断点继续。详见 [`trace.md`](./trace.md) 末节。

---

## 八、实施顺序

0. **前置链路改动（承重墙，必须先做）**：
   - `mode` + `has_subagents` 全链路透传（见 1.6 / 4.3）。
   - 事件协议扩展：translator 新增 `task_created` / `heartbeat` 帧分支；`UpstreamStreamChunk` / `ChatChunkResponse` 加 `task_created` 字段并透传；Go upstream 消费循环识别（见 3.1）。
   - 上游 chat client 超时调整 + 心跳保活（见 4.3）。
1. **数据层**：三张表 + ORM + migration；Go subagent 包（CreateTask 含 seq 事务分配 / UpdateStatus / SaveArtifact / LoadArtifacts / MarkInterrupted）；core 内部端点 `GET /internal/subagent/tasks/{id}`。
2. **Python SubAgent 端点**：`/api/subagent/run` SSE（sid=task_id，使用 `db_dsn`）+ SubAgent 工具集 + 每步写 `sub_agent_steps`（含 tool_call_id）+ done 前完整性校验。
3. **Python ChatAgent 工具**：`create_subagent`（auto HTTP 轮询 + 心跳 / manual 立即返回）+ 查询类工具；不含 ID，仅在已有 SubAgent 时生效。
4. **Go 事件路由**：upstream 识别 `task_created` → 事务分配 seq 建记录 → 写 Redis status → 透传前端 → `go runSubAgent`（下发 db_dsn）；消费 SubAgent SSE 先落 DB 再写 Redis。
5. **Task Center API**：4 个对外端点（含 `:stream` DB 补历史 → Redis tail）+ 内部状态端点。
6. **前端 Task Center 面板**：`task_created` 触发订阅 Task SSE（先 DB 补历史再 tail）；进度条更新；artifact 按 `(key, seq)` 去重逐条追加（图片缩略图网格、文本展开）。
7. **刷新恢复**：主 SSE 复用现有 resumeChat + Task SSE DB-first 重连。
8. **中断恢复**：MarkInterrupted + resume 参数 + `sub_agent_steps` tool_call_id 配对重建上下文。






