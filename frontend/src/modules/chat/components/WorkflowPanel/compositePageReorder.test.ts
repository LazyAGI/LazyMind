import { describe, expect, it } from 'vitest';
import { moveSelectedCompositePages, sameCompositePageOrder } from './compositePageReorder';

describe('moveSelectedCompositePages', () => {
  it('moves non-contiguous selected pages together and preserves their order', () => {
    expect(moveSelectedCompositePages(
      [1, 2, 3, 4, 5],
      new Set([2, 4]),
      5,
    )).toEqual([1, 3, 5, 2, 4]);
  });

  it('moves a selected group before an earlier page', () => {
    expect(moveSelectedCompositePages(
      [1, 2, 3, 4, 5],
      new Set([3, 5]),
      1,
    )).toEqual([1, 3, 5, 2, 4]);
  });

  it('keeps the existing single-page reorder behavior', () => {
    expect(moveSelectedCompositePages(
      [1, 2, 3, 4],
      new Set([2]),
      4,
    )).toEqual([1, 3, 4, 2]);
  });

  it('does not change an already contiguous group dropped inside itself', () => {
    const original = [1, 2, 3, 4];
    const next = moveSelectedCompositePages(original, new Set([2, 3]), 2);
    expect(next).toEqual(original);
    expect(sameCompositePageOrder(original, next)).toBe(true);
  });
});
