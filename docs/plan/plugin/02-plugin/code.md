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

帮助用户生成并增强高质量图片。工作流分五步：

1. **analyze_subject** — 分析用户描述的主体、风格、氛围
2. **collect_materials** — 收集参考素材，为后续生成提供参考
3. **optimize_prompt** — 基于分析结果生成高质量英文图片生成 prompt
4. **generate_image** — 调用图片生成模型产出原始图片
5. **enhance_image** — 对原始图片进行风格增强处理

## 用户意图识别

### 冷启动（无活跃会话）

- 用户提到「生成图片」、「画一张」、「绘制」等图片生成类请求
  → 调用 `trigger_image_plugin(user_input=<用户原始描述>)`

### 有活跃会话时

| 用户意图 | 推荐步骤 | 工具调用 |
|---|---|---|
| 想重新收集参考素材 | collect_materials | `advance_step(step_id='collect_materials', user_input=<说明>)` |
| 对 prompt 不满意，想重新优化 | optimize_prompt | `advance_step(step_id='optimize_prompt', user_input=<说明>)` |
| 想用当前 prompt 重新生图 | generate_image | `advance_step(step_id='generate_image', user_input=<说明>)` |
| 想重新增强（换风格 / 更高清） | enhance_image | `advance_step(step_id='enhance_image', user_input=<说明>)` |
| 对最终结果满意 | （无需操作，DriverAgent 自动判 DONE） | — |

可用的前序步骤由 `advance_step` 工具的 Rewind 列表动态给出，无需在此枚举。

## 重要规则

- 冷启动时必须调用 `trigger_image_plugin`，不要跳过。
- 调用工具后立即停止，不输出额外文字。
- 步骤触发信号由系统处理，你无需等待步骤完成。
```

---

<a id="c2"></a>

## C2. `_trigger_plugin_step` 工具实现（两层校验 + task_created）

`trigger_<plugin_id>` 和 `advance_step` 均调用此共享实现。

```python
import uuid
import httpx
import lazyllm
from lazyllm.tools.agent.base import _write_agent_data
from lazymind.chat.plugin import plugin_loader
from lazymind.config import config as _cfg


def _agentic_config() -> dict:
    try:
        return lazyllm.globals['agentic_config'] or {}
    except Exception:
        return {}


def _trigger_plugin_step(
        plugin_id: str, step_id: str, user_input: str,
        is_cold_start: bool = False,
        runtime_instruction: str = '',
        partial_indices: dict | None = None) -> str:
    cfg = _agentic_config()
    session_id: str = cfg.get('plugin_session_id', '') or str(uuid.uuid4())

    # --- 第一层：格式校验（不需要 DB）---
    if not user_input or not user_input.strip():
        user_input = cfg.get('query', '').strip()
    if not user_input:
        return 'Error: user_input must not be empty.'

    sm = plugin_loader.get_state_machine(plugin_id)
    if sm is None:
        return f'Error: plugin {plugin_id!r} not found.'

    current_step: str = cfg.get('plugin_step', '')
    if not sm.is_reachable(current_step, step_id):
        # 允许 rewind 到已成功的祖先节点
        ancestors = sm.get_ancestors(current_step)
        if step_id in ancestors:
            succeeded = _fetch_succeeded_steps(session_id)
            if step_id not in succeeded:
                return (
                    f'Error: step {step_id!r} is an ancestor of {current_step!r} '
                    f'but has not succeeded in this session yet.'
                )
            # ancestor rewind 合法，跌落到第二层校验
        else:
            reachable = sm.get_reachable_steps(current_step)
            return (
                f'Error: step {step_id!r} is not reachable from '
                f'{repr(current_step) if current_step else repr("__start__")}. '
                f'Reachable steps: {reachable}.'
            )

    # --- 第二层：依赖状态校验（通过 Go core REST API 查询）---
    step_config = plugin_loader.get_step_config(plugin_id, step_id)
    inputs: list = step_config.get('inputs', [])
    if inputs and not is_cold_start and session_id:
        core_url = str(_cfg['core_api_url']).rstrip('/')
        try:
            resp = httpx.get(f'{core_url}/plugin-sessions/{session_id}', timeout=3.0)
            if resp.status_code == 200:
                steps_data = {
                    s['step_id']: s['status']
                    for s in resp.json().get('data', {}).get('session', {}).get('steps', [])
                    if isinstance(s, dict)
                }
                for inp in inputs:
                    artifact_id = inp['artifact_id']
                    required = inp.get('required', True)
                    producer_step = plugin_loader.find_producer_step(plugin_id, artifact_id)
                    if not producer_step:
                        continue
                    step_status = steps_data.get(producer_step)
                    if step_status is None:
                        if required:
                            return (
                                f'Error: required artifact {artifact_id!r} not available. '
                                f'Please trigger {producer_step!r} first.'
                            )
                        continue
                    if step_status in ('running', 'failed', 'interrupted'):
                        return (
                            f'Error: artifact {artifact_id!r} not ready '
                            f'(producer step {producer_step!r} status: {step_status!r}).'
                        )
        except Exception:
            pass  # 降级：跳过校验，Go 侧会再做防御性断言

    # --- 校验通过，发出 task_created 信号 ---
    task_id = str(uuid.uuid4())
    output_keys = [o['artifact_id'] for o in step_config.get('outputs', [])]
    input_keys = [i['artifact_id'] for i in inputs]

    # 框架工具强制前置，插件自定义工具追加
    declared_tools: list = step_config.get('tools', [])
    merged_tools = ['save_artifact', 'load_artifact', 'list_artifacts'] + [
        t for t in declared_tools if t not in {'save_artifact', 'load_artifact', 'list_artifacts'}
    ]

    params: dict = {
        'plugin_id': plugin_id,
        'step_id': step_id,
        'session_id': session_id,
        'user_input': user_input,
        'is_cold_start': is_cold_start,
    }
    if runtime_instruction:
        params['retry_hint'] = runtime_instruction  # Go 侧字段名
    if partial_indices:
        params['partial_indices'] = partial_indices

    _write_agent_data(
        'task_created',
        task_id=task_id,
        title=f'{plugin_id}:{step_id}',
        agent_type='plugin_step',
        mode='manual',          # Plugin step 统一异步；Go 控制是否自动推进
        objective=_render_step_objective(step_config, user_input, runtime_instruction),
        params=params,
        input_artifact_keys=input_keys,
        output_artifact_keys=output_keys,
        tools=merged_tools,
        resume=False,
    )
    return f'Step {step_id!r} triggered. Stop here.'


def _render_step_objective(step_config: dict, user_input: str,
                            runtime_instruction: str = '') -> str:
    '''将 state.yml step.prompt 中的模板变量在 Python 侧替换。

    {{user_input}} 替换为实际用户输入；
    {{runtime_instruction}} 替换为本次临时指令（无则置空）；
    其余变量（如 {{optimized_prompt}}）由 Go 在构造 objective 时注入 artifact 值。
    '''
    prompt = step_config.get('prompt', '')
    prompt = prompt.replace('{{user_input}}', user_input)
    prompt = prompt.replace('{{runtime_instruction}}', runtime_instruction)
    return prompt
```

> **模板变量注入顺序**：`{{user_input}}` 和 `{{runtime_instruction}}` 在 Python 侧触发时替换；`{{optimized_prompt}}` 等依赖前序 artifact 的变量由 Go 在创建 `sub_agent_tasks` 记录时查 `sub_agent_artifacts` 表注入，写入 `objective` 字段。SubAgent 框架从 `objective` 读取，不感知注入过程。

---

<a id="c3"></a>

## C3. `chat_service.py` Plugin 工具注入

```python
# chat_service.py（简化示意）
async def handle_chat(query, history, mode, plugin_context=None, **kwargs):
    agentic_config = {..., 'mode': mode, 'query': query}

    # resolve_plugin_injection 封装了所有 plugin-context 分支逻辑：
    # - 有活跃 session  → 注入 advance_step（含 forward + rewind 步骤列表）
    # - 无活跃 session  → 注入所有已加载插件的 trigger_<id> 工具
    plugin_tools, plugin_prompt, plugin_stop_tools, config_patch = \
        resolve_plugin_injection(plugin_context)

    agentic_config.update(config_patch)

    # set_stop_tools 确保触发后 ReAct 立即停止
    react_agent.set_stop_tools(plugin_stop_tools)

    # 拼入工具列表并注入 system prompt
    all_tools = base_tools + plugin_tools
    system = base_system + ('\n\n' + plugin_prompt if plugin_prompt else '')

    async for ev in drive_agent(react_agent, query, history=history,
                                 system=system, tools=all_tools,
                                 agentic_config=agentic_config):
        yield ev
```

`resolve_plugin_injection` 返回四元组 `(plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch)`，内部处理以下分支：

- **有活跃 session**（`plugin_context.session_id` 非空）：注入 `advance_step` 工具，docstring 动态嵌入当前可 forward / rewind 的步骤列表（通过 Go core REST API 查询已成功步骤）。stop_tools = `['advance_step']`。
- **冷启动**（无 session 或未传 plugin_context）：注入所有已加载插件的 `trigger_<plugin_id>` 工具，stop_tools = 所有 trigger 工具名。

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
You are the DriverAgent for the AI Image Generation plugin.
Your job is to evaluate whether a step result is acceptable and decide how to advance.

## Step evaluation rules

### analyze_subject
- `subject_analysis` artifact saved AND contains ≥ 50 words → PASS
- Artifact missing or too short → RETRY
- Failed 2+ consecutive times → FAIL

### collect_materials
- At least one `material_image` artifact saved → PASS
- For a partial retry, at least the requested items were re-collected → PASS
- No artifacts saved at all → RETRY
- Failed 2+ consecutive times → FAIL

### optimize_prompt
- `optimized_prompt` artifact saved AND contains an English prompt of ≥ 30 words → PASS
- Artifact missing, too short, or not in English → RETRY
- Failed 2+ consecutive times → FAIL

### generate_image
- `generated_image_url` artifact saved AND URL starts with `http://` or `https://` → PASS
- Only text output, no image URL → RETRY
- Failed 2+ consecutive attempts → FAIL

### enhance_image
- `enhanced_image_url` artifact saved AND URL starts with `http://` or `https://` → DONE
- Artifact missing or invalid URL → RETRY
- Failed 2+ consecutive attempts → FAIL

## Output format

Always wrap your verdict in `<verdict>VERDICT</verdict>` and a brief reason in
`<reason>reason</reason>`. When the root cause lies in a prior step, name the
upstream step in your reason so the ChatAgent can rewind to it.

Examples:
<verdict>PASS</verdict><reason>subject_analysis saved with 120 words.</reason>
<verdict>PASS</verdict><reason>optimized_prompt saved: 65-word English prompt.</reason>
<verdict>DONE</verdict><reason>enhanced_image_url saved successfully. Pipeline complete.</reason>
<verdict>RETRY</verdict><reason>No optimized_prompt artifact found in step output.</reason>
<verdict>RETRY</verdict><reason>Generated image off-topic; recommend rewinding to analyze_subject.</reason>
<verdict>FAIL</verdict><reason>generate_image failed 3 consecutive times without producing a URL.</reason>
```
