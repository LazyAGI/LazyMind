# 阶段 4 · 示例代码

> 本文件收录 [`plan.md`](./plan.md) 中引用的示例 / 伪代码。代码仅示意关键逻辑与边界，非最终实现；落地以 `plan.md` 的约束为准。实际类名/函数名以代码为准。

## 目录

- [C1. 用户配置加载与透传](#c1)
- [C2. `OnSubAgentDone` 两档模式](#c2)
- [C3. DriverAgent 配置注入、Artifact 读取与降级](#c3)
- [C4. 停止：Go 取消信号 + Python 轮询](#c4)
- [C5. 继续 / 重试：前端模拟消息](#c5)
- [C6. 意图与约束：UPSERT 与 SubAgent 注入](#c6)
- [C7. 范围重跑：ChatAgent 顺序 advance（方案 A / B）](#c7)
- [C8. `ask_user`：仅 ChatAgent](#c8)
- [C9. `ask_response` 对话历史存储](#c9)
- [C10. 并行执行](#c10)
- [C11. TaskCenter 与定时调度](#c11)
- [C12. 查询工具](#c12)
- [C13. 步骤中断恢复（Checkpoint-Resume）](#c13)

---

<a id="c1"></a>

## C1. 用户配置加载与透传

**Go `resolveConversationModes`（`backend/core/chat/conversation_logic.go`）**

```go
func resolveConversationModes(ctx context.Context, db *gorm.DB, userID, convID string, body map[string]any) (pluginMode, subagentMode string) {
    pluginMode = "dynamic"
    subagentMode = "auto"

    // 1. body 显式覆盖（对话 UI 刚修改）
    if v, ok := body["plugin_mode"].(string); ok && v != "" {
        pluginMode = v
        store.UpdateConversationPluginMode(ctx, db, convID, v)
    } else if conv, _ := store.GetConversation(ctx, db, convID); conv.PluginMode != nil {
        pluginMode = *conv.PluginMode
    } else if us, _ := store.GetUserPluginSettings(ctx, db, userID); us != nil {
        pluginMode = us.PluginMode
    }

    // subagent_mode 同理
    // ...
    return pluginMode, subagentMode
}
```

**Go `buildChatRequestBody`**

```go
pluginMode, subagentMode := resolveConversationModes(ctx, db, userID, convID, raw)
body["plugin_mode"] = pluginMode
body["subagent_mode"] = subagentMode
```

**Python `handle_chat`**

```python
agentic_config['plugin_mode']   = kwargs.get('plugin_mode', 'dynamic')
agentic_config['subagent_mode'] = kwargs.get('subagent_mode', 'auto')
# 不再读取 os.environ['LAZYMIND_PLUGIN_MODE']
```

**废弃 `eventloop.go` 的 `defaultMode()`**：`OnSubAgentDone` 从 `pctx.PluginMode`（Go 创建步骤时从 conversation 解析写入）读取。

---

<a id="c2"></a>

## C2. `OnSubAgentDone` 两档模式

> 对应 plan §3.1。`enable_plugin=false` 时根本不进入插件流水线，此处只处理已进入流水线的情况。

```go
func OnSubAgentDone(ctx context.Context, db *gorm.DB, pctx *PluginChatContext) {
    switch pctx.PluginMode {
    case "auto":
        go advanceAutoMode(ctx, db, pctx)
    case "dynamic":
        _ = UpdateSessionStatus(ctx, db, pctx.SessionID, SessionStatusWaiting)
        sendConvEvent(pctx.ConvID, "step_waiting", map[string]any{
            "reason":   "dynamic_pause",
            "step_id":  pctx.StepID,
        })
    }
}
```

---

<a id="c3"></a>

## C3. DriverAgent 配置注入、Artifact 读取与降级

**Go `callDriverAgent` body 组装**

```go
artifactsSummary, _ := buildPluginArtifactsSummary(ctx, db, pctx.SessionID, pctx.StepID)
body := map[string]any{
    "plugin_id":                pctx.PluginID,
    "step_id":                  pctx.StepID,
    "step_result":              stepResult,
    "session_id":               pctx.SessionID,
    "llm_config":               llmCfg,
    "history_files_per_turn":   pctx.HistoryFilesPerTurn,
    "plugin_artifacts_summary": artifactsSummary,
}
```

**Go `advanceAutoMode` — 全局 max_retries（非 plugin.yaml）**

```go
const defaultDriverMaxRetries = 3 // 或 config.DriverMaxRetries()

if verdict.Verdict == "RETRY" {
    if step.Attempt >= defaultDriverMaxRetries {
        sendConvEvent(pctx.ConvID, "max_retries_exceeded", map[string]any{
            "step_id": pctx.StepID, "attempt": step.Attempt,
        })
        verdict.Verdict = "PASS"
    }
}
```

**Python `evaluate_step`**

```python
def evaluate_step(
    plugin_id: str,
    step_id: str,
    step_result: str,
    session_id: str,
    llm_config: dict | None = None,
    history_files_per_turn: dict | None = None,
    plugin_artifacts_summary: str | None = None,
) -> dict:
    llm = _build_llm(llm_config)
    tools = []
    if history_files_per_turn:
        tools += [
            find_user_attachment(history_files_per_turn),
            read_user_attachment(history_files_per_turn),
        ]
    # 插件产出 artifact
    from lazymind.chat.engine.subagent.tools import find_artifact, read_artifact
    tools += [find_artifact, read_artifact]

    prompt = _build_driver_prompt(plugin_id)
    context = (
        f'step_id={step_id}\n'
        f'step_result={step_result}\n'
        f'artifacts_summary={plugin_artifacts_summary or ""}'
    )
    response = llm.run_with_tools(prompt, context, tools=tools)
    return _parse_verdict(response)
```

---

<a id="c4"></a>

## C4. 停止：Go 取消信号 + Python 轮询

**Go `StopConversation`（扩展 `StopChatGeneration`）**

```go
func StopConversation(ctx context.Context, db *gorm.DB, convID, userID string) error {
    // 1. 设置取消标记（Redis key 或 DB 表 conversation_cancel_flags）
    cancelStore.Set(ctx, convID, time.Now())

    // 2. 中断 running plugin step
    if sess, step := findRunningPluginStep(ctx, db, convID); step != nil {
        subagent.MarkTaskInterruptedByID(ctx, db, step.TaskID)
        store.UpdateStepStatus(ctx, db, step.ID, store.StepStatusInterrupted)
        store.UpdateSessionStatus(ctx, db, sess.ID, store.SessionStatusWaiting)
    }

    // 3. 可选：向 Python FileSystemQueue 推送 control 帧
  enqueueControl(convID, map[string]any{"tag": "control", "action": "cancel"})

    sendConvEvent(convID, "step_waiting", map[string]any{
        "user_stopped": true,
        "reason":       "user_stopped",
    })
    return nil
}
```

**Python ReAct 循环轮询（`chat_service.py` / `reactAgent.py`）**

```python
def _check_cancelled(conversation_id: str) -> bool:
    if cancel_store.is_set(conversation_id):
        return True
    for raw in FileSystemQueue(klass=conversation_id).dequeue():
        msg = json.loads(raw)
        if msg.get('tag') == 'control' and msg.get('action') == 'cancel':
            return True
    return False

# 在 react 每轮 tool call 前：
if _check_cancelled(conversation_id):
    raise ChatCancelledError('user stopped')
```

**SubAgent runner** 同样在长循环内调用 `_check_cancelled(parent_conv_id)`。

---

<a id="c5"></a>

## C5. 继续 / 重试：前端模拟消息

**前端（`newChatContainer` 伪代码）**

```typescript
// 「继续」— 不走 PATCH / advance API
function onContinueClick() {
  openSSE({
    query: '继续',
    input: [{ input_type: 'text', text: '继续执行下一步' }],
    action: 'CHAT_ACTION_NEXT',
  });
}

// 「重试」
function onRetryClick(stepId: string, reason?: string) {
  const text = reason
    ? `重试步骤 ${stepId}，${reason}`
    : `重试步骤 ${stepId}`;
  openSSE({ query: text, input: [{ input_type: 'text', text }], action: 'CHAT_ACTION_NEXT' });
}
```

**Python ChatAgent** 解析用户文本 + session 状态，调用已有 `advance_step`：

```python
# 用户说「重试」且 session 有 interrupted / 刚完成步骤
advance_step(step_id=current_step, rewind=True, retry_hint=user_text)
```

---

<a id="c6"></a>

## C6. 意图与约束：UPSERT 与 SubAgent 注入

> 对应 plan §6。

**`update_intent` 工具（仅 ChatAgent）**

```python
def update_intent(scope: str, content: str, step_id: str | None = None) -> str:
    """UPSERT intent/constraint. scope: 'session' | 'step'. Plugin-agnostic."""
    session_id = agentic_config['plugin_session_id']
    if scope == 'session':
        # UPDATE plugin_sessions SET intent_context = ... WHERE id = session_id
        TaskQueryDB().upsert_session_intent(session_id, content)
    else:
        # INSERT INTO plugin_step_intents ... ON CONFLICT (session_id, step_id) DO UPDATE
        TaskQueryDB().upsert_step_intent(session_id, step_id, content)
    return '约束已更新'
```

插件 `plugin.yaml` **无需**声明 `intent` 字段；约束完全由框架管理。

**`resolve_plugin_injection` 注入 ChatAgent 和 SubAgent**

```python
def _build_intent_section(session_id: str, step_id: str | None = None) -> str:
    """序列化约束注入 system prompt."""
    lines = []
    session_intent = TaskQueryDB().get_session_intent(session_id)
    if session_intent:
        lines.append(f'## 全局约束\n{session_intent}')
    if step_id:
        step_intent = TaskQueryDB().get_step_intent(session_id, step_id)
        if step_intent:
            lines.append(f'## 步骤约束（{step_id}）\n{step_intent}')
    return '\n\n'.join(lines)

# ChatAgent：注入 user turn 附录
plugin_artifact_context += '\n\n' + _build_intent_section(session_id)

# SubAgent 启动时：注入 system prompt 专用章节
# （在 subagent_runner.py 构建 system prompt 时调用）
subagent_system_prompt += '\n\n' + _build_intent_section(session_id, step_id=current_step)
```

`runtime_instruction`（对应 Go 侧 `retry_hint`）仍用于传递**本次执行的临时指令**（重试原因、partial_indices 说明），语义不同，不替代约束注入。

---

<a id="c7"></a>

## C7. 范围重跑：ChatAgent 顺序 advance（方案 A / B）

> 对应 plan §7.2。

**不推荐：Go 批量 reset `plugin_session_steps.status`**

```go
// ❌ 不再采用
// for _, sid := range runRange { store.ResetStepForRerun(...) }
```

**方案 A（默认）— 前两步同步、第三步异步后退出**

```python
advance_step(step_id='step1', rewind=True, retry_hint='...')
advance_step(step_id='step2', rewind=True, retry_hint='...')
advance_step_and_hand_off(step_id='step3', rewind=True, retry_hint='...')
# advance_step_and_hand_off 是 stop-tool，ReAct 退出；step3 SubAgent 后台运行
return '已提交步骤 3 后台执行'
```

**方案 B — 三步全同步，ChatAgent 汇总后退出**

```python
# ChatAgent ReAct 内（dynamic 模式）
for step_id in ['step1', 'step2', 'step3']:
    advance_step(step_id=step_id, rewind=True, retry_hint='用户要求重跑此步')
# 三步完成后 ChatAgent 生成汇总文本，ReAct 自然结束
```

**`build_advance_step_tool` docstring 片段**

```python
"""
...
若用户要求重跑多个步骤，请按顺序多次调用本工具（每次一个 step_id），
不要使用虚构的 run_range 参数；Go 不会批量重置步骤状态。
"""
```

**`advance_step_and_hand_off` 工具定义（注册为 stop-tool）**

```python
def advance_step_and_hand_off(
    step_id: str,
    rewind: bool = False,
    retry_hint: str | None = None,
    resume: bool = False,
) -> str:
    """Advance a plugin step and END the current conversation turn immediately.

    After calling this tool, the current ReAct loop exits and SSE closes.
    The step runs in the background; when it completes, the next decision is
    made by the DriverAgent (auto mode) or the user (dynamic mode).

    This is the DEFAULT tool for advancing steps. Use it unless you explicitly
    need to run multiple steps in sequence within a single turn (e.g. user
    asked to re-run steps 1 through 3). In that case, use `advance_step`
    (synchronous) for intermediate steps and this tool for the final step.
    """
    _write_agent_data('task_created', {
        'agent_type': 'plugin_step',
        'params': {'step_id': step_id, 'rewind': rewind,
                   'retry_hint': retry_hint, 'resume_mode': resume},
    })
    return 'Step queued. Exiting current turn — next decision by DriverAgent or user.'
```

**`advance_step` 工具定义（仅 `dynamic` 模式注册）**

```python
def advance_step(
    step_id: str,
    rewind: bool = False,
    retry_hint: str | None = None,
    resume: bool = False,
) -> str:
    """Advance a plugin step and WAIT for its completion within this turn.

    This tool blocks until the SubAgent finishes, then returns the step result
    summary. Use ONLY when you need to run multiple steps in sequence within
    a single conversation turn (e.g. user said "re-run steps 1 to 3").

    For single-step advancement, prefer `advance_step_and_hand_off` to let the
    user review the result and decide the next action.
    """
    # ... blocks via FileSystemQueue polling until step done ...
    return step_result_summary
```

**`resolve_plugin_injection` 工具注册逻辑**

```python
def _build_plugin_tools(plugin_mode: str, ...) -> list:
    tools = []
    # advance_step_and_hand_off: always registered (both modes)
    tools.append(build_advance_step_and_hand_off_tool(...))

    # advance_step (sync): only in dynamic mode
    if plugin_mode == 'dynamic':
        tools.append(build_advance_step_tool(...))

    # ... other tools (ask_user, update_intent, etc.) ...
    return tools
```

---

<a id="c8"></a>

## C8. `ask_user`：仅 ChatAgent

> 对应 plan §8.2。

```python
def ask_user(
    question: str,
    choices: list[str] | None = None,
    allow_multiple: bool = False,
) -> str:
    """Ask user and end current ReAct turn. Answer arrives on next chat request."""
    ask_id = str(uuid.uuid4())
    _write_agent_data('ask_pending', {
        'ask_id': ask_id,
        'question': question,
        'choices': choices or [],
        'allow_multiple': allow_multiple,
    })
    return f'已向用户提问 (ask_id={ask_id})，等待下轮回答'
```

**注册**：仅 ChatAgent；`ask_user` 加入 `plugin_stop_tools`（调用后 ReAct 立即退出）。

**Go** 转发 SSE `ask_pending`；**结束**当前 chat stream。

---

<a id="c9"></a>

## C9. `ask_response` 对话历史存储

> 对应 plan §8.3。

**下轮请求 body**

```json
{
  "input": [{"input_type": "text", "text": "我选竖版"}],
  "ask_response": { "ask_id": "...", "selected": ["竖版"] }
}
```

**`resolve_plugin_injection` 注入回答**

```python
if ask_response := kwargs.get('ask_response'):
    plugin_artifact_context += (
        f'\n[用户对 ask {ask_response["ask_id"]} 的回答: {ask_response["selected"]}]'
    )
```

**`conversation_messages` 写入（两条记录）**

| 轮次 | role | content | metadata |
| --- | --- | --- | --- |
| ask 发出轮 | `assistant` | ChatAgent 回复文本 | `ask_pending: { ask_id, question, choices }` |
| 用户回答轮 | `user` | 用户可见文本（如「竖版」） | `ask_response: { ask_id, selected }` |

前端重载对话时，检测到 `metadata.ask_pending` 则渲染卡片；已有 `selected` 则展示已选状态，不可再次提交。

SubAgent **不**注册 `ask_user`。

---

<a id="c10"></a>

## C10. 并行执行

> 对应 plan §5。

**原理**：并行由 **ChatAgent** 决策——在一次 ReAct 步骤中同时输出多个 `advance_step` / `advance_step_and_hand_off`（parallel tool calls），每个触发一个独立的 `task_created`，Go 分别走 `HandlePluginStepCreated` → `go subagent.Run`，实现并发执行。Go 侧不主动扫描依赖图，仅做并行跟踪。

**Python：可选辅助查询工具（供 ChatAgent 读取依赖）**

```python
def get_parallel_steps(session_id: str) -> list[str]:
    """Return step IDs whose prerequisites are met and have not yet started.
    ChatAgent uses this to decide which steps to launch in parallel."""
    ...
```

**Go：并行跟踪（`OnSubAgentDone` 扩展）**

`plugin_sessions.parallel_step_ids`（JSONB）记录当前批次在跑的 step 集合。每个步骤完成时调用：

```go
func onParallelStepDone(
    ctx context.Context, db *gorm.DB, pctx *PluginChatContext,
    summary string, onSSE func(string, map[string]any),
) {
    onSSE("step_parallel_done", map[string]any{"step_id": pctx.StepID})
    remaining := RemoveParallelStep(ctx, db, pctx.SessionID, pctx.StepID)
    if len(remaining) > 0 {
        return // 等待其余并行步骤完成
    }
    // 全部完成 → DriverAgent 裁决或 step_waiting
    if pctx.PluginMode == "auto" {
        go advanceAutoMode(ctx, db, stateStore, summary, onSSE, pctx)
    } else {
        _ = UpdateSessionStatus(ctx, db, pctx.SessionID, SessionStatusWaiting)
        onSSE("step_waiting", map[string]any{"reason": "dynamic_pause", "step_id": pctx.StepID})
    }
}
```

**注册并行批次（ChatAgent 触发多个 task_created 时）**

```go
// HandlePluginStepCreated 启动 SubAgent 前，将 step_id 加入并行集合
store.AddParallelStep(ctx, db, sessionID, stepID)
go subagent.Run(ctx, db, stateStore, buildRunRequest(stepID))
```

**混用 sync/exit 的行为**：`_tools_manager(tool_calls)` 并发执行所有工具后，框架检查 stop-tools。`advance_step`（同步）在工具并发执行阶段就等完了；`advance_step_and_hand_off` 立即返回并触发退出。两个 SubAgent 均已启动，结果符合预期，无需特殊处理。

---

<a id="c11"></a>

## C11. TaskCenter 与定时调度

**双层模型**：`user_schedules` = 定时规则；`task_center_tasks` = 每次触发的一次执行（`task_type=scheduled`，`schedule_id` 关联），与普通 Task 同表。

**不复用 `asyncjob.Enqueue("plugin_step_run", ...)`**

```go
// taskcenter/repository.go
func (r *Repo) CreateTask(ctx context.Context, t Task) error {
    // INSERT task_center_tasks — 绑定 conversation_id，非 sub_agent_task_id
}

// 首次 plugin task_created 时
func onPluginTaskCreated(ctx context.Context, convID, sessionID, userID string) {
    taskcenter.EnsureTask(ctx, db, taskcenter.EnsureRequest{
        ConversationID:  convID,
        PluginSessionID: sessionID,
        UserID:          userID,
        TaskType:        "plugin_run",
        Title:           pluginTitle,
    })
}
```

**用户定时 — `create_schedule` 工具**

```python
def create_schedule(cron_expr: str, prompt_template: str, timezone: str = 'Asia/Shanghai') -> str:
    """Create user schedule. NOT from plugin.yaml."""
    TaskQueryDB().create_user_schedule(
        user_id=current_user_id(),
        cron_expr=cron_expr,
        prompt_template=prompt_template,
        timezone=timezone,
    )
    return f'已创建定时任务: {cron_expr}'
```

**`taskcenter/scheduler.go`**

```go
func (s *Scheduler) triggerSchedule(ctx context.Context, sch UserSchedule) {
    convID := sch.ConversationID
    if convID == "" {
        convID = createConversation(ctx, sch.UserID)
    }
    // 每次触发 INSERT 新执行实例（非 recurring 类型行）
    taskcenter.CreateTask(ctx, Task{
        TaskType:       "scheduled",
        ConversationID: convID,
        ScheduleID:     sch.ID,
    })
    triggerNextChatTurn(convID, sch.UserID, sch.PromptTemplate)
    updateScheduleNextRun(ctx, sch)
}
```

---

<a id="c12"></a>

## C12. 查询工具

```python
def list_plugin_steps(session_id: str | None = None) -> str:
    """List step statuses. Read-only."""
    ...

def get_step_result(step_id: str) -> str:
    """Artifact summary for step. Read-only."""
    ...

def get_failed_steps() -> str:
    """Failed steps with error messages. Read-only."""
    ...
```

仅在有 `active_session_id` 时注入 ChatAgent；SubAgent 不注入。

---

<a id="c13"></a>

## C13. 步骤中断恢复（Checkpoint-Resume）

**核心思路**：不新增工具参数、不增加 Go 逻辑。SubAgent 已能看到全部 artifact 清单，ChatAgent 通过 `retry_hint` 传达"续做"语义即可。

**ChatAgent 构造 retry_hint（system prompt 指引）**

```python
# 用户说「继续」且步骤状态为 interrupted：
advance_step_and_hand_off(
    step_id='generate_image',
    retry_hint=(
        'Previous attempt was interrupted. Check existing artifacts for this step '
        'and only produce missing outputs. Do not regenerate already-saved artifacts.'
    ),
)

# 用户说「重试」：
advance_step_and_hand_off(step_id='generate_image', rewind=True)
```

**SubAgent 视角（`_objective_prompt` 已有内容，无需改动）**

SubAgent 启动时已能看到：

```
## Objective
Generate images based on the optimized prompt. Expected outputs: raw_image_url (5 images).

## Available artifacts (session)
  - optimized_prompt: "A serene landscape with soft golden lighting..."
  - raw_image_url[0]: https://...img1.png
  - raw_image_url[1]: https://...img2.png

## Runtime instruction (retry_hint)
Previous attempt was interrupted. Check existing artifacts for this step
and only produce missing outputs. Do not regenerate already-saved artifacts.
```

SubAgent 自行对比 expected outputs（5 张 raw_image_url）与已有 artifact（2 张），续做剩余 3 张。

**为什么这样做更好**

| 对比 | 旧方案（Go 组装 checkpoint） | 新方案（retry_hint + SubAgent 自判断） |
| --- | --- | --- |
| Go 改动 | 新增 `resume_mode` 字段、查 artifact、组装 completed/missing | 零改动 |
| Python 改动 | 新增 `checkpoint_context` 区块注入 | 零改动（`retry_hint` 已有） |
| 工具参数 | 新增 `resume: bool` | 零新增 |
| 灵活性 | 确定性代码，难处理 list 部分保存 | LLM 自行判断，天然理解"5 张存了 2 张" |
| 实现成本 | 高 | 仅 ChatAgent system prompt 加一段指引 |
