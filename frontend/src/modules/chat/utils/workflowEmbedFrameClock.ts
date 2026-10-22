import { getWorkflowDownloadHost } from './workflowDownload';

/** Host-only compatibility for same-origin, dynamically written scratch frames.
 * Does not change HTML, styles, capture/export code, or the top-level clock.
 * The visible embed page supplies callbacks; this is not a paint guarantee.
 */
export function installScratchFrameClock(doc: Document): () => void {
  const clock = doc.defaultView;
  if (!clock) return () => {};
  const request = clock.requestAnimationFrame.bind(clock);
  const cancel = clock.cancelAnimationFrame.bind(clock);
  const active = new Map<HTMLIFrameElement, { document: Document; restore: () => void }>();
  const watched = new Map<HTMLIFrameElement, () => void>();
  let stopped = false;

  const patch = (frame: HTMLIFrameElement) => {
    if (stopped || !frame.isConnected || frame.ownerDocument !== doc) return;
    try {
      // Never patch navigated pages, srcdoc previews, or cross-origin content.
      if (frame.hasAttribute('srcdoc') || (frame.getAttribute('src') || 'about:blank') !== 'about:blank'
        || !(frame.getAttribute('sandbox') || '').split(/\s+/).includes('allow-same-origin')) return;
      const target = frame.contentWindow;
      const targetDoc = frame.contentDocument;
      if (!target || !targetDoc || target.location.href !== 'about:blank') return;
      if (active.get(frame)?.document === targetDoc) return;
      active.get(frame)?.restore();
      const nativeRequest = target.requestAnimationFrame;
      const nativeCancel = target.cancelAnimationFrame;
      if (typeof nativeRequest !== 'function') return;
      const pending = new Map<number, number>();
      let sequence = 0;
      const adaptedRequest: typeof nativeRequest = callback => {
        if (typeof callback !== 'function') throw new TypeError('Expected animation frame callback');
        const rect = frame.getBoundingClientRect();
        const offscreen = rect.right <= 0 || rect.bottom <= 0
          || rect.left >= clock.innerWidth || rect.top >= clock.innerHeight;
        if (!offscreen) return nativeRequest.call(target, callback);
        // Negative IDs distinguish parent-clock requests from existing native ones.
        const id = --sequence;
        const parentId = request(() => {
          pending.delete(id);
          if (stopped || !frame.isConnected || frame.contentDocument !== targetDoc) return;
          callback.call(target, target.performance.now());
        });
        pending.set(id, parentId);
        return id;
      };
      const adaptedCancel: typeof nativeCancel = id => {
        const parentId = pending.get(id);
        if (parentId !== undefined) { pending.delete(id); cancel(parentId); }
        else nativeCancel.call(target, id);
      };
      target.requestAnimationFrame = adaptedRequest;
      target.cancelAnimationFrame = adaptedCancel;
      active.set(frame, { document: targetDoc, restore: () => {
        for (const id of pending.values()) cancel(id);
        pending.clear();
        try {
          if (target.requestAnimationFrame === adaptedRequest) target.requestAnimationFrame = nativeRequest;
          if (target.cancelAnimationFrame === adaptedCancel) target.cancelAnimationFrame = nativeCancel;
        } catch { /* The old frame may have navigated across origins. */ }
      } });
    } catch { /* An inaccessible frame keeps its native behavior. */ }
  };
  const watch = (frame: HTMLIFrameElement) => {
    if (!watched.has(frame)) {
      const loaded = () => patch(frame);
      frame.addEventListener('load', loaded, true);
      watched.set(frame, () => frame.removeEventListener('load', loaded, true));
    }
    patch(frame);
  };
  const scan = (node: Node) => {
    if (!(node instanceof Element)) return;
    if (node.tagName === 'IFRAME') watch(node as HTMLIFrameElement);
    node.querySelectorAll('iframe').forEach(watch);
  };
  const observer = new MutationObserver(records => {
    for (const [frame, state] of active) {
      if (!frame.isConnected) { state.restore(); active.delete(frame); }
    }
    for (const [frame, unwatch] of watched) {
      if (!frame.isConnected) { unwatch(); watched.delete(frame); }
    }
    for (const record of records) record.addedNodes.forEach(scan);
  });
  // Capture-phase load runs before a frame's own onload/capture continuation.
  const loaded = (event: Event) => {
    if (event.target instanceof HTMLIFrameElement) patch(event.target);
  };
  doc.addEventListener('load', loaded, true);
  observer.observe(doc.documentElement, { childList: true, subtree: true });
  doc.querySelectorAll('iframe').forEach(watch);
  const stop = () => {
    stopped = true;
    observer.disconnect();
    doc.removeEventListener('load', loaded, true);
    clock.removeEventListener('pagehide', stop);
    for (const state of active.values()) state.restore();
    active.clear();
    for (const unwatch of watched.values()) unwatch();
    watched.clear();
  };
  clock.addEventListener('pagehide', stop, { once: true });
  return stop;
}

/** Must run before React mounts the original Workflow panel. Opt-in only. */
export function installWorkflowEmbedFrameClock(): () => void {
  if (new URLSearchParams(window.location.search).get('workbuddyFrameClock') !== '1') return () => {};
  try {
    const host = getWorkflowDownloadHost();
    if (!host) return () => {};
    return installScratchFrameClock(document);
  } catch { return () => {}; }
}
