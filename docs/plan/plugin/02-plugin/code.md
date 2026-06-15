# Plugin 方案 · 示例代码

> 本文件收录 [`plan.md`](./plan.md) 中引用的示例 / 伪代码。代码仅示意关键逻辑与边界，非最终实现；落地以 `plan.md` 的约束为准。

## 目录

- [C1. scenario.md 示例（image-plugin）](#c1)
- [C2. `_trigger_plugin_step` 工具实现（两层校验 + task_created）](#c2)
- [C3. `chat_service.py` Plugin 工具注入](#c3)
- [C4. Go Plugin EventLoop（task_created 分支 + done 推进）](#c4)
- [C5. DriverAgent 评判](#c5)

---

<a id="c1"></a>

## C1. scenario.md 示例（image-plugin）

注入 ChatAgent system prompt，用于意图识别与步骤决策。

```markdown
# AI 图片生成插件

## 场景描述

帮助用户生成高质量图片。流程分两步：先将用户描述优化为专业英文 prompt，再调用图片生成模型。

## 各步骤能力

- **optimize_prompt**：将用户的自然语言描述优化为高质量英文图片生成 prompt
- **generate_image**：根据优化后的 prompt 调用图片生成模型，产出图片 URL

## 用户意图识别

- 生成/绘制/创建图片类请求（无活跃会话）→ 调用 `trigger_image_plugin(user_input=...)`
- 已有活跃会话，推进下一步 → 调用 `advance_step(step_id=..., user_input=...)`
- 用户对图片不满意，要求修改描述 → `advance_step(step_id='optimize_prompt', user_input=新描述)`
- 用户要求重新生图（保持描述不变）→ `advance_step(step_id='generate_image', user_input=...)`
- 无关问题 → 直接回答，不调用任何 trigger/advance 工具

## 状态说明

- 无活跃会话：使用 `trigger_image_plugin`
- `optimize_prompt`：正在或已完成提示词优化
- `generate_image`：正在生成或已完成图片

## 重要规则

- 调用 trigger/advance 成功后**立即停止**，不输出额外文字。
- 步骤触发信号由系统处理，你无需等待步骤完成。
```

---

<a id="c2"></a>

## C2. `_trigger_plugin_step` 工具实现（两层校验 + task_created）

`trigger_<plugin_id>` 和 `advance_step` 均调用此共享实现。

```python
import uuid
import lazyllm
from lazymind.chat.engine.subagent.tools import _write_agent_data

def _trigger_plugin_step(step_id: str, user_input: str,
                          is_cold_start: bool = False) -> str:
    cfg = lazyllm.globals.get('agentic_config', {})
    plugin_id = cfg.get('plugin_id', '')
    session_id = cfg.get('plugin_session_id', '') or str(uuid.uuid4())  # 冷启动时生成占位

    # --- 第一层：格式校验（不需要 DB）---
    if not user_input or not user_input.strip():
        return 'Error: user_input must not be empty.'

    sm = plugin_loader.get_state_machine(plugin_id)
    current_step = cfg.get('plugin_step', '')
    if not sm.is_reachable(current_step, step_id):
        reachable = sm.get_reachable_steps(current_step)
        return (f'Error: step {step_id!r} is not reachable from {current_step!r}. '
                f'Reachable: {reachable}.')

    # --- 第二层：依赖状态校验（查 DB）---
    step_config = plugin_loader.get_step_config(plugin_id, step_id)
    inputs = step_config.get('inputs', [])
    if inputs:
        db_factory = cfg.get('db_session_factory')
        if db_factory:
            with db_factory() as db:
                for inp in inputs:
                    artifact_id = inp['artifact_id']
                    required = inp.get('required', True)
                    producer_step = _find_producer_step(plugin_id, artifact_id)
                    if not producer_step:
                        continue
                    row = db.execute(
                        'SELECT pss.status FROM plugin_session_steps pss '
                        'WHERE pss.session_id=:sid AND pss.step_id=:step '
                        'ORDER BY pss.attempt DESC LIMIT 1',
                        {'sid': session_id, 'step': producer_step}
                    ).fetchone()
                    if row is None:
                        if required:
                            return (f'Error: required artifact {artifact_id!r} not available. '
                                    f'Please trigger {producer_step!r} first.')
                        continue
                    if row['status'] in ('running', 'failed', 'interrupted'):
                        return (f'Error: artifact {artifact_id!r} not ready '
                                f'(producer step {producer_step!r} status: {row["status"]!r}).')

    # --- 校验通过，发出 task_created 信号 ---
    task_id = str(uuid.uuid4())
    plugin_yaml = plugin_loader.get_plugin_yaml(plugin_id)
    step_info = next((s for s in plugin_yaml.get('steps', []) if s['id'] == step_id), {})
    output_keys = [o['artifact_id'] for o in step_config.get('outputs', [])]

    _write_agent_data(
        'task_created',
        task_id=task_id,
        title=f'{plugin_id}:{step_id}',
        agent_type='plugin_step',
        mode='manual',          # Plugin step 统一异步（Go 决定是否 auto 推进）
        objective=_render_step_objective(step_config, user_input),
        params={
            'plugin_id': plugin_id,
            'step_id': step_id,
            'session_id': session_id,
            'user_input': user_input,
            'is_cold_start': is_cold_start,
        },
        input_artifact_keys=[i['artifact_id'] for i in inputs],
        output_artifact_keys=output_keys,
        tools=step_config.get('tools', []),
        resume=False,
    )
    return f'Step {step_id!r} triggered. Stop here.'


def _render_step_objective(step_config: dict, user_input: str) -> str:
    '''将 state.yml step.prompt 中的 {{user_input}} 替换为实际输入。
    其余模板变量（{{artifact_id}}）由 Go 在构造 objective 时注入真实 artifact 值。
    '''
    prompt = step_config.get('prompt', '')
    return prompt.replace('{{user_input}}', user_input)
```

> **模板变量注入顺序**：`{{user_input}}` 在 Python 侧触发时替换；`{{optimized_prompt}}` 等依赖前序 artifact 的变量由 Go 在创建 `sub_agent_tasks` 记录时查 `sub_agent_artifacts` 表注入，写入 `objective` 字段。SubAgent 框架从 `objective` 读取，不感知注入过程。

---

<a id="c3"></a>

## C3. `chat_service.py` Plugin 工具注入

```python
# chat_service.py（简化示意）
async def handle_chat(query, history, mode, plugin_context=None, **kwargs):
    agentic_config = {..., 'mode': mode}

    plugin_tools = []
    plugin_prompt = ''

    if plugin_context and plugin_context.get('session_id'):
        # 有活跃 Plugin Session：注入 advance_step 工具
        session_id = plugin_context['session_id']
        plugin_id  = plugin_context['plugin_id']
        current_step = plugin_context.get('current_step', '')

        agentic_config.update({
            'plugin_id': plugin_id,
            'plugin_session_id': session_id,
            'plugin_step': current_step,
        })

        sm = plugin_loader.get_state_machine(plugin_id)
        reachable = sm.get_reachable_steps(current_step)
        if reachable:
            plugin_tools = [build_advance_step_tool(plugin_id, current_step)]
        plugin_prompt = plugin_loader.get_scenario(plugin_id)

    else:
        # 冷启动：注入所有已加载插件的 trigger_<id> 工具
        plugin_tools = build_cold_start_tools()
        if plugin_tools:
            plugin_prompt = _build_cold_start_prompt()

    # set_stop_tools 确保触发后 ReAct 立即停止
    react_agent.set_stop_tools([t.name for t in plugin_tools])

    # 拼入工具列表并注入 system prompt
    all_tools = base_tools + plugin_tools
    system = base_system + ('\n\n' + plugin_prompt if plugin_prompt else '')

    async for ev in drive_agent(react_agent, query, history=history,
                                 system=system, tools=all_tools):
        yield ev
```

---

<a id="c4"></a>

## C4. Go Plugin EventLoop（task_created 分支 + done 推进）

> 拦截点是现有 SubAgent upstream 消费循环（`d.TaskCreated != nil`），Plugin Step 通过 `agent_type='plugin_step'` 走专属分支。

```go
// onUpstreamChunk 中 task_created 分支扩展
func onPluginStepCreated(d UpstreamStreamChunk, sseSender SSESender) {
    tc := d.TaskCreated
    params := tc.Params  // map[string]interface{}

    pluginID  := params["plugin_id"].(string)
    stepID    := params["step_id"].(string)
    sessionID := params["session_id"].(string)
    isCold    := params["is_cold_start"].(bool)

    // 1. 分配/复用 plugin_session
    if isCold {
        sessionID = createPluginSession(db, conv_id, pluginID, tc.TriggerHistoryID)
    }

    // 2. 注入前序 artifact 值到 objective（替换模板变量）
    enrichedObjective := injectArtifactsIntoObjective(db, tc.Objective, sessionID, stepID)

    // 3. 创建 sub_agent_tasks（通用函数，与普通 SubAgent 共用）
    task := createSubAgentTask(db, SubAgentTaskParams{
        ID:                 tc.TaskID,
        ConversationID:     conv_id,
        TriggerHistoryID:   tc.TriggerHistoryID,
        AgentType:          'plugin_step',
        Title:              tc.Title,
        Mode:               'manual',
        Objective:          enrichedObjective,
        Params:             tc.Params,
        InputArtifactKeys:  tc.InputArtifactKeys,
        OutputArtifactKeys: tc.OutputArtifactKeys,
        WorkspacePath:      allocWorkspace(tc.TaskID),
    })

    // 4. 创建 plugin_session_steps 记录
    attempt := getNextAttempt(db, sessionID, stepID)
    createPluginSessionStep(db, sessionID, stepID, attempt, task.ID)
    updatePluginSession(db, sessionID, stepID)

    // 5. 发 task_created 给前端（含 plugin_session_id）
    sseSender.ForwardPluginStepCreated(task, sessionID)

    // 6. 启动 SubAgent（与普通 SubAgent 完全共用 runSubAgent goroutine）
    go runSubAgent(task, false, sessionID, stepID, pluginID)
}

// routeToTaskSSE done 分支新增 Plugin 推进逻辑
func onSubAgentDone(db, rdb, ev TaskEvent, pluginCtx *PluginStepContext) {
    updateTaskFinalStatus(db, ev.TaskID, ev.Status, ev.Summary)
    updatePluginSessionStep(db, pluginCtx.SessionID, pluginCtx.StepID, ev.Status)
    writeRedis(rdb, ev.TaskID, ev)

    if ev.Status != 'succeeded' || pluginCtx == nil {
        return
    }

    stepMode := getStepDefaultMode(pluginCtx.PluginID, pluginCtx.StepID)
    if stepMode == 'auto' {
        // 调 DriverAgent，以 judgment 合成用户消息，触发新一轮 ChatAgent
        judgment := evaluateStep(pluginCtx.PluginID, pluginCtx.StepID, ev.Summary, pluginCtx.SessionID)
        verdict := parseVerdict(judgment)
        switch verdict {
        case 'DONE':
            updatePluginSessionStatus(db, pluginCtx.SessionID, 'completed')
            sseSender.Send(PluginCompletedEvent{SessionID: pluginCtx.SessionID})
        case 'FAIL':
            updatePluginSessionStatus(db, pluginCtx.SessionID, 'failed')
            sseSender.Send(ErrorEvent{Message: judgment})
        default:  // PASS / RETRY
            syntheticMsg := buildSyntheticUserMessage(verdict, pluginCtx.StepID, judgment)
            go triggerNextChatTurn(conv_id, pluginCtx.SessionID, syntheticMsg)
        }
    } else {
        // manual 模式：发 step_waiting，等待用户手动继续
        sseSender.Send(StepWaitingEvent{
            SessionID: pluginCtx.SessionID,
            StepID:    pluginCtx.StepID,
        })
    }
}
```

---

<a id="c5"></a>

## C5. DriverAgent 评判

```python
# driver_agent.py

def evaluate_step(plugin_id: str, step_id: str,
                  step_result: str, session_id: str) -> str:
    driver_md = plugin_loader.get_driver(plugin_id)
    if not driver_md:
        # plugin_loader 加载阶段已阻止 auto step 无 driver.md，此处仅防御
        return 'PASS Step completed. Proceed.'

    # driver.md < 3000 字时追加 scenario.md 补充语境
    if len(driver_md) < 3000:
        driver_md += '\n\n---\n## Scenario context\n' + plugin_loader.get_scenario(plugin_id)

    # 读取本 session 已产出的 artifacts 摘要
    artifacts = load_session_artifacts_summary(session_id)
    artifacts_text = '\n'.join(f'- {k}: {str(v)[:100]}' for k, v in artifacts.items())

    prompt = (
        driver_md
        + '\n\n---\n## Current context\n'
        + f'Step: {step_id}\nResult:\n{step_result[:500]}\n'
        + f'Artifacts:\n{artifacts_text}\n\n'
        + 'Output your verdict starting with PASS / RETRY / DONE / FAIL, '
        + 'followed by your reasoning.'
    )
    try:
        return llm(prompt).strip() or 'PASS Proceed.'
    except Exception as e:
        return f'PASS Driver evaluation failed ({e}). Proceeding.'
```

**裁决格式约定**：输出必须以 `PASS` / `RETRY` / `DONE` / `FAIL` 之一开头（Go 截取首词）。

---

## driver.md 示例（image-plugin）

```markdown
# Image Plugin Driver

你是图片生成流程的评判者。根据当前步骤执行结果，输出裁决。

## 裁决规则

**optimize_prompt 步骤**：
- 已产出 optimized_prompt artifact → PASS
- 未产出 → RETRY

**generate_image 步骤**：
- 已产出 image_url artifact 且 URL 有效 → DONE（流程完成）
- 仅产出文本无图片 → RETRY
- 连续失败 2 次以上 → FAIL

## 输出格式

以裁决词开头，后跟原因，例如：

PASS 提示词优化完成，质量良好，进入生图步骤。
DONE 图片已成功生成，流程完成。
RETRY 未保存 image_url artifact，请重试。
```
