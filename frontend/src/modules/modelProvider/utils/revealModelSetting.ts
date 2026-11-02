/** Reveal a deep-linked model without scrolling the fixed settings shell. */
export function revealModelSetting(target: HTMLElement) {
  const container = target.closest<HTMLElement>(".settings-reference-scroll");
  if (!container) return;
  const bounds = container.getBoundingClientRect();
  const row = target.getBoundingClientRect();
  const top = bounds.top + container.clientTop;
  const bottom = top + container.clientHeight;
  // Use the smallest movement needed. Centering a visible row hides the page
  // heading; scrollIntoView also scrolls overflow:hidden ancestors on Windows.
  const delta = row.top < top ? row.top - top : row.bottom > bottom
    ? Math.min(row.top - top, row.bottom - bottom) : 0;
  if (delta) {
    container.scrollTo({
      top: container.scrollTop + delta,
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
    });
  }
}
