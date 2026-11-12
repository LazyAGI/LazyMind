export interface SlideCanvasSize { width: number; height: number }
export const DEFAULT_SLIDE_CANVAS: SlideCanvasSize = { width: 1600, height: 900 };

/** Historical and current slides may use different fixed canvas sizes. */
export function measureSlideCanvas(doc: Document): SlideCanvasSize {
  const root = doc.querySelector<HTMLElement>('.wrapper, [data-slide-root], .slide');
  if (!root) return DEFAULT_SLIDE_CANVAS;
  const style = doc.defaultView?.getComputedStyle(root);
  const width = root.offsetWidth || parseFloat(style?.width ?? '');
  const height = root.offsetHeight || parseFloat(style?.height ?? '');
  if (![width, height].every(value => Number.isFinite(value) && value >= 64 && value <= 8192)) return DEFAULT_SLIDE_CANVAS;
  return { width, height };
}
