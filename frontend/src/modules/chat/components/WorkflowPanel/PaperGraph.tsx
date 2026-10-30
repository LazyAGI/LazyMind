import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import { AimOutlined, CloseOutlined, DownOutlined, FullscreenOutlined, MinusOutlined, PlusOutlined } from '@ant-design/icons';
import { Modal, Popover } from 'antd';
import { useTranslation } from 'react-i18next';
import dagre from '@dagrejs/dagre';
import { Background, ControlButton, Controls, Handle, MarkerType, Panel, Position, ReactFlow, ReactFlowProvider,
  useNodesInitialized, useNodesState, useReactFlow, useStore, type Edge, type Node, type NodeProps, type ReactFlowState } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { MermaidNodeLinks } from '../MarkdownViewer/MermaidBlock';
import { branchDescendants, connectedPaperGraph, parsePaperGraph, readingPaperGraph, visiblePaperGraph, type PaperGraphData } from './paperGraphData';
import { layoutPaperRelations, RELATION_NODE_HEIGHT, RELATION_NODE_WIDTH } from './paperRelationLayout';
import './PaperGraph.scss';

type PaperLinks = Record<string, MermaidNodeLinks[string] & { title: string }>;
type GraphNode = Node<{ label: string; tone: string; compact: boolean; action?: () => void; actionLabel?: string;
  count?: number; folded?: boolean }, 'paper'>;

function PaperNode({ data }: NodeProps<GraphNode>) {
  return <div className={`paper-graph-node paper-graph-node--${data.tone}${data.compact ? ' paper-graph-node--relation' : ''}`}>
    {[Position.Left, Position.Right, Position.Top, Position.Bottom].map(position => <Handle key={`target-${position}`}
      id={`target-${position}`} type='target' position={position} isConnectable={false} />)}
    {data.action ? <button type='button' className='nodrag nopan' aria-label={data.actionLabel} title={data.label}
      aria-expanded={data.count !== undefined ? !data.folded : undefined} onClick={data.action}>
      <span>{data.label}</span>{data.count !== undefined && <small>{data.count} {data.folded ? <PlusOutlined /> : <MinusOutlined />}</small>}
    </button> : <span>{data.label}</span>}
    {[Position.Left, Position.Right, Position.Top, Position.Bottom].map(position => <Handle key={`source-${position}`}
      id={`source-${position}`} type='source' position={position} isConnectable={false} />)}
  </div>;
}
const nodeTypes = { paper: PaperNode };

function UnlinkedPapers({ ids, links }: { ids: string[]; links: PaperLinks }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return <Popover open={open} onOpenChange={setOpen} trigger='click' placement='bottomRight'
    content={<div className='paper-graph-unlinked-list'>
      <p>{t('paperTasks.workflowResult.unlinkedHint')}</p>
      <ul>{ids.map(id => <li key={id}><button type='button' onClick={() => { setOpen(false); links[id].onClick(); }}>{links[id].title}</button></li>)}</ul>
    </div>}>
    <button type='button' className='paper-graph-unlinked-toggle' aria-expanded={open}>
      {t('paperTasks.workflowResult.unlinkedPapers', { count: ids.length })}<DownOutlined rotate={open ? 180 : 0} />
    </button>
  </Popover>;
}

function GraphCanvas({ graph, folded, branches, focusBranch, toggle, nodeLinks, reading, onViewEvidence }: {
  graph: PaperGraphData; folded: Set<string>; branches: Map<string, Set<string>>; reading: boolean;
  focusBranch?: string; toggle: (id: string) => void; nodeLinks: PaperLinks; onViewEvidence?: () => void;
}) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const { fitView, zoomIn, zoomOut } = useReactFlow();
  const initialized = useNodesInitialized();
  const viewportWidth = useStore((state: ReactFlowState) => state.width);
  const viewportHeight = useStore((state: ReactFlowState) => state.height);
  const links = useRef(nodeLinks);
  links.current = nodeLinks;
  const [hoveredEdge, setHoveredEdge] = useState<string>();
  const [selectedEdge, setSelectedEdge] = useState<string>();
  const [nodes, setNodes, onNodesChange] = useNodesState<GraphNode>([]);
  const visible = useMemo(() => visiblePaperGraph(graph, folded, branches), [graph, folded, branches]);
  const positions = useMemo(() => {
    if (!reading) return layoutPaperRelations(visible);
    const layout = new dagre.graphlib.Graph({ multigraph: true });
    layout.setGraph({ rankdir: 'LR', ranksep: 90, nodesep: 24, marginx: 24, marginy: 24 });
    layout.setDefaultEdgeLabel(() => ({}));
    visible.nodes.forEach(node => layout.setNode(node.id, { width: 240, height: 84 }));
    visible.edges.forEach(edge => layout.setEdge(edge.source, edge.target, {}, edge.id));
    dagre.layout(layout);
    return Object.fromEntries(visible.nodes.map(node => {
      const point = layout.node(node.id);
      return [node.id, { x: point.x - 120, y: point.y - 42 }];
    }));
  }, [visible, reading]);
  const active = visible.edges.find(edge => edge.id === (hoveredEdge || selectedEdge));
  const edges: Edge[] = useMemo(() => visible.edges.map(edge => {
    const from = positions[edge.source], to = positions[edge.target];
    const horizontal = reading || Math.abs(to.x - from.x) / RELATION_NODE_WIDTH > Math.abs(to.y - from.y) / RELATION_NODE_HEIGHT;
    const sourceSide = horizontal ? (to.x >= from.x ? Position.Right : Position.Left) : (to.y >= from.y ? Position.Bottom : Position.Top);
    const targetSide = horizontal ? (to.x >= from.x ? Position.Left : Position.Right) : (to.y >= from.y ? Position.Top : Position.Bottom);
    return { ...edge, type: 'default',
      sourceHandle: `source-${sourceSide}`, targetHandle: `target-${targetSide}`,
      label: reading || active?.id === edge.id ? (edge.label.length > 10 ? `${edge.label.slice(0, 10)}…` : edge.label) : undefined,
      ariaLabel: edge.label,
      markerEnd: edge.directed ? { type: MarkerType.ArrowClosed, width: 16, height: 16 } : undefined,
      style: { strokeDasharray: edge.dashed ? '5 4' : undefined, strokeWidth: active?.id === edge.id ? 2 : 1.2,
        stroke: active?.id === edge.id ? 'var(--color-primary)' : undefined },
    };
  }), [visible, positions, reading, active?.id]);
  useEffect(() => {
    setNodes(visible.nodes.map(node => {
      const descendants = branches.get(node.id);
      const count = descendants ? [...descendants].filter(id => id.startsWith('p_')).length : undefined;
      const link = links.current[node.id];
      return { id: node.id, type: 'paper', position: positions[node.id],
        width: reading ? 240 : RELATION_NODE_WIDTH, height: reading ? 84 : RELATION_NODE_HEIGHT,
        data: { ...node, compact: !reading, count, folded: folded.has(node.id),
          action: count ? () => toggle(node.id) : link ? () => links.current[node.id]?.onClick() : undefined,
          actionLabel: count ? t(`paperTasks.workflowResult.${folded.has(node.id) ? 'expandBranch' : 'collapseBranch'}`, { title: node.label, count }) : link?.label } };
    }));
  }, [visible, folded, branches, positions, reading, setNodes, toggle, t]);
  useEffect(() => {
    if (!initialized) return;
    const frame = requestAnimationFrame(() => {
      const focused = focusBranch ? new Set([focusBranch, ...branches.get(focusBranch) || []]) : undefined;
      void fitView({ nodes: focused ? visible.nodes.filter(node => focused.has(node.id)) : undefined,
        padding: 0.08, minZoom: reading ? 0.65 : 0.5, maxZoom: 0.9 });
    });
    return () => cancelAnimationFrame(frame);
  }, [visible, focusBranch, branches, reading, initialized, viewportWidth, viewportHeight, fitView]);
  return <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange}
    nodesConnectable={false} nodesFocusable={false} edgesFocusable={!reading}
    minZoom={0.15} maxZoom={2} fitView fitViewOptions={{ padding: 0.08, minZoom: reading ? 0.65 : 0.5, maxZoom: 0.9 }}
    onEdgeMouseEnter={(_: MouseEvent, edge: Edge) => setHoveredEdge(edge.id)} onEdgeMouseLeave={() => setHoveredEdge(undefined)}
    onEdgeClick={(_: MouseEvent, edge: Edge) => setSelectedEdge(edge.id)} onPaneClick={() => setSelectedEdge(undefined)}
    zoomOnScroll zoomOnPinch zoomOnDoubleClick={false} preventScrolling proOptions={{ hideAttribution: true }}>
    <Background gap={20} size={1} /><Controls showZoom={false} showFitView={false} showInteractive={false}>
      <ControlButton title={tr('zoomIn')} aria-label={tr('zoomIn')} onClick={() => void zoomIn()}><PlusOutlined /></ControlButton>
      <ControlButton title={tr('zoomOut')} aria-label={tr('zoomOut')} onClick={() => void zoomOut()}><MinusOutlined /></ControlButton>
      <ControlButton title={tr('fitGraph')} aria-label={tr('fitGraph')} onClick={() => void fitView({ padding: 0.12, maxZoom: 1 })}><AimOutlined /></ControlButton>
    </Controls>
    {!reading && active && <Panel position='top-left' className='paper-graph-edge-detail'>
      <button type='button' aria-label={tr('closeRelation')} onClick={() => { setSelectedEdge(undefined); setHoveredEdge(undefined); }}><CloseOutlined /></button>
      <strong>{graph.nodes.find(node => node.id === active.source)?.label} {active.directed ? '→' : '—'} {graph.nodes.find(node => node.id === active.target)?.label}</strong>
      <p>{active.label}</p>
      {onViewEvidence && <button type='button' className='paper-graph-evidence' onClick={onViewEvidence}>{tr('relationEvidence')}</button>}
    </Panel>}
    {!reading && !graph.edges.length && <Panel position='top-left' className='paper-workflow-muted'>{tr('noConfirmedRelations')}</Panel>}
  </ReactFlow>;
}

export default function PaperGraph({ code, nodeLinks, reading = false, paperCount, onViewEvidence }: {
  code: string; nodeLinks: PaperLinks; reading?: boolean; paperCount: number; onViewEvidence?: () => void;
}) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const [state, setState] = useState<{ graph?: PaperGraphData; error?: string }>({});
  const [folded, setFolded] = useState(new Set<string>());
  const [focusBranch, setFocusBranch] = useState<string>();
  const [enlarged, setEnlarged] = useState(false);
  useEffect(() => {
    let cancelled = false;
    setState({});
    parsePaperGraph(code).then(graph => {
      if (cancelled) return;
      const branches = reading ? branchDescendants(graph) : new Map<string, Set<string>>();
      setFolded(new Set(branches.keys()));
      setFocusBranch(undefined); setState({ graph });
    }, error => { if (!cancelled) setState({ error: String(error) }); });
    return () => { cancelled = true; };
  }, [code, reading]);
  const graph = useMemo(() => state.graph && (reading ? readingPaperGraph(state.graph) : connectedPaperGraph(state.graph)), [state.graph, reading]);
  const branches = useMemo(() => graph && reading ? branchDescendants(graph) : new Map<string, Set<string>>(), [graph, reading]);
  const unlinked = useMemo(() => {
    const connected = new Set(graph?.nodes.map(node => node.id));
    return reading ? [] : Object.keys(nodeLinks).filter(id => !connected.has(id));
  }, [graph, nodeLinks, reading]);
  const toggle = useCallback((id: string) => {
    setFocusBranch(id);
    setFolded(previous => {
      const next = new Set(previous);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  }, []);
  const linkedNodes = useMemo(() => Object.fromEntries(Object.entries(nodeLinks).map(([id, link]) => [id, {
    ...link, onClick: () => { setEnlarged(false); link.onClick(); },
  }])), [nodeLinks]);
  if (!graph) return <p role={state.error ? 'alert' : 'status'} className='paper-workflow-muted'>
    {tr(state.error ? 'graphFailed' : 'graphLoading')}
  </p>;
  const visible = visiblePaperGraph(graph, folded, branches);
  const mapped = graph.nodes.filter(node => node.id.startsWith('p_') && nodeLinks[node.id]).length;
  const shown = visible.nodes.filter(node => node.id.startsWith('p_') && nodeLinks[node.id]).length;
  const content = (fullscreen = false) => <div className={`paper-graph${fullscreen ? ' paper-graph--fullscreen' : ''}`}>
    <div className='paper-graph-toolbar'>
      <span>{reading ? t('paperTasks.workflowResult.graphCoverage', { shown, mapped, total: paperCount })
        : t('paperTasks.workflowResult.relationCoverage', { count: mapped, total: paperCount, edges: graph.edges.length })}</span>
      <div>{!!branches.size && <>
        <button type='button' onClick={() => { setFocusBranch(undefined); setFolded(new Set()); }}>{tr('expandAllBranches')}</button>
        <button type='button' onClick={() => { setFocusBranch(undefined); setFolded(new Set(branches.keys())); }}>{tr('collapseAllBranches')}</button>
      </>}
      {!!unlinked.length && <UnlinkedPapers ids={unlinked} links={linkedNodes} />}
      {!reading && onViewEvidence && <button type='button' onClick={() => { setEnlarged(false); onViewEvidence(); }}>{tr('relationEvidence')}</button>}
      {!fullscreen && <button type='button' aria-label={t('chat.markdownEnlargeDiagram')} onClick={() => setEnlarged(true)}><FullscreenOutlined /></button>}</div>
    </div>
    {reading && mapped !== paperCount && <p className='paper-workflow-muted'>{tr('incompleteGraph')}</p>}
    <div className='paper-graph-canvas' aria-label={tr(reading ? 'readingMap' : 'relationGraph')}>
      <ReactFlowProvider><GraphCanvas graph={graph} branches={branches} folded={folded} focusBranch={focusBranch}
        toggle={toggle} nodeLinks={linkedNodes} reading={reading}
        onViewEvidence={onViewEvidence && (() => { setEnlarged(false); onViewEvidence(); })} /></ReactFlowProvider>
    </div>
  </div>;
  return <>{content()}<Modal centered destroyOnHidden footer={null} width='90vw' open={enlarged}
    title={tr(reading ? 'readingMap' : 'relationGraph')} onCancel={() => setEnlarged(false)}>
    {content(true)}
  </Modal></>;
}
