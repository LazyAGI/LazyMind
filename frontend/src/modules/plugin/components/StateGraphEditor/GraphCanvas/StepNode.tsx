import { memo, useRef, useState, useLayoutEffect, useCallback } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps } from '@xyflow/react';
import { Tag, Tooltip } from 'antd';
import { RobotOutlined, UserOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { ValidationError } from '../core/validator';
import { formatExpression, isHiddenId } from '../core/model';
import type { MaterialExpression, NodeLayout } from '../core/model';
import { NODE_MIN_HEIGHT } from '../core/layout';

export const NODE_MIN_WIDTH = 90;  // 148 * ~0.6
export const NODE_DEFAULT_WIDTH = 148;

export interface StepNodeData extends Record<string, unknown> {
  id: string;
  label: string;
  mode: 'human' | 'auto';
  inputs: string[];
  outputs: string[];
  transitions: { to: string; when?: string; condition?: MaterialExpression }[];
  route?: 'all' | 'choice';
  skipIf?: MaterialExpression;
  legacySkipIf?: string;
  hasError: boolean;
  errorMessages: string[];
  /** IDs of nodes that have a transition pointing to this node (predecessor set) */
  predecessorIds: string[];
  /** Display labels for output slots: slotId → label (or slotId if no label) */
  outputLabels: Record<string, string>;
  /** Persisted node width in canvas pixels */
  nodeWidth: number;
  nodeHeight?: number;
  visual: NodeLayout;
  /** Callback to persist width when user finishes resizing */
  onResizeEnd: (nodeId: string, width: number, height?: number) => void;
  /** Callback for live width updates during drag (before mouseup) */
  onResizeDrag: (nodeId: string, width: number, height?: number) => { width: number; height?: number };
  /** Returns current canvas zoom level for screen→canvas coordinate conversion */
  getZoom: () => number;
}

// Chip width estimate: ~6px per char + 16px padding, min 40px
function estimateChipWidth(label: string): number {
  return Math.max(40, label.length * 6 + 16);
}

function OutputChips({ outputs, outputLabels, containerWidth }: {
  outputs: string[];
  outputLabels: Record<string, string>;
  containerWidth: number;
}) {
  const { t } = useTranslation();
  if (outputs.length === 0) return null;

  // Available width minus output prefix (~32px)
  const available = containerWidth - 20;
  const PLUS_CHIP_WIDTH = 32;

  const labels = outputs.map((id) => outputLabels[id] ?? id);

  // Greedily fit chips; always show at least one
  let shown = 0;
  let used = 0;
  for (let i = 0; i < labels.length; i++) {
    const chipW = estimateChipWidth(labels[i]);
    const remaining = labels.length - i - 1;
    if (i === 0) {
      shown = 1;
      used = chipW;
      continue;
    }
    const wouldNeedPlus = remaining > 0;
    const spaceNeeded = chipW + (wouldNeedPlus ? PLUS_CHIP_WIDTH : 0);
    if (used + spaceNeeded <= available) {
      shown++;
      used += chipW;
    } else {
      break;
    }
  }

  const visibleLabels = labels.slice(0, shown);
  const hiddenLabels = labels.slice(shown);

  return (
    <div className="step-node-outputs">
      <span className="step-node-outputs-prefix">{t('selfEvolutionRun.stepNodeOutputPrefix')}</span>
      {visibleLabels.map((lbl, i) => {
        const isTruncated = shown === 1 && lbl.length * 6 + 16 > available;
        return (
          <span key={i} className="step-node-output-chip">
            {isTruncated ? `${lbl.slice(0, Math.max(3, Math.floor((available - 20) / 6)))}…` : lbl}
          </span>
        );
      })}
      {hiddenLabels.length > 0 && (
        <Tooltip title={hiddenLabels.join('、')} placement="top">
          <span className="step-node-output-chip step-node-output-chip--more">
            +{hiddenLabels.length}
          </span>
        </Tooltip>
      )}
    </div>
  );
}

function StepNodeComponent({ data, selected }: NodeProps) {
  const { t } = useTranslation();
  const nodeData = data as unknown as StepNodeData;
  const { hasError, errorMessages, mode, label, id, route, skipIf, legacySkipIf, transitions,
          outputs, outputLabels, nodeWidth, nodeHeight, visual, onResizeEnd, onResizeDrag, getZoom } = nodeData;

  const isChoice = route === 'choice';
  const isParallel = (route === 'all' || !route) && transitions.length > 1;
  const skipSummary = skipIf ? formatExpression(skipIf) : legacySkipIf;
  const isSkippable = Boolean(skipSummary);

  // Measure inner content width for chip layout
  const bodyRef = useRef<HTMLDivElement>(null);
  const [innerWidth, setInnerWidth] = useState(nodeWidth - 20);
  useLayoutEffect(() => {
    if (!bodyRef.current) return;
    const obs = new ResizeObserver(([entry]) => {
      setInnerWidth(entry.contentRect.width);
    });
    obs.observe(bodyRef.current);
    return () => obs.disconnect();
  }, []);

  // Custom right-edge resize handle: works regardless of node selection state.
  const dragStateRef = useRef<{ startX: number; startWidth: number } | null>(null);

  const handleResizeMouseDown = useCallback(
    (axis: 'x' | 'y' | 'xy') => (e: React.MouseEvent) => {
      e.stopPropagation();
      e.preventDefault();
      const zoom = getZoom();
      const startY=e.clientY; const startHeight=nodeHeight ?? bodyRef.current?.getBoundingClientRect().height! / zoom;
      dragStateRef.current = { startX: e.clientX, startWidth: nodeWidth };

      const onMouseMove = (moveEvent: MouseEvent) => {
        if (!dragStateRef.current) return;
        const dx = (moveEvent.clientX - dragStateRef.current.startX) / zoom;
        const newWidth = Math.max(NODE_MIN_WIDTH, Math.round(dragStateRef.current.startWidth + dx));
        const newHeight=Math.max(NODE_MIN_HEIGHT,Math.round(startHeight+(moveEvent.clientY-startY)/zoom));
        onResizeDrag(id, axis === 'y' ? nodeWidth : newWidth, axis === 'x' ? undefined : newHeight);
      };

      const onMouseUp = (upEvent: MouseEvent) => {
        if (!dragStateRef.current) return;
        const dx = (upEvent.clientX - dragStateRef.current.startX) / zoom;
        const newWidth = Math.max(NODE_MIN_WIDTH, Math.round(dragStateRef.current.startWidth + dx));
        const newHeight=Math.max(NODE_MIN_HEIGHT,Math.round(startHeight+(upEvent.clientY-startY)/zoom));
        const snapped=onResizeDrag(id,axis==='y'?nodeWidth:newWidth,axis==='x'?undefined:newHeight);
        onResizeEnd(id, snapped.width, snapped.height);
        dragStateRef.current = null;
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
      };

      document.addEventListener('mousemove', onMouseMove);
      document.addEventListener('mouseup', onMouseUp);
    },
    [id, nodeWidth, nodeHeight, onResizeDrag, onResizeEnd, getZoom],
  );

  const safeVisual=visual??{x:0,y:0};
  const visibility=safeVisual.visible??{};
  const fill=safeVisual.fill;
  const background = fill?.type === 'none' ? 'transparent' : fill?.type === 'solid'
    ? `${fill.color ?? '#ffffff'}${Math.round((fill.opacity ?? 1)*255).toString(16).padStart(2,'0')}`
    : fill?.type === 'linear-gradient' ? `linear-gradient(${fill.angle ?? 90}deg, ${(fill.stops??[]).map(s=>`${s.color}${Math.round(s.opacity*255).toString(16).padStart(2,'0')} ${s.offset*100}%`).join(', ')})` : undefined;
  const border=safeVisual.border;

  return (
    <Tooltip
      title={hasError ? errorMessages.join('\n') : undefined}
      placement="top"
    >
      <div
        ref={bodyRef}
        className={[
          'step-node',
          selected ? 'is-selected' : '',
          hasError ? 'has-error' : '',
          isSkippable ? 'is-skippable' : '',
        ].filter(Boolean).join(' ')}
        aria-label={t('selfEvolutionRun.stepNodeAriaLabel', { label: String(label) })}
        style={{ height: nodeHeight, background, borderStyle:border?.style==='none'?'none':border?.style, borderWidth:border?.width, borderColor:border?.color, borderRadius:border?.radius, overflow:nodeHeight?'hidden':undefined }}
      >
        <Handle
          type="target"
          position={Position.Left}
          className="step-node-handle"
          isConnectableStart={false}
        />

        <div className="step-node-header">
          {visibility.stepId !== false && <span className="step-node-id">{isHiddenId(id) ? '' : String(id)}</span>}
          <div className="step-node-badges">
            {isChoice && visibility.conditionalRoute !== false && (
              <Tooltip title={t('selfEvolutionRun.stepNodeChoiceTooltip')}>
                <span className="step-node-badge step-node-badge--choice" aria-label={t('selfEvolutionRun.stepNodeChoiceTooltip')}>◇</span>
              </Tooltip>
            )}
            {isParallel && visibility.parallelRoute !== false && (
              <Tooltip title={t('selfEvolutionRun.stepNodeParallelTooltip')}>
                <span className="step-node-badge step-node-badge--parallel" aria-label={t('selfEvolutionRun.stepNodeParallelTooltip')}>⑂</span>
              </Tooltip>
            )}
            {isSkippable && visibility.skippable !== false && (
              <Tooltip title={t('selfEvolutionRun.stepNodeSkippableTooltip', { skipif: skipSummary })}>
                <span className="step-node-badge step-node-badge--skip" aria-label={t('selfEvolutionRun.stepNodeSkippableTooltip', { skipif: skipSummary })}>↷</span>
              </Tooltip>
            )}
            {visibility.approval !== false && <Tag
              className="step-node-mode-tag"
              icon={mode === 'auto' ? <RobotOutlined /> : <UserOutlined />}
              color={mode === 'auto' ? 'blue' : 'orange'}
            />}
          </div>
        </div>
        {visibility.label !== false && <div className="step-node-label">{String(label)}</div>}
        {visibility.outputs !== false && <OutputChips outputs={outputs} outputLabels={outputLabels} containerWidth={innerWidth} />}

        <Handle type="source" position={Position.Right} className="step-node-handle" />

        {/* Always-present right-edge resize grip — visible on hover, works without selecting.
            Uses noDragClassName so ReactFlow does not treat mousedown here as a node drag. */}
        <div
          className="step-node-resize-handle nodrag"
          onMouseDown={handleResizeMouseDown('x')}
          aria-hidden="true"
        />
        <div className="step-node-resize-handle step-node-resize-handle--bottom nodrag" onMouseDown={handleResizeMouseDown('y')} aria-hidden="true" />
        <div className="step-node-resize-handle step-node-resize-handle--corner nodrag" onMouseDown={handleResizeMouseDown('xy')} aria-hidden="true" />
      </div>
    </Tooltip>
  );
}

export const StepNodeRenderer = memo(StepNodeComponent);

// Virtual terminal node: __start__ or __end__ — rendered as a card (not a dot)
export function TerminalNode({ data }: NodeProps) {
  const { t } = useTranslation();
  const nodeData = data as unknown as { type: 'start' | 'end'; visual?: NodeLayout };
  const isStart = nodeData.type === 'start';
  const label = isStart ? t('selfEvolutionRun.stepNodeStart') : t('selfEvolutionRun.stepNodeEnd');
  const fill=nodeData.visual?.fill; const border=nodeData.visual?.border;
  const background=fill?.type==='none'?'transparent':fill?.type==='solid'?fill.color:fill?.type==='linear-gradient'?`linear-gradient(${fill.angle??90}deg, ${(fill.stops??[]).map(s=>`${s.color} ${s.offset*100}%`).join(',')})`:undefined;
  return (
    <div className={`terminal-node terminal-node--${nodeData.type}`} aria-label={label} style={{width:'100%',height:nodeData.visual?.height,background,borderStyle:border?.style==='none'?'none':border?.style,borderWidth:border?.width,borderColor:border?.color,borderRadius:border?.radius}}>
      {!isStart && <Handle type="target" position={Position.Left} className="step-node-handle" />}
      <span className="terminal-node-label">{label}</span>
      {isStart && <Handle type="source" position={Position.Right} className="step-node-handle" />}
    </div>
  );
}

// Helper: build node error map from validation errors
export function buildNodeErrorMap(errors: ValidationError[]): Map<string, string[]> {
  const map = new Map<string, string[]>();
  for (const err of errors) {
    if (!err.nodeId) continue;
    if (!map.has(err.nodeId)) map.set(err.nodeId, []);
    map.get(err.nodeId)!.push(err.message);
  }
  return map;
}
