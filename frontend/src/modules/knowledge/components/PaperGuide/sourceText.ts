/** Source intervals belong to immutable block.text, never to rendered math/table DOM. */
function segments(element: HTMLElement) {
  return Array.from(
    element.querySelectorAll<HTMLElement>("[data-guide-source-start]"),
  );
}
export function sourceTextLength(element: HTMLElement): number {
  const parts = segments(element);
  return parts.length
    ? Number(parts[parts.length - 1].dataset.guideSourceEnd)
    : Array.from(element.textContent || "").length;
}
export function mappedRangeAtOffset(
  element: HTMLElement,
  offset: number,
): Range | undefined {
  const parts = segments(element);
  if (!parts.length) return;
  const range = document.createRange();
  for (const part of parts) {
    const start = Number(part.dataset.guideSourceStart),
      end = Number(part.dataset.guideSourceEnd);
    if (offset >= end) continue;
    if (
      part.hasAttribute("data-guide-math") ||
      part.hasAttribute("data-guide-atomic")
    ) {
      range.selectNode(part);
      return range;
    }
    const walker = document.createTreeWalker(part, NodeFilter.SHOW_TEXT);
    let node = walker.nextNode(),
      remaining = Math.max(0, offset - start);
    while (node) {
      const points = Array.from(node.textContent || "");
      if (remaining < points.length) {
        const index = points.slice(0, remaining).join("").length;
        range.setStart(node, index);
        range.setEnd(node, index + points[remaining].length);
        return range;
      }
      remaining -= points.length;
      node = walker.nextNode();
    }
  }
  range.selectNodeContents(element);
  range.collapse(false);
  return range;
}
