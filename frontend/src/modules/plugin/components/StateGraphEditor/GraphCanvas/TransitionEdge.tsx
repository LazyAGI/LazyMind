import { memo, useState } from 'react';
import { EdgeLabelRenderer, getBezierPath } from '@xyflow/react';
import type { EdgeProps } from '@xyflow/react';
import { Input } from 'antd';

export interface TransitionEdgeData extends Record<string, unknown> {
  condition: string;
  hasError: boolean;
  onConditionChange: (sourceId: string, targetId: string, condition: string) => void;
}

function TransitionEdgeComponent({
  id,
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
  const edgeData = data as unknown as TransitionEdgeData | undefined;
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');

  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  const [hovered, setHovered] = useState(false);
  const strokeColor = edgeData?.hasError ? '#ff4d4f' : selected ? '#1677ff' : hovered ? '#555' : '#8c8c8c';
  const strokeWidth = selected ? 2.5 : hovered ? 2.5 : 1.5;

  return (
    <>
      {/* Wide invisible hit area for easier selection */}
      <path
        d={edgePath}
        fill="none"
        stroke="transparent"
        strokeWidth={16}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        style={{ cursor: 'pointer' }}
      />
      <path
        id={id}
        className="react-flow__edge-path"
        d={edgePath}
        stroke={strokeColor}
        strokeWidth={strokeWidth}
        fill="none"
        markerEnd="url(#arrow)"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        style={{ transition: 'stroke-width 0.1s, stroke 0.1s', pointerEvents: 'none' }}
      />
      <EdgeLabelRenderer>
        <div
          style={{
            position: 'absolute',
            transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
            pointerEvents: 'all',
          }}
          className="nodrag nopan"
        >
          {editing ? (
            <Input
              size="small"
              autoFocus
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={() => {
                setEditing(false);
                edgeData?.onConditionChange(source, target, draft);
              }}
              onPressEnter={() => {
                setEditing(false);
                edgeData?.onConditionChange(source, target, draft);
              }}
              style={{ width: 160, fontSize: 11 }}
            />
          ) : (
            <button
              type="button"
              className={`transition-edge-label ${edgeData?.hasError ? 'has-error' : ''}`}
              onClick={() => {
                setDraft(String(edgeData?.condition ?? ''));
                setEditing(true);
              }}
              title="点击编辑转移条件"
            >
              {edgeData?.condition || <span className="transition-edge-label-empty">条件</span>}
            </button>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}

export const TransitionEdge = memo(TransitionEdgeComponent);
