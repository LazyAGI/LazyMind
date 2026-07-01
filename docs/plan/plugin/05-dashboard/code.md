# 阶段 5 · 示例代码

> 对应 [`plan.md`](./plan.md) 中引用的示例 / 伪代码。代码仅示意关键逻辑，非最终实现；落地以 `plan.md` 约束为准。

## 目录

- [C1. Go：GetStateGraph handler](#c1)
- [C2. 前端：StateGraphModal 容器](#c2)
- [C3. 前端：StateGraphView SVG 渲染](#c3)
- [C4. 前端：PluginPanel 入口](#c4)
- [C5. 前端：TaskCenter 入口](#c5)

---

<a id="c1"></a>

## C1. Go：GetStateGraph handler

**`backend/core/plugin/handlers.go`**

```go
// GetStateGraph 组合 state.yml 拓扑与 plugin_session_steps 实时状态，返回前端可直接消费的图结构。
func (h *Handler) GetStateGraph(c *gin.Context) {
    sessionID := c.Param('session_id')
    ctx := c.Request.Context()

    // 1. 读 plugin_session 取 plugin_id 和 current_step_id
    session, err := h.store.GetSessionByID(ctx, sessionID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{'error': 'session not found'})
        return
    }

    // 2. 调 Python 取 plugin spec（含完整 state.yml 结构）
    spec, err := h.pythonClient.GetPluginSpec(ctx, session.PluginID)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{'error': 'failed to load plugin spec'})
        return
    }

    // 3. 查各步骤最新 attempt 的状态
    stepStatuses, err := h.store.GetLatestStepStatuses(ctx, sessionID)
    // stepStatuses: map[step_id]StepStatus{ Status, AttemptAt }

    // 4. 组合 nodes
    nodes := buildNodes(spec, stepStatuses, session.CurrentStepID, session.Status)

    // 5. 组合 edges，标注 is_active_path
    edges := buildEdges(spec, session.CurrentStepID)

    c.JSON(http.StatusOK, gin.H{
        'nodes':           nodes,
        'edges':           edges,
        'initial':         spec.State.Initial,
        'current_step_id': session.CurrentStepID,
    })
}

// GetLatestStepStatuses 返回每个 step_id 最新 attempt 的状态。
// SQL:
//   SELECT step_id, status FROM plugin_session_steps
//   WHERE session_id = $1 AND attempt = (
//     SELECT MAX(attempt) FROM plugin_session_steps p2
//     WHERE p2.session_id = $1 AND p2.step_id = plugin_session_steps.step_id
//   )
func (s *Store) GetLatestStepStatuses(ctx context.Context, sessionID string) (map[string]string, error) {
    // ...
}

func buildNodes(spec *PluginSpec, statuses map[string]string, currentStepID, sessionStatus string) []StateGraphNode {
    nodes := []StateGraphNode{
        {ID: '__start__', Label: '__start__', Status: 'succeeded'},
    }
    for _, step := range spec.State.Steps {
        label := step.Label
        if label == '' {
            label = step.ID
        }
        status := statuses[step.ID]  // '' 表示 pending（未执行过）
        if status == '' {
            status = 'pending'
        }
        nodes = append(nodes, StateGraphNode{
            ID:              step.ID,
            Label:           label,
            Status:          status,
            IsCurrent:       step.ID == currentStepID,
            ArtifactSummary: '', // 后续可接 format_artifact_summary
        })
    }
    endStatus := 'pending'
    if sessionStatus == 'completed' {
        endStatus = 'succeeded'
    }
    nodes = append(nodes, StateGraphNode{ID: '__end__', Label: '__end__', Status: endStatus})
    return nodes
}

func buildEdges(spec *PluginSpec, currentStepID string) []StateGraphEdge {
    var edges []StateGraphEdge
    for fromID, transitions := range spec.State.Transitions {
        for _, t := range transitions {
            edges = append(edges, StateGraphEdge{
                From:         fromID,
                To:           t.To,
                Condition:    t.Condition,
                IsActivePath: fromID == currentStepID,
            })
        }
    }
    return edges
}
```

**路由注册（`backend/core/routes.go`）**：

```go
pluginGroup.GET('/plugin-sessions/:session_id/state-graph', pluginHandler.GetStateGraph)
```

---

<a id="c2"></a>

## C2. 前端：StateGraphModal 容器

**`frontend/src/components/StateGraphModal/index.tsx`**

```typescript
import { useEffect, useState } from 'react';
import { Modal } from 'antd';
import { StateGraphView } from './StateGraphView';
import { useTaskCenterStore } from '@/modules/chat/store/taskCenter';
import { fetchStateGraph, StateGraphData } from './api';

interface Props {
  open: boolean;
  onClose: () => void;
  sessionId: string;
  pluginId: string;
  liveRefresh?: boolean;
  conversationId?: string;
}

export function StateGraphModal({ open, onClose, sessionId, liveRefresh, conversationId }: Props) {
  const [data, setData] = useState<StateGraphData | null>(null);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const result = await fetchStateGraph(sessionId);
      setData(result);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (open && sessionId) load();
  }, [open, sessionId]);

  // 实时刷新：订阅 conv events，步骤状态变更时重新 fetch
  const convEventVersion = useTaskCenterStore(s =>
    liveRefresh && conversationId ? s.convEventVersion?.[conversationId] : null
  );
  useEffect(() => {
    if (open && liveRefresh && convEventVersion != null) load();
  }, [convEventVersion]);

  return (
    <Modal
      title='工作流图'
      open={open}
      onCancel={onClose}
      footer={null}
      width={640}
    >
      <StateGraphView data={data} loading={loading} />
    </Modal>
  );
}
```

`useTaskCenterStore` 需在 `step_waiting` / `plugin_completed` / `step_partial_done` 事件触发时 bump `convEventVersion[conversationId]`（在已有 `subscribeConvEvents` 的事件处理逻辑中加一行即可）。

---

<a id="c3"></a>

## C3. 前端：StateGraphView SVG 渲染

**`frontend/src/components/StateGraphModal/StateGraphView.tsx`**

```typescript
import dagre from '@dagrejs/dagre';
import { useState, useMemo } from 'react';
import { Tooltip } from 'antd';

const NODE_W = 180, NODE_H = 52, CIRCLE_R = 12;

function layoutGraph(nodes: StateGraphNode[], edges: StateGraphEdge[]) {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'TB', nodesep: 40, ranksep: 60 });
  g.setDefaultEdgeLabel(() => ({}));
  nodes.forEach(n => {
    const isSpecial = n.id === '__start__' || n.id === '__end__';
    g.setNode(n.id, { width: isSpecial ? CIRCLE_R * 2 : NODE_W, height: isSpecial ? CIRCLE_R * 2 : NODE_H });
  });
  edges.forEach(e => g.setEdge(e.from, e.to));
  dagre.layout(g);
  return g;
}

// 状态 → 左边框颜色
const STATUS_COLOR: Record<string, string> = {
  succeeded: '#52c41a',
  running: '#1677ff',
  failed: '#ff4d4f',
  interrupted: '#ff4d4f',
  pending: '#d9d9d9',
};

export function StateGraphView({ data, loading }: { data: StateGraphData | null; loading: boolean }) {
  const [expandedNode, setExpandedNode] = useState<string | null>(null);

  const graph = useMemo(() => {
    if (!data) return null;
    return layoutGraph(data.nodes, data.edges);
  }, [data]);  // 布局只在 data.nodes/edges 拓扑变化时重算，status 变化时不重算

  if (loading || !data || !graph) return <div style={{ height: 300, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>加载中...</div>;

  const gInfo = graph.graph();
  const svgW = (gInfo.width ?? 400) + 40;
  const svgH = (gInfo.height ?? 400) + 40;

  return (
    <svg width={svgW} height={svgH} style={{ display: 'block', margin: '0 auto' }}>
      <defs>
        <marker id='arrow' markerWidth='8' markerHeight='8' refX='6' refY='3' orient='auto'>
          <path d='M0,0 L0,6 L8,3 z' fill='#999' />
        </marker>
        <marker id='arrow-active' markerWidth='8' markerHeight='8' refX='6' refY='3' orient='auto'>
          <path d='M0,0 L0,6 L8,3 z' fill='#1677ff' />
        </marker>
      </defs>
      <g transform='translate(20,20)'>
        {/* 边 */}
        {data.edges.map((e, i) => {
          const points = graph.edge(e.from, e.to)?.points ?? [];
          if (!points.length) return null;
          const d = 'M ' + points.map(p => `${p.x},${p.y}`).join(' L ');
          const color = e.isActivePath ? '#1677ff' : '#ccc';
          return (
            <Tooltip key={i} title={e.condition}>
              <path
                d={d}
                fill='none'
                stroke={color}
                strokeWidth={e.isActivePath ? 2 : 1.5}
                strokeDasharray={e.isActivePath ? '6,3' : undefined}
                markerEnd={e.isActivePath ? 'url(#arrow-active)' : 'url(#arrow)'}
                style={{ cursor: 'default' }}
              />
            </Tooltip>
          );
        })}
        {/* 节点 */}
        {data.nodes.map(n => {
          const pos = graph.node(n.id);
          if (!pos) return null;
          const isSpecial = n.id === '__start__' || n.id === '__end__';
          const color = STATUS_COLOR[n.status] ?? '#d9d9d9';
          const isExpanded = expandedNode === n.id;

          if (isSpecial) {
            return (
              <circle key={n.id} cx={pos.x} cy={pos.y} r={CIRCLE_R} fill='#333' />
            );
          }

          return (
            <g key={n.id}
              transform={`translate(${pos.x - NODE_W / 2}, ${pos.y - NODE_H / 2})`}
              onClick={() => n.artifactSummary ? setExpandedNode(isExpanded ? null : n.id) : null}
              style={{ cursor: n.artifactSummary ? 'pointer' : 'default' }}
            >
              <rect
                width={NODE_W} height={NODE_H} rx={6}
                fill={n.isCurrent ? '#e6f4ff' : '#fff'}
                stroke={n.isCurrent ? '#1677ff' : '#e8e8e8'}
                strokeWidth={n.isCurrent ? 2 : 1}
              />
              {/* 左边框状态条 */}
              <rect x={0} y={0} width={4} height={NODE_H} rx={2} fill={color} />
              {/* 步骤名 */}
              <text x={16} y={30} fontSize={13} fill='#333' fontWeight={n.isCurrent ? 600 : 400}>
                {n.label.length > 20 ? n.label.slice(0, 19) + '…' : n.label}
              </text>
              {/* artifact 展开 */}
              {isExpanded && n.artifactSummary && (
                <foreignObject x={0} y={NODE_H + 4} width={NODE_W} height={60}>
                  <div style={{ background: '#f6f8fa', borderRadius: 4, padding: '4px 8px', fontSize: 12, color: '#555' }}>
                    {n.artifactSummary}
                  </div>
                </foreignObject>
              )}
            </g>
          );
        })}
      </g>
    </svg>
  );
}
```

---

<a id="c4"></a>

## C4. 前端：PluginPanel 入口

**`frontend/src/modules/chat/components/PluginPanel/index.tsx`**（增量修改）

```typescript
// 在现有 session 状态 Badge 渲染处，改为可点击
const [stateGraphOpen, setStateGraphOpen] = useState(false);

// Header 中原本的只读 Badge，改为：
<Tag
  color={SESSION_STATUS_COLOR[session.status]}
  onClick={() => setStateGraphOpen(true)}
  style={{ cursor: 'pointer' }}
>
  {SESSION_STATUS_LABEL[session.status]}
</Tag>

// Modal 挂载（在 return 的 JSX 末尾）
<StateGraphModal
  open={stateGraphOpen}
  onClose={() => setStateGraphOpen(false)}
  sessionId={session.id}
  pluginId={session.plugin_id}
  liveRefresh={true}
  conversationId={conversationId}
/>
```

---

<a id="c5"></a>

## C5. 前端：TaskCenter 入口

**`frontend/src/modules/taskCenter/TaskList.tsx`**（增量修改）

```typescript
const [stateGraphTarget, setStateGraphTarget] = useState<{
  sessionId: string; pluginId: string;
} | null>(null);

// 「步骤」列渲染，在现有 Tag 区域外包一层可点击容器
{
  title: t('taskCenter.steps'),
  dataIndex: 'steps',
  render: (steps: TaskStep[], record: Task) => (
    <div
      onClick={() => {
        if (record.plugin_session_id && record.plugin_id) {
          setStateGraphTarget({ sessionId: record.plugin_session_id, pluginId: record.plugin_id });
        }
      }}
      style={{ cursor: record.plugin_session_id ? 'pointer' : 'default' }}
    >
      {/* 原有 step tag 渲染逻辑不变 */}
      {steps.slice(0, 2).map(s => <Tag key={s.step_id}>{s.step_id}</Tag>)}
      {steps.length > 2 && <Tag>+{steps.length - 2}</Tag>}
    </div>
  ),
}

// Modal 挂载
{stateGraphTarget && (
  <StateGraphModal
    open={true}
    onClose={() => setStateGraphTarget(null)}
    sessionId={stateGraphTarget.sessionId}
    pluginId={stateGraphTarget.pluginId}
    liveRefresh={false}
  />
)}
```

> **注意**：需确认 `task_center_tasks` 表（及对应 Go API 响应）中是否已返回 `plugin_session_id` 和 `plugin_id` 字段；若没有，需在 Go `ListTasks` handler 的响应结构中补充这两个字段。
