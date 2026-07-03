import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  addEdge,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  type EdgeTypes,
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
import './Canvas.scss';

const NODE_WIDTH = 160;
const NODE_HEIGHT = 80;
const DEFAULT_SPACING_X = 200;
const DEFAULT_SPACING_Y = 100;

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

  for (const node of model.nodes) {
    for (const t of node.transitions) {
      const edgeKey = `${node.id}->${t.to}`;
      const hasError = edgeErrorSet.has(edgeKey) || !t.condition.trim();
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

export default function Canvas({ model, errors, onModelChange }: Props) {
  const nodeErrorMap = useMemo(() => buildNodeErrorMap(errors), [errors]);

  const handleConditionChange = useCallback(
    (sourceId: string, targetId: string, condition: string) => {
      const updatedNodes = model.nodes.map((n) => {
        if (n.id !== sourceId) return n;
        return {
          ...n,
          transitions: n.transitions.map((t) =>
            t.to === targetId ? { ...t, condition } : t,
          ),
        };
      });
      onModelChange({ ...model, nodes: updatedNodes });
    },
    [model, onModelChange],
  );

  const initialNodes = useMemo(
    () => modelToFlowNodes(model, nodeErrorMap),
    // We intentionally keep stale deps here; synced via useEffect below
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

  // Keep nodes/edges in sync when model changes externally
  useEffect(() => {
    setNodes(modelToFlowNodes(model, nodeErrorMap));
    setEdges(modelToFlowEdges(model, nodeErrorMap, handleConditionChange));
  }, [model, nodeErrorMap, handleConditionChange, setNodes, setEdges]);

  // When a new edge is drawn in the canvas, add a transition to the model
  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) return;
      const sourceNode = model.nodes.find((n) => n.id === connection.source);
      if (!sourceNode) return;

      const newTransition = { to: connection.target, condition: '' };
      const updatedNodes = model.nodes.map((n) =>
        n.id === connection.source
          ? { ...n, transitions: [...n.transitions, newTransition] }
          : n,
      );
      onModelChange({ ...model, nodes: updatedNodes });
      setEdges((eds) => addEdge({ ...connection, type: 'transition' }, eds));
    },
    [model, onModelChange, setEdges],
  );

  // Sync drag-stop positions back to the model layout
  const onNodeDragStop = useCallback(
    (_event: React.MouseEvent, node: Node) => {
      const newLayout = { ...model.layout, [node.id]: { x: node.position.x, y: node.position.y } };
      onModelChange({ ...model, layout: newLayout });
    },
    [model, onModelChange],
  );

  // Handle node deletion via Delete key
  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key !== 'Delete' && event.key !== 'Backspace') return;
      if (!selectedNodeId) return;
      if (selectedNodeId === VIRTUAL_START || selectedNodeId === VIRTUAL_END) return;

      const updatedNodes = model.nodes.filter((n) => n.id !== selectedNodeId);
      const updatedNodesWithEdges = updatedNodes.map((n) => ({
        ...n,
        transitions: n.transitions.filter((t) => t.to !== selectedNodeId),
      }));
      const newLayout = { ...model.layout };
      delete newLayout[selectedNodeId];
      onModelChange({ ...model, nodes: updatedNodesWithEdges, layout: newLayout });
      setSelectedNodeId(null);
    },
    [model, onModelChange, selectedNodeId],
  );

  // Add a new node on canvas double-click
  const onDoubleClick = useCallback(
    (event: React.MouseEvent) => {
      const target = event.target as HTMLElement;
      if (target.closest('.react-flow__node')) return;
      const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect();
      const x = event.clientX - bounds.left - NODE_WIDTH / 2;
      const y = event.clientY - bounds.top - NODE_HEIGHT / 2;

      const newId = `step_${uuidv4().slice(0, 6)}`;
      const newNode: StepNode = {
        id: newId,
        label: '新步骤',
        mode: 'human',
        inputs: [],
        outputs: [],
        transitions: [],
      };
      const newLayout = { ...model.layout, [newId]: { x, y } };
      onModelChange({ ...model, nodes: [...model.nodes, newNode], layout: newLayout });
    },
    [model, onModelChange],
  );

  const selectedNode = selectedNodeId
    ? model.nodes.find((n) => n.id === selectedNodeId)
    : null;

  const handleNodePropertyChange = (updated: StepNode) => {
    const updatedNodes = model.nodes.map((n) => (n.id === selectedNodeId ? updated : n));
    // If id changed, we need to remap
    if (updated.id !== selectedNodeId) {
      const remaId = updated.id;
      const remappedNodes = updatedNodes.map((n) => ({
        ...n,
        transitions: n.transitions.map((t) =>
          t.to === selectedNodeId ? { ...t, to: remaId } : t,
        ),
      }));
      const newLayout = { ...model.layout };
      if (selectedNodeId && newLayout[selectedNodeId]) {
        newLayout[remaId] = newLayout[selectedNodeId];
        delete newLayout[selectedNodeId];
      }
      onModelChange({ ...model, nodes: remappedNodes, layout: newLayout });
      setSelectedNodeId(remaId);
    } else {
      onModelChange({ ...model, nodes: updatedNodes });
    }
  };

  const handleNodeDelete = (nodeId: string) => {
    const updatedNodes = model.nodes.filter((n) => n.id !== nodeId).map((n) => ({
      ...n,
      transitions: n.transitions.filter((t) => t.to !== nodeId),
    }));
    const newLayout = { ...model.layout };
    delete newLayout[nodeId];
    onModelChange({ ...model, nodes: updatedNodes, layout: newLayout });
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
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onNodeDragStop={onNodeDragStop}
        onNodeClick={(_evt, node) => setSelectedNodeId(node.id)}
        onPaneClick={() => setSelectedNodeId(null)}
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
