# M3 — 步骤条 + AsyncJob + human/auto 模式

## 概述

**里程碑目标**：图片插件升级为多步骤可控流程，落地 AsyncJob 长任务调度、human 模式（用户确认后继续）和 auto 模式（agent 读 `driver.md` 自动决策），完成插件框架的执行控制层基建。

**前置依赖**：M2 完成（版本管理、artifacts/versions 表、SSE patch 快照）。

**验收标准（一句话）**：human 模式下 prompt 优化完成后工作流暂停，用户点「继续」后生图；切换 auto 模式后 agent 读 `driver.md` 自动推进；关闭页面重开后步骤状态正确恢复。

---

## 一、需要实现的功能

### 1.1 数据层扩展

**`async_jobs` 表新增字段**

```sql
ALTER TABLE async_jobs
  ADD COLUMN conversation_id   VARCHAR(36) NOT NULL DEFAULT '' INDEX,
  ADD COLUMN lock_ttl_seconds  INT NOT NULL DEFAULT 0;
```

- `lock_ttl_seconds = 0`：使用全局默认 Lock TTL（10 分钟）
- Plugin session Job 使用 `lock_ttl_seconds = 86400`（1 天），支持 human 模式长时间等待用户

**`plugin_sessions.current_step_id` 语义更新**

M1 时 `current_step_id` 暂存步骤名字符串，M3 正式改为 FK 指向 `plugin_session_steps.id`：

```sql
ALTER TABLE plugin_sessions
  ADD CONSTRAINT fk_plugin_sessions_current_step
  FOREIGN KEY (current_step_id) REFERENCES plugin_session_steps(id);
```

**Go ORM 更新**：`AsyncJob` 结构体新增两个字段，`PluginSession.CurrentStepID` 改为正确的 FK 类型。

### 1.2 AsyncJob Handler 多步骤架构

**Plugin Session Job 的生命周期**

每个 plugin session 对应一个 long-lived AsyncJob，生命周期内串行执行所有步骤：

```
创建 Session
  └── 创建 AsyncJob（conversation_id=..., lock_ttl=86400）
        └── Handler 循环执行步骤
              ├── 步骤 N running
              │     → 执行 algorithm.run(ctx)
              │     → emit PluginEvent(step_change, status='running')
              │     → SSE 流式输出
              ├── 步骤 N 完成
              │     → emit PluginEvent(step_change, status='waiting'|'done')
              │     → 更新 plugin_session_steps.step_status
              │     ↓
              │   [human 模式]
              │     → step_status = 'waiting'
              │     → Redis BLPOP 等待信号（key: plugin:proceed:{session_id}）
              │     → 用户点继续 or 对话触发 plugin_proceed()
              │     → Redis 写信号 → Handler 继续
              │   [auto 模式]
              │     → step_status = 'waiting'
              │     → 框架触发一次 ReactAgent 调用
              │     → system prompt 注入 driver.md
              │     → Agent 调用 plugin_proceed() 或 plugin_edit()
              └── 步骤 N done，推进到步骤 N+1
```

**Redis 信号协议**

- Key：`plugin:proceed:{session_id}`
- Value：`{"target_step": "generate", "agent_session_id": "..."}` 或 `{"action": "edit", ...}`
- TTL：与 AsyncJob `lock_ttl_seconds` 对齐（86400s）
- Handler 使用 `BLPOP`（阻塞等待），超时后重试或标记 Job 失败

### 1.3 `plugin_proceed` 工具（Python）

在 `plugins/manager.py` 中注册 `plugin_proceed` 工具：

```python
def plugin_proceed(session_id: str, target_step: str) -> dict:
    """
    推进 plugin session 到指定步骤。
    1. 从 loader 缓存读取 state.yml，执行硬约束校验
    2. 校验通过：写 Redis 信号，唤醒 AsyncJob Handler
    3. 校验失败：返回错误，Agent 不会推进
    """
    state_machine = loader.get_state_machine(session_id_to_plugin_id(session_id))
    current_step = get_current_step(session_id)
    if not state_machine.is_valid_transition(current_step, target_step):
        return {"error": f"Invalid transition: {current_step} → {target_step}"}
    redis.lpush(f"plugin:proceed:{session_id}", json.dumps({
        "target_step": target_step
    }))
    return {"ok": True, "target_step": target_step}
```

**硬约束校验**：即使 Agent 判断有误，`state.yml` 中未定义的步骤跳转会被工具层拦截，保证工作流安全。

### 1.4 `plugin_edit` 工具（Python）

```python
def plugin_edit(session_id: str, artifact_id: str, instruction: str) -> dict:
    """
    对当前步骤的某个 artifact 发起修改请求。
    写 Redis 信号，触发 Handler 重新执行当前步骤的修改逻辑。
    """
    redis.lpush(f"plugin:proceed:{session_id}", json.dumps({
        "action": "edit",
        "artifact_id": artifact_id,
        "instruction": instruction
    }))
    return {"ok": True}
```

### 1.5 `POST /plugin-sessions/:id/proceed` 接口（Go）

human 模式下用户点击「继续」按钮触发：

```
POST /api/v1/plugin-sessions/:id/proceed
Body: { "target_step": "generate" }  // 可选，不传则推进到默认下一步

处理逻辑：
1. 验证 session 存在且属于当前用户
2. 读取当前步骤 step_status == 'waiting'（否则返回 409）
3. 写 Redis 信号（与 plugin_proceed() 工具相同格式）
4. 返回 202 Accepted
```

### 1.6 `step_change` SSE 事件扩展

在 M1 的 `PluginEvent` 中补充 `step_change` 类型：

```python
# step_change 事件字段
{
    "type": "step_change",
    "plugin_session_id": "ps-xxx",
    "step": "generate",
    "step_status": "running" | "waiting" | "done",
    "step_mode": "human" | "auto"   # 步骤完成时携带
}
```

Go SSE Handler 接收到 `step_change` 时：
- 写入 `plugin_session_steps` 表（create or update）
- 更新 `plugin_sessions.current_step_id`

### 1.7 auto 模式 Agent 决策触发

步骤完成且 `step_mode == 'auto'` 时，框架发起一次 ReactAgent 调用：

**触发时机**：AsyncJob Handler 在步骤完成后检查 `step_mode`，若为 `auto` 则：

```python
def trigger_auto_driver(session_id, completed_step):
    driver_md = loader.get_driver(plugin_id)
    current_artifacts = get_artifact_summaries(session_id)
    
    # 构造 system prompt
    system = f"""
{driver_md}

当前已完成步骤：{completed_step}
当前产物摘要：{current_artifacts}
"""
    # 发起正常的 ReactAgent 调用，只是 input 来自框架而非用户
    # Agent 可调用 plugin_proceed() 或 plugin_edit()
    agent.run(system_prompt=system, tools=[plugin_proceed, plugin_edit])
```

**关键约束**：auto 模式不引入新的工具或 API，完全复用 `plugin_proceed` / `plugin_edit` 路径，框架只是自动提供决策依据（`driver.md`）。

### 1.8 前端：`StepProgress` 组件

**功能需求**

- 展示当前插件的步骤列表（从 `plugin.yaml` steps 顺序）
- 每个步骤显示状态：`running`（旋转动画）、`waiting`（等待图标）、`done`（勾选）
- 每个步骤旁有 human/auto 切换开关（Toggle）
  - 切换时调用 `PATCH /plugin-sessions/:id` 或 `PATCH /plugin-sessions/:id/steps/:step_id` 更新 `step_mode`
- 当前步骤为 `waiting`（human 模式）时，显示「继续」按钮

**「继续」按钮交互**

```ts
// 点击「继续」
async function handleProceed(sessionId: string) {
  await fetch(`/api/v1/plugin-sessions/${sessionId}/proceed`, {
    method: 'POST',
    body: JSON.stringify({ target_step: nextStep })
  })
  // 等待 SSE step_change 事件更新步骤状态
}
```

**步骤状态来自 SSE**：`step_change` 事件通过 `pluginSessionStore` 更新步骤状态，组件响应式展示。

### 1.9 human 模式 UI：步骤完成展示

步骤进入 `waiting` 状态时：
- `StepProgress` 中该步骤显示「等待确认」
- `PluginShell` 主内容区展示当前步骤的产物（如优化后的 prompt 文本）
- 底部「继续」按钮（也可在 `StepProgress` 中展示）

---

## 二、实施计划

### 阶段划分

**Week 1 — 数据层 + AsyncJob 扩展**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `async_jobs` migration（两个新字段） | Backend (Go) | 0.5d |
| `plugin_sessions.current_step_id` FK migration | Backend (Go) | 0.5d |
| Go ORM 更新 | Backend (Go) | 0.5d |
| `POST /proceed` API 实现 | Backend (Go) | 1d |
| Redis BLPOP 等待逻辑（AsyncJob Handler） | Backend (Go) | 1d |

**Week 2 — 算法层步骤控制**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `plugin_proceed` 工具（含 state.yml 硬约束校验） | Algorithm (Python) | 1d |
| `plugin_edit` 工具 | Algorithm (Python) | 0.5d |
| `manager.py` 注册两个新工具 | Algorithm (Python) | 0.5d |
| image-plugin 升级为两步骤（prompt-optimize + generate） | Algorithm (Python) | 0.5d |
| `step_change` SSE 事件 emit | Algorithm (Python) | 0.5d |

**Week 3 — auto 模式 + 前端**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| auto 模式 agent 决策触发（`trigger_auto_driver`） | Algorithm (Python) | 1d |
| image-plugin `driver.md` 编写 | Algorithm (Python) | 0.5d |
| Go `step_change` 事件处理（写 DB） | Backend (Go) | 0.5d |
| `StepProgress` 组件（含 human/auto 切换） | Frontend (TS) | 1d |
| `pluginSessionStore` 扩展（步骤状态） | Frontend (TS) | 0.5d |
| human 模式「继续」按钮联调 | All | 0.5d |

**Week 4 — 联调 + 恢复测试**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| 关闭页面重开后状态恢复联调 | All | 1d |
| auto 模式联调（driver.md 注入 + agent 自动推进） | All | 1d |
| E2E 测试 | All | 0.5d |

---

## 三、测试方案

### 3.1 单元测试

**`plugin_proceed` 硬约束校验**

```python
def test_plugin_proceed_valid_transition():
    # state.yml: generate → generate（循环修改）
    # current='generate', target='generate' → ok

def test_plugin_proceed_invalid_transition():
    # state.yml 中 generate 没有到 nonexistent 的边
    # expect 返回 {"error": "Invalid transition: ..."}

def test_plugin_proceed_writes_redis():
    # 合法 transition 后，redis mock 验证 lpush 被调用
```

**AsyncJob Handler 多步骤**

```go
func TestHandlerPausesOnHumanMode(t *testing.T) {
    // 步骤 1 完成（step_mode='human'），验证 job 进入 BLPOP 等待
    // 发送 Redis 信号，验证 job 继续执行步骤 2
}

func TestHandlerLockTTL_1Day(t *testing.T) {
    // 验证 plugin session job 的 lock_ttl_seconds == 86400
}

func TestProceedAPI_RejectsIfNotWaiting(t *testing.T) {
    // 步骤状态为 'running' 时调 /proceed，expect 409
}
```

**auto 模式触发**

```python
def test_auto_mode_triggers_agent_on_step_complete():
    # step_mode='auto'，步骤完成后，mock agent.run 被调用
    # 验证 system prompt 包含 driver.md 内容

def test_auto_mode_agent_can_call_plugin_proceed():
    # agent 调用 plugin_proceed() → Redis 信号 → job 继续
```

### 3.2 API 测试

```
# human 模式：步骤等待，点继续
POST /api/v1/plugin-sessions/:id/proceed
期望：202 Accepted，步骤继续执行

# 步骤不在 waiting 状态时点继续
POST /api/v1/plugin-sessions/:id/proceed
期望：409 Conflict

# step_mode 切换
PATCH /api/v1/plugin-sessions/:id
Body: { "step_mode": "auto" }
期望：200

# 获取步骤列表
GET /api/v1/plugin-sessions/:id/steps
期望：steps 数组，含 step_status 和 step_mode
```

### 3.3 前端测试

**`StepProgress` 组件**

```ts
describe('StepProgress', () => {
  it('shows running spinner for active step', () => {
    // step_status='running'，验证加载动画
  })
  it('shows waiting icon and Continue button', () => {
    // step_status='waiting', step_mode='human'
    // 验证「继续」按钮可见
  })
  it('hides Continue button in auto mode', () => {
    // step_mode='auto'，「继续」按钮不可见
  })
  it('toggle switches step_mode and calls API', async () => {
    // 点击 Toggle，验证 PATCH 请求发出
  })
  it('shows done checkmark for completed step', () => {
    // step_status='done'，验证勾选图标
  })
})
```

### 3.4 端到端（E2E）验收测试

**human 模式完整流程**

```
1. 触发图片生成插件
2. 步骤 1（prompt-optimize）执行
   → 验证 StepProgress 显示「运行中」
3. 步骤 1 完成，进入 waiting
   → 验证 StepProgress 显示「等待确认」
   → 验证「继续」按钮出现
   → 验证优化后的 prompt 文本显示在主内容区
4. 点击「继续」
   → 验证步骤 2（generate）开始运行
   → 验证图片生成完成后 ImageCard 出现
5. 验证整个流程中步骤状态变化顺序：
   running → waiting → (继续) → running → done
```

**auto 模式完整流程**

```
1. 在 StepProgress 中切换为 auto 模式
2. 触发图片生成
3. 步骤 1 完成后
   → 验证 agent 自动决策（无需用户点继续）
   → 验证步骤 2 自动开始
   → 验证最终图片生成
4. 检查 agent 日志：确认 driver.md 内容出现在 system prompt
```

**页面重开状态恢复**

```
1. 触发插件，步骤 1 进入 waiting 状态
2. 刷新页面
3. 验证 StepProgress 正确显示 waiting 状态
4. 验证「继续」按钮仍然可用
5. 点击继续，验证步骤正确恢复执行
```

---

## 四、验收标准

| 验收项 | 验收方式 | 通过标准 |
|--------|----------|----------|
| human 模式暂停等待 | E2E | 步骤完成后显示 waiting，不自动推进 |
| 用户点继续后恢复执行 | E2E | /proceed API 触发后步骤继续 |
| state.yml 硬约束拦截非法跳转 | 单元测试 | 非法 transition 返回 error |
| auto 模式 agent 自动推进 | E2E | driver.md 注入后 agent 自动调 plugin_proceed |
| step_mode 切换生效 | 前端测试 + E2E | Toggle 切换后行为符合预期 |
| 刷新后状态正确恢复 | E2E | waiting 状态刷新后仍是 waiting |
| AsyncJob lock_ttl 为 1 天 | 单元测试 | DB 记录 lock_ttl_seconds=86400 |
| step_change SSE 正确更新 DB | 单元测试 | step_status 正确写入 steps 表 |

---

## 五、注意事项与风险

1. **Redis BLPOP 超时处理**：BLPOP 等待 86400 秒会占用 Redis 连接，建议设置合理超时（如 60s）并在超时后重试，避免连接泄漏。
2. **auto 模式循环风险**：`driver.md` 中规定「每步最多循环修改 2 次，超出则强制推进」，框架需记录每步的 edit 次数并强制执行上限，防止 agent 无限循环修改。
3. **plugin_proceed 并发调用**：用户同时通过 UI 点「继续」和对话触发 `plugin_proceed`，可能写两次 Redis 信号，需要 Handler 侧做幂等处理。
4. **state.yml 缺失时的回退逻辑**：如果 `state.yml` 不存在，`plugin_proceed` 校验需回退到 `plugin.yaml` steps 的线性顺序，不能直接报错。
5. **driver.md 缺失时的 auto 模式**：`driver.md` 不存在时，auto 模式应降级为 human 模式并打印 warning，避免无 guidance 的 agent 做出不可预期的决策。
