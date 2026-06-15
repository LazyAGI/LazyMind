# SubAgent 方案 · 示例代码

> 本文件收录 [`plan.md`](./plan.md) 中引用的示例 / 伪代码。代码仅示意关键逻辑与边界，非最终实现；落地以 `plan.md` 的约束为准。

## 目录

- [C1. `create_subagent` 工具实现（auto / manual 分支）](#c1)
- [C2. `task_created` 事件结构](#c2)
- [C3. Task SSE 事件格式](#c3)
- [C4. Go 主 SSE 拦截与 SubAgent 路由](#c4)
- [C5. Go 启动扫描：标记中断任务](#c5)

---

<a id="c1"></a>

## C1. `create_subagent` 工具实现（auto / manual 分支）

要点：

- `task_id` 由工具内部 `uuid.uuid4()` 生成。
- 通过 `_write_agent_data('task_created', ...)` 写入 `FileSystemQueue`，经 `StreamCallHelper` 的 drain 循环 + translator 翻译后到达 Go。即使工具阻塞轮询，事件仍能实时送出（子线程经 `globals._init_sid(sid)` 继承主请求 sid，与 drain 共用同一队列桶）。
- auto 分支轮询 **core 内部 HTTP 端点**感知终态（不直连 core 业务库）；轮询期间周期性发 `heartbeat` 保活主连接。

```python
def create_subagent(agent_type, title, objective, params,
                    input_artifact_keys, output_artifact_keys, tools=None):
    task_id = str(uuid.uuid4())
    mode = lazyllm.globals['agentic_config']['mode']

    # 1. 写 task_created → FileSystemQueue → astream drain → translator → 主 SSE → Go
    _write_agent_data('task_created', task_id=task_id,
                      title=title, agent_type=agent_type,
                      objective=objective, params=params,
                      input_artifact_keys=input_artifact_keys,
                      output_artifact_keys=output_artifact_keys,
                      tools=tools or [], mode=mode)

    if mode == 'auto':
        # 2. 轮询 core 内部状态端点等待 SubAgent 终态；每 ~15s 发心跳保活主连接
        while True:
            row = get_core_api(f'/internal/subagent/tasks/{task_id}')
            if row and row['status'] in ('succeeded', 'failed', 'interrupted'):
                break
            maybe_emit_heartbeat()      # _write_agent_data('heartbeat')
            time.sleep(2)
        # 3. 拼装摘要返回给 ReactAgent
        if row['status'] == 'succeeded':
            return (f"任务'{title}'已完成。产出 key：{', '.join(output_artifact_keys)}。"
                    f"如需完整内容可调用 get_subagent_artifacts('{title}')。")
        return f"任务'{title}'执行失败：{row.get('current_phase') or row['status']}"

    # manual：不等待，Go 在主 SSE 关闭后后台调 /api/subagent/run
    return f"任务'{title}'已开始后台执行。可通过 get_subagent_status('{title}') 查询进度。"
```

> `get_core_api` 复用 `core_api_client` 模式调 `GET /internal/subagent/tasks/{task_id}`，该端点优先读 `rag/subagent/status:{task_id}`，miss 回落查 `sub_agent_tasks`。

---

<a id="c2"></a>

## C2. `task_created` 事件结构

由 `create_subagent` 写入，经 translator 放进帧的 `data.task_created` 子对象发往 Go。`seq_in_conversation` 不在事件中，由 Go 建记录时分配。

```json
{
  "task_id":              "550e8400-e29b-41d4-a716-446655440000",
  "title":                "生图",
  "agent_type":           "image_generation",
  "mode":                 "auto",
  "objective":            "根据优化后的提示词生成4张漫画风格森林场景图片",
  "params":               {"count": 4},
  "input_artifact_keys":  ["optimized_prompt"],
  "output_artifact_keys": ["images"],
  "tools":                ["image_gen_api"]
}
```

`tools` 可选，不传则加载 agent_type 默认工具集。

---

<a id="c3"></a>

## C3. Task SSE 事件格式

走 Redis `rag/subagent/stream:{task_id}`，由 Go 转发给前端 Task Center。

```json
{"type": "task_start",  "task_id": "..."}
{"type": "progress",    "progress": 40, "current_phase": "已完成第1张，生成第2张...", "estimated_sec": 30}
{"type": "artifact",    "artifact_key": "images", "content_type": "image", "seq": 2,
 "value": {"url": "https://cdn.../img2.png", "path": "images/image_2.png"}}
{"type": "done",        "status": "succeeded", "summary": "已生成4张图片"}
{"type": "error",       "status": "failed",    "message": "缺少 artifact: style_keywords"}
```

同一 `artifact_key` 可多次 emit（`seq` 递增），前端按 `(key, seq)` 去重并逐条追加；具体条数事先不确定。

---

<a id="c4"></a>

## C4. Go 主 SSE 拦截与 SubAgent 路由

> 拦截点是**现有 upstream 消费循环**（`streamSingleAnswer` 中 `for d := range ch`），通过 `d.TaskCreated != nil` 触发，而非新增独立的 `processMainSSEEvent`。下方 `onUpstreamChunk` 仅为逻辑示意。

```go
// 主 ChatAgent SSE 消费循环中，对每个 upstream chunk
func onUpstreamChunk(d UpstreamStreamChunk, sseSender SSESender) {
    if d.TaskCreated != nil {                         // translator 翻译出的 task_created 帧
        seq := allocSeqInTx(db, d.TaskCreated.ConvID) // 事务内分配 seq（FOR UPDATE / 序列）
        task := createTaskRecord(db, d.TaskCreated, seq)
        writeRedisStatus(rdb, task.ID, "pending")
        sseSender.ForwardTaskCreated(task)            // 通知前端（主 SSE）
        go runSubAgent(task)                          // 立即启动 SubAgent（独立 goroutine）
        return
    }
    sseSender.ForwardChunk(d)                          // text / think / sources 照常透传
}

// 启动 SubAgent（auto / manual 共用同一逻辑）
func runSubAgent(task SubAgentTask) {
    // sid=task_id 获得独立队列桶；下发 db_dsn；使用更长的超时
    resp := httpPost("/api/subagent/run", SubAgentRunReq{
        TaskID: task.ID, DBDSN: dsn(), Resume: false,
    })
    for event := range resp.SSE {
        routeToTaskSSE(db, rdb, event)
    }
}

// 路由 SubAgent 事件到 Task SSE：先落 DB（权威），再写 Redis（实时 tail）
func routeToTaskSSE(db, rdb, ev TaskEvent) {
    switch ev.Type {
    case "task_start":
        updateTaskStatus(db, ev.TaskID, "running")
        writeRedis(rdb, ev.TaskID, ev)
    case "progress":
        updateTaskProgress(db, ev.TaskID, ev.Progress, ev.Phase)
        writeRedis(rdb, ev.TaskID, ev)
    case "artifact":
        saveArtifact(db, ev.TaskID, ev)               // 落 sub_agent_artifacts
        writeRedis(rdb, ev.TaskID, ev)
    case "done", "error":
        updateTaskFinalStatus(db, ev.TaskID, ev.Status)
        writeRedis(rdb, ev.TaskID, ev)
    }
    // 执行步骤由 Python SubAgent 框架直接写 sub_agent_steps，Go 不处理
}
```

---

<a id="c5"></a>

## C5. Go 启动扫描：标记中断任务

```go
// running 且 last_heartbeat 超过 5 分钟 → 标为 interrupted
subagent.MarkInterrupted(db, 5*time.Minute)
```
