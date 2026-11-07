/** Workflow graph: topology determines layout; live projection determines visuals. */
import React, { useId, useMemo, useRef, useState } from 'react';
import { Popover, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';
import dagre from '@dagrejs/dagre';
import { workflowTaskDisplayStatus } from '@/modules/chat/utils/workflowTaskStatus';

// ─── Types ────────────────────────────────────────────────────────────────────
export interface SGAttempt {
  attempt: number;
  status: string;
  duration_sec: number;
  artifact_count: number;
  started_at: string;
}

export interface SGNode {
  id: string;
  label: string;
  step_index: number;
  status: string;
  is_current: boolean;
  status_notes?: string[];
  artifact_summary?: string | null; // legacy, unused
  artifact_items?: { slot: string; content_type: string; preview: string }[];
  step_attempts?: SGAttempt[];
}

export interface SGEdge {
  from: string;
  to: string;
  condition: string;
  edge_type: 'executed' | 'current_direct' | 'pruned' | 'bypassed' | 'inactive';
}

export interface StateGraphData {
  nodes: SGNode[];
  edges: SGEdge[];
  initial: string;
}

// ─── Constants ────────────────────────────────────────────────────────────────
const NODE_W = 148;
const NODE_H = 88;
const CIRCLE_R = 14;
const NODESEP = 16;
const RANKSEP = 42;
const SVG_PAD = 44;
const TERMINAL_IDS = new Set(['__start__', '__end__']);

// ─── Status config ────────────────────────────────────────────────────────────
const S: Record<string, { color: string; dot: string; label: string }> = {
  succeeded:   { color: '#389e0d', dot: '#52c41a', label: 'taskCenter.statusCompleted' },
  running:     { color: '#0958d9', dot: '#1677ff', label: 'taskCenter.statusRunning' },
  waiting_input: { color: '#b74616', dot: '#e66a3c', label: 'taskCenter.statusWaitingInput' },
  waiting:     { color: '#d46b08', dot: '#fa8c16', label: 'taskCenter.statusWaiting' },
  failed:      { color: '#cf1322', dot: '#ff4d4f', label: 'taskCenter.statusFailed' },
  paused:      { color: '#a87913', dot: '#e5b544', label: 'taskCenter.statusPaused' },
  pending:     { color: '#177d7a', dot: '#279c9a', label: 'taskCenter.statusPending' },
  canceled:    { color: '#6f7988', dot: '#7a8493', label: 'taskCenter.statusCanceled' },
  skipped:     { color: '#8c7053', dot: '#b89b7a', label: 'taskCenter.statusSkipped' },
};
// These runtime values share a display meaning; keep persisted execution facts intact.
const STATUS_ALIASES: Record<string, string> = {
  completed: 'succeeded', cancelled: 'canceled',
  pruned: 'skipped', bypassed: 'skipped', ready: 'pending', queued: 'pending', claimed: 'pending',
};
function sc(status: string) { return S[STATUS_ALIASES[status] ?? workflowTaskDisplayStatus(status)] ?? S.pending; }

function fmtDur(sec: number): string {
  if (sec < 0) return '';
  if (sec < 60) return `${Math.round(sec)}s`;
  const m = Math.floor(sec / 60), r = Math.round(sec % 60);
  return r > 0 ? `${m}m ${r}s` : `${m}m`;
}

// Max chars to show in node label; Chinese counted as 2.
function truncLabel(str: string): string {
  if (!str) return '';
  let w = 0;
  let i = 0;
  for (; i < str.length; i++) {
    w += str.charCodeAt(i) > 0x7f ? 2 : 1;
    if (w > 14) { return str.slice(0, i) + '…'; }
  }
  return str;
}

// ─── Edge visual config ───────────────────────────────────────────────────────
const EDGE_STYLE: Record<SGEdge['edge_type'], { stroke: string; dash?: string; width: number; label: string }> = {
  executed:       { stroke: '#52c41a', width: 1.5, label: 'chat.workflowGraph.executedRoute' },
  current_direct: { stroke: '#1677ff', width: 1.5, label: 'chat.workflowGraph.activeRoute' },
  pruned:         { stroke: '#b89b7a', dash: '5 3', width: 1.2, label: 'chat.workflowGraph.skippedRoute' },
  bypassed:       { stroke: '#b89b7a', dash: '5 3', width: 1.2, label: 'chat.workflowGraph.skippedRoute' },
  inactive:       { stroke: '#d9d9d9', dash: '5 3', width: 1.0, label: 'chat.workflowGraph.inactiveRoute' },
};

// Go rejects cyclic graphs before runtime. The renderer only drops malformed
// dangling/self edges and never hides a server-provided control edge.
function toDAGEdges(nodes: SGNode[], edges: SGEdge[]): SGEdge[] {
  const ids = new Set(nodes.map((n) => n.id));
  return edges.filter((edge) => edge.from !== edge.to && ids.has(edge.from) && ids.has(edge.to));
}

// ─── Dagre layout ─────────────────────────────────────────────────────────────
interface PosNode { id: string; cx: number; cy: number; w: number; h: number; isTerminal: boolean; data: SGNode }
interface PosEdge { pts: { x: number; y: number }[]; data: SGEdge }

function buildLayout(nodes: SGNode[], dagEdges: SGEdge[]): { pns: PosNode[]; pes: PosEdge[]; svgW: number; svgH: number } {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'LR', nodesep: NODESEP, ranksep: RANKSEP, marginx: SVG_PAD, marginy: SVG_PAD });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes) {
    const t = TERMINAL_IDS.has(n.id);
    g.setNode(n.id, { width: t ? CIRCLE_R * 2 : NODE_W, height: t ? CIRCLE_R * 2 : NODE_H });
  }
  for (const e of dagEdges) g.setEdge(e.from, e.to);
  dagre.layout(g);
  const pns: PosNode[] = nodes.map((n) => {
    const gn = g.node(n.id);
    return { id: n.id, cx: gn.x, cy: gn.y, w: gn.width, h: gn.height, isTerminal: TERMINAL_IDS.has(n.id), data: n };
  });
  const nm = new Map(pns.map((p) => [p.id, p]));
  const pes: PosEdge[] = dagEdges.map((e) => {
    const ge = g.edge(e.from, e.to);
    let pts = ge?.points ?? [];
    if (!pts.length) {
      const a = nm.get(e.from), b = nm.get(e.to);
      if (a && b) pts = [{ x: a.cx + a.w / 2, y: a.cy }, { x: b.cx - b.w / 2, y: b.cy }];
    }
    return { pts, data: e };
  });
  const gi = g.graph();
  return { pns, pes, svgW: (gi.width ?? 500) + SVG_PAD * 2, svgH: (gi.height ?? 300) + SVG_PAD * 2 };
}

function ptsToD(pts: { x: number; y: number }[]): string {
  if (pts.length < 2) return '';
  const [p0, ...rest] = pts;
  const parts = [`M ${p0.x.toFixed(1)} ${p0.y.toFixed(1)}`];
  for (let i = 0; i < rest.length; i++) {
    const prev = i === 0 ? p0 : rest[i - 1];
    const cur = rest[i];
    const mx = ((prev.x + cur.x) / 2).toFixed(1);
    parts.push(`C ${mx} ${prev.y.toFixed(1)}, ${mx} ${cur.y.toFixed(1)}, ${cur.x.toFixed(1)} ${cur.y.toFixed(1)}`);
  }
  return parts.join(' ');
}

// ─── Node Popover ─────────────────────────────────────────────────────────────
function AttemptTag({ status }: { status: string }) {
  const { t } = useTranslation();
  const c = sc(status);
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3, background: `${c.dot}18`, border: `1px solid ${c.dot}50`, borderRadius: 4, padding: '1px 7px', fontSize: 11, fontWeight: 500, color: c.color }}>
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: c.dot, display: 'inline-block' }} />
      {t(c.label)}
    </span>
  );
}

function NodePopover({ node }: { node: SGNode }) {
  const { t } = useTranslation();
  const c = sc(node.status);
  const attempts = node.step_attempts ?? [];
  const totalArtifacts = attempts.reduce((s, a) => s + a.artifact_count, 0);
  const latest = attempts.length > 0 ? attempts[attempts.length - 1] : null;
  return (
    <div style={{ minWidth: 280, maxWidth: 360 }}>
      <div style={{ padding: '12px 16px 10px', borderBottom: '1px solid #f0f0f0' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
          {node.step_index > 0 && (
            <span style={{ background: `${c.dot}18`, color: c.color, borderRadius: 5, padding: '1px 7px', fontSize: 11, fontWeight: 700, flexShrink: 0 }}>
              {String(node.step_index).padStart(2, '0')}
            </span>
          )}
          <span style={{ fontWeight: 600, fontSize: 14, color: '#141414', flex: 1 }}>{node.label}</span>
          <AttemptTag status={node.status} />
        </div>
        {node.status_notes?.map(note => (
          <div key={note} style={{ fontSize: 12, color: '#595959', marginBottom: 6, overflowWrap: 'anywhere' }}>{note}</div>
        ))}
        <div style={{ display: 'flex', gap: 14, fontSize: 12, color: '#8c8c8c' }}>
          <span>{t('chat.workflowGraph.attempts', { count: attempts.length })}</span>
          {totalArtifacts > 0 && <span>📎 {totalArtifacts}</span>}
          {latest && latest.duration_sec >= 0 && <span>⏱ {fmtDur(latest.duration_sec)}</span>}
        </div>
      </div>
      {attempts.length > 0 && (
        <div style={{ padding: '10px 16px', borderBottom: '1px solid #f0f0f0' }}>
          <div style={{ fontSize: 11, color: '#bfbfbf', fontWeight: 600, letterSpacing: '0.5px', textTransform: 'uppercase', marginBottom: 8 }}>{t('chat.workflowGraph.history')}</div>
          {[...attempts].reverse().map((a) => (
            <div key={a.attempt} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '5px 0', borderBottom: '1px solid #f5f5f5' }}>
              <span style={{ color: '#8c8c8c', fontSize: 12, minWidth: 24, fontWeight: 600 }}>#{a.attempt}</span>
              <AttemptTag status={a.status} />
              <span style={{ marginLeft: 'auto', fontSize: 12, color: '#8c8c8c' }}>{fmtDur(a.duration_sec)}</span>
              {a.artifact_count > 0 && <span style={{ fontSize: 11, color: '#fa8c16' }}>📎 {a.artifact_count}</span>}
            </div>
          ))}
        </div>
      )}
      <div style={{ padding: '10px 16px' }}>
        <div style={{ fontSize: 11, color: '#bfbfbf', fontWeight: 600, letterSpacing: '0.5px', textTransform: 'uppercase', marginBottom: 8 }}>{t('chat.workflowGraph.artifacts')}</div>
        {node.artifact_items && node.artifact_items.length > 0 ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {node.artifact_items.map((item, idx) => (
              <div key={item.slot} style={{ display: 'flex', alignItems: 'baseline', gap: 6, fontSize: 12, color: '#262626', lineHeight: 1.5 }}>
                <span style={{ color: '#8c8c8c', flexShrink: 0, minWidth: 16 }}>{idx + 1}.</span>
                <span style={{ flexShrink: 0, color: '#8c8c8c', background: '#f5f5f5', borderRadius: 3, padding: '0 4px', fontSize: 11, fontFamily: 'monospace' }}>
                  [{item.content_type}]
                </span>
                <span style={{ wordBreak: 'break-all', color: '#595959' }}>{item.preview || '–'}</span>
              </div>
            ))}
          </div>
        ) : (
          <div style={{ fontSize: 12, color: '#d9d9d9', textAlign: 'center', padding: '8px 0' }}>{t('chat.workflowGraph.noArtifacts')}</div>
        )}
      </div>
    </div>
  );
}

// ─── SVG components ───────────────────────────────────────────────────────────
function TerminalNode({ pn }: { pn: PosNode }) {
	// __start__ is always active; __end__ turns green only when Go reports completion.
	const fill = pn.id === '__start__' ? '#1677ff' : pn.data.status === 'succeeded' ? '#52c41a' : '#bfbfbf';
  const isEnd = pn.id === '__end__';
  return (
    <g>
      <circle cx={pn.cx} cy={pn.cy} r={CIRCLE_R} fill={fill} />
      {isEnd && <circle cx={pn.cx} cy={pn.cy} r={CIRCLE_R - 4} fill='none' stroke='#fff' strokeWidth={2.5} />}
    </g>
  );
}

function StepNode({ pn, svgRef }: { pn: PosNode; svgRef: React.RefObject<SVGSVGElement | null> }) {
  const { t } = useTranslation();
  const { data } = pn;
  const c = sc(data.status);
  const attempts = data.step_attempts ?? [];
  const latest = attempts.length > 0 ? attempts[attempts.length - 1] : null;
  const totalArtifacts = attempts.reduce((s, a) => s + a.artifact_count, 0);
  const lx = pn.cx - NODE_W / 2;
  const ty = pn.cy - NODE_H / 2;
  const truncated = truncLabel(data.label);
  const needsTooltip = truncated !== data.label;

  const card = (
    <Popover
      content={<NodePopover node={data} />}
      title={null}
      trigger='click'
      overlayInnerStyle={{ padding: 0, borderRadius: 10, overflow: 'hidden', boxShadow: '0 6px 24px rgba(0,0,0,0.12)' }}
      getPopupContainer={() => (svgRef.current?.closest('.sgv-scroll') as HTMLElement) ?? document.body}
    >
      <div style={{
        width: NODE_W, height: NODE_H, background: '#fff', borderRadius: 10,
        border: `1.5px solid ${data.is_current ? c.dot : '#e8e8e8'}`,
        boxShadow: data.is_current ? `0 0 0 3px ${c.dot}28, 0 2px 8px rgba(0,0,0,0.10)` : '0 2px 6px rgba(0,0,0,0.08)',
        boxSizing: 'border-box', padding: '10px 11px 8px',
        display: 'flex', flexDirection: 'column', justifyContent: 'space-between',
        cursor: 'pointer', userSelect: 'none', overflow: 'hidden',
      }}>
        {/* Row 1: index badge */}
        <div>
          {data.step_index > 0 && (
            <span style={{ display: 'inline-block', background: `${c.dot}20`, color: c.color, borderRadius: 6, fontSize: 11, fontWeight: 700, padding: '1px 6px', lineHeight: '18px' }}>
              {String(data.step_index).padStart(2, '0')}
            </span>
          )}
        </div>
        {/* Row 2: label */}
        <div style={{ fontSize: 13, fontWeight: 600, color: '#141414', lineHeight: 1.3 }}>
          {truncated}
        </div>
        {/* Row 3: status · artifacts · duration */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11, flexWrap: 'nowrap', overflow: 'hidden' }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4, color: c.color, flexShrink: 0 }}>
            <span style={{ width: 7, height: 7, borderRadius: '50%', background: c.dot, display: 'inline-block' }} />
            {t(c.label)}
          </span>
          {totalArtifacts > 0 && <span style={{ color: '#8c8c8c', flexShrink: 0 }}>📎{totalArtifacts}</span>}
          {latest && latest.duration_sec >= 0 && <span style={{ color: '#8c8c8c', flexShrink: 0 }}>⏱{fmtDur(latest.duration_sec)}</span>}
        </div>
      </div>
    </Popover>
  );

  return (
    <foreignObject x={lx} y={ty} width={NODE_W} height={NODE_H} style={{ overflow: 'visible' }}>
      {needsTooltip ? (
        <Tooltip title={data.label} getPopupContainer={() => (svgRef.current?.closest('.sgv-scroll') as HTMLElement) ?? document.body}>
          {card}
        </Tooltip>
      ) : card}
    </foreignObject>
  );
}

function EdgeLine({ pe, svgRef, markerPrefix }: { markerPrefix: string; pe: PosEdge; svgRef: React.RefObject<SVGSVGElement | null> }) {
  const [hov, setHov] = useState(false);
  const { data, pts } = pe;
  if (pts.length < 2) return null;

  const style = EDGE_STYLE[data.edge_type];
  const markerId = `${markerPrefix}-${data.edge_type}`;
  const d = ptsToD(pts);

  return (
    <Tooltip
      title={hov && data.condition ? data.condition : undefined}
      getPopupContainer={() => (svgRef.current?.closest('.sgv-scroll') as HTMLElement) ?? document.body}
    >
      <g onMouseEnter={() => setHov(true)} onMouseLeave={() => setHov(false)}>
        {/* Wide invisible hit area */}
        <path d={d} fill='none' stroke='transparent' strokeWidth={14} style={{ cursor: data.condition ? 'help' : 'default' }} />
        {/* Visible edge */}
        <path d={d} fill='none' stroke={style.stroke} strokeWidth={hov ? style.width + 0.5 : style.width} strokeDasharray={style.dash} markerEnd={`url(#${markerId})`} />
      </g>
    </Tooltip>
  );
}

// ─── Legend ───────────────────────────────────────────────────────────────────
function Legend({ nodes, edges }: { nodes: SGNode[]; edges: SGEdge[] }) {
  const { t } = useTranslation();
  const stepNodes = nodes.filter(node => !TERMINAL_IDS.has(node.id));
  const statuses = [...new Map(stepNodes.map(node => {
    const config = sc(node.status);
    return [config.label, config] as const;
  })).values()];
  const edgeStyles = [...new Map(edges.map(edge => {
    const config = EDGE_STYLE[edge.edge_type];
    return [config.label, config] as const;
  })).values()];
  return (
    <div aria-label={t('chat.workflowGraph.legend')} style={{ display: 'flex', alignItems: 'center', gap: '10px 20px', padding: '12px 20px', borderBottom: '1px solid #f0f0f0', fontSize: 12, color: '#595959', flexWrap: 'wrap', background: '#fff' }}>
      {statuses.map(c => (
        <span key={c.label} style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: c.dot, display: 'inline-block' }} />
          {t(c.label)}
        </span>
      ))}
      {edgeStyles.map(c => (
        <span key={c.label} style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <svg width={28} height={8}><line x1={0} y1={4} x2={28} y2={4} stroke={c.stroke} strokeWidth={c.width} strokeDasharray={c.dash} /></svg>
          {t(c.label)}
        </span>
      ))}
    </div>
  );
}

// ─── Main ─────────────────────────────────────────────────────────────────────
export default function StateGraphView({ data }: { data: StateGraphData }) {
  const svgRef = useRef<SVGSVGElement>(null);
  const markerPrefix = useId().replace(/:/g, '');
  const nodes = data?.nodes ?? [];
  const allEdges = data?.edges ?? [];
  const dagEdges = useMemo(() => toDAGEdges(nodes, allEdges), [nodes, allEdges]);
  const { pns, pes, svgW, svgH } = useMemo(
    () => buildLayout(nodes, dagEdges),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [nodes.map((n) => n.id).join('|'), dagEdges.map((e) => `${e.from}→${e.to}`).join('|')],
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Legend nodes={nodes} edges={dagEdges} />
      <div className='sgv-scroll' style={{ flex: 1, overflow: 'auto', background: '#f5f5f5' }}>
        <svg ref={svgRef} width={svgW} height={svgH} style={{ display: 'block', background: '#f5f5f5', minWidth: '100%' }} aria-label='Workflow graph'>
          <defs>
            {Object.entries(EDGE_STYLE).map(([type, style]) => (
              <marker key={type} id={`${markerPrefix}-${type}`} markerWidth={8} markerHeight={8} refX={7} refY={3} orient='auto'><path d='M0,0 L0,6 L8,3 z' fill={style.stroke} /></marker>
            ))}
          </defs>
          {/* Cache coordinates only: statuses, labels and conditions must stay live. */}
          {pes.map((pe, i) => <EdgeLine key={i} pe={{ ...pe, data: dagEdges[i] }} svgRef={svgRef} markerPrefix={markerPrefix} />)}
          {pns.map((pn, i) => {
            const liveNode = { ...pn, data: nodes[i] };
            return pn.isTerminal ? <TerminalNode key={pn.id} pn={liveNode} /> : <StepNode key={pn.id} pn={liveNode} svgRef={svgRef} />;
          })}
        </svg>
      </div>
    </div>
  );
}
