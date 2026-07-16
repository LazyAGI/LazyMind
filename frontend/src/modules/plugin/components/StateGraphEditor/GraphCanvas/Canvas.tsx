import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  addEdge,
  useNodesState,
  useEdgesState,
  useReactFlow,
  useStore,
  ReactFlowProvider,
  PanOnScrollMode,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  type EdgeTypes,
  type NodeChange,
  type EdgeChange,
  MarkerType,
  SelectionMode,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { v4 as uuidv4 } from 'uuid';
import type { GraphModel, StepNode, NodeLayout } from '../core/model';
import {
  VIRTUAL_END,
  VIRTUAL_START,
  formatExpression,
  newHiddenId,
} from '../core/model';
import type { ValidationError } from '../core/validator';
import type { PluginModel } from '../core/pluginModel';
import type { ScenarioData } from '../ScenarioEditor';
import { StepNodeRenderer, TerminalNode, buildNodeErrorMap, NODE_DEFAULT_WIDTH, NODE_MIN_WIDTH, type StepNodeData } from './StepNode';
import { TransitionEdge } from './TransitionEdge';
import { EdgeVisualPanel, NodeVisualPanel } from './VisualPropertiesPanel';
import { deleteLayoutNode, edgeId, reconnectEdgeLayout, renameLayoutNode, NODE_MIN_HEIGHT } from '../core/layout';
import NodePropertiesPanel from './NodePropertiesPanel';
import { useAlignmentGuides } from './useAlignmentGuides';
import { AlignmentGuides } from './AlignmentGuides';
import './Canvas.scss';

const NODE_WIDTH = NODE_DEFAULT_WIDTH;
const NODE_HEIGHT = 80;
const DEFAULT_SPACING_X = 200;
const DEFAULT_SPACING_Y = 100;

export interface CanvasHandle {
  addNode: () => void;
  focusNode: (nodeId: string) => boolean;
}

interface Props {
  model: GraphModel;
  errors: ValidationError[];
  onModelChange: (model: GraphModel) => void;
  pluginModel?: PluginModel;
  scenarioData?: ScenarioData;
  onScenarioChange?: (data: ScenarioData) => void;
  canvasRef?: React.Ref<CanvasHandle>;
  readonly?: boolean;
  onCreateArtifact?: () => void;
}

const nodeTypes: NodeTypes = {
  step: StepNodeRenderer as NodeTypes['step'],
  terminal: TerminalNode as NodeTypes['terminal'],
};

const edgeTypes: EdgeTypes = {
  transition: TransitionEdge as EdgeTypes['transition'],
};

function buildPredecessorMap(model: GraphModel): Map<string, string[]> {
  const map = new Map<string, string[]>();
  for (const t of model.startTransitions) {
    if (!map.has(t.to)) map.set(t.to, []);
    map.get(t.to)!.push(VIRTUAL_START);
  }
  for (const node of model.nodes) {
    for (const t of node.transitions) {
      if (!map.has(t.to)) map.set(t.to, []);
      map.get(t.to)!.push(node.id);
    }
  }
  return map;
}

function modelToFlowNodes(
  model: GraphModel,
  nodeErrorMap: Map<string, string[]>,
  onResizeEnd: (nodeId: string, width: number, height?: number) => void,
  onResizeDrag: (nodeId: string, width: number, height?: number) => { width: number; height?: number },
  getZoom: () => number,
): Node[] {
  const flowNodes: Node[] = [];
  let autoX = 80;
  const predMap = buildPredecessorMap(model);

  // __start__ virtual node
  const startVisual=model.layout[VIRTUAL_START]??{x:autoX,y:200};
  flowNodes.push({
    id: VIRTUAL_START,
    type: 'terminal',
    position: startVisual,
    data: { type: 'start', label: 'START', visual:startVisual },
    ...(startVisual.width != null ? {width:startVisual.width}:{}),
    draggable: true,
  });

  autoX += DEFAULT_SPACING_X;

  for (const node of model.nodes) {
    const pos: NodeLayout = model.layout[node.id] ?? { x: autoX, y: 150 + (flowNodes.length % 2) * DEFAULT_SPACING_Y };
    const errMsgs = nodeErrorMap.get(node.id) ?? [];
    const nodeWidth = pos.width ?? NODE_WIDTH;
    // Build output label map: slotId → display label
    const outputLabels: Record<string, string> = {};
    for (const ref of node.outputs) {
      const slotId = ref.material;
      const slot = model.slots[slotId];
      outputLabels[slotId] = slot?.label ?? slotId;
    }
    flowNodes.push({
      id: node.id,
      type: 'step',
      position: pos,
      data: {
        ...node,
        inputs: node.inputs.flatMap((input) => [input.material, ...(input.alternatives ?? [])]),
        outputs: node.outputs.map((r) => r.material),
        hasError: errMsgs.length > 0,
        errorMessages: errMsgs,
        predecessorIds: predMap.get(node.id) ?? [],
        outputLabels,
        nodeWidth,
        nodeHeight: pos.height,
        visual: pos,
        onResizeEnd,
        onResizeDrag,
        getZoom,
      },
      selected: false,
      width: nodeWidth,
    });
    autoX += DEFAULT_SPACING_X;
  }

  // __end__ virtual node
  const endVisual=model.layout[VIRTUAL_END]??{x:autoX,y:200};
  flowNodes.push({
    id: VIRTUAL_END,
    type: 'terminal',
    position: endVisual,
    data: { type: 'end', label: 'END', visual:endVisual },
    ...(endVisual.width != null ? {width:endVisual.width}:{}),
    draggable: true,
  });

  return flowNodes;
}

function modelToFlowEdges(model: GraphModel, nodeErrorMap: Map<string, string[]>): Edge[] {
  const edges: Edge[] = [];
  const edgeErrorSet = new Set(
    [...nodeErrorMap.entries()].flatMap(([, msgs]) => msgs.filter((m) => m.includes('->'))),
  );

  // Render __start__ → target edges for each startTransition
  for (const t of model.startTransitions) {
    edges.push({
      id: `${VIRTUAL_START}->${t.to}`,
      source: VIRTUAL_START,
      target: t.to,
      type: 'transition',
      reconnectable: 'target' as const,
      markerEnd: { type: MarkerType.ArrowClosed },
      data: { condition: t.when || formatExpression(t.condition) || '', hasError: false, visual: model.edgeLayout[`${VIRTUAL_START}->${t.to}`] },
    });
  }

  for (const node of model.nodes) {
    const isMultiExit = node.transitions.length > 1;
    const isParallel = (node.route === 'all' || !node.route) && isMultiExit;
    for (const t of node.transitions) {
      const edgeKey = `${node.id}->${t.to}`;
      const hasError = edgeErrorSet.has(edgeKey) || Boolean(t.condition);
      edges.push({
        id: edgeKey,
        source: node.id,
        target: t.to,
        type: 'transition',
        reconnectable: 'target' as const,
        markerEnd: { type: MarkerType.ArrowClosed },
        data: {
          condition: t.when || formatExpression(t.condition) || '',
          hasError,
          isParallel,
          visual: model.edgeLayout[edgeKey],
        },
      });
    }
  }

  return edges;
}

function CanvasInner({ model, errors, onModelChange, pluginModel, scenarioData, onScenarioChange, readonly = false, onCreateArtifact }: Props, ref: React.Ref<CanvasHandle>) {
  const { t } = useTranslation();
  const { screenToFlowPosition, zoomIn, zoomOut, getZoom, setCenter } = useReactFlow();
  const nodeErrorMap = useMemo(() => buildNodeErrorMap(errors), [errors]);
  const { guides, onNodeDrag: computeGuides, onNodeDragStop: clearGuides, onNodeResize } = useAlignmentGuides();

  // Stable resize callback used both by initialNodes (useMemo) and runtime.
  // Defined as a ref so it's available before setNodes is initialized.
  const handleNodeResizeEndRef = useRef<(nodeId: string, width: number, height?: number) => void>(() => {});
  const stableResizeEnd = useCallback((nodeId: string, width: number, height?: number) => {
    handleNodeResizeEndRef.current(nodeId, width, height);
  }, []);

  // Live resize drag: updates ReactFlow node width in real time without persisting to model.
  const handleNodeResizeDragRef = useRef<(nodeId: string, width: number, height?: number) => {width:number;height?:number}>((_id,width,height) => ({width,height}));
  const stableResizeDrag = useCallback((nodeId: string, width: number, height?: number) => {
    return handleNodeResizeDragRef.current(nodeId, width, height);
  }, []);

  // Stable zoom getter passed into StepNode so the resize handle can do correct
  // screen→canvas coordinate conversion without needing React context.
  const getZoomRef = useRef(getZoom);
  getZoomRef.current = getZoom;
  const stableGetZoom = useCallback(() => getZoomRef.current(), []);

  // Read all current nodes directly from the ReactFlow store.
  // onNodeDrag's third arg `allNodes` only contains the dragged node in RF 12.x.
  const allNodesFromStore = useStore((s) => s.nodes);

  // Track the last snapped position per node so onNodeDragStop can persist it
  // instead of the pre-snap position that ReactFlow passes in its callback arg.
  const snapPositionRef = useRef<Record<string, { x: number; y: number }>>({});

  // Keep latest model/onModelChange in refs so callbacks below don't need them
  // as deps (avoids re-creating callbacks on every model change).
  const modelRef = useRef(model);
  const onModelChangeRef = useRef(onModelChange);
  // Update synchronously (not via useEffect) so that any Canvas callback fired
  // in the same render cycle always reads the latest model, even before React
  // has committed and run effects. useEffect would lag by one commit.
  modelRef.current = model;
  onModelChangeRef.current = onModelChange;

  // Keep nodeErrorMap in a ref so stable callbacks can always read the latest value.
  const nodeErrorMapRef = useRef(nodeErrorMap);
  nodeErrorMapRef.current = nodeErrorMap;

  const initialNodes = useMemo(
    () => modelToFlowNodes(model, nodeErrorMap, stableResizeEnd, stableResizeDrag, stableGetZoom),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  const initialEdges = useMemo(
    () => modelToFlowEdges(model, nodeErrorMap),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState(initialNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initialEdges);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [selectedNodeIds, setSelectedNodeIds] = useState<Set<string>>(new Set());
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null);
  const [createAt, setCreateAt] = useState<{flowX:number;flowY:number;left:number;top:number}|null>(null);
  const resizeFrameRef = useRef<number | null>(null);
  const visualClipboardRef = useRef<{type:'node'; value:Pick<NodeLayout,'visible'|'fill'|'border'>}|{type:'edge';value:GraphModel['edgeLayout'][string]}|null>(null);

  useEffect(() => () => {
    if (resizeFrameRef.current !== null) cancelAnimationFrame(resizeFrameRef.current);
  }, []);

  // When a canvas-internal operation calls onModelChange, we set this flag so
  // the sync useEffect below knows NOT to re-derive nodes/edges from the model
  // (the canvas already has the correct visual state from the operation itself).
  const skipSyncRef = useRef(false);

  // Track node selection purely via click events — one clear entry point,
  // no stale-closure or race-condition issues.
  const handleNodeClick = useCallback(
    (event: React.MouseEvent, node: Node) => {
      const multi=event.shiftKey||event.metaKey||event.ctrlKey;
      setSelectedNodeIds((current)=>{
        const next=multi?new Set(current):new Set<string>();
        if(multi&&next.has(node.id))next.delete(node.id);else next.add(node.id);
        setNodes((items)=>items.map(item=>({...item,selected:next.has(item.id)})));
        return next;
      });
      setSelectedNodeId(node.id);
      setSelectedEdgeId(null);
    },
    [setNodes],
  );

  const handleNodesChange = useCallback(
    (changes: NodeChange[]) => {
      onNodesChange(changes);
    },
    [onNodesChange],
  );
  const handleSelectionChange=useCallback(({nodes:selected=[],edges:selectedEdges=[]}: {nodes?:Node[];edges?:Edge[]})=>{
    if(selected.length>0){const ids=new Set(selected.map(node=>node.id));setSelectedNodeIds(ids);setSelectedNodeId(selected.length===1?selected[0].id:null);setSelectedEdgeId(null);return;}
    if(selectedEdges.length>0){setSelectedEdgeId(selectedEdges[0].id);setSelectedNodeId(null);setSelectedNodeIds(new Set());}
  },[]);

  const handleEdgesChange = useCallback(
    (changes: EdgeChange[]) => {
      onEdgesChange(changes);
      for (const change of changes) {
        if (change.type === 'select') {
          if (change.selected) {
            setSelectedEdgeId(change.id);
            setSelectedNodeId(null);
            setSelectedNodeIds(new Set());
          }
        }
      }
    },
    [onEdgesChange],
  );
  const handleEdgeClick=useCallback((_event:React.MouseEvent,edge:Edge)=>{
    setSelectedEdgeId(edge.id);setSelectedNodeId(null);setSelectedNodeIds(new Set());
    setEdges(items=>items.map(item=>({...item,selected:item.id===edge.id})));
  },[setEdges]);

  // Keep nodes/edges in sync when model changes from OUTSIDE the canvas
  // (e.g. YAML editor, undo). Internal canvas operations use skipSyncRef to
  // opt out, since they already have the correct ReactFlow visual state.
  useEffect(() => {
    if (skipSyncRef.current) {
      skipSyncRef.current = false;
      return;
    }
    const newNodes = modelToFlowNodes(model, nodeErrorMapRef.current, stableResizeEnd, stableResizeDrag, stableGetZoom);
    // Preserve selected state and, importantly, the current rendered width of each
    // node. Width is managed independently via onResizeDrag/onResizeEnd and may
    // have been updated inside a setNodes callback that hasn't propagated back into
    // model.layout yet. Re-deriving width from model here would cause a flicker
    // back to the stale value. Always trust the current ReactFlow node's width.
    setNodes((currentNodes) => {
      const currentById = new Map(currentNodes.map((n) => [n.id, n]));
      const selectedSet = new Set(currentNodes.filter((n) => n.selected).map((n) => n.id));
      return newNodes.map((n) => {
        const current = currentById.get(n.id);
        // Prefer our own nodeWidth over ReactFlow's measured node.width — ReactFlow
        // may re-measure from the DOM and overwrite the resize value we set, while
        // nodeWidth is only ever updated by our own resize callbacks.
        const preservedNodeWidth = (current?.data as { nodeWidth?: number } | undefined)?.nodeWidth
          ?? (n.data as { nodeWidth?: number }).nodeWidth;
        const preservedWidth = preservedNodeWidth ?? current?.width ?? n.width;
        return {
          ...n,
          width: preservedWidth,
          data: { ...n.data, nodeWidth: preservedNodeWidth },
          selected: selectedSet.has(n.id) ? true : n.selected,
        };
      });
    });
    setEdges(modelToFlowEdges(model, nodeErrorMapRef.current));
  }, [model, setNodes, setEdges]);

  // Propagate error state changes to ReactFlow nodes independently of the main
  // model sync. This runs even when skipSyncRef suppresses the full sync above,
  // so error highlights update immediately after the parent re-validates.
  useEffect(() => {
    setNodes((currentNodes) => {
      let changed = false;
      const next = currentNodes.map((n) => {
        const errMsgs = nodeErrorMap.get(n.id) ?? [];
        const hasError = errMsgs.length > 0;
        // Compare by value to avoid updating nodes whose error state didn't change.
        const prevHasError = (n.data as { hasError?: boolean }).hasError ?? false;
        const prevMsgs = (n.data as { errorMessages?: string[] }).errorMessages ?? [];
        if (prevHasError === hasError && prevMsgs.length === errMsgs.length && prevMsgs.every((m, i) => m === errMsgs[i])) {
          return n;
        }
        changed = true;
        return { ...n, data: { ...n.data, hasError, errorMessages: errMsgs } };
      });
      return changed ? next : currentNodes;
    });
    setEdges(modelToFlowEdges(modelRef.current, nodeErrorMap));
  }, [nodeErrorMap, setNodes, setEdges]);

  // When a new edge is drawn in the canvas, add a transition to the model
  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) return;

      const m = modelRef.current;
      if (connection.source === VIRTUAL_START
        ? m.startTransitions.some((t) => t.to === connection.target)
        : m.nodes.find((n) => n.id === connection.source)?.transitions.some((t) => t.to === connection.target)) return;

      if (connection.source === VIRTUAL_START) {
        // Prevent duplicate edges to the same target
        const target = connection.target;
        if (m.startTransitions.some((t) => t.to === target)) return;
        const newTransition = { to: target };
        const newStartEdgeId = edgeId(VIRTUAL_START, target);
        setEdges((eds) =>
          addEdge(
            {
              ...connection,
              id: newStartEdgeId,
              type: 'transition',
              markerEnd: { type: MarkerType.ArrowClosed },
              data: { condition: '', hasError: false },
            },
            eds,
          ),
        );
        skipSyncRef.current = true;
        onModelChangeRef.current({ ...m, startTransitions: [...m.startTransitions, newTransition] });
        return;
      }

      const sourceNode = m.nodes.find((n) => n.id === connection.source);
      if (!sourceNode) return;

      const newTransition = { to: connection.target };
      const updatedNodes = m.nodes.map((n) =>
        n.id === connection.source
          ? { ...n, transitions: [...n.transitions, newTransition] }
          : n,
      );
      const newModel = { ...m, nodes: updatedNodes };
      skipSyncRef.current = true;
      onModelChangeRef.current(newModel);
      // Sync the source node's data in ReactFlow so the properties panel reflects
      // the new transition immediately (without waiting for the parent re-render).
      const updatedSource = updatedNodes.find((n) => n.id === connection.source)!;
      const errMsgs = nodeErrorMap.get(connection.source) ?? [];
      setNodes((nds) =>
        nds.map((n) =>
          n.id === connection.source
            ? { ...n, data: { ...n.data, ...updatedSource, hasError: errMsgs.length > 0, errorMessages: errMsgs } }
            : n,
        ),
      );
      setEdges((eds) => addEdge({ ...connection, type: 'transition', markerEnd: { type: MarkerType.ArrowClosed }, data: { condition: '', hasError: false } }, eds));
    },
    [setEdges, setNodes, nodeErrorMap],
  );

  // Reconnect: user drags the target end of an existing edge to a new node.
  // Only the target end is reconnectable (controlled by edge.reconnectable = 'target').
  const onReconnect = useCallback(
    (oldEdge: Edge, newConnection: Connection) => {
      if (!newConnection.source || !newConnection.target) return;
      const oldSrc = oldEdge.source;
      const oldTgt = oldEdge.target;
      const newTgt = newConnection.target;
      if (oldTgt === newTgt) return;

      const m = modelRef.current;
      const candidateId = edgeId(oldSrc, newTgt);
      if (edges.some((edge) => edge.id === candidateId && edge.id !== oldEdge.id)) return;

      if (oldSrc === VIRTUAL_START) {
        const newStartTransitions = m.startTransitions.map((t) =>
          t.to === oldTgt ? { ...t, to: newTgt } : t,
        );
        setEdges((eds) => eds.map((e) =>
          e.id === oldEdge.id ? { ...e, id: `${VIRTUAL_START}->${newTgt}`, target: newTgt } : e,
        ));
        skipSyncRef.current = true;
        onModelChangeRef.current({ ...m, startTransitions: newStartTransitions, edgeLayout: reconnectEdgeLayout(m.edgeLayout, oldEdge.id, candidateId) });
        return;
      }

      const newNodes = m.nodes.map((n) => {
        if (n.id !== oldSrc) return n;
        return {
          ...n,
          transitions: n.transitions.map((t) =>
            t.to === oldTgt ? { ...t, to: newTgt } : t,
          ),
        };
      });
      const newEdgeId = `${oldSrc}->${newTgt}`;
      setEdges((eds) => eds.map((e) =>
        e.id === oldEdge.id ? { ...e, id: newEdgeId, target: newTgt } : e,
      ));
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, nodes: newNodes, edgeLayout: reconnectEdgeLayout(m.edgeLayout, oldEdge.id, newEdgeId) });
    },
    [setEdges, edges],
  );

  // Sync drag-stop positions back to the model layout
  const onNodeDragStop = useCallback(
    (_event: React.MouseEvent, node: Node) => {
      clearGuides();
      // Use the snapped position if one was recorded during this drag; otherwise
      // fall back to whatever position ReactFlow reports (pre-snap coordinates).
      const snapped = snapPositionRef.current[node.id];
      const pos = snapped ?? { x: node.position.x, y: node.position.y };
      delete snapPositionRef.current[node.id];

      const m = modelRef.current;
      // Preserve existing width so drag-stop doesn't reset a resized node back to default.
      const existingEntry = m.layout[node.id];
      const newLayout = { ...m.layout, [node.id]: { ...(existingEntry ?? {}), ...pos } };
      // If we snapped, also update the ReactFlow node state so it stays at the
      // snapped position (ReactFlow resets to its own tracked pos on drag-stop).
      if (snapped) {
        setNodes((nds) =>
          nds.map((n) => (n.id === node.id ? { ...n, position: snapped } : n)),
        );
      }
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, layout: newLayout });
    },
    [clearGuides, setNodes],
  );

  const onNodeDrag = useCallback(
    (_event: React.MouseEvent, node: Node) => {
      const merged = allNodesFromStore.map((n) => (n.id === node.id ? node : n));
      const snap = computeGuides(node, merged);
      if (snap) {
        snapPositionRef.current[node.id] = snap;
        // Defer setNodes to avoid calling React setState inside ReactFlow's own
        // synchronous event handler, which causes Minified React error #185.
        queueMicrotask(() => {
          setNodes((nds) =>
            nds.map((n) => (n.id === node.id ? { ...n, position: snap } : n)),
          );
        });
      } else {
        delete snapPositionRef.current[node.id];
      }
    },
    [computeGuides, allNodesFromStore, setNodes],
  );

  // Persist resize: update layout width when user finishes resizing a node.
  const handleNodeResizeEnd = useCallback(
    (nodeId: string, width: number, height?: number) => {
      const w = Math.max(NODE_MIN_WIDTH, width);clearGuides();
      if (resizeFrameRef.current !== null) {
        cancelAnimationFrame(resizeFrameRef.current);
        resizeFrameRef.current = null;
      }
      setNodes((nds) =>
        nds.map((n) => {
          if (n.id !== nodeId) return n;
          return { ...n, width: w, data: { ...n.data, nodeWidth: w, ...(height != null ? { nodeHeight: height } : {}) } };
        }),
      );
      // Persist on the next frame, after ReactFlow has committed the visual width.
      // Updating the parent model and ReactFlow's controlled node store in the
      // same resize event can make its internal dimension sync recurse (#185).
      resizeFrameRef.current = requestAnimationFrame(() => {
        resizeFrameRef.current = null;
        const m = modelRef.current;
        const rfNode = allNodesFromStore.find((n) => n.id === nodeId);
        const pos = m.layout[nodeId] ?? rfNode?.position ?? { x: 0, y: 0 };
        const newLayout = { ...m.layout, [nodeId]: { ...pos, width: w, ...(height != null ? { height: Math.max(NODE_MIN_HEIGHT, height) } : {}) } };
        skipSyncRef.current = true;
        onModelChangeRef.current({ ...m, layout: newLayout });
      });
    },
    [setNodes, allNodesFromStore, clearGuides],
  );
  // Wire the stable ref to the real implementation now that setNodes is available.
  handleNodeResizeEndRef.current = handleNodeResizeEnd;

  // Live resize drag: update ReactFlow node width in real time (no model persist).
  const handleNodeResizeDrag = useCallback(
    (nodeId: string, width: number, height?: number) => {
      const snapped=onNodeResize(nodeId,width,height,allNodesFromStore);const w = Math.max(NODE_MIN_WIDTH, snapped.width);height=snapped.height;
      if (resizeFrameRef.current !== null) cancelAnimationFrame(resizeFrameRef.current);
      resizeFrameRef.current = requestAnimationFrame(() => {
        resizeFrameRef.current = null;
        setNodes((nds) =>
          nds.map((n) => {
            if (n.id !== nodeId) return n;
            return { ...n, width: w, data: { ...n.data, nodeWidth: w, ...(height != null ? { nodeHeight: height } : {}) } };
          }),
        );
      });
      return snapped;
    },
    [setNodes,onNodeResize,allNodesFromStore],
  );
  handleNodeResizeDragRef.current = handleNodeResizeDrag;

  // Handle node/edge deletion via Delete or Backspace key,
  // and Cmd+/- zoom within the canvas (prevents browser zoom).
  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'Escape' && createAt) {
        event.preventDefault();
        setCreateAt(null);
        return;
      }
      // Property panels live inside the canvas container, but their editing keys
      // must never be interpreted as canvas-level delete/zoom shortcuts.
      if ((event.target as HTMLElement).closest('.node-props-panel')) return;
      // Cmd/Ctrl + '=' (Plus) or '-' (Minus) — zoom canvas, block browser zoom
      if (event.metaKey || event.ctrlKey) {
        if (event.key === '+' || event.key === '=' || event.key === '-') {
          event.preventDefault();
          if (event.key === '-') {
            zoomOut({ duration: 200 });
          } else {
            zoomIn({ duration: 200 });
          }
          return;
        }
      }

      if (event.key !== 'Delete' && event.key !== 'Backspace') return;
      const tag = (event.target as HTMLElement).tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA') return;
      // Also skip contenteditable elements (e.g. PromptEditor)
      if ((event.target as HTMLElement).isContentEditable) return;

      const m = modelRef.current;

      if (selectedEdgeId) {
        const [srcId, tgtId] = selectedEdgeId.split('->');
        // Deleting a __start__ edge removes the corresponding startTransition
        if (srcId === VIRTUAL_START) {
          setEdges((eds) => eds.filter((e) => e.id !== selectedEdgeId));
          skipSyncRef.current = true;
          onModelChangeRef.current({
            ...m,
            startTransitions: m.startTransitions.filter((t) => t.to !== tgtId),
            edgeLayout: Object.fromEntries(Object.entries(m.edgeLayout).filter(([id]) => id !== selectedEdgeId)),
          });
          setSelectedEdgeId(null);
          return;
        }
        const updatedNodes = m.nodes.map((n) => {
          if (n.id !== srcId) return n;
          return { ...n, transitions: n.transitions.filter((t) => t.to !== tgtId) };
        });
        setEdges((eds) => eds.filter((e) => e.id !== selectedEdgeId));
        skipSyncRef.current = true;
        onModelChangeRef.current({ ...m, nodes: updatedNodes, edgeLayout: Object.fromEntries(Object.entries(m.edgeLayout).filter(([id]) => id !== selectedEdgeId)) });
        setSelectedEdgeId(null);
        return;
      }

      if (!selectedNodeId) return;
      if (selectedNodeId === VIRTUAL_START || selectedNodeId === VIRTUAL_END) return;

      const updatedNodes = m.nodes.filter((n) => n.id !== selectedNodeId);
      const updatedNodesWithEdges = updatedNodes.map((n) => ({
        ...n,
        transitions: n.transitions.filter((t) => t.to !== selectedNodeId),
      }));
      const cleaned = deleteLayoutNode(m.layout, m.edgeLayout, selectedNodeId);
      const removedId = selectedNodeId;
      const newStartTransitions = m.startTransitions.filter((t) => t.to !== removedId);
      setNodes((nds) => nds.filter((n) => n.id !== removedId));
      setEdges((eds) => eds.filter((e) => e.source !== removedId && e.target !== removedId));
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, nodes: updatedNodesWithEdges, ...cleaned, startTransitions: newStartTransitions });
      setSelectedNodeId(null);
    },
    [createAt, selectedEdgeId, selectedNodeId, setEdges, setNodes, zoomIn, zoomOut],
  );

  // Add a new node — places it at the current viewport center
  const addNodeAtCenter = useCallback(() => {
    const container = document.querySelector('.graph-canvas-container');
    const rect = container?.getBoundingClientRect();
    const screenCx = rect ? rect.left + rect.width / 2 : window.innerWidth / 2;
    const screenCy = rect ? rect.top + rect.height / 2 : window.innerHeight / 2;
    const pos = screenToFlowPosition({ x: screenCx, y: screenCy });
    const newId = `step_${uuidv4().slice(0, 6)}`;
    const newNode: StepNode = {
      id: newId,
      label: t('selfEvolutionRun.canvasNewStepLabel'),
      mode: 'human',
      inputs: [],
      outputs: [],
      transitions: [],
    };
    const m = modelRef.current;
    const flowPos = { x: pos.x - NODE_WIDTH / 2, y: pos.y - NODE_HEIGHT / 2 };
    const newLayout = { ...m.layout, [newId]: flowPos };
    // Update ReactFlow nodes state directly so the node appears immediately
    setNodes((nds) => [
      ...nds,
      {
        id: newId,
        type: 'step',
        position: flowPos,
        data: { ...newNode, hasError: false, errorMessages: [], predecessorIds: [], outputLabels: {}, nodeWidth: NODE_WIDTH, visual: { ...flowPos }, onResizeEnd: stableResizeEnd, onResizeDrag: stableResizeDrag, getZoom: stableGetZoom },
        width: NODE_WIDTH,
      },
    ]);
    skipSyncRef.current = true;
    onModelChangeRef.current({ ...m, nodes: [...m.nodes, newNode], layout: newLayout });
  }, [screenToFlowPosition, setNodes, stableResizeEnd, stableResizeDrag, stableGetZoom]);

  const focusNode = useCallback((nodeId: string): boolean => {
    const node = allNodesFromStore.find((candidate) => candidate.id === nodeId);
    if (!node) return false;
    const width = node.width ?? (node.data as { nodeWidth?: number }).nodeWidth ?? NODE_WIDTH;
    const height = node.height ?? NODE_HEIGHT;
    setSelectedNodeId(nodeId);
    setSelectedEdgeId(null);
    void setCenter(
      node.position.x + width / 2,
      node.position.y + height / 2,
      { zoom: Math.max(getZoom(), 1), duration: 350 },
    );
    return true;
  }, [allNodesFromStore, getZoom, setCenter]);

  useImperativeHandle(ref, () => ({ addNode: addNodeAtCenter, focusNode }), [addNodeAtCenter, focusNode]);

  const addNodeAtPosition = useCallback(
    (pos: {x:number;y:number}) => {
      const newId = `step_${uuidv4().slice(0, 6)}`;
      const newNode: StepNode = {
        id: newId,
        label: t('selfEvolutionRun.canvasNewStepLabel'),
        mode: 'human',
        inputs: [],
        outputs: [],
        transitions: [],
      };
      const m = modelRef.current;
      const flowPos = { x: pos.x - NODE_WIDTH / 2, y: pos.y - NODE_HEIGHT / 2 };
      const newLayout = { ...m.layout, [newId]: flowPos };
      setNodes((nds) => [
        ...nds,
        {
          id: newId,
          type: 'step',
          position: flowPos,
          data: { ...newNode, hasError: false, errorMessages: [], predecessorIds: [], outputLabels: {}, nodeWidth: NODE_WIDTH, visual: { ...flowPos }, onResizeEnd: stableResizeEnd, onResizeDrag: stableResizeDrag, getZoom: stableGetZoom },
          width: NODE_WIDTH,
        },
      ]);
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, nodes: [...m.nodes, newNode], layout: newLayout });
    },
    [setNodes, stableResizeEnd, stableResizeDrag, stableGetZoom],
  );

  const openCreateMenu=useCallback((event:React.MouseEvent)=>{
    if(readonly||!event.ctrlKey)return false;
    event.preventDefault();
    const flow=screenToFlowPosition({x:event.clientX,y:event.clientY});
    const rect=containerRef.current?.getBoundingClientRect();
    const rawLeft=event.clientX-(rect?.left??0);
    const rawTop=event.clientY-(rect?.top??0);
    setCreateAt({flowX:flow.x,flowY:flow.y,left:Math.max(8,Math.min(rawLeft,(rect?.width??rawLeft+188)-188)),top:Math.max(8,Math.min(rawTop,(rect?.height??rawTop+104)-104))});
    return true;
  },[readonly,screenToFlowPosition]);

  const handlePaneClick=useCallback((event:React.MouseEvent)=>{
    if(openCreateMenu(event))return;
    setCreateAt(null);
    setSelectedNodeId(null);setSelectedNodeIds(new Set());setSelectedEdgeId(null);
  },[openCreateMenu]);

  const handlePaneContextMenu=useCallback((event:React.MouseEvent)=>{
    if(event.ctrlKey)openCreateMenu(event);
  },[openCreateMenu]);

  // Derive selectedNode from ReactFlow's nodes state (not model prop) so the
  // panel stays open immediately after an id-rename, before the parent re-renders.
  // Guard: only open the panel for step nodes — terminal nodes (__start__/__end__)
  // have no StepNode data and must not be passed to NodePropertiesPanel.
  const isStepNodeSelected = selectedNodeId !== null
    && selectedNodeId !== VIRTUAL_START
    && selectedNodeId !== VIRTUAL_END;
  const selectedNode = isStepNodeSelected
    ? (nodes.find((n) => n.id === selectedNodeId)?.data as unknown as StepNodeData | undefined) ?? null
    : null;
  // NodePropertiesPanel expects a StepNode; build it from the canvas node data.
  // For __start__, synthesize a virtual StepNode from model.startTransitions.
  const selectedStepNode: StepNode | null = (() => {
    if (!selectedNode || typeof selectedNode.id !== 'string') return null;
    if (selectedNode.id === VIRTUAL_START) {
      return {
        id: VIRTUAL_START,
        label: '__start__',
        mode: 'auto',
        inputs: [],
        outputs: [],
        transitions: model.startTransitions,
        route: model.startRoute,
      } as StepNode;
    }
    return model.nodes.find((n) => n.id === selectedNode.id) ?? null;
  })();

  const updateNodeVisual = useCallback((nodeId: string, visual: NodeLayout) => {
    const m=modelRef.current;
    setNodes(items=>items.map(item=>item.id===nodeId?{...item,position:{x:visual.x,y:visual.y},width:visual.width??item.width,data:{...item.data,visual,nodeWidth:visual.width??(item.data as {nodeWidth?:number}).nodeWidth,nodeHeight:visual.height}}:item));
    skipSyncRef.current=true;
    onModelChangeRef.current({...m,layout:{...m.layout,[nodeId]:visual}});
  },[setNodes]);
  const updateEdgeVisual = useCallback((id: string, visual: GraphModel['edgeLayout'][string]) => {
    const m=modelRef.current;
    setEdges(items=>items.map(item=>item.id===id?{...item,selected:true,data:{...item.data,visual}}:item));
    skipSyncRef.current=true;
    onModelChangeRef.current({...m,edgeLayout:{...m.edgeLayout,[id]:visual}});
  },[setEdges]);
  const batchIds=[...selectedNodeIds];
  const updateBatchVisual=(visual:NodeLayout)=>{
    const m=modelRef.current; const layout={...m.layout};
    for(const id of batchIds){const flowNode=nodes.find(node=>node.id===id);const old=layout[id]??flowNode?.position??{x:0,y:0};layout[id]={...old,width:visual.width,height:visual.height,visible:visual.visible,fill:visual.fill,border:visual.border};}
    onModelChangeRef.current({...m,layout});
  };
  const alignSelected=(kind:'left'|'hcenter'|'right'|'top'|'vcenter'|'bottom'|'hspace'|'vspace')=>{
    const chosen=nodes.filter(n=>selectedNodeIds.has(n.id)); if(chosen.length<2)return;
    const layout={...modelRef.current.layout};
    const box=(n:Node)=>({x:n.position.x,y:n.position.y,w:n.width??NODE_WIDTH,h:n.height??NODE_HEIGHT});
    const boxes=chosen.map(box); const left=Math.min(...boxes.map(b=>b.x)); const right=Math.max(...boxes.map(b=>b.x+b.w)); const top=Math.min(...boxes.map(b=>b.y)); const bottom=Math.max(...boxes.map(b=>b.y+b.h));
    if(kind==='hspace'||kind==='vspace'){
      const sorted=[...chosen].sort((a,b)=>kind==='hspace'?a.position.x-b.position.x:a.position.y-b.position.y); const total=sorted.reduce((s,n)=>s+(kind==='hspace'?(n.width??NODE_WIDTH):(n.height??NODE_HEIGHT)),0); const span=kind==='hspace'?right-left:bottom-top; const gap=(span-total)/(sorted.length-1); let cursor=kind==='hspace'?left:top;
      sorted.forEach(n=>{const old=layout[n.id]??n.position;layout[n.id]={...old,[kind==='hspace'?'x':'y']:cursor};cursor+=(kind==='hspace'?(n.width??NODE_WIDTH):(n.height??NODE_HEIGHT))+gap;});
    }else chosen.forEach(n=>{const b=box(n),old=layout[n.id]??n.position;let x=b.x,y=b.y;if(kind==='left')x=left;if(kind==='hcenter')x=(left+right-b.w)/2;if(kind==='right')x=right-b.w;if(kind==='top')y=top;if(kind==='vcenter')y=(top+bottom-b.h)/2;if(kind==='bottom')y=bottom-b.h;layout[n.id]={...old,x,y};});
    onModelChangeRef.current({...modelRef.current,layout});
  };

  const handleNodePropertyChange = useCallback((updated: StepNode): boolean => {
    const m = modelRef.current;

    // Handle __start__ virtual node: update startTransitions and startRoute only.
    if (updated.id === VIRTUAL_START) {
      const newModel = { ...m, startTransitions: updated.transitions, startRoute: updated.route };
      onModelChangeRef.current(newModel);
      return true;
    }

    const effectiveId = updated.id || newHiddenId();
    const normalised = updated.id ? updated : { ...updated, id: effectiveId };
    const currentSelectedNodeId = selectedNodeId;

    if (normalised.id !== currentSelectedNodeId && m.nodes.some((n) => n.id === normalised.id)) {
      return false;
    }

    const updatedNodes = m.nodes.map((n) => (n.id === currentSelectedNodeId ? normalised : n));

    if (normalised.id !== currentSelectedNodeId) {
      // Id changed: remap transitions, startTransitions and layout, then let model sync handle RF state.
      const remaId = normalised.id;
      const remappedNodes = updatedNodes.map((n) => ({
        ...n,
        transitions: n.transitions.map((t) =>
          t.to === currentSelectedNodeId ? { ...t, to: remaId } : t,
        ),
      }));
      const remappedStartTransitions = m.startTransitions.map((t) =>
        t.to === currentSelectedNodeId ? { ...t, to: remaId } : t,
      );
      const remappedLayout = renameLayoutNode(m.layout, m.edgeLayout, currentSelectedNodeId!, remaId);
      onModelChangeRef.current({ ...m, nodes: remappedNodes, startTransitions: remappedStartTransitions, ...remappedLayout });
      if (scenarioData && onScenarioChange && currentSelectedNodeId) {
        const stepDescriptions = { ...scenarioData.stepDescriptions };
        stepDescriptions[remaId] = stepDescriptions[currentSelectedNodeId] ?? '';
        delete stepDescriptions[currentSelectedNodeId];
        onScenarioChange({ ...scenarioData, stepDescriptions });
      }
      setSelectedNodeId(remaId);
    } else {
      // Same id, only data changed. Let the model-sync effect update ReactFlow.
      // Writing the parent model and the controlled ReactFlow nodes/edges from
      // the same input event creates two competing sources of truth. React 18
      // also batches queueMicrotask updates, so deferring the second write does
      // not isolate it and can make ReactFlow's controlled-store sync recurse
      // until React raises error #185.
      const newModel = { ...m, nodes: updatedNodes };
      onModelChangeRef.current(newModel);
    }
    return true;
  }, [selectedNodeId, scenarioData, onScenarioChange]);

  const handleNodeDelete = (nodeId: string) => {
    const m = modelRef.current;
    const updatedNodes = m.nodes.filter((n) => n.id !== nodeId).map((n) => ({
      ...n,
      transitions: n.transitions.filter((t) => t.to !== nodeId),
    }));
    const cleaned = deleteLayoutNode(m.layout, m.edgeLayout, nodeId);
    const newStartTransitions = m.startTransitions.filter((t) => t.to !== nodeId);
    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    skipSyncRef.current = true;
    onModelChangeRef.current({ ...m, nodes: updatedNodes, ...cleaned, startTransitions: newStartTransitions });
    setSelectedNodeId(null);
  };

  const containerRef = useRef<HTMLDivElement>(null);

  // Prevent macOS back/forward navigation gesture triggered by horizontal wheel.
  // Attach on `document` in the capture phase so it is stable across ReactFlow
  // internal re-renders, and check that the event target is inside our canvas.
  // `preventDefault()` does NOT block ReactFlow's pan — RF moves its viewport
  // directly in JS and does not rely on native scroll, so preventing the
  // browser default only stops the navigation gesture, not the canvas pan.
  useEffect(() => {
    const root=document.documentElement;const body=document.body;
    const previousRoot=root.style.overscrollBehaviorX;const previousBody=body.style.overscrollBehaviorX;
    root.style.overscrollBehaviorX='none';body.style.overscrollBehaviorX='none';
    let gestureUntil=0;
    const handler = (e: WheelEvent) => {
      const el = containerRef.current;
      if(el?.contains(e.target as Element))gestureUntil=Date.now()+300;
      if(Math.abs(e.deltaX)>0&&Date.now()<=gestureUntil)e.preventDefault();
    };
    window.addEventListener('wheel', handler, { passive: false, capture: true });
    return () => {window.removeEventListener('wheel', handler, { capture: true });root.style.overscrollBehaviorX=previousRoot;body.style.overscrollBehaviorX=previousBody;};
  }, []);

  return (
    <div
      ref={containerRef}
      className="graph-canvas-container"
      onKeyDown={onKeyDown}
      tabIndex={0}
      role="application"
      aria-label={t('selfEvolutionRun.canvasAriaLabel')}
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={handleNodesChange}
        onEdgesChange={handleEdgesChange}
        onEdgeClick={handleEdgeClick}
        onNodeClick={handleNodeClick}
        onSelectionChange={handleSelectionChange}
        onConnect={onConnect}
        onReconnect={onReconnect}
        reconnectRadius={8}
        onNodeDrag={onNodeDrag}
        onNodeDragStop={onNodeDragStop}
        onPaneClick={handlePaneClick}
        onPaneContextMenu={handlePaneContextMenu}
        selectNodesOnDrag={false}
        selectionOnDrag
        selectionMode={SelectionMode.Partial}
        selectionKeyCode={null}
        multiSelectionKeyCode={['Meta','Control','Shift']}
        elevateEdgesOnSelect
        fitView
        attributionPosition="bottom-right"
        // Interaction: two-finger swipe to pan, pinch to zoom, no left-drag pan
        panOnScroll
        panOnScrollMode={PanOnScrollMode.Free}
        panOnDrag={false}
        zoomOnScroll={false}
        zoomOnPinch
        zoomOnDoubleClick={false}
      >
        <Background />
        <Controls />
        <MiniMap />
      </ReactFlow>
      <AlignmentGuides guides={guides} />
      {createAt&&<div className="canvas-create-menu" style={{left:createAt.left,top:createAt.top}} role="menu" onPointerDown={(event)=>event.stopPropagation()} onClick={(event)=>event.stopPropagation()}>
        <button type="button" role="menuitem" onClick={()=>{addNodeAtPosition({x:createAt.flowX,y:createAt.flowY});setCreateAt(null);}}><span className="canvas-create-menu-icon">＋</span><span><strong>新建步骤</strong><small>在此处添加流程步骤</small></span></button>
        <button type="button" role="menuitem" onClick={()=>{setCreateAt(null);onCreateArtifact?.();}}><span className="canvas-create-menu-icon">◇</span><span><strong>新建素材</strong><small>打开素材面板并创建</small></span></button>
      </div>}

      {selectedStepNode && selectedNodeIds.size <= 1 && (
        <NodePropertiesPanel
          node={selectedStepNode}
          model={model}
          pluginModel={pluginModel}
          scenarioData={scenarioData}
          onScenarioChange={onScenarioChange}
          onClose={() => setSelectedNodeId(null)}
          onChange={handleNodePropertyChange}
          onDelete={handleNodeDelete}
          disableAddTransition={false}
          readonly={readonly}
          visualContent={<NodeVisualPanel
            value={model.layout[selectedStepNode.id] ?? {x:0,y:0}}
            readonly={readonly}
            onChange={(visual) => updateNodeVisual(selectedStepNode.id, visual)}
            onReset={() => { const current=model.layout[selectedStepNode.id]; if (current) updateNodeVisual(selectedStepNode.id,{x:current.x,y:current.y,width:current.width,height:current.height}); }}
            onCopy={() => { const {visible,fill,border}=model.layout[selectedStepNode.id]??{x:0,y:0}; visualClipboardRef.current={type:'node',value:{visible,fill,border}}; }}
            onPaste={visualClipboardRef.current?.type==='node' ? () => updateNodeVisual(selectedStepNode.id,{...(model.layout[selectedStepNode.id]??{x:0,y:0}),...visualClipboardRef.current!.value}) : undefined}
          />}
        />
      )}
      {selectedNodeIds.size>1&&<div className="node-props-panel"><div className="node-props-panel-header"><span className="node-props-panel-title">批量视觉效果（{selectedNodeIds.size}）</span><button className="visual-close" onClick={()=>{setSelectedNodeIds(new Set());setSelectedNodeId(null);}}>×</button></div><div className="batch-align">{[['left','左对齐'],['hcenter','水平居中'],['right','右对齐'],['top','顶对齐'],['vcenter','垂直居中'],['bottom','底对齐'],['hspace','水平等距'],['vspace','垂直等距']].map(([id,label])=><button key={id} onClick={()=>alignSelected(id as Parameters<typeof alignSelected>[0])}>{label}</button>)}</div><NodeVisualPanel batch value={model.layout[batchIds[0]]??{x:0,y:0}} readonly={readonly} onChange={updateBatchVisual} onReset={()=>{const m=modelRef.current,layout={...m.layout};batchIds.forEach(id=>{const v=layout[id];if(v)layout[id]={x:v.x,y:v.y,width:v.width,height:v.height};});onModelChangeRef.current({...m,layout});}} /></div>}
      {selectedNodeId && (selectedNodeId===VIRTUAL_START||selectedNodeId===VIRTUAL_END) && <div className="node-props-panel" role="complementary"><div className="node-props-panel-header"><span className="node-props-panel-title">{selectedNodeId===VIRTUAL_START?'开始':'结束'}节点视觉效果</span><button className="visual-close" onClick={()=>setSelectedNodeId(null)}>×</button></div><NodeVisualPanel terminal value={model.layout[selectedNodeId]??{x:0,y:0}} readonly={readonly} onChange={(visual)=>updateNodeVisual(selectedNodeId,visual)} onReset={()=>{const current=model.layout[selectedNodeId];if(current)updateNodeVisual(selectedNodeId,{x:current.x,y:current.y,width:current.width,height:current.height});}} /></div>}
      {selectedEdgeId && !selectedStepNode && <div className="node-props-panel" role="complementary">
        <div className="node-props-panel-header"><span className="node-props-panel-title">连线视觉效果</span><button className="visual-close" onClick={()=>setSelectedEdgeId(null)}>×</button></div>
        <EdgeVisualPanel value={model.edgeLayout[selectedEdgeId]??{}} readonly={readonly} onChange={(visual)=>updateEdgeVisual(selectedEdgeId,visual)} onReset={()=>{ const next={...model.edgeLayout}; delete next[selectedEdgeId]; setEdges(items=>items.map(item=>item.id===selectedEdgeId?{...item,selected:true,data:{...item.data,visual:{}}}:item)); skipSyncRef.current=true; onModelChangeRef.current({...model,edgeLayout:next}); }} onCopy={()=>{visualClipboardRef.current={type:'edge',value:model.edgeLayout[selectedEdgeId]??{}};}} onPaste={visualClipboardRef.current?.type==='edge'?()=>{const copied=visualClipboardRef.current;if(copied?.type==='edge')updateEdgeVisual(selectedEdgeId,copied.value);}:undefined} />
      </div>}
    </div>
  );
}

const CanvasWithRef = forwardRef<CanvasHandle, Props>(CanvasInner);

export default function Canvas(props: Props & { canvasRef?: React.Ref<CanvasHandle> }) {
  const { canvasRef, ...rest } = props;
  return (
    <ReactFlowProvider>
      <CanvasWithRef {...rest} ref={canvasRef} />
    </ReactFlowProvider>
  );
}
