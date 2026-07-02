/**
 * StateGraphView — Plugin workflow graph renderer.
 *
 * - DAG only: self-loops and back-edges are dropped (only forward edges shown).
 * - Layout: dagre LR.
 * - Nodes rendered via SVG foreignObject (real HTML/CSS for precise styling).
 * - Click node → Popover with execution history + artifact summary.
 * - Hover edge → Tooltip with full condition text.
 */
import React, { useMemo, useRef, useState } from 'react';
import { Popover, Tooltip } from 'antd';
import dagre from '@dagrejs/dagre';

// ─── Public types ─────────────────────────────────────────────────────────────
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
  artifact_summary?: string | null;
  step_attempts?: SGAttempt[];
}

export interface SGEdge {
  from: string;
  to: string;
  condition: string;
  is_active_path: boolean;
}

export interface StateGraphData {
  nodes: SGNode[];
  edges: SGEdge[];
  initial: string;
  current_step_id: string;
}

// ─── Constants ────────────────────────────────────────────────────────────────
const NODE_W = 148;
const NODE_H = 88;
const CIRCLE_R = 12;
const NODESEP = 20;
const RANKSEP = 60;
const SVG_PAD = 48;
const TERMINAL_IDS = new Set(['__start__', '__end__']);

// ─── Status config ────────────────────────────────────────────────────────────
const S: Record<string, { color: string; bg: string; dot: string; label: string; icon: string }> = {
  succeeded:   { color: '#389e0d', bg: '#f6ffed', dot: '#52c41a', label: '已完成',   icon: '✓' },
  running:     { color: '#0958d9', bg: '#e6f4ff', dot: '#1677ff', label: '运行中',   icon: '↻' },
  waiting:     { color: '#d46b08', bg: '#fff7e6', dot: '#fa8c16', label: '等待确认', icon: '⏸' },
  failed:      { color: '#cf1322', bg: '#fff2f0', dot: '#ff4d4f', label: '失败',     icon: '✕' },
  interrupted: { color: '#cf1322', bg: '#fff2f0', dot: '#ff4d4f', label: '中断',     icon: '✕' },
  pending:     { color: '#8c8c8c', bg: '#fafafa', dot: '#bfbfbf', label: '未执行',   icon: '–' },
};

function s(status: string) { return S[status] ?? S.pending; }

function fmtDur(sec: number): string {
  if (sec < 0) return '';
  if (sec < 60) return `${Math.round(sec)}s`;
  const m = Math.floor(sec / 60), r = Math.round(sec % 60);
  return r > 0 ? `${m}m ${r}s` : `${m}m`;
}

function trunc(str: string, n: number) {
  return str && str.length > n ? str.slice(0, n) + '…' : (str ?? '');
}

// ─── DAG edge filter: remove back-edges using DFS ordering ────────────────────
function toDAG(nodes: SGNode[], edges: SGEdge[]): SGEdge[] {
  const ids = new Set(nodes.map((n) => n.id));
  const adj = new Map<string, string[]>();
  for (const e of edges) {
    if (e.from === e.to) continue;
    if (!ids.has(e.from) || !ids.has(e.to)) continue;
    if (!adj.has(e.from)) adj.set(e.from, []);
    adj.get(e.from)!.push(e.to);
  }
  // DFS to find back-edges.
  const visited = new Set<string>();
  const inStack = new Set<string>();
  const backEdges = new Set<string>();
  function dfs(id: string) {
    visited.add(id);
    inStack.add(id);
    for (const nb of adj.get(id) ?? []) {
      if (inStack.has(nb)) backEdges.add(`${id}→${nb}`);
      else if (!visited.has(nb)) dfs(nb);
    }
    inStack.delete(id);
  }
  for (const n of nodes) if (!visited.has(n.id)) dfs(n.id);
  return edges.filter((e) => e.from !== e.to && !backEdges.has(`${e.from}→${e.to}`));
}

// ─── Dagre layout ─────────────────────────────────────────────────────────────
interface PosNode { id: string; cx: number; cy: number; w: number; h: number; isTerminal: boolean; data: SGNode }
interface PosEdge { pts: { x: number; y: number }[]; data: SGEdge }

function layout(nodes: SGNode[], dagEdges: SGEdge[]): { pns: PosNode[]; pes: PosEdge[]; svgW: number; svgH: number } {
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

// ─── Node Popover content ─────────────────────────────────────────────────────
const ATTEMPT_STATUS_STYLE: Record<string, React.CSSProperties> = {
  succeeded:   { color: '#389e0d', background: '#f6ffed', border: '1px solid #b7eb8f' },
  running:     { color: '#0958d9', background: '#e6f4ff', border: '1px solid #91caff' },
  waiting:     { color: '#d46b08', background: '#fff7e6', border: '1px solid #ffd591' },
  failed:      { color: '#cf1322', background: '#fff2f0', border: '1px solid #ffa39e' },
  interrupted: { color: '#cf1322', background: '#fff2f0', border: '1px solid #ffa39e' },
  pending:     { color: '#8c8c8c', background: '#fafafa', border: '1px solid #d9d9d9' },
};

function AttemptStatusTag({ status }: { status: string }) {
  const sc = s(status);
  const style = ATTEMPT_STATUS_STYLE[status] ?? ATTEMPT_STATUS_STYLE.pending;
  return (
    <span style={{ ...style, borderRadius: 4, padding: '1px 7px', fontSize: 11, fontWeight: 500, display: 'inline-flex', alignItems: 'center', gap: 3 }}>
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: sc.dot, display: 'inline-block' }} />
      {sc.label}
    </span>
  );
}

function NodePopoverContent({ node }: { node: SGNode }) {
  const sc = s(node.status);
  const attempts = node.step_attempts ?? [];
  const totalArtifacts = attempts.reduce((sum, a) => sum + a.artifact_count, 0);
  const latest = attempts.length > 0 ? attempts[attempts.length - 1] : null;

  return (
    <div style={{ width: 300, fontFamily: 'inherit' }}>
      {/* Header */}
      <div style={{ padding: '12px 16px 10px', borderBottom: '1px solid #f0f0f0' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
          {node.step_index > 0 && (
            <span style={{
              background: sc.dot, color: '#fff', borderRadius: 5, padding: '1px 7px',
              fontSize: 11, fontWeight: 700, minWidth: 26, textAlign: 'center', flexShrink: 0,
            }}>
              {String(node.step_index).padStart(2, '0')}
            </span>
          )}
          <span style={{ fontWeight: 600, fontSize: 14, color: '#141414', flex: 1, lineHeight: 1.3 }}>{node.label}</span>
          <AttemptStatusTag status={node.status} />
        </div>
        <div style={{ display: 'flex', gap: 14, fontSize: 12, color: '#8c8c8c' }}>
          <span>执行 {attempts.length} 次</span>
          {totalArtifacts > 0 && <span>📎 {totalArtifacts} 个产出</span>}
          {latest && latest.duration_sec >= 0 && <span>⏱ {fmtDur(latest.duration_sec)}</span>}
        </div>
      </div>

      {/* Execution history */}
      {attempts.length > 0 && (
        <div style={{ padding: '10px 16px', borderBottom: '1px solid #f0f0f0' }}>
          <div style={{ fontSize: 11, color: '#bfbfbf', fontWeight: 600, letterSpacing: '0.5px', textTransform: 'uppercase', marginBottom: 8 }}>执行历史</div>
          {[...attempts].reverse().map((a) => (
            <div key={a.attempt} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '5px 0', borderBottom: '1px solid #f5f5f5' }}>
              <span style={{ color: '#8c8c8c', fontSize: 12, minWidth: 24, fontWeight: 600 }}>#{a.attempt}</span>
              <AttemptStatusTag status={a.status} />
              <span style={{ marginLeft: 'auto', fontSize: 12, color: '#8c8c8c' }}>{fmtDur(a.duration_sec)}</span>
              {a.artifact_count > 0 && (
                <span style={{ fontSize: 11, color: '#fa8c16' }}>📎 {a.artifact_count}</span>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Artifact summary */}
      <div style={{ padding: '10px 16px' }}>
        <div style={{ fontSize: 11, color: '#bfbfbf', fontWeight: 600, letterSpacing: '0.5px', textTransform: 'uppercase', marginBottom: 8 }}>产出摘要</div>
        {node.artifact_summary ? (
          <div style={{
            fontSize: 12, color: '#262626', background: '#fafafa', border: '1px solid #f0f0f0',
            borderRadius: 6, padding: '8px 10px', lineHeight: 1.6, wordBreak: 'break-all', maxHeight: 100, overflowY: 'auto',
          }}>
            {node.artifact_summary}
          </div>
        ) : (
          <div style={{ fontSize: 12, color: '#d9d9d9', textAlign: 'center', padding: '8px 0' }}>暂无产出摘要</div>
        )}
      </div>
    </div>
  );
}

// ─── SVG Node with foreignObject ──────────────────────────────────────────────
function StepNode({ pn, svgRef }: { pn: PosNode; svgRef: React.RefObject<SVGSVGElement | null> }) {
  const { data } = pn;
  const sc = s(data.status);
  const attempts = data.step_attempts ?? [];
  const latest = attempts.length > 0 ? attempts[attempts.length - 1] : null;
  const totalArtifacts = attempts.reduce((sum, a) => sum + a.artifact_count, 0);
  const lx = pn.cx - NODE_W / 2;
  const ty = pn.cy - NODE_H / 2;

  const cardStyle: React.CSSProperties = {
    width: NODE_W,
    height: NODE_H,
    background: '#fff',
    borderRadius: 10,
    border: `1.5px solid ${data.is_current ? sc.dot : '#e8e8e8'}`,
    boxShadow: data.is_current
      ? `0 0 0 3px ${sc.dot}28, 0 2px 8px rgba(0,0,0,0.10)`
      : '0 2px 6px rgba(0,0,0,0.08)',
    boxSizing: 'border-box',
    padding: '10px 11px 8px',
    display: 'flex',
    flexDirection: 'column',
    justifyContent: 'space-between',
    cursor: 'pointer',
    userSelect: 'none',
    overflow: 'hidden',
  };

  // Badge: light-tinted background with same-hue text (like target UI)
  const badgeStyle: React.CSSProperties = {
    display: 'inline-block',
    background: `${sc.dot}20`,  // 12% opacity tint
    color: sc.color,
    borderRadius: 6,
    fontSize: 11,
    fontWeight: 700,
    padding: '1px 6px',
    lineHeight: '18px',
  };

  return (
    <Popover
      content={<NodePopoverContent node={data} />}
      title={null}
      trigger='click'
      overlayInnerStyle={{ padding: 0, borderRadius: 10, overflow: 'hidden', boxShadow: '0 6px 24px rgba(0,0,0,0.12)' }}
      getPopupContainer={() => (svgRef.current?.closest('.sgv-scroll') as HTMLElement) ?? document.body}
    >
      <foreignObject x={lx} y={ty} width={NODE_W} height={NODE_H} style={{ overflow: 'visible' }}>
        <div style={cardStyle}>
          {/* Row 1: index badge */}
          <div>
            {data.step_index > 0 && (
              <span style={badgeStyle}>
                {String(data.step_index).padStart(2, '0')}
              </span>
            )}
          </div>
          {/* Row 2: step label */}
          <div style={{ fontSize: 13, fontWeight: 600, color: '#141414', lineHeight: 1.3, overflow: 'hidden', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical' }}>
            {data.label}
          </div>
          {/* Row 3: status · artifacts · duration */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11, color: '#8c8c8c', flexWrap: 'nowrap', overflow: 'hidden' }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: 4, color: sc.color, flexShrink: 0 }}>
              <span style={{ width: 7, height: 7, borderRadius: '50%', background: sc.dot, display: 'inline-block' }} />
              {sc.label}
            </span>
            {totalArtifacts > 0 && (
              <span style={{ display: 'flex', alignItems: 'center', gap: 2, flexShrink: 0 }}>
                <span>📎</span><span>{totalArtifacts}</span>
              </span>
            )}
            {latest && latest.duration_sec >= 0 && (
              <span style={{ display: 'flex', alignItems: 'center', gap: 2, flexShrink: 0 }}>
                <span>⏱</span><span>{fmtDur(latest.duration_sec)}</span>
              </span>
            )}
          </div>
        </div>
      </foreignObject>
    </Popover>
  );
}

function TerminalNode({ pn }: { pn: PosNode }) {
  const isEnd = pn.id === '__end__';
  return (
    <g>
      <circle cx={pn.cx} cy={pn.cy} r={CIRCLE_R} fill='#262626' />
      {isEnd && <circle cx={pn.cx} cy={pn.cy} r={CIRCLE_R - 4} fill='none' stroke='#fff' strokeWidth={2.5} />}
    </g>
  );
}

// ─── Edge ─────────────────────────────────────────────────────────────────────
function Edge({ pe, svgRef }: { pe: PosEdge; svgRef: React.RefObject<SVGSVGElement | null> }) {
  const [hov, setHov] = useState(false);
  const { data, pts } = pe;
  if (pts.length < 2) return null;
  const d = ptsToD(pts);
  const isActive = data.is_active_path;
  const stroke = hov ? (isActive ? '#0050b3' : '#595959') : (isActive ? '#1677ff' : '#bfbfbf');
  const mid = pts[Math.floor(pts.length / 2)];

  return (
    <Tooltip
      title={data.condition || undefined}
      open={hov && !!data.condition}
      overlayStyle={{ maxWidth: 300 }}
      getPopupContainer={() => (svgRef.current?.closest('.sgv-scroll') as HTMLElement) ?? document.body}
    >
      <g onMouseEnter={() => setHov(true)} onMouseLeave={() => setHov(false)}>
        <path d={d} fill='none' stroke='transparent' strokeWidth={14} style={{ cursor: data.condition ? 'help' : 'default' }} />
        <path d={d} fill='none' stroke={stroke} strokeWidth={hov ? 2 : 1.5} strokeDasharray={isActive ? '6 3' : undefined} markerEnd={`url(#${isActive ? 'arr-a' : 'arr-d'})`} />
        {data.condition && !hov && (
          <text x={mid.x} y={mid.y - 7} textAnchor='middle' fill='#bfbfbf' fontSize={10} pointerEvents='none'>
            {trunc(data.condition, 16)}
          </text>
        )}
      </g>
    </Tooltip>
  );
}

// ─── Legend ───────────────────────────────────────────────────────────────────
function Legend() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 18, padding: '8px 20px', borderBottom: '1px solid #f0f0f0', fontSize: 12, color: '#595959', flexWrap: 'wrap', background: '#fff' }}>
      {Object.entries(S).map(([, c]) => (
        <span key={c.label} style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: c.dot, display: 'inline-block' }} />
          {c.label}
        </span>
      ))}
      <span style={{ display: 'flex', alignItems: 'center', gap: 5, marginLeft: 4 }}>
        <svg width={28} height={8}><line x1={0} y1={4} x2={28} y2={4} stroke='#1677ff' strokeWidth={2} strokeDasharray='5 2' /></svg>
        当前合法后继路径
      </span>
    </div>
  );
}

// ─── Main ─────────────────────────────────────────────────────────────────────
export default function StateGraphView({ data }: { data: StateGraphData }) {
  const svgRef = useRef<SVGSVGElement>(null);
  const nodes = data?.nodes ?? [];
  const allEdges = data?.edges ?? [];

  // Keep only forward DAG edges (drop self-loops and back-edges).
  const dagEdges = useMemo(() => toDAG(nodes, allEdges), [nodes, allEdges]);

  const { pns, pes, svgW, svgH } = useMemo(
    () => layout(nodes, dagEdges),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [nodes.map((n) => n.id).join('|'), dagEdges.map((e) => `${e.from}→${e.to}`).join('|')],
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Legend />
      <div className='sgv-scroll' style={{ flex: 1, overflow: 'auto', background: '#f5f5f5', padding: 0 }}>
        <svg
          ref={svgRef}
          width={svgW}
          height={svgH}
          style={{ display: 'block', background: '#f5f5f5', minWidth: '100%' }}
          aria-label='Plugin workflow graph'
        >
          <defs>
            <marker id='arr-d' markerWidth={8} markerHeight={8} refX={7} refY={3} orient='auto'><path d='M0,0 L0,6 L8,3 z' fill='#bfbfbf' /></marker>
            <marker id='arr-a' markerWidth={8} markerHeight={8} refX={7} refY={3} orient='auto'><path d='M0,0 L0,6 L8,3 z' fill='#1677ff' /></marker>
          </defs>
          {pes.map((pe, i) => <Edge key={i} pe={pe} svgRef={svgRef} />)}
          {pns.map((pn) => pn.isTerminal ? <TerminalNode key={pn.id} pn={pn} /> : <StepNode key={pn.id} pn={pn} svgRef={svgRef} />)}
        </svg>
      </div>
    </div>
  );
}
