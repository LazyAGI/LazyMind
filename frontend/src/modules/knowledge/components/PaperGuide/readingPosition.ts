/** Offsets stored by Core are Unicode code points, while DOM Range uses UTF-16. */
export function rangeAtUnicodeOffset(
  element: HTMLElement,
  offset: number,
): Range {
  const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  let node = walker.nextNode();
  let remaining = offset;
  while (node) {
    const text = node.textContent || "";
    const points = Array.from(text);
    if (remaining < points.length) {
      const start = points.slice(0, remaining).join("").length;
      range.setStart(node, start);
      range.setEnd(node, start + (points[remaining]?.length || 0));
      return range;
    }
    remaining -= points.length;
    node = walker.nextNode();
  }
  range.selectNodeContents(element);
  range.collapse(false);
  return range;
}
export function visibleTextOffset(element: HTMLElement, top: number): number {
  const length = Array.from(element.textContent || "").length;
  let low = 0,
    high = length;
  // A binary search over character line boxes avoids saving viewport-dependent pixels.
  while (low < high) {
    const mid = Math.floor((low + high) / 2);
    const rect = rangeAtUnicodeOffset(element, mid).getBoundingClientRect();
    if (rect.bottom <= top) low = mid + 1;
    else high = mid;
  }
  return low;
}
export function restoreTextPosition(
  container: HTMLElement,
  element: HTMLElement,
  offset: number,
) {
  const rect = rangeAtUnicodeOffset(element, offset).getBoundingClientRect();
  container.scrollTop += rect.top - container.getBoundingClientRect().top - 24;
}
