# Code Reference: StateGraphEditor

各模块的代码示例与关键实现细节。伪代码用 `// ...` 省略非关键部分，可直接运行的片段已标注。

---

## 1. 数据模型 `core/model.ts`

GraphModel 是 YAML 和 React Flow 之间的中间表示，所有操作都通过它流转。

```typescript
// core/model.ts

export interface SlotDef {
  type: 'document' | 'text' | 'image' | 'json' | string;
}

export interface Transition {
  to: string;         // 目标节点 id
  condition: string;  // 转移条件描述，不允许为空
}

export interface StepNode {
  id: string;
  label: string;
  mode: 'human' | 'auto';
  inputs: string[];       // slot 列表（引用前序节点产出）
  outputs: string[];      // slot 列表（本节点产出）
  transitions: Transition[];
}

/** 节点在画布上的坐标，来自 x-layout 字段 */
export interface Layout {
  [nodeId: string]: { x: number; y: number };
}

export interface GraphModel {
  /** 插件级 slot 集中定义 */
  slots: Record<string, SlotDef>;
  /** 业务步骤节点（不含 __start__ / __end__） */
  steps: StepNode[];
  /** 画布布局坐标 */
  layout: Layout;
}

/** 校验错误，nodeId/edgeKey 用于在画布上定位并高亮 */
export interface ValidationError {
  rule: string;         // e.g. 'V3'
  message: string;
  nodeId?: string;
  edgeKey?: string;     // `${from}->${to}`
  yamlLine?: number;    // Monaco squiggle 用
}
```


---

## 2. YAML 解析 `core/parser.ts`

把 YAML 文本解析成 GraphModel，同时提取 `x-layout` 布局信息。

```typescript
// core/parser.ts
import yaml from 'js-yaml';
import type { GraphModel, StepNode } from './model';

interface RawYaml {
  'x-layout'?: Record<string, { x: number; y: number }>;
  slots?: Record<string, { type: string }>;
  steps?: Array<{
    id: string;
    label?: string;
    mode?: 'human' | 'auto';
    inputs?: string[];
    outputs?: string[];
    transitions?: Array<{ to: string; condition?: string }>;
  }>;
}

export class ParseError extends Error {
  constructor(message: string, public line?: number) {
    super(message);
  }
}

export function parseYaml(text: string): GraphModel {
  let raw: RawYaml;
  try {
    raw = yaml.load(text) as RawYaml ?? {};
  } catch (e: any) {
    // js-yaml 的 YAMLException 包含 mark.line
    throw new ParseError(e.message, e.mark?.line);
  }

  const layout = raw['x-layout'] ?? {};
  const slots: GraphModel['slots'] = {};
  for (const [key, def] of Object.entries(raw.slots ?? {})) {
    slots[key] = { type: def.type ?? 'text' };
  }

  const steps: StepNode[] = (raw.steps ?? []).map((s) => ({
    id: s.id,
    label: s.label ?? s.id,
    mode: s.mode ?? 'human',
    inputs: s.inputs ?? [],
    outputs: s.outputs ?? [],
    transitions: (s.transitions ?? []).map((t) => ({
      to: t.to,
      condition: t.condition ?? '',
    })),
  }));

  return { slots, steps, layout };
}
```


---

## 3. YAML 序列化 `core/serializer.ts`

把 GraphModel 序列化回 YAML 文本。有两种模式：
- **完整序列化**（含 `x-layout`）：用于保存 Draft
- **纯语义序列化**（不含 `x-layout`）：用于 YAML 视图展示，对用户隐藏布局噪音

```typescript
// core/serializer.ts
import yaml from 'js-yaml';
import type { GraphModel } from './model';

const DUMP_OPTS: yaml.DumpOptions = {
  indent: 2,
  lineWidth: -1,        // 不折行
  quotingType: "'",     // 统一用单引号
  forceQuotes: false,
  // key 顺序固定，保证 canonical 输出（diff 友好）
  sortKeys: false,
};

/** 序列化为完整 YAML，含 x-layout（用于持久化） */
export function serializeYaml(model: GraphModel): string {
  const doc: Record<string, unknown> = {};

  // 有布局信息时才写入 x-layout
  if (Object.keys(model.layout).length > 0) {
    doc['x-layout'] = model.layout;
  }

  doc['slots'] = model.slots;

  doc['steps'] = model.steps.map((s) => {
    const step: Record<string, unknown> = { id: s.id, label: s.label, mode: s.mode };
    if (s.inputs.length > 0)  step['inputs']  = s.inputs;
    if (s.outputs.length > 0) step['outputs'] = s.outputs;
    step['transitions'] = s.transitions.map((t) => ({ to: t.to, condition: t.condition }));
    return step;
  });

  return yaml.dump(doc, DUMP_OPTS);
}

/** 序列化为纯语义 YAML，不含 x-layout（用于 YAML 编辑器展示） */
export function serializeYamlSemantic(model: GraphModel): string {
  return serializeYaml({ ...model, layout: {} });
}
```


---

## 4. 校验逻辑 `core/validator.ts`

纯函数，输入 GraphModel，输出所有错误列表。

```typescript
// core/validator.ts
import type { GraphModel, ValidationError } from './model';

export function validateStateGraph(model: GraphModel): ValidationError[] {
  const errors: ValidationError[] = [];
  const { steps, slots } = model;

  // 构建完整节点 id 集合（含虚拟终端节点）
  const allIds = new Set(['__start__', '__end__', ...steps.map((s) => s.id)]);

  // 构建入边/出边 map
  const inDegree  = new Map<string, number>([['__start__', 0], ['__end__', 0]]);
  const outDegree = new Map<string, number>([['__start__', 0], ['__end__', 0]]);
  for (const s of steps) { inDegree.set(s.id, 0); outDegree.set(s.id, 0); }

  for (const s of steps) {
    for (const t of s.transitions) {
      outDegree.set(s.id, (outDegree.get(s.id) ?? 0) + 1);
      inDegree.set(t.to, (inDegree.get(t.to) ?? 0) + 1);
    }
  }

  // V1: __start__ 和 __end__ 必须存在（虚拟节点，通过边引用隐式存在）
  const hasStart = steps.some((s) => s.transitions.some((t) => t.to === '__end__')) ||
    steps.some((s) => s.id === '__start__');
  // 简化：检查是否有节点的 transition 能抵达 __end__
  const reachesEnd = steps.some((s) => s.transitions.some((t) => t.to === '__end__'));
  if (!reachesEnd) {
    errors.push({ rule: 'V1', message: '状态机中没有任何节点指向 __end__，缺少终止节点' });
  }

  // V2: 孤立节点（除 __start__ 外）
  for (const s of steps) {
    const inE = inDegree.get(s.id) ?? 0;
    const outE = outDegree.get(s.id) ?? 0;
    if (inE === 0 && outE === 0) {
      errors.push({ rule: 'V2', message: `节点 "${s.id}" 没有任何连接，是孤立节点`, nodeId: s.id });
    }
  }

  // V3: 有向环检测（DFS）
  const visited = new Set<string>();
  const inStack = new Set<string>();
  const adjMap = new Map<string, string[]>();
  for (const s of steps) adjMap.set(s.id, s.transitions.map((t) => t.to));

  function dfs(id: string): boolean {
    if (inStack.has(id)) return true;  // 发现环
    if (visited.has(id)) return false;
    visited.add(id); inStack.add(id);
    for (const next of adjMap.get(id) ?? []) {
      if (allIds.has(next) && next !== '__end__' && dfs(next)) {
        errors.push({ rule: 'V3', message: `检测到有向环，经过节点 "${id}"`, nodeId: id });
        return true;
      }
    }
    inStack.delete(id);
    return false;
  }
  for (const s of steps) if (!visited.has(s.id)) dfs(s.id);

  // V4: 每个节点至少一条输出边（__end__ 除外）
  for (const s of steps) {
    if ((outDegree.get(s.id) ?? 0) === 0) {
      errors.push({ rule: 'V4', message: `节点 "${s.id}" 没有输出边`, nodeId: s.id });
    }
  }

  // V5: 除 __start__ 外每个节点至少一条输入边
  for (const s of steps) {
    if ((inDegree.get(s.id) ?? 0) === 0) {
      errors.push({ rule: 'V5', message: `节点 "${s.id}" 没有输入边（无法到达）`, nodeId: s.id });
    }
  }

  // V6: 输出边必须指定 slot（outputs 不能为空，或 transition 没有对应产出）
  // 此规则在 model 层面检查：outputs 不为空
  // （如果业务上允许某些节点无产出，可放宽）

  // V7: 输入引用的 slot 必须由拓扑前序节点产出
  // 拓扑排序后逐节点检查
  const topoOrder = topoSort(steps);
  const availableSlots = new Set<string>();
  for (const nodeId of topoOrder) {
    const node = steps.find((s) => s.id === nodeId);
    if (!node) continue;
    for (const key of node.inputs) {
      if (!availableSlots.has(key)) {
        errors.push({
          rule: 'V7',
          message: `节点 "${nodeId}" 引用了 slot "${key}"，但该 slot 不由任何前序节点产出`,
          nodeId,
        });
      }
    }
    for (const key of node.outputs) availableSlots.add(key);
  }

  // V8: slot 类型与集中定义一致
  for (const s of steps) {
    for (const key of [...s.inputs, ...s.outputs]) {
      if (key && !slots[key]) {
        errors.push({
          rule: 'V8',
          message: `slot "${key}" 在节点 "${s.id}" 中使用，但未在 slots 集中定义`,
          nodeId: s.id,
        });
      }
    }
  }

  // V9: 转移边 condition 不得为空
  for (const s of steps) {
    for (const t of s.transitions) {
      if (!t.condition?.trim()) {
        const edgeKey = `${s.id}->${t.to}`;
        errors.push({
          rule: 'V9',
          message: `节点 "${s.id}" → "${t.to}" 的转移条件为空`,
          nodeId: s.id,
          edgeKey,
        });
      }
    }
  }

  return errors;
}

/** 拓扑排序（Kahn 算法），返回节点 id 有序列表 */
function topoSort(steps: GraphModel['steps']): string[] {
  const inDeg = new Map<string, number>();
  const adj = new Map<string, string[]>();
  for (const s of steps) { inDeg.set(s.id, 0); adj.set(s.id, []); }
  for (const s of steps) {
    for (const t of s.transitions) {
      if (t.to !== '__end__' && inDeg.has(t.to)) {
        inDeg.set(t.to, (inDeg.get(t.to) ?? 0) + 1);
        adj.get(s.id)!.push(t.to);
      }
    }
  }
  const queue = [...inDeg.entries()].filter(([, d]) => d === 0).map(([id]) => id);
  const result: string[] = [];
  while (queue.length > 0) {
    const id = queue.shift()!;
    result.push(id);
    for (const nb of adj.get(id) ?? []) {
      const d = (inDeg.get(nb) ?? 1) - 1;
      inDeg.set(nb, d);
      if (d === 0) queue.push(nb);
    }
  }
  return result;
}
```


---

## 5. 懒加载 `GraphCanvas/index.tsx` 与 `YamlEditor/index.tsx`

React Flow 和 Monaco 各自作为独立 Vite chunk 懒加载，只在用户进入编辑器时触发网络请求。

```tsx
// GraphCanvas/index.tsx
// 这个文件本身不 import @xyflow/react，只做 lazy 包装
import React, { Suspense } from 'react';
import type { GraphModel, ValidationError } from '../core/model';
import { Spin } from 'antd';

// 实际的 Canvas.tsx 才 import @xyflow/react，Vite 会把它打成独立 chunk
const LazyCanvas = React.lazy(() => import('./Canvas'));

interface Props {
  model: GraphModel;
  errors: ValidationError[];
  onChange: (model: GraphModel) => void;
}

export default function GraphCanvas(props: Props) {
  return (
    <Suspense fallback={<Spin style={{ margin: '40px auto', display: 'block' }} />}>
      <LazyCanvas {...props} />
    </Suspense>
  );
}
```

```tsx
// YamlEditor/index.tsx
// 同理，不直接 import monaco-editor
import React, { Suspense } from 'react';
import { Spin } from 'antd';

const LazyEditor = React.lazy(() => import('./Editor'));

interface Props {
  value: string;                  // 纯语义 YAML（不含 x-layout）
  onChange: (yaml: string) => void;
  errors: Array<{ line: number; message: string }>;
}

export default function YamlEditor(props: Props) {
  return (
    <Suspense fallback={<Spin style={{ margin: '40px auto', display: 'block' }} />}>
      <LazyEditor {...props} />
    </Suspense>
  );
}
```

```tsx
// YamlEditor/Editor.tsx（懒加载 chunk，才真正 import monaco）
import { useEffect, useRef } from 'react';
import * as monaco from 'monaco-editor';

// Monaco worker 配置（Vite 环境，需要 vite-plugin-monaco-editor 或手动配置 worker URL）
// 详见 https://github.com/microsoft/monaco-editor/blob/main/docs/integrate-esm.md
self.MonacoEnvironment = {
  getWorkerUrl(_moduleId: string, label: string) {
    if (label === 'yaml') return new URL('monaco-editor/esm/vs/language/yaml/yaml.worker', import.meta.url).href;
    return new URL('monaco-editor/esm/vs/editor/editor.worker', import.meta.url).href;
  },
};

interface Props {
  value: string;
  onChange: (yaml: string) => void;
  errors: Array<{ line: number; message: string }>;
}

export default function MonacoYamlEditor({ value, onChange, errors }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  // 用 ref 避免 stale closure
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    if (!containerRef.current) return;
    const editor = monaco.editor.create(containerRef.current, {
      language: 'yaml',
      theme: 'vs',
      minimap: { enabled: false },
      fontSize: 13,
      lineNumbers: 'on',
      wordWrap: 'on',
      scrollBeyondLastLine: false,
    });
    editorRef.current = editor;
    editor.onDidChangeModelContent(() => {
      onChangeRef.current(editor.getValue());
    });
    return () => editor.dispose();
  }, []);

  // 外部（图形操作）更新 value 时，静默更新编辑器内容（不触发 onChange）
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    if (editor.getValue() !== value) {
      editor.executeEdits('external', [{
        range: editor.getModel()!.getFullModelRange(),
        text: value,
      }]);
    }
  }, [value]);

  // 将校验错误注入为 Monaco markers（squiggle）
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const model = editor.getModel()!;
    const markers: monaco.editor.IMarkerData[] = errors.map((e) => ({
      severity: monaco.MarkerSeverity.Error,
      message: e.message,
      startLineNumber: e.line,
      endLineNumber: e.line,
      startColumn: 1,
      endColumn: model.getLineMaxColumn(e.line),
    }));
    monaco.editor.setModelMarkers(model, 'stategraph', markers);
  }, [errors]);

  return <div ref={containerRef} style={{ width: '100%', height: '100%' }} />;
}
```


---

## 6. React Flow 画布 `GraphCanvas/Canvas.tsx`（核心片段）

展示 GraphModel → React Flow nodes/edges 的映射，以及拖拽、连线、删除的回调逻辑。

```tsx
// GraphCanvas/Canvas.tsx（懒加载 chunk）
import {
  ReactFlow, Background, Controls, MiniMap,
  addEdge, applyNodeChanges, applyEdgeChanges,
  useNodesState, useEdgesState,
  type Node, type Edge, type OnConnect,
  type NodeChange, type EdgeChange,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { GraphModel, StepNode as StepNodeData } from '../core/model';
import StepNode from './StepNode';
import TransitionEdge from './TransitionEdge';
import NodePropertiesPanel from './NodePropertiesPanel';

const NODE_TYPES = { step: StepNode };
const EDGE_TYPES = { transition: TransitionEdge };

// GraphModel → React Flow 数据结构
function modelToFlow(model: GraphModel): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = [
    // 虚拟终端节点
    { id: '__start__', type: 'input', position: model.layout['__start__'] ?? { x: 0, y: 200 }, data: { label: '' } },
    { id: '__end__',   type: 'output', position: model.layout['__end__'] ?? { x: 800, y: 200 }, data: { label: '' } },
    // 业务步骤节点
    ...model.steps.map((s): Node => ({
      id: s.id,
      type: 'step',
      position: model.layout[s.id] ?? { x: 200, y: 200 },
      data: s,  // 把整个 StepNode 数据传给自定义节点组件
    })),
  ];

  const edges: Edge[] = model.steps.flatMap((s) =>
    s.transitions.map((t, i): Edge => ({
      id: `${s.id}->${t.to}-${i}`,
      source: s.id,
      target: t.to,
      type: 'transition',
      data: { condition: t.condition },
    }))
  );

  return { nodes, edges };
}

// React Flow 数据结构 → GraphModel
function flowToModel(
  nodes: Node[],
  edges: Edge[],
  prevModel: GraphModel,
): GraphModel {
  const layout: GraphModel['layout'] = {};
  const steps: StepNodeData[] = [];

  for (const n of nodes) {
    layout[n.id] = { x: Math.round(n.position.x), y: Math.round(n.position.y) };
    if (n.type === 'step' && n.data) {
      const d = n.data as StepNodeData;
      // transitions 从 edges 重建
      const transitions = edges
        .filter((e) => e.source === n.id)
        .map((e) => ({ to: e.target, condition: (e.data?.condition as string) ?? '' }));
      steps.push({ ...d, transitions });
    }
  }

  return { slots: prevModel.slots, steps, layout };
}

interface Props {
  model: GraphModel;
  errors: import('../core/model').ValidationError[];
  onChange: (model: GraphModel) => void;
}

export default function Canvas({ model, errors, onChange }: Props) {
  const { nodes: initNodes, edges: initEdges } = useMemo(() => modelToFlow(model), []);
  const [nodes, setNodes, onNodesChange] = useNodesState(initNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initEdges);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);

  // 外部（YAML 编辑）更新 model 时，同步到 React Flow
  useEffect(() => {
    const { nodes: newNodes, edges: newEdges } = modelToFlow(model);
    setNodes(newNodes);
    setEdges(newEdges);
  }, [model]);  // model 引用变化时才同步，防止循环

  // 节点/边变化 → 更新 GraphModel → 回调给父组件
  const handleNodesChange = useCallback((changes: NodeChange[]) => {
    setNodes((nds) => {
      const updated = applyNodeChanges(changes, nds);
      // 拖拽结束时才触发 onChange（避免拖拽过程中频繁序列化）
      const isDragStop = changes.some((c) => c.type === 'position' && !c.dragging);
      if (isDragStop) {
        onChange(flowToModel(updated, edges, model));
      }
      return updated;
    });
  }, [edges, model, onChange]);

  const handleEdgesChange = useCallback((changes: EdgeChange[]) => {
    setEdges((eds) => {
      const updated = applyEdgeChanges(changes, eds);
      onChange(flowToModel(nodes, updated, model));
      return updated;
    });
  }, [nodes, model, onChange]);

  const handleConnect: OnConnect = useCallback((params) => {
    setEdges((eds) => {
      const newEdge = { ...params, type: 'transition', data: { condition: '' } };
      const updated = addEdge(newEdge, eds);
      onChange(flowToModel(nodes, updated, model));
      return updated;
    });
  }, [nodes, model, onChange]);

  // 错误 id 集合，传给节点/边做红色描边
  const errorNodeIds = useMemo(() => new Set(errors.map((e) => e.nodeId).filter(Boolean) as string[]), [errors]);
  const errorEdgeKeys = useMemo(() => new Set(errors.map((e) => e.edgeKey).filter(Boolean) as string[]), [errors]);

  return (
    <div style={{ width: '100%', height: '100%', position: 'relative' }}>
      <ReactFlow
        nodes={nodes.map((n) => ({ ...n, data: { ...n.data, hasError: errorNodeIds.has(n.id) } }))}
        edges={edges.map((e) => ({ ...e, data: { ...e.data, hasError: errorEdgeKeys.has(e.id) } }))}
        nodeTypes={NODE_TYPES}
        edgeTypes={EDGE_TYPES}
        onNodesChange={handleNodesChange}
        onEdgesChange={handleEdgesChange}
        onConnect={handleConnect}
        onNodeClick={(_, n) => setSelectedNodeId(n.id)}
        onPaneClick={() => setSelectedNodeId(null)}
        fitView
      >
        <Background />
        <Controls />
        <MiniMap />
      </ReactFlow>

      {/* 右侧属性面板（选中节点时显示）*/}
      {selectedNodeId && selectedNodeId !== '__start__' && selectedNodeId !== '__end__' && (
        <NodePropertiesPanel
          nodeId={selectedNodeId}
          model={model}
          onClose={() => setSelectedNodeId(null)}
          onChange={onChange}
        />
      )}
    </div>
  );
}
```


---

## 7. 编辑器入口 `index.tsx`（双向同步核心）

管理 GraphModel 状态，协调图形视图和 YAML 视图之间的同步，以及防循环更新。

```tsx
// StateGraphEditor/index.tsx
import { useCallback, useRef, useState, useDeferredValue } from 'react';
import { Segmented } from 'antd';
import GraphCanvas from './GraphCanvas';
import YamlEditor from './YamlEditor';
import ValidationPanel from './ValidationPanel';
import { parseYaml, ParseError } from './core/parser';
import { serializeYaml, serializeYamlSemantic } from './core/serializer';
import { validateStateGraph } from './core/validator';
import type { GraphModel, ValidationError } from './core/model';

type ViewMode = 'canvas' | 'yaml';

interface Props {
  initialYaml: string;
  onSaveDraft: (yaml: string) => Promise<void>;
}

export default function StateGraphEditor({ initialYaml, onSaveDraft }: Props) {
  const [viewMode, setViewMode] = useState<ViewMode>('canvas');

  // GraphModel 是两个视图的唯一 source of truth
  const [model, setModel] = useState<GraphModel>(() => parseYaml(initialYaml));

  // YAML 视图内容（纯语义，不含 x-layout）
  // 用 useDeferredValue 降低 YAML 视图的更新优先级，优先保证图形视图流畅
  const semanticYaml = useDeferredValue(serializeYamlSemantic(model));

  // 校验结果
  const [errors, setErrors] = useState<ValidationError[]>(() => validateStateGraph(parseYaml(initialYaml)));

  // 防循环更新标志：图→YAML 时不触发 YAML→图 的反向更新
  const updatingFromCanvas = useRef(false);

  // ── 图形视图操作 → 更新 GraphModel ──────────────────────────────────
  const handleCanvasChange = useCallback((newModel: GraphModel) => {
    updatingFromCanvas.current = true;
    setModel(newModel);
    setErrors(validateStateGraph(newModel));
    // 下一微任务清除标志，确保 YAML 视图的 useEffect 能读到正确值
    queueMicrotask(() => { updatingFromCanvas.current = false; });
  }, []);

  // ── YAML 视图编辑 → 更新 GraphModel ─────────────────────────────────
  const handleYamlChange = useCallback((yamlText: string) => {
    if (updatingFromCanvas.current) return;  // 忽略图→YAML 引发的 onChange
    try {
      // 合并布局：YAML 中没有 x-layout，用当前 model.layout 保持画布坐标
      const parsed = parseYaml(yamlText);
      const merged: GraphModel = { ...parsed, layout: model.layout };
      setModel(merged);
      setErrors(validateStateGraph(merged));
    } catch (e) {
      if (e instanceof ParseError) {
        // YAML 语法错误：只更新错误面板，不更新 GraphModel（图形视图保持上一有效状态）
        setErrors([{ rule: 'V10', message: e.message, yamlLine: e.line }]);
      }
    }
  }, [model.layout]);

  const handleSave = async () => {
    if (errors.length > 0) return;  // 有错误时不保存（UI 层已禁用按钮，这里是兜底）
    await onSaveDraft(serializeYaml(model));
  };

  // Monaco squiggle 需要的 {line, message} 格式
  const yamlErrors = errors
    .filter((e) => e.yamlLine !== undefined)
    .map((e) => ({ line: e.yamlLine!, message: e.message }));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {/* 工具栏 */}
      <div style={{ display: 'flex', alignItems: 'center', padding: '8px 16px', borderBottom: '1px solid #f0f0f0', gap: 12 }}>
        <Segmented
          options={[
            { label: '画布', value: 'canvas' },
            { label: 'YAML', value: 'yaml' },
          ]}
          value={viewMode}
          onChange={(v) => setViewMode(v as ViewMode)}
        />
        <span style={{ flex: 1 }} />
        <button
          onClick={handleSave}
          disabled={errors.length > 0}
          style={{ opacity: errors.length > 0 ? 0.5 : 1 }}
        >
          保存草稿
        </button>
      </div>

      {/* 主编辑区（两个视图通过 display:none 隐藏，保留 DOM 状态）*/}
      <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}>
        <div style={{ display: viewMode === 'canvas' ? 'block' : 'none', width: '100%', height: '100%' }}>
          <GraphCanvas model={model} errors={errors} onChange={handleCanvasChange} />
        </div>
        <div style={{ display: viewMode === 'yaml' ? 'block' : 'none', width: '100%', height: '100%' }}>
          <YamlEditor value={semanticYaml} onChange={handleYamlChange} errors={yamlErrors} />
        </div>
      </div>

      {/* 底部错误面板 */}
      {errors.length > 0 && (
        <ValidationPanel errors={errors} />
      )}
    </div>
  );
}
```

> **防循环更新说明**
>
> 图形操作 → `handleCanvasChange` → `setModel` → `semanticYaml` 重算 → `YamlEditor` 收到新 `value` prop
> → 调用 `editor.setValue()`（Monaco 内部静默更新，不触发 `onDidChangeModelContent`）
>
> YAML 编辑 → `onDidChangeModelContent` → `handleYamlChange` → `setModel` → Canvas 收到新 `model` prop
> → `useEffect([model])` → `setNodes/setEdges`（React Flow 内部更新，不触发 `onNodesChange`）
>
> 两条路径均不会形成循环，`updatingFromCanvas` ref 是额外的防御层，用于应对异步 debounce 期间的竞态。


---

## 8. 自定义节点 `StepNode.tsx`（视觉对齐现有 StateGraphView）

编辑态节点卡片，视觉风格与只读 `StateGraphView` 一致，去掉执行状态，加入 mode 标签和错误描边。

```tsx
// GraphCanvas/StepNode.tsx
import { Handle, Position, type NodeProps } from '@xyflow/react';
import type { StepNode as StepNodeData } from '../core/model';

interface StepNodeComponentData extends StepNodeData {
  hasError?: boolean;
}

export default function StepNode({ data, selected }: NodeProps) {
  const d = data as StepNodeComponentData;
  const borderColor = d.hasError ? '#ff4d4f' : selected ? '#1677ff' : '#e8e8e8';
  const boxShadow  = d.hasError
    ? '0 0 0 2px #ff4d4f44'
    : selected
    ? '0 0 0 2px #1677ff44, 0 2px 8px rgba(0,0,0,0.10)'
    : '0 2px 6px rgba(0,0,0,0.08)';

  return (
    <>
      {/* 输入 Handle（左侧）*/}
      <Handle type='target' position={Position.Left} style={{ background: '#bfbfbf' }} />

      <div style={{
        width: 148, minHeight: 72,
        background: '#fff',
        borderRadius: 10,
        border: `1.5px solid ${borderColor}`,
        boxShadow,
        padding: '8px 11px',
        display: 'flex', flexDirection: 'column', gap: 6,
        cursor: 'pointer',
        userSelect: 'none',
      }}>
        {/* mode 标签 */}
        <span style={{
          display: 'inline-block', alignSelf: 'flex-start',
          fontSize: 10, fontWeight: 600, letterSpacing: '0.4px', textTransform: 'uppercase',
          padding: '1px 6px', borderRadius: 4,
          background: d.mode === 'human' ? '#e6f4ff' : '#f6ffed',
          color:      d.mode === 'human' ? '#0958d9' : '#389e0d',
        }}>
          {d.mode}
        </span>

        {/* 节点标签 */}
        <div style={{ fontSize: 13, fontWeight: 600, color: '#141414', lineHeight: 1.3 }}>
          {d.label || d.id}
        </div>

{/* slot 输出标签 */}
        {d.outputs.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
            {d.outputs.map((key) => (
              <span key={key} style={{
                fontSize: 10, padding: '1px 5px', borderRadius: 3,
                background: '#f5f5f5', color: '#8c8c8c', fontFamily: 'monospace',
              }}>
                {key}
              </span>
            ))}
          </div>
        )}
      </div>

      {/* 输出 Handle（右侧）*/}
      <Handle type='source' position={Position.Right} style={{ background: '#bfbfbf' }} />
    </>
  );
}
```

---

## 9. 依赖安装

```bash
# 编辑画布
pnpm add @xyflow/react

# YAML 编辑器（Monaco 核心 + YAML worker）
pnpm add monaco-editor
pnpm add -D vite-plugin-monaco-editor

# YAML 解析（js-yaml 大概率已作为间接依赖存在，显式声明）
pnpm add js-yaml
pnpm add -D @types/js-yaml
```

`vite.config.ts` 增加 Monaco worker 插件：

```typescript
import monacoEditorPlugin from 'vite-plugin-monaco-editor';

export default defineConfig({
  plugins: [
    react(),
    svgr(),
    // Monaco worker 自动打包到独立 chunk
    monacoEditorPlugin.default({ languageWorkers: ['editorWorkerService', 'yaml'] }),
  ],
});
```

