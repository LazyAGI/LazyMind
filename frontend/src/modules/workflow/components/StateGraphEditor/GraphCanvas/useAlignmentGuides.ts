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

/**
 * Symmetry guide style: dashed to distinguish from regular alignment guides.
 * Carried as an optional flag on the guide line object.
 */
export interface GuideLineHSym extends GuideLineH { symmetric?: true }
export interface GuideLineVSym extends GuideLineV { symmetric?: true }

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

/**
 * Check if two nodes (a, b) are symmetric about an axis node (pivot) center.
 * Returns the snap positions for the dragging node if near-symmetric is detected.
 */
function checkSymmetry(
  drag: ReturnType<typeof nodeRect>,
  other: ReturnType<typeof nodeRect>,
  pivot: ReturnType<typeof nodeRect>,
  snapThreshold: number,
): { snapX?: number; snapY?: number; axisX?: number; axisY?: number } | null {
  const result: { snapX?: number; snapY?: number; axisX?: number; axisY?: number } = {};
  let found = false;

  // Horizontal symmetry: drag and other should be equidistant from pivot centerX
  // drag.centerX = 2 * pivot.centerX - other.centerX
  const targetCX = 2 * pivot.centerX - other.centerX;
  if (Math.abs(drag.centerX - targetCX) <= snapThreshold) {
    result.snapX = targetCX - drag.w / 2;
    result.axisX = pivot.centerX;
    found = true;
  }

  // Vertical symmetry: drag and other equidistant from pivot centerY
  const targetCY = 2 * pivot.centerY - other.centerY;
  if (Math.abs(drag.centerY - targetCY) <= snapThreshold) {
    result.snapY = targetCY - drag.h / 2;
    result.axisY = pivot.centerY;
    found = true;
  }

  return found ? result : null;
}

export interface AlignmentResult {
  guides: GuideLine[];
  /** Returns snapped position if alignment triggered, null otherwise */
  onNodeDrag: (dragging: Node, allNodes: Node[]) => { x: number; y: number } | null;
  onNodeDragStop: () => void;
  onNodeResize: (nodeId: string, width: number, height?: number, allNodes?: Node[]) => { width: number; height?: number };
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

    // --- Symmetry guides: drag + another node symmetric about a pivot node's center ---
    // Only trigger when no existing alignment snap has been found for that axis,
    // so regular alignment has priority.
    for (let i = 0; i < others.length; i++) {
      for (let j = i + 1; j < others.length; j++) {
        // others[i] is candidate "pivot", others[j] is candidate "mirror"
        const pivot = nodeRect(others[i]);
        const mirror = nodeRect(others[j]);
        const sym = checkSymmetry(drag, mirror, pivot, SNAP_THRESHOLD);
        if (!sym) continue;

        if (sym.axisX !== undefined) {
          // Vertical axis of symmetry — draw a dashed vertical guide at pivot.centerX
          const minY = Math.min(drag.top, mirror.top, pivot.top) - GUIDE_PADDING;
          const maxY = Math.max(drag.bottom, mirror.bottom, pivot.bottom) + GUIDE_PADDING;
          const guide: GuideLineVSym = { type: 'vertical', x: sym.axisX, y1: minY, y2: maxY, symmetric: true };
          newGuides.push(guide);
          if (snapX === null && sym.snapX !== undefined) snapX = sym.snapX;
        }

        if (sym.axisY !== undefined) {
          // Horizontal axis of symmetry — draw a dashed horizontal guide at pivot.centerY
          const minX = Math.min(drag.left, mirror.left, pivot.left) - GUIDE_PADDING;
          const maxX = Math.max(drag.right, mirror.right, pivot.right) + GUIDE_PADDING;
          const guide: GuideLineHSym = { type: 'horizontal', y: sym.axisY, x1: minX, x2: maxX, symmetric: true };
          newGuides.push(guide);
          if (snapY === null && sym.snapY !== undefined) snapY = sym.snapY;
        }
      }
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

  const onNodeResize = useCallback((nodeId: string, width: number, height?: number, allNodes: Node[] = []) => {
    const node=allNodes.find(n=>n.id===nodeId); if(!node)return{width,height};
    const left=node.position.x,top=node.position.y; let snappedWidth=width,snappedHeight=height;
    let bestX=SNAP_THRESHOLD+1,bestY=SNAP_THRESHOLD+1; const nextGuides:GuideLine[]=[];
    for(const other of allNodes){if(other.id===nodeId)continue;const r=nodeRect(other);
      const sameWidthDist=Math.abs(width-r.w);if(sameWidthDist<=SNAP_THRESHOLD&&sameWidthDist<bestX){bestX=sameWidthDist;snappedWidth=r.w;nextGuides.splice(0,nextGuides.length,...nextGuides.filter(g=>g.type!=='vertical'),{type:'vertical',x:left+r.w,y1:Math.min(top,r.top)-GUIDE_PADDING,y2:Math.max(top+(height??nodeRect(node).h),r.bottom)+GUIDE_PADDING});}
      if(height!=null){const sameHeightDist=Math.abs(height-r.h);if(sameHeightDist<=SNAP_THRESHOLD&&sameHeightDist<bestY){bestY=sameHeightDist;snappedHeight=r.h;nextGuides.splice(0,nextGuides.length,...nextGuides.filter(g=>g.type!=='horizontal'),{type:'horizontal',y:top+r.h,x1:Math.min(left,r.left)-GUIDE_PADDING,x2:Math.max(left+snappedWidth,r.right)+GUIDE_PADDING});}}
      for(const x of [r.left,r.centerX,r.right]){const dist=Math.abs(left+width-x);if(dist<=SNAP_THRESHOLD&&dist<bestX){bestX=dist;snappedWidth=Math.max(90,x-left);nextGuides.splice(0,nextGuides.length,...nextGuides.filter(g=>g.type!=='vertical'),{type:'vertical',x,y1:Math.min(top,r.top)-GUIDE_PADDING,y2:Math.max(top+(height??nodeRect(node).h),r.bottom)+GUIDE_PADDING});}}
      if(height!=null)for(const y of [r.top,r.centerY,r.bottom]){const dist=Math.abs(top+height-y);if(dist<=SNAP_THRESHOLD&&dist<bestY){bestY=dist;snappedHeight=Math.max(64,y-top);nextGuides.splice(0,nextGuides.length,...nextGuides.filter(g=>g.type!=='horizontal'),{type:'horizontal',y,x1:Math.min(left,r.left)-GUIDE_PADDING,x2:Math.max(left+snappedWidth,r.right)+GUIDE_PADDING});}}
    }
    setGuides(nextGuides);return{width:snappedWidth,height:snappedHeight};
  },[]);

  return { guides, onNodeDrag, onNodeDragStop, onNodeResize };
}
