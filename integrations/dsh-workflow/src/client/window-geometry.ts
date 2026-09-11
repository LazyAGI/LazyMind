export interface WindowRect { left: number; top: number; width: number; height: number }
export interface Viewport { width: number; height: number }
export type ResizeEdge = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw'

export const RESIZE_EDGES: readonly ResizeEdge[] = ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw']
export const DEFAULT_WINDOW_SIZE = { width: 960, height: 720 }
export const MIN_WINDOW_SIZE = { width: 480, height: 360 }
const MARGIN = 20

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), Math.max(min, max))
}

function sizeBounds(viewport: Viewport) {
  return {
    minWidth: Math.min(MIN_WINDOW_SIZE.width, viewport.width),
    minHeight: Math.min(MIN_WINDOW_SIZE.height, viewport.height),
  }
}

export function clampRect(rect: WindowRect, viewport: Viewport): WindowRect {
  const { minWidth, minHeight } = sizeBounds(viewport)
  const width = clamp(rect.width, minWidth, viewport.width)
  const height = clamp(rect.height, minHeight, viewport.height)
  return {
    left: clamp(rect.left, 0, Math.max(0, viewport.width - width)),
    top: clamp(rect.top, 0, Math.max(0, viewport.height - height)),
    width,
    height,
  }
}

export function defaultWindowRect(viewport: Viewport): WindowRect {
  const width = Math.min(DEFAULT_WINDOW_SIZE.width, Math.max(0, viewport.width - MARGIN * 2))
  const height = Math.min(DEFAULT_WINDOW_SIZE.height, Math.max(0, viewport.height - MARGIN * 2))
  return clampRect({
    left: viewport.width - width - MARGIN,
    top: Math.round((viewport.height - height) / 2),
    width,
    height,
  }, viewport)
}

export function moveRect(start: WindowRect, left: number, top: number, viewport: Viewport): WindowRect {
  return clampRect({ ...start, left, top }, viewport)
}

export function resizeRect(start: WindowRect, edge: ResizeEdge, delta: { x: number; y: number }, viewport: Viewport): WindowRect {
  const { minWidth, minHeight } = sizeBounds(viewport)
  let left = start.left
  let top = start.top
  let right = start.left + start.width
  let bottom = start.top + start.height
  if (edge.includes('e')) right = clamp(start.left + start.width + delta.x, left + minWidth, viewport.width)
  if (edge.includes('s')) bottom = clamp(start.top + start.height + delta.y, top + minHeight, viewport.height)
  if (edge.includes('w')) left = clamp(start.left + delta.x, 0, right - minWidth)
  if (edge.includes('n')) top = clamp(start.top + delta.y, 0, bottom - minHeight)
  return { left, top, width: right - left, height: bottom - top }
}

export function resizeHandleStyle(edge: ResizeEdge): Record<string, string | number> {
  const base = { position: 'absolute', zIndex: 2, touchAction: 'none' }
  const edgeSize = 6
  const corner = 14
  if (edge === 'n') return { ...base, top: 0, left: corner, right: corner, height: edgeSize, cursor: 'ns-resize' }
  if (edge === 's') return { ...base, bottom: 0, left: corner, right: corner, height: edgeSize, cursor: 'ns-resize' }
  if (edge === 'e') return { ...base, top: corner, right: 0, bottom: corner, width: edgeSize, cursor: 'ew-resize' }
  if (edge === 'w') return { ...base, top: corner, left: 0, bottom: corner, width: edgeSize, cursor: 'ew-resize' }
  if (edge === 'ne') return { ...base, top: 0, right: 0, width: corner, height: corner, cursor: 'nesw-resize' }
  if (edge === 'nw') return { ...base, top: 0, left: 0, width: corner, height: corner, cursor: 'nwse-resize' }
  if (edge === 'sw') return { ...base, bottom: 0, left: 0, width: corner, height: corner, cursor: 'nesw-resize' }
  return { ...base, bottom: 0, right: 0, width: corner, height: corner, cursor: 'nwse-resize' }
}
