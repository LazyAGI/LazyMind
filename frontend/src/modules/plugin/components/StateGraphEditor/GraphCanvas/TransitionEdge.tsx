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

  const strokeColor = edgeData?.hasError ? '#ff4d4f' : selected ? '#1677ff' : '#8c8c8c';

  return (
    <>
      <path
        id={id}
        className="react-flow__edge-path"
        d={edgePath}
        stroke={strokeColor}
        strokeWidth={selected ? 2.5 : 1.5}
        fill="none"
        markerEnd="url(#arrow)"
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
