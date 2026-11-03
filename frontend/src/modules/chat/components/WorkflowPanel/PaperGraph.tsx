import { useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import { AimOutlined, CloseOutlined, DownOutlined, FullscreenOutlined, MinusOutlined, PlusOutlined } from '@ant-design/icons';
import { Modal, Popover } from 'antd';
import { useTranslation } from 'react-i18next';
import { Background, ControlButton, Controls, Handle, MarkerType, Panel, Position, ReactFlow, ReactFlowProvider,
  getNodesBounds, getViewportForBounds, useNodesState, useReactFlow, useStore, type Edge, type Node, type NodeProps, type ReactFlowState } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { paperRelationColors, type PaperGraphData } from './paperGraphData';
import { layoutPaperRelations, RELATION_NODE_HEIGHT, RELATION_NODE_WIDTH } from './paperRelationLayout';
import './PaperGraph.scss';

type PaperLinks = Record<string, { title: string; subtitle?: string; label: string; onClick: () => void }>;
type GraphNode = Node<{ label: string; tone: string; title?: string; subtitle?: string; action?: () => void; actionLabel?: string;
}, 'paper'>;

function PaperNode({ data }: NodeProps<GraphNode>) {
  return <div className={`paper-graph-node paper-graph-node--${data.tone}`}>
    {[Position.Left, Position.Right, Position.Top, Position.Bottom].map(position => <Handle key={`target-${position}`}
      id={`target-${position}`} type='target' position={position} isConnectable={false} />)}
    {data.action ? <button type='button' className='nodrag nopan paper-graph-node-detail' aria-label={data.actionLabel} title={data.title || data.label} onClick={data.action}>
      <span>{data.label}</span>{data.subtitle && <small>{data.subtitle}</small>}
    </button> : <span title={data.label}>{data.label}</span>}
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

function GraphCanvas({ graph, nodeLinks, onViewEvidence, fullscreen }: {
  fullscreen: boolean; graph: PaperGraphData; nodeLinks: PaperLinks; onViewEvidence?: () => void;
}) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const { viewportInitialized, setViewport, zoomIn, zoomOut } = useReactFlow();
  const viewportWidth = useStore((state: ReactFlowState) => state.width);
  const viewportHeight = useStore((state: ReactFlowState) => state.height);
  const links = useRef(nodeLinks);
  links.current = nodeLinks;
  const [hoveredEdge, setHoveredEdge] = useState<string>();
  const [selectedEdge, setSelectedEdge] = useState<string>();
  const [nodes, setNodes, onNodesChange] = useNodesState<GraphNode>([]);
  const positions = useMemo(() => layoutPaperRelations(graph), [graph]);
  const canFit = viewportInitialized && viewportWidth > 0 && viewportHeight > 0;
  const fitAllNodes = () => {
    if (!canFit || !nodes.length) return;
    const bounds = getNodesBounds(nodes);
    void setViewport(getViewportForBounds(bounds, viewportWidth, viewportHeight, 0.15, 1, 0.04));
  };
  const active = graph.edges.find(edge => edge.id === (hoveredEdge || selectedEdge));
  const edges: Edge[] = useMemo(() => graph.edges.map(edge => {
    const from = positions[edge.source], to = positions[edge.target];
    const horizontal = Math.abs(to.x - from.x) / RELATION_NODE_WIDTH > Math.abs(to.y - from.y) / RELATION_NODE_HEIGHT;
    const sourceSide = horizontal ? (to.x >= from.x ? Position.Right : Position.Left) : (to.y >= from.y ? Position.Bottom : Position.Top);
    const targetSide = horizontal ? (to.x >= from.x ? Position.Left : Position.Right) : (to.y >= from.y ? Position.Top : Position.Bottom);
    const color = paperRelationColors[edge.relationType || ''] || 'var(--color-text-secondary)';
    return { ...edge, type: 'default',
      sourceHandle: `source-${sourceSide}`, targetHandle: `target-${targetSide}`,
      label: edge.label,
      ariaLabel: edge.label,
      markerEnd: edge.directed ? { type: MarkerType.ArrowClosed, width: 16, height: 16, color } : undefined,
      style: { strokeDasharray: edge.dashed ? '5 4' : undefined, strokeWidth: active?.id === edge.id ? 2 : 1.2,
        stroke: color, opacity: !active || active.id === edge.id ? 1 : 0.58 },
    };
  }), [graph, positions, active?.id]);
  useEffect(() => {
    setNodes(graph.nodes.map(node => {
      const link = links.current[node.id];
      return { id: node.id, type: 'paper', position: positions[node.id],
        width: RELATION_NODE_WIDTH, height: RELATION_NODE_HEIGHT,
        data: { ...node, title: link?.title, subtitle: link?.subtitle, action: link ? () => links.current[node.id]?.onClick() : undefined, actionLabel: link?.label } };
    }));
  }, [graph, positions, setNodes]);
  useEffect(() => {
    if (!canFit || !graph.nodes.length) return;
    const bounds = getNodesBounds(graph.nodes.map(node => ({
      id: node.id, position: positions[node.id], width: RELATION_NODE_WIDTH, height: RELATION_NODE_HEIGHT,
      data: {},
    })));
    const viewport = getViewportForBounds(bounds, viewportWidth, viewportHeight, fullscreen ? 0.15 : 0.9, 1, 0.04);
    const seed = fullscreen ? undefined : graph.nodes.find(node => node.tone === 'amber');
    if (seed) {
      const position = positions[seed.id];
      viewport.x = viewportWidth / 2 - (position.x + RELATION_NODE_WIDTH / 2) * viewport.zoom;
      viewport.y = viewportHeight / 2 - (position.y + RELATION_NODE_HEIGHT / 2) * viewport.zoom;
    }
    void setViewport(viewport);
  }, [canFit, fullscreen, graph.nodes, positions, viewportWidth, viewportHeight, setViewport]);
  return <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange}
    nodesConnectable={false} nodesFocusable={false} edgesFocusable
    minZoom={0.15} maxZoom={2}
    onEdgeMouseEnter={(_: MouseEvent, edge: Edge) => setHoveredEdge(edge.id)} onEdgeMouseLeave={() => setHoveredEdge(undefined)}
    onEdgeClick={(_: MouseEvent, edge: Edge) => setSelectedEdge(edge.id)} onPaneClick={() => setSelectedEdge(undefined)}
    zoomOnScroll={false} zoomOnPinch zoomOnDoubleClick={false} preventScrolling={false} proOptions={{ hideAttribution: true }}>
    <Background gap={20} size={1} /><Controls showZoom={false} showFitView={false} showInteractive={false}>
      <ControlButton title={tr('zoomIn')} aria-label={tr('zoomIn')} onClick={() => void zoomIn()}><PlusOutlined /></ControlButton>
      <ControlButton title={tr('zoomOut')} aria-label={tr('zoomOut')} onClick={() => void zoomOut()}><MinusOutlined /></ControlButton>
      <ControlButton title={tr('fitGraph')} aria-label={tr('fitGraph')} onClick={fitAllNodes}><AimOutlined /></ControlButton>
    </Controls>
    {active && <Panel position='top-left' className='paper-graph-edge-detail'>
      <button type='button' aria-label={tr('closeRelation')} onClick={() => { setSelectedEdge(undefined); setHoveredEdge(undefined); }}><CloseOutlined /></button>
      <strong>{graph.nodes.find(node => node.id === active.source)?.label} {active.directed ? '→' : '—'} {graph.nodes.find(node => node.id === active.target)?.label}</strong>
      <p>{active.description || active.label}</p>
      {onViewEvidence && <button type='button' className='paper-graph-evidence' onClick={onViewEvidence}>{tr('relationEvidence')}</button>}
    </Panel>}
    {!graph.edges.length && <Panel position='top-left' className='paper-workflow-muted'>{tr('noConfirmedRelations')}</Panel>}
  </ReactFlow>;
}

export default function PaperGraph({ graph, nodeLinks, paperCount, onViewEvidence }: {
  graph: PaperGraphData; nodeLinks: PaperLinks; paperCount: number; onViewEvidence?: () => void;
}) {
  const { t } = useTranslation();
  const tr = (key: string) => t(`paperTasks.workflowResult.${key}`);
  const [enlarged, setEnlarged] = useState(false);
  const [canvasHeight, setCanvasHeight] = useState(300);
  const inlineCanvas = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const canvas = inlineCanvas.current;
    const scroll = canvas?.closest<HTMLElement>('.paper-workflow-scroll');
    if (!canvas || !scroll) return;
    const resize = () => {
      const beforeCanvas = canvas.getBoundingClientRect().top - scroll.getBoundingClientRect().top + scroll.scrollTop;
      setCanvasHeight(Math.max(300, Math.min(440, Math.floor(scroll.clientHeight - beforeCanvas - 16))));
    };
    const observer = new ResizeObserver(resize);
    observer.observe(scroll);
    resize();
    return () => observer.disconnect();
  }, [graph]);
  const unlinked = useMemo(() => {
    const connected = new Set(graph.nodes.map(node => node.id));
    return Object.keys(nodeLinks).filter(id => !connected.has(id));
  }, [graph, nodeLinks]);
  const linkedNodes = useMemo(() => Object.fromEntries(Object.entries(nodeLinks).map(([id, link]) => [id, {
    ...link, onClick: () => { setEnlarged(false); link.onClick(); },
  }])), [nodeLinks]);
  const mapped = graph.nodes.filter(node => node.id.startsWith('p_') && nodeLinks[node.id]).length;
  const content = (fullscreen = false) => <div className={`paper-graph${fullscreen ? ' paper-graph--fullscreen' : ''}`}>
    <div className='paper-graph-toolbar'>
      <span>{t('paperTasks.workflowResult.networkCoverage', { count: mapped, total: paperCount, edges: graph.edges.length })}</span>
      <div>
      {!!unlinked.length && <UnlinkedPapers ids={unlinked} links={linkedNodes} />}
      {onViewEvidence && <button type='button' onClick={() => { setEnlarged(false); onViewEvidence(); }}>{tr('relationEvidence')}</button>}
      {!fullscreen && <button type='button' aria-label={t('chat.markdownEnlargeDiagram')} onClick={() => setEnlarged(true)}><FullscreenOutlined /></button>}</div>
    </div>
    <div className='paper-graph-legend'>
      {[...new Set(graph.edges.map(edge => edge.relationType).filter((type): type is string => Boolean(type)))].map(type => <span key={type}><i style={{ borderColor: paperRelationColors[type] }} />{tr(`relationType_${type}`)}</span>)}
    </div>

    <div className='paper-graph-canvas' ref={fullscreen ? undefined : inlineCanvas} style={fullscreen ? undefined : { height: canvasHeight }} aria-label={tr('relationGraph')}>
      <ReactFlowProvider><GraphCanvas graph={graph} fullscreen={fullscreen} nodeLinks={linkedNodes}
        onViewEvidence={onViewEvidence && (() => { setEnlarged(false); onViewEvidence(); })} /></ReactFlowProvider>
    </div>
  </div>;
  return <>{content()}<Modal centered destroyOnHidden footer={null} width='90vw' open={enlarged}
    title={tr('relationGraph')} onCancel={() => setEnlarged(false)}>
    {content(true)}
  </Modal></>;
}
