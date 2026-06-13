# SubAgent 方案 · 交互路径（文生图场景）

> 配合 [`plan.md`](./plan.md) 阅读，示例代码见 [`code.md`](./code.md)。本文件走通一个端到端场景，用于验证各组件协作是否自洽。

**场景**：用户发送「帮我画一张漫画风格的森林场景」，**auto 模式**。

**设计原则体现**：只有真正复杂 / 耗时的步骤（生图）才用 SubAgent；其余步骤（搜索素材、优化 prompt）由 ChatAgent 通过普通工具调用或 LLM 推理直接完成。

> 说明：下文示意里 ChatAgent 的原始事件（`text` / `tool_calls` / `tool_results`）在 **Python 侧**就由 `AgentEventFrameTranslator` 翻译成标准帧再发往 Go，Go 收到的是成品帧（不含 `tag`）。为便于理解仍保留原始 tag 形态标注。

---

## T=0：用户发送消息，建立连接

```
FE → Go:   POST /api/core/conversations:chat  (SSE)
Body: { conversation_id: "conv-001", input: [...], mode: "auto" }

Go → DB:   读取 conversations + 最近 chat_histories（按 seq）
Go → DB:   INSERT/UPDATE chat_histories {id:'h-001', seq}；Redis 标记 status='generating'
Go → Algo: POST /api/chat/stream  (SSE)
Body: { query: "帮我画一张漫画风格的森林场景", history: [...], mode: "auto",
        has_subagents: false, tools: ["web_search", "create_subagent", "todo_writer", ...] }
```

---

## T=1：ChatAgent ReAct — 普通工具调用（不用 SubAgent）

ChatAgent LLM 推理 + 普通工具调用，Python 侧 translator 翻译后经主 SSE 流式输出：

```
ChatAgent 原始事件（Python 侧）:
{"tag":"text",         "delta":"好的，我来帮您生成一张漫画风格的森林场景图片。首先搜索一些参考素材。"}
{"tag":"tool_calls",   "tool_calls":[{"name":"web_search","args":{"query":"吉卜力风格森林场景参考图"}}]}
{"tag":"tool_results", "tool_results":[{"name":"web_search","result":"...搜索结果..."}]}
{"tag":"tool_calls",   "tool_calls":[{"name":"web_search","args":{"query":"漫画森林场景风格关键词"}}]}
{"tag":"tool_results", "tool_results":[{"name":"web_search","result":"...关键词列表..."}]}
{"tag":"text",         "delta":"素材已收集，正在优化图像生成提示词..."}

translator 翻译（Python 侧）→ Go → FE（主SSE）:
  text          → {"text":"好的，我来帮您..."}
  tool_calls/   → {"text":"```web_search\n...\n```"}   （工具调用渲染为 markdown 代码块）
  tool_results
```

Go 仅透传翻译后的标准帧给前端主 SSE，不做二次翻译。

---

## T=2：ChatAgent ReAct — 决定创建生图 SubAgent

LLM 综合搜索结果优化 prompt 后，决定创建生图 SubAgent（耗时长、逐张输出，适合 SubAgent）。`create_subagent` 工具写 `task_created`（完整实现见 [code.md C1](./code.md#c1)）：

```python
task_id = str(uuid.uuid4())  # = "a1b2c3d4-..."
_write_agent_data('task_created', task_id='a1b2c3d4-...',
                  title='生图', agent_type='image_generation', mode='auto',
                  objective='根据优化后的提示词生成4张漫画风格森林场景图片',
                  params={'count': 4, 'prompt': 'A breathtaking manga-style forest...'},
                  input_artifact_keys=[], output_artifact_keys=['images'],  # key 固定，张数不定
                  tools=['image_gen_api'])
# → FileSystemQueue → astream drain → translator → 主 SSE → Go
```

Go 在 upstream 消费循环识别 `d.TaskCreated != nil`（见 [code.md C4](./code.md#c4)）：

```
Go → DB:   事务内分配 seq_in_conversation（FOR UPDATE / 序列），INSERT INTO sub_agent_tasks
           {id:'a1b2c3d4-...', conversation_id:'conv-001', trigger_history_id:'h-001',
            seq_in_conversation:1, title:'生图', agent_type:'image_generation',
            mode:'auto', status:'pending',
            output_artifact_keys:'["images"]',
            workspace_path:'/data/subagent/user-xyz/a1b2c3d4/'}
Go → Redis: HSET rag/subagent/status:a1b2c3d4 {status:"pending",progress:0}
Go → FE（主SSE）:
  data: {"task_created":{"task_id":"a1b2c3d4-...","title":"生图","status":"pending"}}
Go:  立即 go runSubAgent(task)  ← 启动 SubAgent（带 db_dsn，sid=task_id）

FE:  右侧 Task Center 出现"生图"任务卡片（pending）
FE:  立即订阅 GET /api/core/tasks/a1b2c3d4-...:stream（先 DB 补历史再 tail Redis）
```

与此同时 `create_subagent` 进入轮询等待（HTTP 查内部状态端点 + 心跳保活）。

---

## T=3：Go 独立执行 SubAgent（独立 HTTP，独立队列桶）

`runSubAgent` goroutine 调 `/api/subagent/run`，SubAgent 用独立 sid（=task_id）获得独立 `FileSystemQueue` 桶：

```
Go → Python:  POST /api/subagent/run
Body: { task_id: "a1b2c3d4-...", db_dsn: "postgresql://...", resume: false }  # sid=task_id

Python SubAgent（独立请求上下文，独立 sid → 独立队列桶）:
  - 用 db_dsn 连库读取 task 参数（objective / artifact_keys / workspace_path）
  - 内部 ReactAgent 跑 image_gen_api 工具
  - 每步写 sub_agent_steps（含 tool_call_id 配对）
  → SSE 输出：
    {"type":"task_start"}
    {"type":"progress","progress":10,"current_phase":"开始生成第1张...","estimated_sec":60}
    {"type":"artifact","artifact_key":"images","seq":1,"content_type":"image","value":{...}}
    {"type":"progress","progress":35,"current_phase":"已完成第1张，生成第2张..."}
    {"type":"artifact","artifact_key":"images","seq":2,...}
    {"type":"artifact","artifact_key":"images","seq":3,...}
    {"type":"artifact","artifact_key":"images","seq":4,...}
    {"type":"done","status":"succeeded","summary":"已生成4张图片"}
```

Go 消费这条 SSE（`runSubAgent` goroutine 内，先落 DB 再写 Redis）：

```
task_start  → DB: UPDATE sub_agent_tasks status='running', last_heartbeat=NOW()
            → Redis: RPUSH rag/subagent/stream:a1b2c3d4 <task_start>
            → FE（Task SSE）

progress(每次) → DB: UPDATE progress_pct=N, current_phase=...
            → Redis RPUSH → FE（Task SSE）

artifact(每张) → DB: INSERT sub_agent_artifacts {artifact_key:'images', seq:N, ...}
            → Redis RPUSH → FE（Task SSE）
            FE: 每张图到达立刻追加缩略图，不等全部完成

done        → DB: UPDATE status='succeeded', progress_pct=100
            → Redis RPUSH → FE（Task SSE）
            FE: 任务卡片 ✓，4张图全部展示
```

**注意**：整个过程中主 SSE 保持连接但无业务文本（ChatAgent 工具阻塞在轮询）。drain 循环空转 + 周期性 heartbeat 帧保活，配合调大的 chat client 超时，连接不断。

---

## T=4：ChatAgent 继续，输出最终文本

Go 写入 `status='succeeded'` 后，`create_subagent` 轮询 break，return 摘要：

```
工具 return:
"任务'生图'已完成。产出 key：images。如需完整内容可调用 get_subagent_artifacts('生图')。"

ChatAgent LLM 收到 tool_result → 决策无需再 spawn → 输出最终文本（Python 侧 translator 翻译为标准帧）

Go → FE（主SSE）:
  data: {"text":"图片已生成完毕！以下是4张漫画风格森林场景图片："}
  data: [DONE]

Go → DB:  UPDATE chat_histories SET result='...' WHERE id='h-001'；Redis status='completed'
Go → DB:  UPDATE conversations SET updated_at=NOW(), chat_times=chat_times+1

FE:  主消息框显示最终文本；Task Center 生图任务 ✓，4张图已展示
```

---

## T=5：刷新后状态恢复

**主 SSE 中途断开**（Redis 中 `chat_histories` 对应状态为 `generating`）：复用现有 resume 机制（`/api/v1/conversations:resumeChat`，见现有 `getGeneratingHistoryIDs` / `watchChatChunks`）。Go 检测到 `sub_agent_tasks` 有 running 任务，对应 Task SSE 走"DB 补历史 → Redis tail"重连；主 SSE 等当前 ChatAgent 轮次完成后继续。

**页面刷新后恢复 Task Center**（DB 为准，不依赖 Redis）：

```
FE → Go:  GET /api/core/conversations/conv-001/tasks
Go → DB:  SELECT * FROM sub_agent_tasks WHERE conversation_id='conv-001' ORDER BY seq_in_conversation
Go → DB:  SELECT * FROM sub_agent_artifacts WHERE task_id IN (...) ORDER BY artifact_key, seq
Go → FE:  {tasks: [{title:'生图', status:'succeeded',
                    artifacts:[{artifact_key:'images',seq:1,content_type:'image',value:{url:'...'}},
                               {artifact_key:'images',seq:2,...}, ...]}]}

FE:  Task Center 恢复展示，缩略图重新渲染
FE:  对仍 running 的任务再订阅 Task SSE（先 DB 补历史再 tail Redis）
```

---

## Manual 模式差异

**同一场景，mode="manual"**：ChatAgent 调用 `create_subagent` 后**立即 return**（不轮询）：

```
ChatAgent → Go（主 SSE，经 translator 翻译）:
  data: {"task_created":{"task_id":"task-1","title":"生图","mode":"manual","status":"pending"}}
  data: {"text":"生图任务已开始后台执行，请在右侧任务中心查看进度。"}

Go 处理 task_created（upstream 消费循环识别 d.TaskCreated）：
  → 建 DB 记录（事务内分配 seq）
  → FE（主SSE）: task_created + 文本 + [DONE]（主 SSE 关闭）
  → go runSubAgent(task)  ← 与 auto 共用逻辑（带 db_dsn，sid=task_id）
  → SubAgent 事件走 Task SSE，不依赖主 SSE
```

SubAgent 完成后 Go **不自动**继续 ChatAgent，用户主动发消息才触发下一轮：

```
FE → Go:  POST /api/core/conversations:chat   Body: { input:[{text:"继续"}], mode:"manual" }
Go → Algo: POST /api/chat/stream（history 含 task-1 完成状态）
Algo:  ChatAgent 调 list_subagents() → 看到 succeeded → 决策下一步
```

---

## 中断恢复

用户触发恢复（auto / manual 均支持）：

```
FE → Go:  POST /api/core/conversations:chat   Body: { input:[{text:"继续被中断的任务"}], mode:"auto" }
Go → Algo: POST /api/chat/stream（history 含 interrupted 任务状态）
Algo:  ChatAgent 调 list_subagents("interrupted") → 决策恢复 → 调 create_subagent（resume=True）
```

`create_subagent(resume=True)` → 工具调 `/api/subagent/run`（`resume=true`）→ Python 框架查 `sub_agent_steps ORDER BY seq`、按 `tool_call_id` 校验配对后重建 LLM 上下文 → 从断点继续，已完成步骤不重复执行。
