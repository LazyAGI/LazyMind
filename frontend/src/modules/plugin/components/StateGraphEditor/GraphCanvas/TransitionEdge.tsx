import { memo, useRef, useState } from 'react';
import { EdgeLabelRenderer, getBezierPath } from '@xyflow/react';
import type { EdgeProps } from '@xyflow/react';
import { Input } from 'antd';
import { useTranslation } from 'react-i18next';
import { EditOutlined } from '@ant-design/icons';

export interface TransitionEdgeData extends Record<string, unknown> {
  condition: string;
  hasError: boolean;
  isParallel?: boolean;
  onConditionChange: (sourceId: string, targetId: string, condition: string) => void;
}

function TransitionEdgeComponent({  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  selected,
  source,
  target,
}: EdgeProps) {
  const { t } = useTranslation();
  const edgeData = data as unknown as TransitionEdgeData | undefined;
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [hovered, setHovered] = useState(false);

  // Debounce leave so moving between path ↔ popover doesn't flicker
  const leaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const onEnter = () => {
    if (leaveTimer.current) clearTimeout(leaveTimer.current);
    setHovered(true);
  };

  const onLeave = () => {
    leaveTimer.current = setTimeout(() => {
      if (!editing) setHovered(false);
    }, 120);
  };

  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  const isParallel = edgeData?.isParallel ?? false;
  const hasError = edgeData?.hasError ?? false;
  const condition = edgeData?.condition ?? '';

  const strokeColor = hasError ? '#ff4d4f' : selected ? '#1677ff' : hovered ? '#555' : '#8c8c8c';
  const strokeWidth = selected || hovered ? 2.5 : 1.5;
  const strokeDash = isParallel ? '6 3' : undefined;

  const commitEdit = () => {
    setEditing(false);
    setHovered(false);
    edgeData?.onConditionChange(source, target, draft);
  };

  // Position popover above the midpoint of the edge
  const popX = labelX;
  const popY = labelY - 44;

  return (
    <>
      {/* Wide invisible hit area */}
      <path
        d={edgePath}
        fill="none"
        stroke="transparent"
        strokeWidth={16}
        onMouseEnter={onEnter}
        onMouseLeave={onLeave}
        style={{ cursor: 'pointer' }}
      />
      <path
        id={id}
        className="react-flow__edge-path"
        d={edgePath}
        stroke={strokeColor}
        strokeWidth={strokeWidth}
        strokeDasharray={strokeDash}
        fill="none"
        markerEnd="url(#arrow)"
        style={{ transition: 'stroke-width 0.1s, stroke 0.1s', pointerEvents: 'none' }}
      />

      {/* Popover label — floats above the edge midpoint */}
      <EdgeLabelRenderer>
        {(hovered || editing) && (
          <div
            className="nodrag nopan transition-edge-popover"
            style={{
              position: 'absolute',
              transform: `translate(-50%, -100%) translate(${popX}px,${popY}px)`,
              pointerEvents: 'all',
            }}
            onMouseEnter={onEnter}
            onMouseLeave={onLeave}
          >
            {editing ? (
              <Input
                size="small"
                autoFocus
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onBlur={commitEdit}
                onPressEnter={commitEdit}
                onKeyDown={(e) => { if (e.key === 'Escape') { setEditing(false); setHovered(false); } }}
                style={{ width: 160, fontSize: 12 }}
                 placeholder={t('selfEvolutionRun.transitionConditionPlaceholder')}
              />
            ) : (
              <button
                type="button"
                className={`transition-edge-popover-inner${hasError ? ' has-error' : ''}`}
                onClick={() => {
                  setDraft(condition);
                  setEditing(true);
                }}
                title={t('selfEvolutionRun.transitionEdgeClickToEdit')}
              >
                <EditOutlined className="transition-edge-popover-icon" />
                <span className="transition-edge-popover-text">
                  {condition || <span className="transition-edge-popover-empty">{t('selfEvolutionRun.transitionEdgeClickToAddCondition')}</span>}
                </span>
              </button>
            )}
            {/* Arrow pointing down to the edge */}
            {!editing && <div className="transition-edge-popover-arrow" />}
          </div>
        )}
      </EdgeLabelRenderer>
    </>
  );
}

export const TransitionEdge = memo(TransitionEdgeComponent);
