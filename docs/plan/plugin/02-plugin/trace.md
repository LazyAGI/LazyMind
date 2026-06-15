# Plugin 方案 · 交互路径（文生图场景）

> 配合 [`plan.md`](./plan.md) 阅读，示例代码见 [`code.md`](./code.md)。本文件走通一个端到端场景，用于验证各组件协作是否自洽。

**场景**：用户发送「帮我画一只戴帽子的猫」，**auto 模式（mode: auto）**，插件为 `image-plugin`（两个 Step：`optimize_prompt` → `generate_image`）。

---

## T=0：用户发送消息，建立连接

```
FE → Go:   POST /api/core/conversations:chat  (SSE)
Body: { conversation_id: "conv-001", input: [{text: "帮我画一只戴帽子的猫"}], mode: "auto" }

Go → DB:   读取 conversations + 最近 chat_histories
Go → DB:   INSERT chat_histories {id:'h-001', status:'generating'}
Go → DB:   SELECT COUNT(*) FROM plugin_sessions WHERE conversation_id=? → 0（无活跃 session）

Go → Algo: POST /api/chat/stream  (SSE)
Body: {
  query: "帮我画一只戴帽子的猫",
  history: [],
  mode: "auto",
  plugin_context: null,    ← 无活跃 session，不注入
  has_subagents: false,
  tools: ["trigger_image_plugin", "web_search", "todo_writer", ...]
}
```

---

## T=1：ChatAgent 意图识别，触发插件冷启动

ChatAgent LLM 识别到图片生成意图，调用 `trigger_image_plugin`：

```
ChatAgent 原始事件（Python 侧）:
{"tag":"text",       "delta":"好的，我来帮您生成一只戴帽子的猫的图片。"}
{"tag":"tool_calls", "tool_calls":[{
  "id": "call_1",
  "name": "trigger_image_plugin",
  "args": {"user_input": "帮我画一只戴帽子的猫"}
}]}

_trigger_plugin_step() 执行：
  第一层校验通过（user_input 非空，optimize_prompt 是冷启动初始步骤）
  第二层校验跳过（冷启动无前序依赖）
  生成 task_id = 'task-001'，session_id 占位 = 'ps-placeholder-001'

  _write_agent_data('task_created',
    task_id='task-001',
    title='image-plugin:optimize_prompt',
    agent_type='plugin_step',
    mode='manual',
    objective='用户想生成一张图片。用户描述：帮我画一只戴帽子的猫\n将描述优化为高质量英文图片生成 prompt...',
    params={'plugin_id':'image-plugin','step_id':'optimize_prompt',
            'session_id':'ps-placeholder-001','user_input':'帮我画一只戴帽子的猫',
            'is_cold_start':true},
    input_artifact_keys=[],
    output_artifact_keys=['optimized_prompt'],
    tools=[],
    resume=False
  )
  返回: "Step 'optimize_prompt' triggered. Stop here."

# stop_tool 触发，ReAct 立即停止，不进入 summarize

translator 翻译后，主 SSE 发出:
  data: {"text":"好的，我来帮您生成一只戴帽子的猫的图片。"}
  data: {"task_created":{"task_id":"task-001","title":"image-plugin:optimize_prompt",...}}
  data: [DONE]
```

---

## T=2：Go 处理 task_created（Plugin Step 分支）

Go 在 upstream 消费循环识别 `d.TaskCreated != nil` 且 `agent_type='plugin_step'`：

```
Go → DB:   INSERT plugin_sessions
           {id:'ps-001', conversation_id:'conv-001',
            plugin_id:'image-plugin', trigger_history_id:'h-001',
            status:'active', current_step_id:'optimize_prompt'}

Go:        injectArtifactsIntoObjective → 无前序 artifact，objective 保持不变

Go → DB:   INSERT sub_agent_tasks
           {id:'task-001', conversation_id:'conv-001', trigger_history_id:'h-001',
            seq_in_conversation:1,
            agent_type:'plugin_step', title:'image-plugin:optimize_prompt',
            mode:'manual', status:'pending',
            objective:'用户想生成一张图片。...',
            params:'{"plugin_id":"image-plugin","step_id":"optimize_prompt",...}',
            output_artifact_keys:'["optimized_prompt"]',
            workspace_path:'/data/subagent/user-xyz/task-001/'}

Go → DB:   INSERT plugin_session_steps
           {id:'task-001', session_id:'ps-001', step_id:'optimize_prompt',
            attempt:1, task_id:'task-001', status:'pending'}

Go → Redis: HSET rag/subagent/status:task-001 {status:'pending', progress:0}

Go → FE（主SSE）:
  data: {"task_created":{"task_id":"task-001","title":"image-plugin:optimize_prompt",
                          "plugin_session_id":"ps-001","status":"pending"}}
  data: [DONE]（主 SSE 关闭）

Go:   立即 go runSubAgent(task, resume=false)  ← 直接复用 SubAgent goroutine，传入 db_dsn
FE:   Task Center 出现 "image-plugin" 分组，含 "optimize_prompt" 任务卡片（pending）
FE:   订阅 GET /api/core/tasks/task-001:stream
```

---

## T=3：SubAgent 执行 optimize_prompt Step

`runSubAgent` goroutine 调 `/api/subagent/run`，**完全复用 SubAgent 协议**：

```
Go → Python:  POST /api/subagent/run
Body: { task_id: "task-001", db_dsn: "postgresql://...", resume: false }

Python SubAgent（独立 sid=task-001，独立队列桶）:
  load_task('task-001') 读取 objective / workspace_path / output_artifact_keys
  内部 ReactAgent（无额外工具，仅 save_artifact）执行

  → SSE 输出:
    {"type":"task_start","task_id":"task-001"}
    {"type":"progress","task_id":"task-001","progress":5,"current_phase":"开始执行..."}
    {"type":"think","task_id":"task-001","think":"用户要画一只戴帽子的猫..."}
    {"type":"tool_calls","task_id":"task-001","tool_calls":[{
      "id":"call_2","name":"save_artifact",
      "args":{"key":"optimized_prompt","value":"A charming cat wearing a red hat...","content_type":"text"}
    }]}
    {"type":"artifact","task_id":"task-001","artifact_key":"optimized_prompt","seq":1,
     "content_type":"text","value":{"text":"A charming cat wearing a red hat, watercolor style..."}}
    {"type":"progress","task_id":"task-001","progress":90,"current_phase":"已保存 prompt"}
    {"type":"done","task_id":"task-001","status":"succeeded",
     "summary":"已优化提示词：A charming cat wearing a red hat, watercolor style..."}
```

Go 消费 SubAgent SSE（先落 DB 再写 Redis，与普通 SubAgent 完全一致）：

```
task_start  → DB: UPDATE sub_agent_tasks status='running'
            → DB: UPDATE plugin_session_steps status='running'
            → Redis RPUSH → FE（Task SSE）

artifact    → DB: INSERT sub_agent_artifacts {task_id:'task-001', artifact_key:'optimized_prompt',...}
            → Redis RPUSH → FE（Task SSE）
            FE: Task Center 展示 "optimized_prompt" 文本内容

done        → DB: UPDATE sub_agent_tasks status='succeeded', summary=...
            → DB: UPDATE plugin_session_steps status='succeeded'
            → Redis RPUSH → FE（Task SSE）
```

---

## T=4：auto 模式推进——DriverAgent 评判

Go 检测到 `done`，读取 Go侧用户配置的全局插件自动开关 `mode = 'auto'`：

```
Go → Python:  POST /api/plugin/driver
Body: {
  plugin_id: "image-plugin",
  step_id: "optimize_prompt",
  step_result: "已优化提示词：A charming cat...",
  session_id: "ps-001"
}

Python DriverAgent 读取 driver.md（< 3000 字，追加 scenario.md）:
  prompt = driver_md + scenario_md + current context
  LLM 输出: "PASS 提示词优化完成，质量良好，可进入生图步骤。"

Go:  parseVerdict("PASS ...") → PASS
     syntheticMsg = "Step optimize_prompt completed. Result: 已优化提示词: A charming cat... PASS 提示词优化完成。"
     go triggerNextChatTurn(conv_id='conv-001', session_id='ps-001', msg=syntheticMsg)
```

---

## T=5：ChatAgent 第二轮——决策触发 generate_image

Go 以合成消息触发 ChatAgent（携带 plugin_context）：

```
Go → Algo: POST /api/chat/stream  (SSE)
Body: {
  query: "Step optimize_prompt completed. PASS 提示词优化完成。",
  history: [{"role":"user","content":"帮我画一只戴帽子的猫"},
             {"role":"assistant","content":"好的，我来..."}],
  mode: "auto",
  plugin_context: {
    "session_id": "ps-001",
    "plugin_id": "image-plugin",
    "current_step": "optimize_prompt",
    "advance": false
  },
  tools: ["advance_step"]    ← 有活跃 session，只注入 advance_step
}

ChatAgent LLM 收到合成消息，结合 scenario.md 判断下一步为 generate_image:
  {"tag":"tool_calls","tool_calls":[{
    "id":"call_3","name":"advance_step",
    "args":{"step_id":"generate_image","user_input":"生成戴帽子的猫图片"}
  }]}

_trigger_plugin_step('generate_image', '生成戴帽子的猫图片'):
  第一层校验：generate_image 从 optimize_prompt 可达 ✓
  第二层校验：查 plugin_session_steps，optimize_prompt status='succeeded' ✓
  生成 task_id='task-002'

  _write_agent_data('task_created',
    task_id='task-002',
    agent_type='plugin_step',
    params={'plugin_id':'image-plugin','step_id':'generate_image',
            'session_id':'ps-001','user_input':'生成戴帽子的猫图片',
            'is_cold_start':false},
    input_artifact_keys=['optimized_prompt'],
    output_artifact_keys=['image_url'],
    tools=['dalle_generate'],
  )
  返回: "Step 'generate_image' triggered. Stop here."

主 SSE:
  data: {"task_created":{"task_id":"task-002","title":"image-plugin:generate_image","plugin_session_id":"ps-001"}}
  data: [DONE]
```

---

## T=6：Go 处理 generate_image step_created

```
Go:   injectArtifactsIntoObjective('task-002', 'ps-001', 'generate_image')
      → 查 sub_agent_artifacts WHERE task_id='task-001' AND artifact_key='optimized_prompt'
      → 取 value.text = "A charming cat wearing a red hat..."
      → 替换 objective 中的 {{optimized_prompt}}：
        "使用优化后的 prompt 生成图片：A charming cat wearing a red hat, watercolor style...
         调用 dalle_generate(prompt) 生成图片，完成后调用 save_artifact('image_url', url)。"

Go → DB:   INSERT sub_agent_tasks {id:'task-002', ..., objective=enriched_objective,
            input_artifact_keys:'["optimized_prompt"]',
            output_artifact_keys:'["image_url"]'}
Go → DB:   INSERT plugin_session_steps
           {id:'task-002', session_id:'ps-001', step_id:'generate_image',
            attempt:1, task_id:'task-002', status:'pending'}
Go → DB:   UPDATE plugin_sessions SET current_step_id='generate_image'

Go → FE:   data: {"task_created":{...}}
Go:        go runSubAgent(task-002, resume=false)

FE:   Task Center 中 image-plugin 分组新增 "generate_image" 卡片（pending）
FE:   订阅 GET /api/core/tasks/task-002:stream
```

---

## T=7：SubAgent 执行 generate_image Step

```
Python SubAgent（sid=task-002，独立队列桶）:
  load_task('task-002') 读取 objective（已注入 optimized_prompt 值）
  ReactAgent 调用 dalle_generate 工具

  → SSE 输出:
    {"type":"task_start","task_id":"task-002"}
    {"type":"progress","task_id":"task-002","progress":5,"current_phase":"开始生图..."}
    {"type":"tool_calls","task_id":"task-002","tool_calls":[{
      "id":"call_4","name":"dalle_generate",
      "args":{"prompt":"A charming cat wearing a red hat, watercolor style..."}
    }]}
    {"type":"tool_results","task_id":"task-002","tool_results":[{
      "id":"call_4","name":"dalle_generate","result":"https://cdn.../cat_hat.png"
    }]}
    {"type":"artifact","task_id":"task-002","artifact_key":"image_url","seq":1,
     "content_type":"image","value":{"url":"https://cdn.../cat_hat.png","path":"images/cat_hat.png"}}
    {"type":"done","task_id":"task-002","status":"succeeded","summary":"已生成图片"}

Go 消费:
  artifact → DB: INSERT sub_agent_artifacts {artifact_key:'image_url',...}
           → FE: Task Center 显示图片缩略图
  done     → DB: status='succeeded'
           → DB: plugin_session_steps status='succeeded'
```

---

## T=8：auto 推进——DriverAgent 判定 DONE

```
Go → DriverAgent:
  step_id='generate_image', result='已生成图片', session_id='ps-001'

DriverAgent 输出: "DONE 图片已成功生成，流程完成。"

Go:  parseVerdict("DONE ...") → DONE
     → DB: UPDATE plugin_sessions SET status='completed'
     → FE: data: {"type":"plugin_completed","session_id":"ps-001","plugin_id":"image-plugin"}
     → 不再触发新一轮 ChatAgent，auto loop 结束

FE:  Task Center 中 image-plugin 分组所有步骤 ✓
FE:  主消息框可显示插件完成摘要（由前端根据 plugin_completed 事件触发）
```

---

## manual 模式差异

相同场景，`mode: manual`（用户在Go侧做全局配置）：

Step 执行完成后，Go **不调 DriverAgent**，直接：

```
Go → FE:  data: {"type":"step_waiting","session_id":"ps-001","step_id":"optimize_prompt"}
Go:  当轮 SSE 关闭（等待用户手动继续）

FE:  显示「提示词优化完成，点击继续生图」按钮
```

用户点击继续（`advance=true`，上次 step 状态 `succeeded`）：

```
FE → Go:  POST /conversations:chat
Body: { plugin_context: {session_id:'ps-001', advance:true, current_step:'optimize_prompt'} }

Go:  检查 plugin_session_steps 最后一条 status='succeeded'
     → 合成「Step optimize_prompt completed. User confirmed. Please proceed.」
     → 调 ChatAgent（携带 plugin_context），ChatAgent 决策触发 generate_image
```

---

## 页面刷新后 Plugin 状态恢复

```
FE → Go:  GET /api/core/conversations/conv-001/plugin-sessions
Go → DB:  SELECT * FROM plugin_sessions WHERE conversation_id='conv-001' ORDER BY created_at DESC
Go → DB:  SELECT * FROM plugin_session_steps WHERE session_id='ps-001'
Go → DB:  SELECT * FROM sub_agent_artifacts WHERE task_id IN ('task-001','task-002')
Go → FE:  {
  sessions: [{
    id: 'ps-001', plugin_id: 'image-plugin', status: 'completed',
    steps: [
      {step_id:'optimize_prompt', status:'succeeded', task_id:'task-001',
       artifacts:[{artifact_key:'optimized_prompt',...}]},
      {step_id:'generate_image',  status:'succeeded', task_id:'task-002',
       artifacts:[{artifact_key:'image_url', value:{url:'https://cdn.../cat_hat.png'},...}]}
    ]
  }]
}

FE:  Task Center 恢复展示，图片重新渲染
FE:  对仍 running 的 step 订阅对应 Task SSE（DB 补历史 → Redis tail）
```

---

## Step 被中断后恢复

用户点击「继续」，Step status='interrupted'（心跳超时）：

```
FE → Go:  POST /conversations:chat
Body: { plugin_context: {session_id:'ps-001', advance:true, current_step:'generate_image'} }

Go:  检查 plugin_session_steps status='interrupted'
     → 直接 go runSubAgent(task-002, resume=true)（跳过 ChatAgent）
     SubAgent 框架从 sub_agent_steps 恢复执行上下文，继续未完成步骤
```
