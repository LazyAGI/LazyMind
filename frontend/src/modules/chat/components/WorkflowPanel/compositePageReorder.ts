/**
 * Move the selected composite pages to a visual gap while preserving their
 * relative order. The gap is expressed against the original page array, so
 * selected pages before the gap must be discounted before insertion.
 */
export function moveSelectedCompositePages<T>(
  pages: readonly T[],
  selectedPages: ReadonlySet<T>,
  gapIndex: number,
): T[] {
  const boundedGap = Math.max(0, Math.min(gapIndex, pages.length));
  const moving = pages.filter((page) => selectedPages.has(page));
  if (!moving.length) return [...pages];

  const selectedBeforeGap = pages
    .slice(0, boundedGap)
    .filter((page) => selectedPages.has(page))
    .length;
  const remaining = pages.filter((page) => !selectedPages.has(page));
  const insertAt = Math.max(
    0,
    Math.min(boundedGap - selectedBeforeGap, remaining.length),
  );

  return [
    ...remaining.slice(0, insertAt),
    ...moving,
    ...remaining.slice(insertAt),
  ];
}

export function sameCompositePageOrder<T>(left: readonly T[], right: readonly T[]): boolean {
  return left.length === right.length && left.every((page, index) => page === right[index]);
}
