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
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { v4 as uuidv4 } from 'uuid';
import type { GraphModel, StepNode, NodeLayout } from '../core/model';
import { VIRTUAL_END, VIRTUAL_START, newHiddenId } from '../core/model';
import type { ValidationError } from '../core/validator';
import type { PluginModel } from '../core/pluginModel';
import type { ScenarioData } from '../ScenarioEditor';
import { StepNodeRenderer, TerminalNode, buildNodeErrorMap, NODE_DEFAULT_WIDTH, NODE_MIN_WIDTH, type StepNodeData } from './StepNode';
import { TransitionEdge } from './TransitionEdge';
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

/**
 * V11 guard: returns a rejection message if adding the edge src→tgt would
 * violate the "parallel sub-steps must not have multiple exits" rule, or null
 * if the connection is allowed.
 *
 * Rule: a node that is a direct child of a parallel fork (route:all, >1 exits)
 * must not itself have more than one outgoing transition.
 */
function v11RejectReason(model: GraphModel, srcId: string, tgtId: string): string | null {
  // After the new edge is added, src will have (current + 1) outgoing transitions.
  const srcNode = model.nodes.find((n) => n.id === srcId);
  if (!srcNode) return null;

  const srcExitsAfter = srcNode.transitions.length + 1; // after adding the new edge

  // Check 1: src's parent is a parallel fork → src must stay at ≤1 exit.
  if (srcExitsAfter > 1) {
    for (const n of model.nodes) {
      const isParallelFork = (n.route === 'all' || !n.route) && n.transitions.length > 1;
      if (isParallelFork && n.transitions.some((t) => t.to === srcId)) {
        return `step "${srcId}" is a parallel fork child and cannot have multiple exits`;
      }
    }
  }

  // Check 2: src is itself a parallel fork → tgt must not already have >0 exits.
  const srcIsParallelForkAfter = (srcNode.route === 'all' || !srcNode.route) && srcExitsAfter > 1;
  if (srcIsParallelForkAfter) {
    const tgtNode = model.nodes.find((n) => n.id === tgtId);
    if (tgtNode && tgtNode.transitions.length > 0) {
      return `step "${tgtId}" already has exits and cannot be a parallel fork child`;
    }
  }

  return null;
}

function modelToFlowNodes(
  model: GraphModel,
  nodeErrorMap: Map<string, string[]>,
  onResizeEnd: (nodeId: string, width: number) => void,
  onResizeDrag: (nodeId: string, width: number) => void,
  getZoom: () => number,
): Node[] {
  const flowNodes: Node[] = [];
  let autoX = 80;
  const predMap = buildPredecessorMap(model);

  // __start__ virtual node
  flowNodes.push({
    id: VIRTUAL_START,
    type: 'terminal',
    position: model.layout[VIRTUAL_START] ?? { x: autoX, y: 200 },
    data: { type: 'start', label: 'START' },
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
      const slotId = ref.slot;
      const slot = model.slots[slotId];
      outputLabels[slotId] = slot?.label ?? slotId;
    }
    flowNodes.push({
      id: node.id,
      type: 'step',
      position: pos,
      data: {
        ...node,
        // StepNodeData.inputs/outputs are string[] (slot ids for display); StepNode uses StepInputRef[].
        inputs: node.inputs.map((r) => r.slot),
        outputs: node.outputs.map((r) => r.slot),
        hasError: errMsgs.length > 0,
        errorMessages: errMsgs,
        predecessorIds: predMap.get(node.id) ?? [],
        outputLabels,
        nodeWidth,
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
  flowNodes.push({
    id: VIRTUAL_END,
    type: 'terminal',
    position: model.layout[VIRTUAL_END] ?? { x: autoX, y: 200 },
    data: { type: 'end', label: 'END' },
    draggable: true,
  });

  return flowNodes;
}

function modelToFlowEdges(model: GraphModel, nodeErrorMap: Map<string, string[]>, onConditionChange: (src: string, tgt: string, cond: string) => void): Edge[] {
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
      data: { condition: t.condition, hasError: false, onConditionChange },
    });
  }

  for (const node of model.nodes) {
    const isMultiExit = node.transitions.length > 1;
    const isParallel = (node.route === 'all' || !node.route) && isMultiExit;
    for (const t of node.transitions) {
      const edgeKey = `${node.id}->${t.to}`;
      const hasError = edgeErrorSet.has(edgeKey) || (node.route === 'choice' && !t.condition.trim());
      edges.push({
        id: edgeKey,
        source: node.id,
        target: t.to,
        type: 'transition',
        reconnectable: 'target' as const,
        markerEnd: { type: MarkerType.ArrowClosed },
        data: {
          condition: t.condition,
          hasError,
          isParallel,
          onConditionChange,
        },
      });
    }
  }

  return edges;
}

function CanvasInner({ model, errors, onModelChange, pluginModel, scenarioData, onScenarioChange, readonly = false }: Props, ref: React.Ref<CanvasHandle>) {
  const { t } = useTranslation();
  const { screenToFlowPosition, zoomIn, zoomOut, getZoom } = useReactFlow();
  const nodeErrorMap = useMemo(() => buildNodeErrorMap(errors), [errors]);
  const { guides, onNodeDrag: computeGuides, onNodeDragStop: clearGuides } = useAlignmentGuides();

  // Stable resize callback used both by initialNodes (useMemo) and runtime.
  // Defined as a ref so it's available before setNodes is initialized.
  const handleNodeResizeEndRef = useRef<(nodeId: string, width: number) => void>(() => {});
  const stableResizeEnd = useCallback((nodeId: string, width: number) => {
    handleNodeResizeEndRef.current(nodeId, width);
  }, []);

  // Live resize drag: updates ReactFlow node width in real time without persisting to model.
  const handleNodeResizeDragRef = useRef<(nodeId: string, width: number) => void>(() => {});
  const stableResizeDrag = useCallback((nodeId: string, width: number) => {
    handleNodeResizeDragRef.current(nodeId, width);
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
  useEffect(() => { nodeErrorMapRef.current = nodeErrorMap; }, [nodeErrorMap]);

  // Stable callback — never changes reference, reads from refs.
  const handleConditionChange = useCallback(
    (sourceId: string, targetId: string, condition: string) => {
      const m = modelRef.current;
      if (sourceId === VIRTUAL_START) {
        const updatedTransitions = m.startTransitions.map((t) =>
          t.to === targetId ? { ...t, condition } : t,
        );
        onModelChangeRef.current({ ...m, startTransitions: updatedTransitions });
        return;
      }
      const updatedNodes = m.nodes.map((n) => {
        if (n.id !== sourceId) return n;
        return {
          ...n,
          transitions: n.transitions.map((t) =>
            t.to === targetId ? { ...t, condition } : t,
          ),
        };
      });
      onModelChangeRef.current({ ...m, nodes: updatedNodes });
    },
    [], // stable — intentionally no deps
  );

  const initialNodes = useMemo(
    () => modelToFlowNodes(model, nodeErrorMap, stableResizeEnd, stableResizeDrag, stableGetZoom),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  const initialEdges = useMemo(
    () => modelToFlowEdges(model, nodeErrorMap, handleConditionChange),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState(initialNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initialEdges);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null);

  // When a canvas-internal operation calls onModelChange, we set this flag so
  // the sync useEffect below knows NOT to re-derive nodes/edges from the model
  // (the canvas already has the correct visual state from the operation itself).
  const skipSyncRef = useRef(false);

  // Track node selection purely via click events — one clear entry point,
  // no stale-closure or race-condition issues.
  const handleNodeClick = useCallback(
    (_event: React.MouseEvent, node: Node) => {
      if (node.id === VIRTUAL_END) return;
      setSelectedNodeId(node.id);
      setSelectedEdgeId(null);
    },
    [],
  );

  const handleNodesChange = useCallback(
    (changes: NodeChange[]) => {
      onNodesChange(changes);
    },
    [onNodesChange],
  );

  const handleEdgesChange = useCallback(
    (changes: EdgeChange[]) => {
      onEdgesChange(changes);
      for (const change of changes) {
        if (change.type === 'select') {
          if (change.selected) {
            setSelectedEdgeId(change.id);
            setSelectedNodeId(null);
          } else if (change.id === selectedEdgeId) {
            setSelectedEdgeId(null);
          }
        }
      }
    },
    [onEdgesChange, selectedEdgeId],
  );

  // Keep nodes/edges in sync when model changes from OUTSIDE the canvas
  // (e.g. YAML editor, undo). Internal canvas operations use skipSyncRef to
  // opt out, since they already have the correct ReactFlow visual state.
  useEffect(() => {
    if (skipSyncRef.current) {
      skipSyncRef.current = false;
      return;
    }
    const newNodes = modelToFlowNodes(model, nodeErrorMap, stableResizeEnd, stableResizeDrag, stableGetZoom);
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
        const preservedHeight = current?.height; // undefined → ReactFlow measures from DOM
        return {
          ...n,
          width: preservedWidth,
          ...(preservedHeight != null ? { height: preservedHeight } : {}),
          data: { ...n.data, nodeWidth: preservedNodeWidth },
          selected: selectedSet.has(n.id) ? true : n.selected,
        };
      });
    });
    setEdges(modelToFlowEdges(model, nodeErrorMap, handleConditionChange));
  }, [model, nodeErrorMap, handleConditionChange, setNodes, setEdges]);

  // Propagate error state changes to ReactFlow nodes independently of the main
  // model sync. This runs even when skipSyncRef suppresses the full sync above,
  // so error highlights update immediately after the parent re-validates.
  useEffect(() => {
    // nodeErrorMapRef is already kept up to date by the effect above; use it to
    // detect whether the map actually changed before running the update.
    const prevMap = nodeErrorMapRef.current;
    if (prevMap === nodeErrorMap) return;
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
  }, [nodeErrorMap, setNodes]);

  // When a new edge is drawn in the canvas, add a transition to the model
  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) return;

      const m = modelRef.current;

      if (connection.source === VIRTUAL_START) {
        // Prevent duplicate edges to the same target
        const target = connection.target;
        if (m.startTransitions.some((t) => t.to === target)) return;
        const newTransition = { to: target, condition: '' };
        const edgeId = `${VIRTUAL_START}->${target}`;
        setEdges((eds) =>
          addEdge(
            {
              ...connection,
              id: edgeId,
              type: 'transition',
              markerEnd: { type: MarkerType.ArrowClosed },
              data: { condition: '', hasError: false, onConditionChange: handleConditionChange },
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

      // V11 guard: reject if this connection would violate the no-re-fork rule.
      const reject = v11RejectReason(m, connection.source, connection.target);
      if (reject) {
        // Silent rejection — the invalid edge simply doesn't get drawn.
        return;
      }

      const newTransition = { to: connection.target, condition: '' };
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
      setEdges((eds) => addEdge({ ...connection, type: 'transition', markerEnd: { type: MarkerType.ArrowClosed }, data: { condition: '', hasError: false, onConditionChange: handleConditionChange } }, eds));
    },
    [setEdges, handleConditionChange],
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

      if (oldSrc === VIRTUAL_START) {
        const newStartTransitions = m.startTransitions.map((t) =>
          t.to === oldTgt ? { ...t, to: newTgt } : t,
        );
        setEdges((eds) => eds.map((e) =>
          e.id === oldEdge.id ? { ...e, id: `${VIRTUAL_START}->${newTgt}`, target: newTgt } : e,
        ));
        skipSyncRef.current = true;
        onModelChangeRef.current({ ...m, startTransitions: newStartTransitions });
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
      onModelChangeRef.current({ ...m, nodes: newNodes });
    },
    [setEdges],
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
    (nodeId: string, width: number) => {
      const m = modelRef.current;
      const w = Math.max(NODE_MIN_WIDTH, width);
      // Read current node position from the snapshot synchronously before setNodes.
      // Do NOT call onModelChangeRef or mutate skipSyncRef inside the setNodes updater
      // (updaters must be pure functions; side effects there are unsafe in concurrent React).
      const rfNodes = allNodesFromStore;
      const rfNode = rfNodes.find((n) => n.id === nodeId);
      const pos = m.layout[nodeId] ?? rfNode?.position ?? { x: 0, y: 0 };
      const newLayout = { ...m.layout, [nodeId]: { ...pos, width: w } };
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, layout: newLayout });
      setNodes((nds) =>
        nds.map((n) =>
          n.id === nodeId
            ? { ...n, width: w, data: { ...n.data, nodeWidth: w } }
            : n,
        ),
      );
    },
    [setNodes, allNodesFromStore],
  );
  // Wire the stable ref to the real implementation now that setNodes is available.
  handleNodeResizeEndRef.current = handleNodeResizeEnd;

  // Live resize drag: update ReactFlow node width in real time (no model persist).
  const handleNodeResizeDrag = useCallback(
    (nodeId: string, width: number) => {
      const w = Math.max(NODE_MIN_WIDTH, width);
      setNodes((nds) =>
        nds.map((n) =>
          n.id === nodeId
            ? { ...n, width: w, data: { ...n.data, nodeWidth: w } }
            : n,
        ),
      );
    },
    [setNodes],
  );
  handleNodeResizeDragRef.current = handleNodeResizeDrag;

  // Handle node/edge deletion via Delete or Backspace key,
  // and Cmd+/- zoom within the canvas (prevents browser zoom).
  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
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
        onModelChangeRef.current({ ...m, nodes: updatedNodes });
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
      const newLayout = { ...m.layout };
      delete newLayout[selectedNodeId];
      const removedId = selectedNodeId;
      const newStartTransitions = m.startTransitions.filter((t) => t.to !== removedId);
      setNodes((nds) => nds.filter((n) => n.id !== removedId));
      setEdges((eds) => eds.filter((e) => e.source !== removedId && e.target !== removedId));
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, nodes: updatedNodesWithEdges, layout: newLayout, startTransitions: newStartTransitions });
      setSelectedNodeId(null);
    },
    [selectedEdgeId, selectedNodeId, setEdges, setNodes, zoomIn, zoomOut],
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
        data: { ...newNode, hasError: false, errorMessages: [], predecessorIds: [], outputLabels: {}, nodeWidth: NODE_WIDTH, onResizeEnd: stableResizeEnd, onResizeDrag: stableResizeDrag, getZoom: stableGetZoom },
        width: NODE_WIDTH,
      },
    ]);
    skipSyncRef.current = true;
    onModelChangeRef.current({ ...m, nodes: [...m.nodes, newNode], layout: newLayout });
  }, [screenToFlowPosition, setNodes, stableResizeEnd, stableResizeDrag, stableGetZoom]);

  useImperativeHandle(ref, () => ({ addNode: addNodeAtCenter }), [addNodeAtCenter]);

  // Add a new node on canvas double-click
  const onDoubleClick = useCallback(
    (event: React.MouseEvent) => {
      const target = event.target as HTMLElement;
      if (target.closest('.react-flow__node')) return;
      const pos = screenToFlowPosition({ x: event.clientX, y: event.clientY });

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
          data: { ...newNode, hasError: false, errorMessages: [], predecessorIds: [], outputLabels: {}, nodeWidth: NODE_WIDTH, onResizeEnd: stableResizeEnd, onResizeDrag: stableResizeDrag, getZoom: stableGetZoom },
          width: NODE_WIDTH,
        },
      ]);
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, nodes: [...m.nodes, newNode], layout: newLayout });
    },
    [screenToFlowPosition, setNodes, stableResizeEnd, stableResizeDrag, stableGetZoom],
  );

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

  // Whether the selected node is a direct child of a parallel fork.
  // If true, "添加分支" must be disabled to prevent V11 violations.
  const selectedIsParallelChild = selectedNodeId
    ? model.nodes.some(
        (n) =>
          (n.route === 'all' || !n.route) &&
          n.transitions.length > 1 &&
          n.transitions.some((t) => t.to === selectedNodeId),
      )
    : false;

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
      const newLayout = { ...m.layout };
      if (currentSelectedNodeId && newLayout[currentSelectedNodeId]) {
        newLayout[remaId] = newLayout[currentSelectedNodeId];
        delete newLayout[currentSelectedNodeId];
      }
      onModelChangeRef.current({ ...m, nodes: remappedNodes, startTransitions: remappedStartTransitions, layout: newLayout });
      setSelectedNodeId(remaId);
    } else {
      // Same id, only data changed. Set skipSyncRef so the model-sync useEffect
      // does not run a full RF rebuild (which would cause a position flicker).
      // Then defer the RF node/edge update to the next microtask so it runs
      // outside the current React render batch — this prevents Minified React
      // error #185 caused by two concurrent setState calls in the same batch.
      const newModel = { ...m, nodes: updatedNodes };
      skipSyncRef.current = true;
      onModelChangeRef.current(newModel);
      queueMicrotask(() => {
        const errMap = nodeErrorMapRef.current;
        setNodes((nds) =>
          nds.map((n) => {
            if (n.id !== currentSelectedNodeId) return n;
            const updatedOutputLabels: Record<string, string> = {};
            for (const ref of normalised.outputs) {
              const slot = newModel.slots[ref.slot];
              updatedOutputLabels[ref.slot] = slot?.label ?? ref.slot;
            }
            return {
              ...n,
              data: {
                ...n.data,
                ...normalised,
                inputs: normalised.inputs.map((r) => r.slot),
                outputs: normalised.outputs.map((r) => r.slot),
                outputLabels: updatedOutputLabels,
                hasError: (errMap.get(currentSelectedNodeId!) ?? []).length > 0,
                errorMessages: errMap.get(currentSelectedNodeId!) ?? [],
              },
            };
          }),
        );
        setEdges(modelToFlowEdges(newModel, errMap, handleConditionChange));
      });
    }
    return true;
  }, [selectedNodeId, setNodes, setEdges]);

  const handleNodeDelete = (nodeId: string) => {
    const m = modelRef.current;
    const updatedNodes = m.nodes.filter((n) => n.id !== nodeId).map((n) => ({
      ...n,
      transitions: n.transitions.filter((t) => t.to !== nodeId),
    }));
    const newLayout = { ...m.layout };
    delete newLayout[nodeId];
    const newStartTransitions = m.startTransitions.filter((t) => t.to !== nodeId);
    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    skipSyncRef.current = true;
    onModelChangeRef.current({ ...m, nodes: updatedNodes, layout: newLayout, startTransitions: newStartTransitions });
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
    const handler = (e: WheelEvent) => {
      if (Math.abs(e.deltaX) === 0) return;
      const el = containerRef.current;
      if (el && el.contains(e.target as Element)) {
        e.preventDefault();
      }
    };
    document.addEventListener('wheel', handler, { passive: false, capture: true });
    return () => document.removeEventListener('wheel', handler, { capture: true });
  }, []);

  return (
    <div
      ref={containerRef}
      className="graph-canvas-container"
      onKeyDown={onKeyDown}
      onDoubleClick={onDoubleClick}
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
        onNodeClick={handleNodeClick}
        onConnect={onConnect}
        onReconnect={onReconnect}
        reconnectRadius={8}
        onNodeDrag={onNodeDrag}
        onNodeDragStop={onNodeDragStop}
        onPaneClick={() => { setSelectedNodeId(null); setSelectedEdgeId(null); }}
        selectNodesOnDrag={false}
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
        <svg style={{ position: 'absolute', width: 0, height: 0 }}>
          <defs>
            <marker
              id="arrow"
              markerWidth="10"
              markerHeight="10"
              refX="9"
              refY="3"
              orient="auto"
            >
              <path d="M0,0 L0,6 L9,3 z" fill="#8c8c8c" />
            </marker>
          </defs>
        </svg>
      </ReactFlow>
      <AlignmentGuides guides={guides} />

      {selectedStepNode && (
        <NodePropertiesPanel
          node={selectedStepNode}
          model={model}
          pluginModel={pluginModel}
          scenarioData={scenarioData}
          onScenarioChange={onScenarioChange}
          onClose={() => setSelectedNodeId(null)}
          onChange={handleNodePropertyChange}
          onDelete={handleNodeDelete}
          disableAddTransition={selectedIsParallelChild}
          readonly={readonly}
        />
      )}
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
