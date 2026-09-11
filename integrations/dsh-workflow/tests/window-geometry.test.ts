import { describe, expect, it } from 'vitest'
import {
  DEFAULT_WINDOW_SIZE, MIN_WINDOW_SIZE, clampRect, defaultWindowRect, moveRect, resizeHandleStyle, resizeRect,
} from '../src/client/window-geometry'

const desktop = { width: 1440, height: 900 }
const start = { left: 200, top: 100, width: 800, height: 500 }

describe('workflow overlay geometry', () => {
  it('defaults to a right-aligned panel larger than the old fixed 760x560 window', () => {
    const rect = defaultWindowRect(desktop)
    expect(rect).toEqual({
      left: desktop.width - DEFAULT_WINDOW_SIZE.width - 20,
      top: Math.round((desktop.height - DEFAULT_WINDOW_SIZE.height) / 2),
      width: DEFAULT_WINDOW_SIZE.width,
      height: DEFAULT_WINDOW_SIZE.height,
    })
    expect(rect.width).toBeGreaterThan(760)
    expect(rect.height).toBeGreaterThan(560)
  })
  it('shrinks the default panel to the viewport instead of overflowing a small screen', () => {
    const rect = defaultWindowRect({ width: 700, height: 500 })
    expect(rect.left).toBeGreaterThanOrEqual(0)
    expect(rect.top).toBeGreaterThanOrEqual(0)
    expect(rect.left + rect.width).toBeLessThanOrEqual(700)
    expect(rect.top + rect.height).toBeLessThanOrEqual(500)
  })
  it('keeps a dragged panel inside the viewport', () => {
    expect(moveRect(start, -80, -40, desktop)).toEqual({ ...start, left: 0, top: 0 })
    expect(moveRect(start, 2000, 2000, desktop)).toEqual({ ...start, left: 640, top: 400 })
  })
  it('grows and shrinks from the south-east corner without leaving the viewport', () => {
    expect(resizeRect(start, 'se', { x: 120, y: 80 }, desktop)).toEqual({ ...start, width: 920, height: 580 })
    expect(resizeRect(start, 'se', { x: 5000, y: 5000 }, desktop)).toEqual({
      left: 200, top: 100, width: desktop.width - 200, height: desktop.height - 100,
    })
    expect(resizeRect(start, 'se', { x: -700, y: -400 }, desktop)).toEqual({
      ...start, width: MIN_WINDOW_SIZE.width, height: MIN_WINDOW_SIZE.height,
    })
  })
  it('resizes from the north-west corner while the opposite edges stay put', () => {
    expect(resizeRect(start, 'nw', { x: 50, y: 40 }, desktop)).toEqual({
      left: 250, top: 140, width: 750, height: 460,
    })
    const min = resizeRect(start, 'nw', { x: 700, y: 400 }, desktop)
    expect(min).toEqual({
      left: start.left + start.width - MIN_WINDOW_SIZE.width,
      top: start.top + start.height - MIN_WINDOW_SIZE.height,
      width: MIN_WINDOW_SIZE.width,
      height: MIN_WINDOW_SIZE.height,
    })
  })
  it('clamps an oversized layout back into the viewport', () => {
    expect(clampRect({ left: -20, top: -10, width: 2000, height: 1600 }, desktop)).toEqual({
      left: 0, top: 0, width: desktop.width, height: desktop.height,
    })
  })
  it('exposes a south-east resize cursor so the corner grip is discoverable', () => {
    expect(resizeHandleStyle('se').cursor).toBe('nwse-resize')
    expect(resizeHandleStyle('n').cursor).toBe('ns-resize')
  })
})
