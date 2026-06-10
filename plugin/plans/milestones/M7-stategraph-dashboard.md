# M7 — StateGraph 可视化/编辑 + 任务看板（增强功能）

## 概述

**里程碑目标**：在主流程完备（M1–M6）之后，提供开发者工具和用户监控能力的增强功能：StateGraph 可视化帮助理解工作流状态；StateGraph 编辑器（P1，仅限管理员）支持热重载修改状态机；任务看板提供会话级 Job 监控。M7 对 M1–M6 的主流程无影响。

**前置依赖**：M6 完成（写作 + PPT 插件均已验证，框架完整）。

**优先级划分**：
- **P0（必做）**：StateGraph 只读可视化 + 运行态叠加 + 任务看板
- **P1（按需）**：StateGraph 可编辑模式 + 状态机热重载

**验收标准（一句话）**：StateGraph 正确展示当前运行态（步骤状态 badge + 合法后继高亮）；编辑器修改 state.yml 后热重载生效（P1）；任务看板展示多插件并发进度。

---

## 一、需要实现的功能

### 1.1 StateGraph 可视化（P0）

**后端 API**

```
GET /api/v1/plugin-sessions/:id/state-graph
```

**响应格式**：合并 state.yml 的图结构 + 数据库中的运行态

```json
{
  "plugin_id": "writing-plugin",
  "session_id": "ps-xxx",
  "nodes": [
    {
      "id": "outline",
      "label": "大纲",
      "status": "done",         // 'idle' | 'running' | 'waiting' | 'done'
      "step_mode": "human",
      "artifact_summary": {
        "outline": "第一章: 背景...(共 5 章)"
      }
    },
    {
      "id": "draft",
      "label": "Draft",
      "status": "running",
      "step_mode": "human",
      "artifact_summary": {}
    },
    {
      "id": "final",
      "label": "完稿",
      "status": "idle",
      "step_mode": "auto",
      "artifact_summary": {}
    }
  ],
  "edges": [
    {
      "from": "outline",
      "to": "draft",
      "condition": "用户明确确认大纲，或表示满意并要求开始写作",
      "is_valid_from_current": true    // 当前步骤的合法后继（虚线高亮）
    },
    {
      "from": "outline",
      "to": "outline",
      "condition": "用户要求修改大纲方向",
      "is_valid_from_current": true
    },
    {
      "from": "draft",
      "to": "final",
      "condition": "用户确认 draft 可以生成完稿",
      "is_valid_from_current": false
    }
  ],
  "current_step": "draft"
}
```

**Go 实现逻辑**

```go
func GetStateGraph(c *gin.Context) {
    sessionID := c.Param("id")
    session := getSession(sessionID)
    
    // 读取 state.yml（通过 Python loader 缓存，Go 侧调内部 API 或共享缓存）
    stateYML := getStateYMLFromCache(session.PluginID)
    
    // 读取运行态（plugin_session_steps 表）
    steps := getSessionSteps(sessionID)
    
    // 合并：节点状态来自 steps 表，边结构来自 state.yml
    // 标记当前步骤的合法后继（is_valid_from_current）
    return mergeGraphAndRuntime(stateYML, steps, session.CurrentStepID)
}
```

**`StateGraph` 前端组件（readonly 模式，P0）**

基于 `ReactFlow` 实现：

- **节点**：每个步骤一个节点，显示步骤名 + 状态 badge（运行中/等待/完成/待开始）
- **边**：状态机转移边，带 condition label（悬停展示完整条件文字）
- **合法后继高亮**：当前可达的边用虚线高亮显示
- **artifact 预览**：节点展开后显示 `artifact_summary`
- **`readonly` prop**：P0 只读，P1 扩展为可编辑

```tsx
<StateGraph
  sessionId={sessionId}
  readonly={true}
  onRefresh={() => refetchStateGraph(sessionId)}
/>
```

**集成到 `PluginShell` 侧栏**

`PluginShell` 新增侧栏选项卡「流程图」，可折叠：

```tsx
<PluginShell
  pluginSessionId={sessionId}
  sidebar={
    <SidebarTabs>
      <Tab label="目录"><SidebarTOC ... /></Tab>
      <Tab label="流程图">
        <StateGraph sessionId={sessionId} readonly={true} />
      </Tab>
    </SidebarTabs>
  }
>
```

**实时更新**：`StateGraph` 监听 `step_change` SSE 事件，步骤状态变化时自动 refetch state-graph API（或从 store 直接更新节点状态）。

### 1.2 StateGraph 可视化编辑（P1）

**权限控制**

仅以下情况可访问编辑器：
- 用户角色为 `admin`
- 或环境变量 `LAZYMIND_DEV_MODE=true`

**后端 API**

```
GET  /api/v1/plugins/:plugin_id/state-machine
PUT  /api/v1/plugins/:plugin_id/state-machine
```

`GET` 返回当前 `state.yml` 内容（YAML 文本 + 解析后的图结构）：

```json
{
  "plugin_id": "writing-plugin",
  "state_yml_content": "initial: outline\ntransitions:\n  ...",
  "graph": { "nodes": [...], "edges": [...] },
  "layout": {
    "outline": { "x": 100, "y": 200 },
    "draft": { "x": 300, "y": 200 }
  }
}
```

`PUT` 保存修改后的状态机：

```json
// 请求体
{
  "state_yml_content": "initial: outline\ntransitions:\n  ...",
  "layout": { ... }   // 节点布局持久化
}

// 处理逻辑：
// 1. 调 Python validate_state_yml() 校验（通过 /plugins/:id/validate 接口）
// 2. 有 error → 返回 422 + 错误列表
// 3. 无 error → 写入 state.yml 文件 + 触发 loader.py 热重载（reload_plugin）
// 4. 返回 200 + 新的图结构

// 响应（成功）
{
  "ok": true,
  "graph": { "nodes": [...], "edges": [...] }
}

// 响应（失败）
{
  "ok": false,
  "errors": ["step 'nonexistent' in transitions.outline not found in steps"],
  "warnings": []
}
```

**热重载机制**

```python
# plugins/loader.py
def reload_plugin(plugin_id: str):
    """
    重新加载指定插件的 Scenario 文件缓存（不重启服务）。
    写入新的 state.yml 后调用此方法。
    """
    plugin_dir = get_plugin_dir(plugin_id)
    # 重新解析 state.yml → 更新 StateMachine 缓存
    # 重新读取 scenario.md / driver.md / prompts/
    _cache[plugin_id] = _load_plugin_scenario(plugin_dir)
```

**`StateGraph` 编辑模式（`editable` prop）**

```tsx
<StateGraph
  pluginId={pluginId}
  editable={isAdmin || isDevMode}
  onSave={async (newStateMachineYAML, layout) => {
    const res = await fetch(`/api/v1/plugins/${pluginId}/state-machine`, {
      method: 'PUT',
      body: JSON.stringify({ state_yml_content: newStateMachineYAML, layout })
    })
    if (!res.ok) {
      // 展示 errors，不关闭编辑器
    }
  }}
/>
```

**编辑功能**

- 节点增删改（步骤 ID、label、default_mode）
- 边增删改（from、to、condition 文本内联编辑）
- 节点拖拽（调整 layout，布局持久化存 DB 或本地文件）
- YAML 预览面板（编辑图形时实时生成 YAML，也支持直接编辑 YAML）
- 「保存」触发 `PUT /state-machine`，错误时在界面上显示

### 1.3 任务看板

**后端 API**

```
GET /api/v1/conversations/:id/jobs
```

响应：当前会话下所有关联 Job 及其 plugin session 信息

```json
{
  "conversation_id": "conv-xxx",
  "jobs": [
    {
      "job_id": "job-001",
      "plugin_session_id": "ps-001",
      "plugin_id": "writing-plugin",
      "plugin_name": "AI 写作",
      "current_step": "draft",
      "step_status": "running",
      "step_mode": "human",
      "steps": [
        { "id": "outline", "label": "大纲", "status": "done" },
        { "id": "draft", "label": "Draft", "status": "running" },
        { "id": "final", "label": "完稿", "status": "idle" }
      ],
      "job_status": "running",
      "created_at": "...",
      "updated_at": "..."
    },
    {
      "job_id": "job-002",
      "plugin_session_id": "ps-002",
      "plugin_id": "ppt-plugin",
      ...
    }
  ]
}
```

**前端任务监控面板**

- 位置：聊天界面右侧或底部，可折叠
- 展示当前会话所有 plugin session 的进度条
- 每个任务显示：插件名、当前步骤、步骤状态（运行中/等待/完成）
- 点击任务可跳转到对应插件的 `PluginShell`
- 实时更新（监听 SSE `step_change` 事件）

```tsx
function TaskBoard({ conversationId }: { conversationId: string }) {
  const jobs = useConversationJobs(conversationId)
  
  return (
    <div className="task-board">
      <h3>任务看板</h3>
      {jobs.map(job => (
        <TaskCard
          key={job.jobId}
          pluginName={job.pluginName}
          steps={job.steps}
          currentStep={job.currentStep}
          onFocus={() => scrollToPlugin(job.pluginSessionId)}
        />
      ))}
    </div>
  )
}
```

### 1.4 文档补全

**`docs/plugin-protocol.md`**（收尾任务）

完整记录插件协议，作为后续插件开发者的参考手册：

- 插件目录结构（`plugin.yaml` 字段说明）
- Scenario 文件规范（scenario.md / state.yml / driver.md / prompts 格式）
- `PluginEvent` 类型与字段说明
- `plugin_context` 请求体格式
- REST API 完整列表（含所有 M1-M7 接口）
- 框架提供的能力清单（不需要插件关心的内容）
- 新建插件标准清单（checklist）
- 典型插件示例（image-plugin 简化版）

---

## 二、实施计划

### 阶段划分

**Week 1 — 后端 API（P0 + P1）**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `GET /plugin-sessions/:id/state-graph` | Backend (Go) | 1.5d |
| `GET /conversations/:id/jobs` | Backend (Go) | 1d |
| `GET/PUT /plugins/:id/state-machine`（P1） | Backend (Go) | 1.5d |

**Week 2 — 热重载 + 算法层（P1）**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `loader.py` 新增 `reload_plugin()` 方法 | Algorithm (Python) | 1d |
| `PUT /state-machine` 校验 + 文件写入 + 热重载链路 | Backend (Go) + Python | 1.5d |
| 权限校验（admin 或 DEV_MODE） | Backend (Go) | 0.5d |

**Week 3 — 前端组件（P0）**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `StateGraph` 组件（readonly，ReactFlow） | Frontend (TS) | 2d |
| 节点状态 badge + 边 condition label + 合法后继高亮 | Frontend (TS) | 1d |
| `PluginShell` 侧栏「流程图」Tab 集成 | Frontend (TS) | 0.5d |
| 任务看板 `TaskBoard` 组件 | Frontend (TS) | 1d |

**Week 4 — StateGraph 编辑器（P1）+ 文档**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `StateGraph` 编辑模式（节点/边增删改、layout 持久化） | Frontend (TS) | 2d |
| YAML 预览面板 + 编辑错误展示 | Frontend (TS) | 1d |
| `docs/plugin-protocol.md` 编写 | All | 1.5d |
| 全功能 E2E 测试 | All | 0.5d |

---

## 三、测试方案

### 3.1 后端单元测试

**state-graph API**

```go
func TestGetStateGraph_MergesStaticAndRuntime(t *testing.T) {
    // 准备 state.yml（3 步骤）+ DB 运行态（outline=done, draft=running, final=idle）
    // 调 GET /state-graph
    // 验证：outline.status='done', draft.status='running', final.status='idle'
    // 验证：draft→final 和 draft→draft 的 is_valid_from_current=true（当前步骤 draft）
    // 验证：outline→draft 的 is_valid_from_current=false（当前步骤不是 outline）
}

func TestGetStateGraph_NoActiveSession(t *testing.T) {
    // session 不存在，期望 404
}
```

**state-machine API（P1）**

```go
func TestPutStateMachine_ValidYAML_ReloadsCache(t *testing.T) {
    // 提交合法的新 state.yml
    // 验证：返回 200，loader 缓存已更新（后续 GET /state-graph 反映新图结构）
}

func TestPutStateMachine_InvalidYAML_Returns422(t *testing.T) {
    // 提交包含不存在步骤的 state.yml
    // 验证：返回 422，errors 非空，loader 缓存未更新
}

func TestPutStateMachine_RequiresAdminOrDevMode(t *testing.T) {
    // 非 admin 用户 + DEV_MODE=false
    // 验证：返回 403
}
```

**jobs API**

```go
func TestGetConversationJobs_ReturnsAllJobs(t *testing.T) {
    // conversation 下有 2 个 plugin session
    // 验证：返回 2 个 job，含步骤状态
}

func TestGetConversationJobs_EmptyConversation(t *testing.T) {
    // 无 plugin session，返回空数组
}
```

### 3.2 热重载测试

```python
def test_reload_plugin_updates_state_machine_cache():
    # 初始 state.yml: outline → draft → final（3 步骤线性）
    # 修改 state.yml: 增加 final → outline 回退边
    # 调用 loader.reload_plugin('writing-plugin')
    # 验证 get_state_machine('writing-plugin') 包含新边

def test_reload_plugin_does_not_affect_other_plugins():
    # reload writing-plugin
    # 验证 ppt-plugin 的 state machine 不变
```

### 3.3 前端组件测试

**`StateGraph`（readonly）**

```ts
describe('StateGraph readonly', () => {
  it('renders all nodes with correct status badges', () => {
    // mock state-graph API 返回 3 个节点
    // 验证 3 个节点可见，badge 文字正确
  })
  it('highlights valid transitions from current step', () => {
    // is_valid_from_current=true 的边有高亮样式
  })
  it('shows condition label on edge hover', async () => {
    // hover 边，验证 condition 文字显示
  })
  it('refreshes on step_change SSE event', async () => {
    // dispatch step_change，验证 refetch API 被调用
  })
})
```

**`StateGraph`（editable，P1）**

```ts
describe('StateGraph editable', () => {
  it('only visible to admin or dev mode', () => {
    // isAdmin=false, isDevMode=false → 编辑按钮不可见
    // isAdmin=true → 编辑按钮可见
  })
  it('save triggers PUT /state-machine', async () => {
    // 修改节点 condition，点保存
    // 验证 PUT 请求包含新的 state_yml_content
  })
  it('shows errors on invalid state machine', async () => {
    // mock PUT 返回 422 + errors
    // 验证错误信息展示在界面上，编辑器未关闭
  })
  it('layout persisted after save', async () => {
    // 拖拽节点到新位置，保存
    // 重新加载，验证节点位置保持
  })
})
```

**`TaskBoard`**

```ts
describe('TaskBoard', () => {
  it('renders all active plugin sessions', () => {
    // mock 2 个 job，验证 2 个 TaskCard
  })
  it('clicking task scrolls to plugin shell', async () => {
    // 点击 TaskCard，验证 scrollToPlugin 被调用
  })
  it('updates in real time on step_change', async () => {
    // dispatch step_change，验证进度条更新
  })
})
```

### 3.4 端到端（E2E）验收测试

**StateGraph 可视化验收（P0）**

```
1. 触发写作插件，大纲步骤 running
2. 打开 PluginShell 侧栏「流程图」Tab
   → 验证 StateGraph 展示 3 个节点
   → 验证 outline 节点有「运行中」badge
   → 验证 outline→draft 和 outline→outline 边有虚线高亮（合法后继）
3. 大纲完成，进入 waiting
   → 验证 outline 节点变为「等待确认」badge
4. 推进到 draft
   → 验证 outline 节点变为「完成」
   → 验证 draft 节点变为「运行中」
   → 验证合法后继高亮切换到 draft→final 和 draft→draft
5. 悬停 draft→final 边，验证 condition 文字显示
```

**StateGraph 编辑 + 热重载验收（P1）**

```
1. 以 admin 身份打开 StateGraph 编辑器
2. 为 final→outline 新增一条回退边，condition='用户需要完全重来'
3. 点击保存
   → 验证 PUT /state-machine 响应 200
   → 验证 loader 热重载完成（无需重启服务）
4. 对写作插件 session 发送对话「我想从头重来」
   → 验证 ReactAgent 可以调 plugin_proceed(target_step='outline')（新边生效）
5. 验证写入的 state.yml 文件内容包含新边
```

**任务看板验收**

```
1. 同时运行写作插件（draft 步骤）和 PPT 插件（design 步骤）
2. 打开任务看板
   → 验证展示 2 个任务卡片
   → 验证每个卡片步骤进度正确
3. 写作插件 draft 完成
   → 验证任务看板中写作插件步骤更新（实时）
4. 点击 PPT 插件卡片
   → 验证页面滚动到 PPT 插件的 PluginShell
```

---

## 四、验收标准

| 验收项 | 验收方式 | 通过标准 |
|--------|----------|----------|
| StateGraph 正确展示节点状态 | E2E | 步骤 badge 与实际 step_status 一致 |
| 合法后继虚线高亮 | E2E | 当前步骤的 is_valid_from_current 边有高亮 |
| 步骤变化时 StateGraph 实时更新 | E2E | step_change SSE 触发节点 badge 更新 |
| StateGraph 集成到 PluginShell | E2E | 侧栏「流程图」Tab 可见且正常展示 |
| P1 编辑保存后热重载生效 | E2E（P1） | 新 transition 在下次对话中可路由 |
| P1 非法修改返回错误 | 单元测试（P1） | 422 + errors，缓存未更新 |
| P1 权限控制 | 单元测试（P1） | 非 admin 且非 DEV_MODE 时 403 |
| 任务看板展示多插件进度 | E2E | 2 个插件均在看板中显示 |
| 任务看板实时更新 | E2E | step_change 后看板秒级刷新 |
| plugin-protocol.md 完整 | 人工评审 | 包含所有接口、字段、checklist |
| M1-M6 功能不受影响 | 回归测试 | M1-M6 所有验收项仍通过 |

---

## 五、注意事项与风险

1. **ReactFlow 包体大小**：ReactFlow（或 @xyflow/react）包体较大（~200KB gzip 前），建议懒加载（`React.lazy` + Suspense），只在用户打开「流程图」Tab 时才加载。
2. **StateGraph 与 state.yml 格式强耦合**：`GET /state-graph` 依赖 loader 缓存的 StateMachine 对象，若 loader 未正确热重载，展示的图可能与文件不一致，需要在 API 响应中附加 cache_version 用于调试。
3. **P1 热重载的并发安全**：`reload_plugin()` 执行期间，若有正在进行的 plugin_proceed() 调用在读取旧的 StateMachine，可能产生竞争。建议用读写锁（`asyncio.Lock` 或 Go 侧 `sync.RWMutex`）保护缓存。
4. **state.yml 文件写入**：`PUT /state-machine` 需要写磁盘文件，在容器化部署中文件系统可能是只读的或不持久化；需要考虑将 state.yml 存入 DB（作为 BLOB），而非仅依赖文件系统。
5. **任务看板 SSE 事件扇出**：多个插件同时运行时，SSE 连接会收到来自所有插件的 `step_change` 事件，任务看板的更新逻辑需要按 `plugin_session_id` 路由到正确的 TaskCard，避免错误更新。
6. **`plugin-protocol.md` 维护**：文档应在每次框架接口变更时同步更新，建议将文档更新纳入 PR checklist，避免文档与实现脱节。
7. **M7 为增强功能，不阻塞主流程**：若 M7 开发进度延误，P1（编辑器）可以拆分为独立任务推迟；P0（只读可视化）和任务看板应保持在 M7 计划内完成。
