import { useState, useCallback } from 'react';
import type { Node } from '@xyflow/react';

export interface GuideLineH {
  type: 'horizontal';
  y: number;
  x1: number;
  x2: number;
}

export interface GuideLineV {
  type: 'vertical';
  x: number;
  y1: number;
  y2: number;
}

export type GuideLine = GuideLineH | GuideLineV;

/** Snap threshold in flow-coordinate pixels */
const SNAP_THRESHOLD = 5;
const FALLBACK_W = 148;
const FALLBACK_H = 80;
/** Extra padding beyond the outermost node edge */
const GUIDE_PADDING = 20;

function nodeRect(n: Node) {
  const w = (n.measured?.width ?? n.width ?? FALLBACK_W) as number;
  const h = (n.measured?.height ?? n.height ?? FALLBACK_H) as number;
  return {
    left: n.position.x,
    right: n.position.x + w,
    centerX: n.position.x + w / 2,
    top: n.position.y,
    bottom: n.position.y + h,
    centerY: n.position.y + h / 2,
    w,
    h,
  };
}

export interface AlignmentResult {
  guides: GuideLine[];
  /** Returns snapped position if alignment triggered, null otherwise */
  onNodeDrag: (dragging: Node, allNodes: Node[]) => { x: number; y: number } | null;
  onNodeDragStop: () => void;
}

export function useAlignmentGuides(): AlignmentResult {
  const [guides, setGuides] = useState<GuideLine[]>([]);

  const onNodeDrag = useCallback((dragging: Node, allNodes: Node[]): { x: number; y: number } | null => {
    const drag = nodeRect(dragging);
    const others = allNodes.filter((n) => n.id !== dragging.id);

    /**
     * For each unique Y value that aligns with the dragging node, collect all
     * node X-ranges (including the dragging node itself) that share that Y value.
     * The final guide will span from the global min-X to max-X across all of them.
     *
     * Map key:  rounded axis value (to avoid float noise)
     * Map value: { axisVal, xRanges: [{x1, x2}], snapDragY }
     */
    type HEntry = { axisVal: number; xRanges: Array<{ x1: number; x2: number }>; snapDragY: number; dist: number };
    type VEntry = { axisVal: number; yRanges: Array<{ y1: number; y2: number }>; snapDragX: number; dist: number };

    const hMap = new Map<number, HEntry>();
    const vMap = new Map<number, VEntry>();

    // Candidate edges for the dragging node
    const dragYEdges = [
      { edge: 'top' as const, val: drag.top },
      { edge: 'center' as const, val: drag.centerY },
      { edge: 'bottom' as const, val: drag.bottom },
    ];
    const dragXEdges = [
      { edge: 'left' as const, val: drag.left },
      { edge: 'center' as const, val: drag.centerX },
      { edge: 'right' as const, val: drag.right },
    ];

    for (const other of others) {
      const r = nodeRect(other);

      // --- Horizontal guides (Y-axis alignment) ---
      const otherYVals = [r.top, r.centerY, r.bottom];
      for (const dc of dragYEdges) {
        for (const oy of otherYVals) {
          const dist = Math.abs(dc.val - oy);
          if (dist > SNAP_THRESHOLD) continue;

          // Snap position: where should drag.top be if this edge aligns?
          let snapDragY: number;
          switch (dc.edge) {
            case 'top':    snapDragY = oy; break;
            case 'center': snapDragY = oy - drag.h / 2; break;
            case 'bottom': snapDragY = oy - drag.h; break;
          }

          const key = Math.round(oy * 10); // bucket by axis value
          const existing = hMap.get(key);
          if (!existing || dist < existing.dist) {
            // Better match — reset entry with just this other node + dragging
            hMap.set(key, {
              axisVal: oy,
              xRanges: [
                { x1: r.left, x2: r.right },
                { x1: drag.left, x2: drag.right },
              ],
              snapDragY,
              dist,
            });
          } else if (existing && dist === existing.dist) {
            // Same quality match — extend the range to include this node
            existing.xRanges.push({ x1: r.left, x2: r.right });
          }
        }
      }

      // --- Vertical guides (X-axis alignment) ---
      const otherXVals = [r.left, r.centerX, r.right];
      for (const dc of dragXEdges) {
        for (const ox of otherXVals) {
          const dist = Math.abs(dc.val - ox);
          if (dist > SNAP_THRESHOLD) continue;

          let snapDragX: number;
          switch (dc.edge) {
            case 'left':   snapDragX = ox; break;
            case 'center': snapDragX = ox - drag.w / 2; break;
            case 'right':  snapDragX = ox - drag.w; break;
          }

          const key = Math.round(ox * 10);
          const existing = vMap.get(key);
          if (!existing || dist < existing.dist) {
            vMap.set(key, {
              axisVal: ox,
              yRanges: [
                { y1: r.top, y2: r.bottom },
                { y1: drag.top, y2: drag.bottom },
              ],
              snapDragX,
              dist,
            });
          } else if (existing && dist === existing.dist) {
            existing.yRanges.push({ y1: r.top, y2: r.bottom });
          }
        }
      }
    }

    // Build final guide lines — each guide spans the full min→max across all aligned nodes
    const newGuides: GuideLine[] = [];
    let snapX: number | null = null;
    let snapY: number | null = null;

    for (const entry of hMap.values()) {
      const allX1 = entry.xRanges.map((r) => r.x1);
      const allX2 = entry.xRanges.map((r) => r.x2);
      const minX = Math.min(...allX1) - GUIDE_PADDING;
      const maxX = Math.max(...allX2) + GUIDE_PADDING;
      newGuides.push({ type: 'horizontal', y: entry.axisVal, x1: minX, x2: maxX });
      // Use the best (closest) Y snap
      if (snapY === null) snapY = entry.snapDragY;
    }

    for (const entry of vMap.values()) {
      const allY1 = entry.yRanges.map((r) => r.y1);
      const allY2 = entry.yRanges.map((r) => r.y2);
      const minY = Math.min(...allY1) - GUIDE_PADDING;
      const maxY = Math.max(...allY2) + GUIDE_PADDING;
      newGuides.push({ type: 'vertical', x: entry.axisVal, y1: minY, y2: maxY });
      if (snapX === null) snapX = entry.snapDragX;
    }

    setGuides(newGuides);

    if (snapX !== null || snapY !== null) {
      return {
        x: snapX ?? dragging.position.x,
        y: snapY ?? dragging.position.y,
      };
    }
    return null;
  }, []);

  const onNodeDragStop = useCallback(() => {
    setGuides([]);
  }, []);

  return { guides, onNodeDrag, onNodeDragStop };
}
