/** Keep the reviewed paragraph visible when a taller diff collapses into the document. */
export function captureRewriteScrollAnchor(targets: Array<HTMLElement | null | undefined>) {
  const candidates = targets.filter((target): target is HTMLElement => Boolean(target?.isConnected));
  const first = candidates[0];
  if (!first) return () => {};
  let scroller = first.parentElement;
  while (scroller && !(/(auto|scroll|overlay)/.test(getComputedStyle(scroller).overflowY)
    && scroller.scrollHeight > scroller.clientHeight)) scroller = scroller.parentElement;
  if (!scroller) return () => {};
  const scrollContainer = scroller;
  const viewportTop = scrollContainer.getBoundingClientRect().top + scrollContainer.clientTop;
  const target = candidates.reduce((nearest, candidate) =>
    Math.abs(candidate.getBoundingClientRect().top - viewportTop)
      < Math.abs(nearest.getBoundingClientRect().top - viewportTop) ? candidate : nearest);
  const offset = Math.max(0, target.getBoundingClientRect().top - viewportTop);
  const editor = target.closest('.writer-ir__document--editable');
  const nodeId = target.closest<HTMLElement>('[data-node-id]')?.dataset.nodeId;

  return () => requestAnimationFrame(() => {
    // IR rendering replaces the DOM; find the same block in the new document.
    const block = nodeId && editor
      ? Array.from(editor.querySelectorAll<HTMLElement>('[data-node-id]')).find(node => node.dataset.nodeId === nodeId)
      : null;
    const current = block?.querySelector<HTMLElement>('[data-writer-block-content]')
      ?? (target.isConnected ? target : null);
    if (!current || !scrollContainer.isConnected) return;
    const top = scrollContainer.getBoundingClientRect().top + scrollContainer.clientTop;
    scrollContainer.scrollTop += current.getBoundingClientRect().top - top - offset;
  });
}
