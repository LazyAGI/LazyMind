/** Keep resizing live at up to 30 fps, without starving updates during a drag.
 * The callback measures the latest geometry; intermediate ResizeObserver events
 * are coalesced. Rasterized PDF canvases are reused by RenderPdf at every size.
 */
export function createPdfViewportScheduler(update: () => void) {
  let frame: number | undefined;
  let lastUpdate = -Infinity;
  const tick = (time: number) => {
    if (time - lastUpdate < 32) {
      frame = requestAnimationFrame(tick);
      return;
    }
    frame = undefined;
    lastUpdate = time;
    update();
  };
  return {
    schedule: () => {
      if (frame === undefined) frame = requestAnimationFrame(tick);
    },
    cancel: () => {
      if (frame !== undefined) cancelAnimationFrame(frame);
      frame = undefined;
    },
  };
}
