# Plugin 方案 · 交互路径（文生图场景）

> 配合 [`plan.md`](./plan.md) 阅读，示例代码见 [`code.md`](./code.md)。本文件走通一个端到端场景，用于验证各组件协作是否自洽。

**场景**：用户发送「帮我画一只戴帽子的猫」，**auto 模式（mode: auto）**，插件为 `image-plugin`（五个 Step：`analyze_subject` → `collect_materials` → `optimize_prompt` → `generate_image` → `enhance_image`）。

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
  第一层校验通过（user_input 非空，analyze_subject 是冷启动初始步骤）
  第二层校验跳过（冷启动无前序依赖）
  生成 task_id = 'task-001'，session_id 占位 = 'ps-placeholder-001'

  _write_agent_data('task_created',
    task_id='task-001',
    title='image-plugin:analyze_subject',
    agent_type='plugin_step',
    mode='manual',
    objective='用户想创建一张图片。用户描述：帮我画一只戴帽子的猫\n分析主体、风格、氛围...',
    params={'plugin_id':'image-plugin','step_id':'analyze_subject',
            'session_id':'ps-placeholder-001','user_input':'帮我画一只戴帽子的猫',
            'is_cold_start':true},
    input_artifact_keys=[],
    output_artifact_keys=['subject_analysis'],
    tools=['save_artifact','load_artifact','list_artifacts'],
    resume=False
  )
  返回: "Step 'analyze_subject' triggered. Stop here."

# stop_tool 触发，ReAct 立即停止，不进入 summarize

translator 翻译后，主 SSE 发出:
  data: {"text":"好的，我来帮您生成一只戴帽子的猫的图片。"}
  data: {"task_created":{"task_id":"task-001","title":"image-plugin:analyze_subject",...}}
  data: [DONE]
```

---

## T=2：Go 处理 task_created（Plugin Step 分支）

Go 在 upstream 消费循环识别 `d.TaskCreated != nil` 且 `agent_type='plugin_step'`：

```
Go → DB:   INSERT plugin_sessions
           {id:'ps-001', conversation_id:'conv-001',
            plugin_id:'image-plugin', trigger_history_id:'h-001',
            status:'active', current_step_id:'analyze_subject'}

Go:        injectArtifactsIntoObjective → 无前序 artifact，objective 保持不变

Go → DB:   INSERT sub_agent_tasks
           {id:'task-001', conversation_id:'conv-001', trigger_history_id:'h-001',
            seq_in_conversation:1,
            agent_type:'plugin_step', title:'image-plugin:analyze_subject',
            mode:'manual', status:'pending',
            objective:'用户想创建一张图片。...',
            params:'{"plugin_id":"image-plugin","step_id":"analyze_subject",...}',
            output_artifact_keys:'["subject_analysis"]',
            workspace_path:'/data/subagent/user-xyz/task-001/'}

Go → DB:   INSERT plugin_session_steps
           {id:'task-001', session_id:'ps-001', step_id:'analyze_subject',
            attempt:1, task_id:'task-001', status:'pending'}

Go → Redis: HSET rag/subagent/status:task-001 {status:'pending', progress:0}

Go → FE（主SSE）:
  data: {"task_created":{"task_id":"task-001","title":"image-plugin:analyze_subject",
                          "plugin_session_id":"ps-001","status":"pending"}}
  data: [DONE]（主 SSE 关闭）

Go:   立即 go runSubAgent(task, resume=false)
FE:   Task Center 出现 "image-plugin" 分组，含 "analyze_subject" 任务卡片（pending）
FE:   订阅 GET /api/core/tasks/task-001:stream
```

---

## T=3：SubAgent 执行 analyze_subject Step

`runSubAgent` goroutine 调 `/api/subagent/run`，**完全复用 SubAgent 协议**：

```
Go → Python:  POST /api/subagent/run
Body: { task_id: "task-001", db_dsn: "postgresql://...", resume: false }

Python SubAgent（独立 sid=task-001，独立队列桶）:
  load_task('task-001') 读取 objective / workspace_path / output_artifact_keys
  内部 ReactAgent（仅 save_artifact 框架工具）执行

  → SSE 输出:
    {"type":"task_start","task_id":"task-001"}
    {"type":"progress","task_id":"task-001","progress":5,"current_phase":"开始分析..."}
    {"type":"think","task_id":"task-001","think":"用户要画一只戴帽子的猫...分析主体：猫，帽子；风格：可爱写实"}
    {"type":"tool_calls","task_id":"task-001","tool_calls":[{
      "id":"call_2","name":"save_artifact",
      "args":{"key":"subject_analysis","value":"Subject: a cat wearing a hat...","content_type":"text"}
    }]}
    {"type":"artifact","task_id":"task-001","artifact_key":"subject_analysis","seq":1,
     "content_type":"text","value":{"text":"Subject: a cat wearing a hat, style: cute realistic..."}}
    {"type":"progress","task_id":"task-001","progress":90,"current_phase":"已保存分析"}
    {"type":"done","task_id":"task-001","status":"succeeded",
     "summary":"已完成主体分析：猫+帽子，写实可爱风格"}
```

Go 消费 SubAgent SSE：

```
task_start  → DB: UPDATE sub_agent_tasks status='running'
            → DB: UPDATE plugin_session_steps status='running'
            → Redis RPUSH → FE（Task SSE）

artifact    → DB: INSERT sub_agent_artifacts {task_id:'task-001', artifact_key:'subject_analysis',...}
            → 查 state.yml outputs → slot_id='subject_analysis' → cardinality=single
            → DB: INSERT plugin_slot_revisions {slot_id:'subject_analysis', revision:1, selected:TRUE}
            → Redis RPUSH → FE（Task SSE）
            FE: Plugin Panel "Analysis" Tab 展示分析结果

done        → DB: UPDATE sub_agent_tasks status='succeeded', summary=...
            → DB: UPDATE plugin_session_steps status='succeeded'
            → Redis RPUSH → FE（Task SSE）
```

---

## T=4：auto 模式推进——DriverAgent 评判

Go 检测到 `done`，读取全局模式 `mode = 'auto'`：

```
Go → Python:  POST /api/plugin/driver
Body: {
  plugin_id: "image-plugin",
  step_id: "analyze_subject",
  step_result: "已完成主体分析：猫+帽子，写实可爱风格",
  session_id: "ps-001"
}

Python DriverAgent 读取 driver.md（< 3000 字，追加 scenario.md）:
  LLM 输出: "<verdict>PASS</verdict><reason>subject_analysis saved with 80 words covering subject, style, and lighting.</reason>"

Go:  parseVerdict → PASS
     syntheticMsg = "Step analyze_subject completed. PASS subject_analysis saved with 80 words."
     go triggerNextChatTurn(conv_id='conv-001', session_id='ps-001', msg=syntheticMsg)
```

---

## T=5：ChatAgent 第二轮——决策触发 collect_materials

Go 以合成消息触发 ChatAgent（携带 plugin_context）：

```
Go → Algo: POST /api/chat/stream  (SSE)
Body: {
  query: "Step analyze_subject completed. PASS subject_analysis saved with 80 words.",
  history: [{"role":"user","content":"帮我画一只戴帽子的猫"},
             {"role":"assistant","content":"好的，我来..."}],
  mode: "auto",
  plugin_context: {
    "session_id": "ps-001",
    "plugin_id": "image-plugin",
    "current_step": "analyze_subject",
    "advance": false
  },
  tools: ["advance_step"]    ← 有活跃 session，只注入 advance_step
}

ChatAgent LLM 结合 scenario.md 判断下一步为 collect_materials:
  {"tag":"tool_calls","tool_calls":[{
    "id":"call_3","name":"advance_step",
    "args":{"step_id":"collect_materials","user_input":"收集戴帽子的猫的参考素材"}
  }]}

_trigger_plugin_step('collect_materials', '收集戴帽子的猫的参考素材'):
  第一层校验：collect_materials 从 analyze_subject 可达 ✓
  第二层校验：查 plugin_session_steps，analyze_subject status='succeeded' ✓
  生成 task_id='task-002'

  _write_agent_data('task_created',
    task_id='task-002',
    agent_type='plugin_step',
    params={'plugin_id':'image-plugin','step_id':'collect_materials',
            'session_id':'ps-001','user_input':'收集戴帽子的猫的参考素材',
            'is_cold_start':false},
    input_artifact_keys=['subject_analysis'],
    output_artifact_keys=['material_image'],
    tools=['save_artifact','load_artifact','list_artifacts','web_search_tool','image_search_tool'],
  )
  返回: "Step 'collect_materials' triggered. Stop here."

主 SSE:
  data: {"task_created":{"task_id":"task-002","title":"image-plugin:collect_materials","plugin_session_id":"ps-001"}}
  data: [DONE]
```

后续每一轮依此模式循环推进，直到 `enhance_image` 完成后 DriverAgent 裁决 `DONE`。以下仅列出关键节点。

---

## T=6：Go 处理 collect_materials step_created → SubAgent 执行 → DriverAgent PASS → …

```
Go:   injectArtifactsIntoObjective('task-002', 'ps-001', 'collect_materials')
      → 查 sub_agent_artifacts WHERE task_id='task-001' AND artifact_key='subject_analysis'
      → 取 value.text → 替换 objective 中的 {{subject_analysis}}

Go → DB:   INSERT sub_agent_tasks {id:'task-002', ..., objective=enriched}
Go → DB:   INSERT plugin_session_steps {session_id:'ps-001', step_id:'collect_materials', attempt:1}
Go → DB:   UPDATE plugin_sessions SET current_step_id='collect_materials'
Go:        go runSubAgent(task-002, resume=false)

SubAgent 执行（sid=task-002）:
  调用 web_search_tool / image_search_tool 收集参考图
  多次调用 save_artifact(key='material_image', content_type='image', value=<url>)
  每次调用产出一条 artifact 事件 → Go 追加 plugin_slot_revisions（cardinality=list）
  FE: Plugin Panel "Materials" Tab 实时追加参考图

Go 消费 done 事件 → DriverAgent PASS:
  "<verdict>PASS</verdict><reason>At least 3 material_image artifacts saved.</reason>"
  → 合成消息 → ChatAgent 第三轮决策触发 optimize_prompt（task-003）
```

后续步骤以相同模式串行推进，Go 每轮注入前序 artifact 值到 objective。

---

## T=7：SubAgent 执行 generate_image Step（task-004）

```
Python SubAgent（sid=task-004，objective 已注入 optimized_prompt 值）:
  ReactAgent 调用 generate_image_tool 工具

  → SSE 输出:
    {"type":"task_start","task_id":"task-004"}
    {"type":"tool_calls","task_id":"task-004","tool_calls":[{
      "id":"call_8","name":"generate_image_tool",
      "args":{"prompt":"A charming cat wearing a red hat, watercolor style..."}
    }]}
    {"type":"tool_results","task_id":"task-004","tool_results":[{
      "id":"call_8","name":"generate_image_tool",
      "result":"https://mock-images.example.com/generated/12345_678.png"
    }]}
    {"type":"artifact","task_id":"task-004","artifact_key":"generated_image_url","seq":1,
     "content_type":"image","value":{"url":"https://mock-images.example.com/generated/12345_678.png"}}
    {"type":"done","task_id":"task-004","status":"succeeded","summary":"图片已生成"}

Go 消费:
  artifact → DB: INSERT sub_agent_artifacts {artifact_key:'generated_image_url',...}
           → 查 state.yml outputs → slot_id='image_output' → cardinality=single
           → DB: INSERT plugin_slot_revisions {slot_id:'image_output', selected:TRUE}
           → FE: Plugin Panel "Result" Tab 展示原始图
  done     → DriverAgent PASS → 合成消息 → ChatAgent 决策触发 enhance_image（task-005）
```

---

## T=8：auto 推进——enhance_image 完成，DriverAgent 判定 DONE

```
SubAgent task-005 执行 enhance_image，产出 enhanced_image_url artifact：
  FE: Plugin Panel "Result" Tab 新增增强图列表项（cardinality=list 追加）

Go → DriverAgent:
  step_id='enhance_image', result='增强图已生成', session_id='ps-001'

DriverAgent 输出:
  "<verdict>DONE</verdict><reason>enhanced_image_url saved successfully. Pipeline complete.</reason>"

Go:  parseVerdict → DONE
     → DB: UPDATE plugin_sessions SET status='completed'
     → Conversation Events SSE: {"type":"plugin_completed","session_id":"ps-001","plugin_id":"image-plugin"}
     → 不再触发新一轮 ChatAgent，auto loop 结束

FE:  收到 plugin_completed 事件，停止 slots 轮询，刷新最终 Slot 内容
FE:  Task Center 中 image-plugin 分组所有步骤 ✓（共 5 个任务卡片）
```

---

## manual 模式差异

相同场景，`mode: manual`（用户在 Go 侧做全局配置）：

Step 执行完成后，Go **不调 DriverAgent**，直接通过 **Conversation Events SSE** 发 `step_waiting` 事件：

```
Conversation Events SSE → FE:  {"type":"step_waiting","session_id":"ps-001","step_id":"analyze_subject"}
（chat stream 已正常关闭，step_waiting 走独立的常驻长连接）

FE:  PluginPanel 显示「继续」/「重试」按钮
```

用户点击「继续」（上次 step 状态 `succeeded`）：

```
FE → Go:  POST /conversations:chat  （普通聊天消息通道）
Body: {
  query: "继续",
  plugin_context: {session_id:'ps-001', plugin_id:'image-plugin', current_step:'analyze_subject'}
}

Go:  检查 plugin_session_steps 最后一条 status='succeeded'
     → 合成「Step analyze_subject completed. User confirmed. Please proceed.」
     → 调 ChatAgent（携带 plugin_context），ChatAgent 决策触发 collect_materials
```

---

## 页面刷新后 Plugin 状态恢复

```
FE → Go:  GET /api/core/conversations/conv-001/plugin-sessions
Go → DB:  SELECT * FROM plugin_sessions WHERE conversation_id='conv-001' ORDER BY created_at DESC
Go → DB:  SELECT * FROM plugin_session_steps WHERE session_id='ps-001'
Go → DB:  SELECT * FROM sub_agent_artifacts WHERE task_id IN ('task-001','task-002','task-003','task-004','task-005')
Go → FE:  {
  sessions: [{
    id: 'ps-001', plugin_id: 'image-plugin', status: 'completed',
    steps: [
      {step_id:'analyze_subject',   status:'succeeded', task_id:'task-001',
       artifacts:[{artifact_key:'subject_analysis',...}]},
      {step_id:'collect_materials', status:'succeeded', task_id:'task-002',
       artifacts:[{artifact_key:'material_image',...},{artifact_key:'material_image',...}]},
      {step_id:'optimize_prompt',   status:'succeeded', task_id:'task-003',
       artifacts:[{artifact_key:'optimized_prompt',...}]},
      {step_id:'generate_image',    status:'succeeded', task_id:'task-004',
       artifacts:[{artifact_key:'generated_image_url', value:{url:'https://mock-images.example.com/generated/12345_678.png'},...}]},
      {step_id:'enhance_image',     status:'succeeded', task_id:'task-005',
       artifacts:[{artifact_key:'enhanced_image_url', value:{url:'https://mock-images.example.com/enhanced/12345_678.png'},...}]}
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
FE → Go:  POST /conversations:chat  （普通聊天消息通道）
Body: {
  query: "继续",
  plugin_context: {session_id:'ps-001', plugin_id:'image-plugin', current_step:'generate_image'}
}

Go:  检查 plugin_session_steps status='interrupted'
     → 直接 go runSubAgent(task-004, resume=true)（跳过 ChatAgent）
     SubAgent 框架从 sub_agent_steps 恢复执行上下文，继续未完成步骤
```
