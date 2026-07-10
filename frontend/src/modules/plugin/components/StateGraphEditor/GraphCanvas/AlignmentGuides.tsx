import { useStore } from '@xyflow/react';
import type { GuideLine, GuideLineHSym, GuideLineVSym } from './useAlignmentGuides';

interface Props {
  guides: GuideLine[];
}

/**
 * Renders alignment guide lines as an SVG overlay.
 *
 * Placed as a sibling of <ReactFlow> (still inside ReactFlowProvider) so the
 * SVG sits in the normal document flow at position:absolute over the canvas.
 * We read the viewport transform from the ReactFlow store to map flow-space
 * coordinates to screen-space pixels.
 */
export function AlignmentGuides({ guides }: Props) {
  const transform = useStore((s) => s.transform);
  const [vpX, vpY, zoom] = transform;

  if (guides.length === 0) return null;

  const toScreen = (fx: number, fy: number) => ({
    sx: fx * zoom + vpX,
    sy: fy * zoom + vpY,
  });

  return (
    <svg
      style={{
        position: 'absolute',
        inset: 0,
        width: '100%',
        height: '100%',
        pointerEvents: 'none',
        zIndex: 10,
        overflow: 'hidden',
      }}
    >
      {guides.map((g, i) => {
        const isSym = (g as GuideLineHSym | GuideLineVSym).symmetric === true;
        // Symmetric guides use a distinct color and a longer dash pattern
        const stroke = isSym ? '#722ed1' : '#f5222d';
        const dasharray = isSym ? '8 4' : '5 4';

        if (g.type === 'horizontal') {
          const { sx: sx1, sy } = toScreen(g.x1, g.y);
          const { sx: sx2 } = toScreen(g.x2, g.y);
          return (
            <line
              key={i}
              x1={sx1}
              y1={sy}
              x2={sx2}
              y2={sy}
              stroke={stroke}
              strokeWidth={1}
              strokeDasharray={dasharray}
              opacity={0.9}
            />
          );
        } else {
          const { sx, sy: sy1 } = toScreen(g.x, g.y1);
          const { sy: sy2 } = toScreen(g.x, g.y2);
          return (
            <line
              key={i}
              x1={sx}
              y1={sy1}
              x2={sx}
              y2={sy2}
              stroke={stroke}
              strokeWidth={1}
              strokeDasharray={dasharray}
              opacity={0.9}
            />
          );
        }
      })}
    </svg>
  );
}
