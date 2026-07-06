import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
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
import { VIRTUAL_END, VIRTUAL_START } from '../core/model';
import type { ValidationError } from '../core/validator';
import { StepNodeRenderer, TerminalNode, buildNodeErrorMap } from './StepNode';
import { TransitionEdge } from './TransitionEdge';
import NodePropertiesPanel from './NodePropertiesPanel';
import { useAlignmentGuides } from './useAlignmentGuides';
import { AlignmentGuides } from './AlignmentGuides';
import './Canvas.scss';

const NODE_WIDTH = 148;
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
}

const nodeTypes: NodeTypes = {
  step: StepNodeRenderer as NodeTypes['step'],
  terminal: TerminalNode as NodeTypes['terminal'],
};

const edgeTypes: EdgeTypes = {
  transition: TransitionEdge as EdgeTypes['transition'],
};

function modelToFlowNodes(model: GraphModel, nodeErrorMap: Map<string, string[]>): Node[] {
  const flowNodes: Node[] = [];
  let autoX = 80;

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
    flowNodes.push({
      id: node.id,
      type: 'step',
      position: pos,
      data: {
        ...node,
        hasError: errMsgs.length > 0,
        errorMessages: errMsgs,
      },
      selected: false,
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
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
      markerEnd: { type: MarkerType.ArrowClosed },
      data: { condition: t.condition, hasError: false, onConditionChange },
    });
  }

  for (const node of model.nodes) {
    const isMultiExit = node.transitions.length > 1;
    for (const t of node.transitions) {
      const edgeKey = `${node.id}->${t.to}`;
      const hasError = edgeErrorSet.has(edgeKey) || (isMultiExit && !t.condition.trim());
      edges.push({
        id: edgeKey,
        source: node.id,
        target: t.to,
        type: 'transition',
        markerEnd: { type: MarkerType.ArrowClosed },
        data: {
          condition: t.condition,
          hasError,
          onConditionChange,
        },
      });
    }
  }

  return edges;
}

function CanvasInner({ model, errors, onModelChange }: Props, ref: React.Ref<CanvasHandle>) {
  const { screenToFlowPosition } = useReactFlow();
  const nodeErrorMap = useMemo(() => buildNodeErrorMap(errors), [errors]);
  const { guides, onNodeDrag: computeGuides, onNodeDragStop: clearGuides } = useAlignmentGuides();

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
  useEffect(() => { modelRef.current = model; }, [model]);
  useEffect(() => { onModelChangeRef.current = onModelChange; }, [onModelChange]);

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
    () => modelToFlowNodes(model, nodeErrorMap),
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

  // Intercept ReactFlow's own selection changes to keep our state in sync.
  // This is more reliable than onNodeClick/onPaneClick which have drag-protection delays.
  const handleNodesChange = useCallback(
    (changes: NodeChange[]) => {
      onNodesChange(changes);
      for (const change of changes) {
        if (change.type === 'select') {
          if (change.selected) {
            setSelectedNodeId(change.id);
            setSelectedEdgeId(null);
          } else if (change.id === selectedNodeId) {
            setSelectedNodeId(null);
          }
        }
      }
    },
    [onNodesChange, selectedNodeId],
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
    setNodes(modelToFlowNodes(model, nodeErrorMap));
    setEdges(modelToFlowEdges(model, nodeErrorMap, handleConditionChange));
  // handleConditionChange is stable (no deps), nodeErrorMap changes only when
  // errors change — both are safe in this dep array.
  }, [model, nodeErrorMap, handleConditionChange, setNodes, setEdges]);

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

      const newTransition = { to: connection.target, condition: '' };
      const updatedNodes = m.nodes.map((n) =>
        n.id === connection.source
          ? { ...n, transitions: [...n.transitions, newTransition] }
          : n,
      );
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, nodes: updatedNodes });
      setEdges((eds) => addEdge({ ...connection, type: 'transition' }, eds));
    },
    [setEdges, handleConditionChange],
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
      const newLayout = { ...m.layout, [node.id]: pos };
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

  // Handle node/edge deletion via Delete or Backspace key
  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key !== 'Delete' && event.key !== 'Backspace') return;
      const tag = (event.target as HTMLElement).tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA') return;

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
    [selectedEdgeId, selectedNodeId, setEdges, setNodes],
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
      label: '新步骤',
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
        data: { ...newNode, hasError: false, errorMessages: [] },
        width: NODE_WIDTH,
        height: NODE_HEIGHT,
      },
    ]);
    skipSyncRef.current = true;
    onModelChangeRef.current({ ...m, nodes: [...m.nodes, newNode], layout: newLayout });
  }, [screenToFlowPosition, setNodes]);

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
        label: '新步骤',
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
          data: { ...newNode, hasError: false, errorMessages: [] },
          width: NODE_WIDTH,
          height: NODE_HEIGHT,
        },
      ]);
      skipSyncRef.current = true;
      onModelChangeRef.current({ ...m, nodes: [...m.nodes, newNode], layout: newLayout });
    },
    [screenToFlowPosition, setNodes],
  );

  const selectedNode = selectedNodeId
    ? model.nodes.find((n) => n.id === selectedNodeId)
    : null;

  const handleNodePropertyChange = (updated: StepNode) => {
    const m = modelRef.current;
    const updatedNodes = m.nodes.map((n) => (n.id === selectedNodeId ? updated : n));
    if (updated.id !== selectedNodeId) {
      const remaId = updated.id;
      const remappedNodes = updatedNodes.map((n) => ({
        ...n,
        transitions: n.transitions.map((t) =>
          t.to === selectedNodeId ? { ...t, to: remaId } : t,
        ),
      }));
      const newLayout = { ...m.layout };
      if (selectedNodeId && newLayout[selectedNodeId]) {
        newLayout[remaId] = newLayout[selectedNodeId];
        delete newLayout[selectedNodeId];
      }
      // Node id changed: let useEffect re-sync so ReactFlow picks up the new id
      onModelChangeRef.current({ ...m, nodes: remappedNodes, layout: newLayout });
      setSelectedNodeId(remaId);
    } else {
      // Only data changed — update ReactFlow state in-place to avoid full re-sync flicker
      const newModel = { ...m, nodes: updatedNodes };
      skipSyncRef.current = true;
      onModelChangeRef.current(newModel);
      const errMsgs = nodeErrorMap.get(selectedNodeId!) ?? [];
      setNodes((nds) =>
        nds.map((n) => {
          if (n.id !== selectedNodeId) return n;
          return {
            ...n,
            data: {
              ...updated,
              hasError: errMsgs.length > 0,
              errorMessages: errMsgs,
            },
          };
        }),
      );
      setEdges(modelToFlowEdges(newModel, nodeErrorMap, handleConditionChange));
    }
  };

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

  return (
    <div
      className="graph-canvas-container"
      onKeyDown={onKeyDown}
      onDoubleClick={onDoubleClick}
      tabIndex={0}
      role="application"
      aria-label="状态机画布，双击空白处新建节点"
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={handleNodesChange}
        onEdgesChange={handleEdgesChange}
        onConnect={onConnect}
        onNodeDrag={onNodeDrag}
        onNodeDragStop={onNodeDragStop}
        onPaneClick={() => { setSelectedNodeId(null); setSelectedEdgeId(null); }}
        selectNodesOnDrag={false}
        elevateEdgesOnSelect
        fitView
        attributionPosition="bottom-right"
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

      {selectedNode && (
        <NodePropertiesPanel
          node={selectedNode}
          model={model}
          onClose={() => setSelectedNodeId(null)}
          onChange={handleNodePropertyChange}
          onDelete={handleNodeDelete}
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
