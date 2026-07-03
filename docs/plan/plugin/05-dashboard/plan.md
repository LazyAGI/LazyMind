# 阶段 5：StateGraph 可视化

> 为用户提供插件工作流的只读图形化监控。StateGraph 以 SVG 有向图方式呈现 state.yml 拓扑结构与当前运行进度，通过两处弹层入口触达：PluginPanel 状态 Badge 和 TaskCenter 步骤列。
>
> 前置依赖：阶段 1–4 全部落地。任务看板（`task_center_tasks`、TaskCenter 全局页面）已完整实现，本阶段不涉及。
>
> 配套文档：示例代码见 [`code.md`](./code.md)

## 阅读顺序

1. 先读「一、设计原则」了解边界。
2. 「二、数据接口」是唯一新增后端工作。
3. 「三、前端组件」描述 StateGraphModal 的完整结构。
4. 「四、集成入口」描述两处触发位置。
5. 「五、实施顺序」是排期与验收标准。

---

## 一、设计原则

### 1.1 只读渲染，不引入编辑能力

本阶段 StateGraph 仅作展示，不支持节点拖拽或边编辑。后续编辑阶段可引入 `@xyflow/react`（React Flow v12）完整替换渲染层，两阶段技术方案不冲突。

### 1.2 不新增 Tab，以弹层触发

StateGraph 通过弹层展示，而非作为 PluginPanel 的新 Tab。理由：
- PluginPanel Tab 由 `plugin.yaml` 的 `ui.tabs` 声明驱动，StateGraph 是框架级系统能力，不属于插件内容
- 弹层不占用 Panel 主体空间，用户可以同时看到 Artifact 内容和图结构
- TaskCenter 列表页也需要同样的入口，弹层更容易复用

### 1.3 SVG + dagre 布局，不引入图形框架

只读渲染阶段引入 `@dagrejs/dagre`（约 28 KB，纯 JS 布局引擎）计算节点坐标，渲染层用 React SVG 组件自绘。不引入 `react-flow`、`d3`、`vis-network` 等重型依赖，保持 bundle 轻量。

### 1.4 Go 是唯一数据组合层

Go 侧负责将 Python 提供的 state.yml 拓扑与 `plugin_session_steps` 实时状态合并，返回前端可直接消费的 `nodes + edges` 结构。前端不直接解析 state.yml。

---

## 二、数据接口

### 2.1 新增接口：`GET /plugin-sessions/{session_id}/state-graph`

**请求**：无 body，无 query 参数。

**响应结构**：

```json
{
  "nodes": [
    {
      "id": "optimize_prompt",
      "label": "Optimize Prompt",
      "status": "succeeded",
      "is_current": false,
      "artifact_summary": "A cyberpunk city at night, neon lights..."
    },
    {
      "id": "__end__",
      "label": "__end__",
      "status": "pending",
      "is_current": false,
      "artifact_summary": null
    }
  ],
  "edges": [
    {
      "from": "optimize_prompt",
      "to": "generate_image",
      "condition": "提示词优化完成，可进入生图",
      "is_active_path": true
    }
  ],
  "initial": "__start__",
  "current_step_id": "generate_image"
}
```

**字段说明**：

| 字段 | 说明 |
| --- | --- |
| `nodes[].id` | step_id，对应 state.yml 中的键名；`__start__` / `__end__` 保留 |
| `nodes[].label` | 展示名称，优先用 plugin.yaml `steps[].label`，无则用 `id` |
| `nodes[].status` | 取该 step 最新一次 attempt 的状态：`pending \| running \| succeeded \| failed \| interrupted`；`__start__` 固定 `succeeded`，`__end__` 固定 `pending`（除非 session 已 `completed`） |
| `nodes[].is_current` | 是否为 `plugin_sessions.current_step_id` 指向的节点 |
| `nodes[].artifact_summary` | Python `format_artifact_summary` 的输出，可为 null |
| `edges[].is_active_path` | 从 `current_step_id` 节点出发的合法后继边标记为 true |
| `current_step_id` | 即 `plugin_sessions.current_step_id` |

**Go 实现逻辑**（`backend/core/plugin/handlers.go` 新增 `GetStateGraph`）：

1. 读 `plugin_sessions` 取 `plugin_id`、`current_step_id`、`status`
2. HTTP 调 Python `GET /api/plugins/{plugin_id}` 取完整 `state` 字段（已含 `transitions` + `steps` + `initial`）
3. 查 `plugin_session_steps` 按 `(step_id, MAX(attempt))` 取各步骤最新状态
4. 组合构造 `nodes`（`__start__` / `__end__` 固定插入）和 `edges`
5. 标注 `is_active_path`：遍历 `transitions[current_step_id]` 中所有 `to` 字段

**路由注册**（`backend/core/routes.go`）：

```
GET /plugin-sessions/{session_id}/state-graph → plugin.GetStateGraph
```

---

## 三、前端组件

### 3.1 组件结构

新建两个文件：

```
frontend/src/components/StateGraphModal/
  index.tsx        ← Modal 容器，负责 fetch + 生命周期
  StateGraphView.tsx ← SVG 渲染子组件，纯展示
  index.scss
```

**`StateGraphModal` props**：

```typescript
interface StateGraphModalProps {
  open: boolean;
  onClose: () => void;
  sessionId: string;
  pluginId: string;
  liveRefresh?: boolean;    // true = 监听 SSE 实时刷新；默认 false
  conversationId?: string;  // liveRefresh=true 时必传
}
```

### 3.2 布局与视觉设计

参考 [`status-board.png`](./status-board.png) 中 TaskCenter 步骤 tooltip 的样式风格（深色背景、绿色 succeeded badge、步骤名 + artifact_key 组合展示）。

StateGraph Modal 内容区：

```
┌─────────────────────────────────────────────────────┐
│  工作流图                                      [×]  │
├─────────────────────────────────────────────────────┤
│                                                     │
│   ●                                                 │
│   │  (start)                                        │
│   ▼                                                 │
│  ┌──────────────────┐                               │
│  │ ✓ analyze_subject│ ← succeeded 节点（绿色左边框） │
│  └──────────────────┘                               │
│   │  条件文本（截断）                               │
│   ▼                                                 │
│  ┌──────────────────┐                               │
│  │ ▶ generate_image │ ← 当前节点（蓝色边框高亮）    │
│  └──────────────────┘                               │
│   │  ~~虚线后继边~~                                 │
│   ▼                                                 │
│  ┌──────────────────┐                               │
│  │   enhance_image  │ ← pending（灰色）             │
│  └──────────────────┘                               │
│   │                                                 │
│   ▼                                                 │
│   ●  (end)                                          │
│                                                     │
└─────────────────────────────────────────────────────┘
```

**节点视觉规则**：

| 状态 | 左边框颜色 | 状态图标 | 背景 |
| --- | --- | --- | --- |
| `succeeded` | 绿色 | ✓ | 白 |
| `running` | 蓝色（动画） | ⟳ | 浅蓝 |
| `failed` / `interrupted` | 红色 | ✗ | 白 |
| `pending`（未开始） | 灰色 | — | 白 |
| 当前节点（`is_current=true`） | 蓝色加粗 | — | 同状态色 |
| `__start__` / `__end__` | — | — | 实心圆，无矩形框 |

**边视觉规则**：

| 类型 | 样式 |
| --- | --- |
| 普通边（历史已走过） | 实线，灰色 |
| `is_active_path=true`（当前节点合法后继） | 虚线，蓝色 |
| 自环边（重试路径） | 曲线自环，橙色 |

**交互**：
- hover 边：Tooltip 展示完整 `condition` 文本
- 点击节点：内联展开 `artifact_summary` 文本块（无 artifact 则不可点击）

### 3.3 dagre 布局

```typescript
import dagre from '@dagrejs/dagre';

function layoutGraph(nodes: Node[], edges: Edge[]) {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'TB', nodesep: 40, ranksep: 60 });
  g.setDefaultEdgeLabel(() => ({}));
  nodes.forEach(n => g.setNode(n.id, { width: 180, height: 52 }));
  edges.forEach(e => g.setEdge(e.from, e.to));
  dagre.layout(g);
  return { nodeMap: g, width: g.graph().width, height: g.graph().height };
}
```

布局方向：从上到下（`TB`）。`__start__` 圆节点 `width/height=24`；普通步骤节点 `width=180, height=52`。

### 3.4 实时刷新（仅 PluginPanel 入口）

`liveRefresh=true` 时，`StateGraphModal` 从 `useTaskCenterStore` 订阅 `convEventBus`：当收到 `step_waiting` / `step_partial_done` / `plugin_completed` / `plugin_error` 事件时，重新调用 `GET /plugin-sessions/{session_id}/state-graph` 更新 `nodes` 状态。布局（dagre 计算结果）缓存，仅替换节点 `status` 字段，不重新布局。

---

## 四、集成入口

### 4.1 入口一：PluginPanel 状态 Badge

**文件**：`frontend/src/modules/chat/components/PluginPanel/index.tsx`

PluginPanel Header 中的 Session 状态 Badge 改为可点击，点击展开 `StateGraphModal`。

```
image-plugin  [等待确认 ← 可点击]   ① 用户意图  ×  ∨
```

- Badge 保持原有样式，增加 `cursor: pointer` 和 hover 下划线效果
- 点击后 `setStateGraphOpen(true)`，传入 `sessionId`、`pluginId`、`liveRefresh=true`、`conversationId`
- Modal 关闭时 `setStateGraphOpen(false)`

### 4.2 入口二：TaskCenter 步骤列

**文件**：`frontend/src/modules/taskCenter/TaskList.tsx`

「步骤」列的 step Tag 区域（`+N` tooltip）改为整体可点击，点击弹出 `StateGraphModal`。

参考 [`status-board.png`](./status-board.png) 中的步骤 tooltip 样式：深色背景 popover 展示各步骤状态（succeeded badge + step_id + artifact_key）。

- 仅 `task.plugin_session_id` 存在（即 `task_type=plugin_run`）时才可点击
- 点击后 `setStateGraphTarget({ sessionId: task.plugin_session_id, pluginId: task.plugin_id })`
- 传入 `liveRefresh=false`（TaskCenter 无 SSE 上下文）

---

## 五、实施顺序

### 5.1 后端

1. `backend/core/plugin/handlers.go`：新增 `GetStateGraph` handler（约 80 行）
2. `backend/core/routes.go`：注册路由 `GET /plugin-sessions/{session_id}/state-graph`

### 5.2 前端

3. `package.json`：新增 `"@dagrejs/dagre": "^1.0.4"`（locked version）
4. 新建 `frontend/src/components/StateGraphModal/index.tsx`（Modal 容器 + fetch + SSE 订阅）
5. 新建 `frontend/src/components/StateGraphModal/StateGraphView.tsx`（SVG 渲染 + dagre 布局）
6. 新建 `frontend/src/components/StateGraphModal/index.scss`
7. `frontend/src/modules/chat/components/PluginPanel/index.tsx`：状态 Badge 添加 `onClick`，挂载 `StateGraphModal`
8. `frontend/src/modules/taskCenter/TaskList.tsx`：步骤列添加 `onClick`，挂载 `StateGraphModal`

### 5.3 验收标准

- [ ] `GET /plugin-sessions/{session_id}/state-graph` 返回正确的 nodes/edges/current_step_id
- [ ] PluginPanel 状态 Badge 点击能打开 Modal，图中节点状态与实际步骤状态一致
- [ ] 步骤运行中时，当前节点有蓝色高亮，合法后继边为虚线
- [ ] 节点点击能展开 artifact_summary（有 artifact 的步骤）
- [ ] hover 边能展示完整 condition 文本
- [ ] SSE 事件（step_waiting / plugin_completed）触发后，Modal 内节点状态自动更新
- [ ] TaskCenter 步骤列点击能打开 Modal（plugin_run 类型任务）
- [ ] `__start__` / `__end__` 渲染为实心圆节点
