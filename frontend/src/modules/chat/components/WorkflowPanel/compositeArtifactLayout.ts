/** Find the revision aligned to a composite page, optionally repeating a sole input. */
export function findAlignedCompositeRevision<T extends { sort_order?: number }>(
  revisions: readonly T[],
  sortOrder: number,
  repeatSingle = false,
): T | undefined {
  const exact = revisions.find((revision) => revision.sort_order === sortOrder);
  if (exact) return exact;
  return repeatSingle && revisions.length === 1 ? revisions[0] : undefined;
}

/** Drop absent cells only when the workflow explicitly requests empty-column hiding. */
export function filterPresentCompositeItems<T>(
  items: readonly T[],
  isPresent: (item: T) => boolean,
  hideEmpty: boolean,
): T[] {
  return hideEmpty ? items.filter(isPresent) : [...items];
}
