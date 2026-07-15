import { memo, useLayoutEffect, useRef, useState } from 'react';
import { EdgeLabelRenderer, getBezierPath } from '@xyflow/react';
import type { EdgeProps } from '@xyflow/react';

export interface TransitionEdgeData extends Record<string, unknown> {
  condition: string;
  hasError: boolean;
  isParallel?: boolean;
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
}: EdgeProps) {
  const edgeData = data as unknown as TransitionEdgeData | undefined;
  const [hovered, setHovered] = useState(false);
  const pathRef = useRef<SVGPathElement>(null);
  const [arrow, setArrow] = useState<{ x: number; y: number; angle: number } | null>(null);

  // Debounce leave so moving between path ↔ popover doesn't flicker
  const leaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const onEnter = () => {
    if (leaveTimer.current) clearTimeout(leaveTimer.current);
    setHovered(true);
  };

  const onLeave = () => {
    leaveTimer.current = setTimeout(() => {
      setHovered(false);
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

  // SVG markers use the mathematical tangent exactly at the endpoint. React
  // Flow's bezier path forces that final tangent to match the handle direction,
  // while the visible curve a few pixels before the node can be much steeper.
  // Sample the rendered path around the actual arrow position instead, so the
  // arrow follows the line users see rather than the node edge normal.
  useLayoutEffect(() => {
    const path = pathRef.current;
    if (!path) return;
    const length = path.getTotalLength();
    const tipLength = Math.max(0, length - 4);
    const from = path.getPointAtLength(Math.max(0, tipLength - 12));
    const tip = path.getPointAtLength(tipLength);
    setArrow({
      x: tip.x,
      y: tip.y,
      angle: Math.atan2(tip.y - from.y, tip.x - from.x) * 180 / Math.PI,
    });
  }, [edgePath]);

  const isParallel = edgeData?.isParallel ?? false;
  const hasError = edgeData?.hasError ?? false;
  const condition = edgeData?.condition ?? '';

  const strokeColor = hasError ? '#ff4d4f' : selected ? '#1677ff' : hovered ? '#555' : '#8c8c8c';
  const strokeWidth = selected || hovered ? 2.5 : 1.5;
  const strokeDash = isParallel ? '6 3' : undefined;

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
        ref={pathRef}
        id={id}
        className="react-flow__edge-path"
        d={edgePath}
        stroke={strokeColor}
        strokeWidth={strokeWidth}
        strokeDasharray={strokeDash}
        fill="none"
        style={{ transition: 'stroke-width 0.1s, stroke 0.1s', pointerEvents: 'none' }}
      />
      {arrow && (
        <path
          d="M 0 0 L -10 -5 L -10 5 Z"
          fill={strokeColor}
          transform={`translate(${arrow.x} ${arrow.y}) rotate(${arrow.angle})`}
          style={{ pointerEvents: 'none', transition: 'fill 0.1s' }}
        />
      )}

      {/* Popover label — floats above the edge midpoint */}
      <EdgeLabelRenderer>
        {hovered && condition && (
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
            <div className={`transition-edge-popover-inner${hasError ? ' has-error' : ''}`}>
              <span className="transition-edge-popover-text">{condition}</span>
            </div>
            {/* Arrow pointing down to the edge */}
            <div className="transition-edge-popover-arrow" />
          </div>
        )}
      </EdgeLabelRenderer>
    </>
  );
}

export const TransitionEdge = memo(TransitionEdgeComponent);
