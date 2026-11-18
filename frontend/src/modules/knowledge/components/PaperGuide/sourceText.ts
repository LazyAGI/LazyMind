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
export function sourceBoundary(
  element: HTMLElement,
  node: Node,
  offset: number,
  edge: "start" | "end",
): number {
  const point = document.createRange();
  point.setStart(node, offset);
  point.collapse(true);
  const parts = segments(element);
  let previousEnd: number | undefined;
  for (const part of parts) {
    const span = document.createRange();
    span.selectNodeContents(part);
    const first = span.cloneRange();
    first.collapse(true);
    const last = span.cloneRange();
    last.collapse(false);
    const start = Number(part.dataset.guideSourceStart),
      end = Number(part.dataset.guideSourceEnd);
    if (point.compareBoundaryPoints(Range.START_TO_START, first) <= 0)
      // In a hidden source gap, map the boundary toward the selected content.
      return edge === "end" ? previousEnd ?? start : start;
    if (point.compareBoundaryPoints(Range.START_TO_START, last) >= 0) {
      previousEnd = end;
      continue;
    }
    if (
      part.hasAttribute("data-guide-math") ||
      part.hasAttribute("data-guide-atomic")
    )
      return edge === "start" ? start : end;
    span.setEnd(node, offset);
    const prefix = span.toString();
    // Native endpoints are UTF-16. If a boundary bisects a supplementary
    // character, include that complete source code point conservatively.
    const splitsPair =
      node.nodeType === Node.TEXT_NODE &&
      offset > 0 &&
      /[\uD800-\uDBFF]/.test((node.textContent || "")[offset - 1] || "") &&
      /[\uDC00-\uDFFF]/.test((node.textContent || "")[offset] || "");
    return (
      start +
      Array.from(prefix).length -
      (edge === "start" && splitsPair ? 1 : 0)
    );
  }
  if (parts.length) return sourceTextLength(element);
  const before = document.createRange();
  before.selectNodeContents(element);
  before.setEnd(node, offset);
  return Array.from(before.toString()).length;
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
